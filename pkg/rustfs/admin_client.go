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
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const (
	rustfsApiVersion    = "v3"
	adminSigningRegion  = "us-east-01"
	adminSigningService = "s3"
)

type RustfsAdminConfig struct {
	AccessKey    string
	AccessSecret string
	Endpoint     string
	Ssl          bool
	Insecure     bool
}

type RustfsAdmin struct {
	httpClient   *http.Client
	endpointURL  string
	accessKey    string
	accessSecret string
}

type RequestData struct {
	CustomHeaders http.Header
	QueryValues   url.Values
	RelPath       string // URL path relative to admin API base endpoint
	Content       []byte
	Method        string
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
	client.endpointURL = client.createEndpointUrl(config.Endpoint, config.Ssl)
	client.httpClient = &http.Client{}
	client.accessKey = config.AccessKey
	client.accessSecret = config.AccessSecret
	return
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

func (c *RustfsAdmin) createEndpointUrl(endpoint string, secure bool) string {
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

	return scheme + "://" + endpoint + "/rustfs/admin/" + rustfsApiVersion
}

func (c *RustfsAdmin) createRequest(ctx context.Context, request RequestData) (*http.Request, error) {
	// Initialize a new HTTP request for the method.
	urlStr := c.endpointURL + "/" + request.RelPath
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
	urlStr := strings.Replace(c.endpointURL, "/rustfs/admin/"+rustfsApiVersion, "", 1) + "/" + request.RelPath
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
