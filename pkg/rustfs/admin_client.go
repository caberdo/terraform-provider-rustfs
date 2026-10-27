// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const (
	adminVersionV3 = "v3"
	adminVersionV4 = "v4"
	// defaultAdminAPIVersion is the admin API version a request without an
	// explicit RequestData.Version uses. It keeps every pre-v4 call byte
	// identical to the URLs the provider emitted before the version parameter
	// existed.
	defaultAdminAPIVersion = adminVersionV3
	// adminBasePath is the path prefix shared by every admin API version.
	adminBasePath = "/rustfs/admin"
	// adminSigningRegion is the credential-scope region for every signed
	// request. RustFS does not validate the scope's region: live probes on
	// 1.0.1, 1.0.2 and 1.0.3 accepted both us-east-01 (this value) and
	// us-east-1 for the /iceberg/v1 table-bucket routes, with the S3 data
	// plane and the admin API behaving the same way. The value stays fixed for
	// every call so existing requests are not re-signed; table_bucket_test.go
	// pins the emitted credential scope.
	adminSigningRegion  = "us-east-01"
	adminSigningService = "s3"
)

type RustfsAdminConfig struct {
	AccessKey    string
	AccessSecret string
	// Endpoint is the host[:port] the client dials; Ssl selects the transport.
	// A scheme in Endpoint is honoured (http/https selects the transport), so a
	// caller cannot end up at https://https://host. The provider resolves the
	// configured endpoint into both fields and rejects a contradiction before
	// they reach the client.
	Endpoint string
	Ssl      bool
	// HTTPClient, when set, serves every admin request. The provider passes its
	// own client so that both API clients share one transport (proxy settings,
	// timeouts and TLS policy, including the provider's `insecure` attribute).
	// When nil, a default http.Client is used.
	HTTPClient *http.Client
}

type RustfsAdmin struct {
	httpClient         *http.Client
	rootURL            string
	adminBaseURL       string
	configuredEndpoint string
	accessKey          string
	accessSecret       string
}

type RequestData struct {
	CustomHeaders http.Header
	QueryValues   url.Values
	RelPath       string // URL path relative to admin API base endpoint
	// Version selects the admin API version the request is sent to. Empty
	// means defaultAdminAPIVersion ("v3"), so existing callers are unchanged;
	// "v4" targets /rustfs/admin/v4/... DoDirectRequest rejects a non-empty
	// value because it always resolves against the host root.
	Version string
	Content []byte
	Method  string
}

// APIError is a structured client error produced from a non-2xx admin/S3
// response. Code and Message are parsed from the S3-style XML (or JSON) error
// body when possible; Raw holds the original response body and is what Error()
// returns, so callers that match on the raw body keep working.
type APIError struct {
	Code       string
	Message    string
	StatusCode int
	Raw        string
}

func (e *APIError) Error() string {
	if e.Raw != "" {
		return e.Raw
	}
	return fmt.Sprintf("status %d: %s: %s", e.StatusCode, e.Code, e.Message)
}

// xmlErrorBody and jsonErrorBody are the parse targets for S3-style and
// admin-style error payloads.
type xmlErrorBody struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

type jsonErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// parseAPIError turns a non-2xx response into an *APIError. XML parsing wins
// (RustFS answers S3-style with <Error><Code>..</Code></Error>); JSON is a
// fallback for admin endpoints that answer with {"code":..,"message":..}.
func parseAPIError(resp *http.Response, body []byte) *APIError {
	e := &APIError{StatusCode: resp.StatusCode, Raw: string(body)}

	var xe xmlErrorBody
	if err := xml.Unmarshal(body, &xe); err == nil && xe.Code != "" {
		e.Code, e.Message = xe.Code, xe.Message
		return e
	}

	var je jsonErrorBody
	if err := json.Unmarshal(body, &je); err == nil && (je.Code != "" || je.Message != "") {
		e.Code, e.Message = je.Code, je.Message
	}

	return e
}

// IsUpgradeRequired reports whether err is the server's answer for an admin v4
// path it does not serve: 511 Upgrade Required with an empty body. The body
// carries no Code, so the status code is the only signal; in particular this is
// not 501, which means the server does not implement the route at all.
func IsUpgradeRequired(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusUpgradeRequired
}

// IsAPIErrorCode reports whether err carries one of the given structured error
// codes as a *APIError. It never performs substring matching, so unrelated
// errors containing the code text are not misclassified.
func IsAPIErrorCode(err error, codes ...string) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, c := range codes {
		if apiErr.Code == c {
			return true
		}
	}
	return false
}

func New(config *RustfsAdminConfig) (client RustfsAdmin) {
	client.rootURL = RootURL(config.Endpoint, config.Ssl)
	client.adminBaseURL = client.rootURL + adminBasePath
	client.configuredEndpoint = config.Endpoint
	client.httpClient = config.HTTPClient
	if client.httpClient == nil {
		client.httpClient = &http.Client{}
	}
	client.accessKey = config.AccessKey
	client.accessSecret = config.AccessSecret
	return
}

