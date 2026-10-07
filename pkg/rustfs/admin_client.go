package rustfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

func New(config *RustfsAdminConfig) (client RustfsAdmin) {
	client.endpointURL = client.createEndpointUrl(config.Endpoint, config.Ssl)
	client.httpClient = &http.Client{}
	client.accessKey = config.AccessKey
	client.accessSecret = config.AccessSecret
	return
}

func (c *RustfsAdmin) IsAdmin() (bool, error) {
	data := RequestData{
		RelPath: "is-admin",
		Method:  "GET",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.doRequest(ctx, data)
	if err != nil {
		return false, err
	}

	type adminRequest struct {
		Admin bool `json:"is_admin"`
	}
	var is adminRequest
	err = json.NewDecoder(resp.Body).Decode(&is)
	return is.Admin, err
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
		return res, errors.New(string(body))
	}

	return
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
		return res, errors.New(string(body))
	}

	return
}