// Endpoint returns the endpoint the client was configured with, before the
// scheme is filled in. It is what a caller can compare against configuration.
func (c RustfsAdmin) Endpoint() string {
	return c.configuredEndpoint
}

func (c *RustfsAdmin) doRequest(ctx context.Context, reqData RequestData) (res *http.Response, err error) {
	req, err := c.createRequest(ctx, reqData)
	if err != nil {
		return
	}

	res, err = c.httpClient.Do(req)
	if err != nil {
		return
	}
	if res.StatusCode > 299 {
		body, _ := io.ReadAll(res.Body)
		drainClose(res)
		return res, parseAPIError(res, body)
	}

	return
}

// drainClose discards any unread response body and closes it. Draining keeps the
// underlying connection reusable and both errors are intentionally ignored.
func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// RootURL composes the origin a client dials from an endpoint and the effective
// transport. It is the only URL composer: the provider uses it for the S3
// BaseEndpoint so both clients reach the same origin whatever form the user
// configured. An http/https scheme already present in endpoint selects the
// transport here as well, so a caller that passes a URL is normalized instead
// of producing https://https://host.
func RootURL(endpoint string, secure bool) string {
	if i := strings.Index(endpoint, "://"); i >= 0 {
		switch strings.ToLower(endpoint[:i]) {
		case "http":
			secure = false
		case "https":
			secure = true
		}
		endpoint = endpoint[i+3:]
	}

	scheme := "https"
	if !secure {
		scheme = "http"
	}

	// Strip the default port so the endpoint URL stays canonical.
	if secure && strings.HasSuffix(endpoint, ":443") {
		endpoint = strings.TrimSuffix(endpoint, ":443")
	}
	if !secure && strings.HasSuffix(endpoint, ":80") {
		endpoint = strings.TrimSuffix(endpoint, ":80")
	}

	return scheme + "://" + endpoint
}

// adminURL returns the versioned admin API base a request is sent to. The
// empty version keeps the historical v3 URL. Only v3 and v4 are accepted: the
// provider deliberately models no other namespace.
func (c *RustfsAdmin) adminURL(ctx context.Context, version string) (string, error) {
	switch version {
	case "":
		version = defaultAdminAPIVersion
	case adminVersionV3, adminVersionV4:
	default:
		return "", fmt.Errorf(
			"unsupported admin API version %q: expected %q or %q",
			version, adminVersionV3, adminVersionV4,
		)
	}
	return c.adminBaseURL + "/" + version, nil
}

func (c *RustfsAdmin) createRequest(ctx context.Context, request RequestData) (*http.Request, error) {
	// Initialize a new HTTP request for the method.
	adminURL, err := c.adminURL(ctx, request.Version)
	if err != nil {
		return nil, err
	}
	urlStr := adminURL + "/" + request.RelPath
	// If there are any query values, add them to the end.
	if len(request.QueryValues) > 0 {
		urlStr = urlStr + "?" + queryEncode(request.QueryValues)
	}

	req, err := http.NewRequestWithContext(ctx, request.Method, urlStr, bytes.NewReader(request.Content))
	if err != nil {
		return nil, err
	}
	if length := len(request.Content); length > 0 {
		req.ContentLength = int64(length)
	}
	if err := c.signRequest(ctx, req, request.Content); err != nil {
		return nil, err
	}
	return req, nil
}

func queryEncode(values url.Values) string {
	return strings.ReplaceAll(values.Encode(), "+", "%20")
}

func (c *RustfsAdmin) signRequest(ctx context.Context, req *http.Request, content []byte) error {
	sum := sha256.Sum256(content)
	payloadHash := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	credentials := aws.Credentials{
		AccessKeyID:     c.accessKey,
		SecretAccessKey: c.accessSecret,
	}
	signer := v4.NewSigner()
	return signer.SignHTTP(ctx, credentials, req, payloadHash, adminSigningService, adminSigningRegion, time.Now().UTC())
}

func (c *RustfsAdmin) DoDirectRequest(ctx context.Context, request RequestData) (res *http.Response, err error) {
	if request.Version != "" {
		return nil, fmt.Errorf(
			"DoDirectRequest targets the host root and does not take an admin API version (got %q)",
			request.Version,
		)
	}
	urlStr := c.rootURL + "/" + request.RelPath
	// If there are any query values, add them to the end.
	if len(request.QueryValues) > 0 {
		urlStr = urlStr + "?" + queryEncode(request.QueryValues)
	}

	req, err := http.NewRequestWithContext(ctx, request.Method, urlStr, bytes.NewReader(request.Content))
	if err != nil {
		return
	}
	if length := len(request.Content); length > 0 {
		req.ContentLength = int64(length)
	}
	if err := c.signRequest(ctx, req, request.Content); err != nil {
		return nil, err
	}

	res, err = c.httpClient.Do(req)
	if err != nil {
		return
	}
	if res.StatusCode != 200 && res.StatusCode != 204 {
		body, _ := io.ReadAll(res.Body)
		drainClose(res)
		return res, parseAPIError(res, body)
	}

	return
}
