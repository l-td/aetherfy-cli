package api

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/go-resty/resty/v2"
)

// Transport-level retries are restricted to methods that carry no server-side
// effect.
//
// Resty's default with a retry count but no conditions is "retry on ANY
// transport error, for ANY method" (v2 retry.go: `needsRetry := err != nil &&
// err == err1` when no RetryConditions are registered). For a GET that is
// harmless; for a POST/PATCH/DELETE it means a request the server may already
// be executing gets sent a second time on nothing more than a slow reply or a
// dropped connection — the client cannot tell "never arrived" from "arrived and
// is still running".
//
// That is not theoretical: in nightly #30 it re-sent `agents stop` after a
// client-side timeout while the server was still working, and the retry
// collided with attempt 1's own agent-row lock (409 AGENT_OPERATION_IN_PROGRESS)
// — the CLI reporting failure against itself. The same reflex on `deploy` or
// `spawn` double-executes a create.
//
// A mutating verb that genuinely wants a retry should get a deliberate,
// response-code-aware one at its own call site, where idempotency is known.
var retryableMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// configureRetries applies the shared retry policy to a client.
func configureRetries(client *resty.Client) {
	client.SetRetryCount(2)
	client.SetRetryWaitTime(1 * time.Second)
	client.AddRetryCondition(func(resp *resty.Response, err error) bool {
		if err == nil || resp == nil || resp.Request == nil {
			return false
		}
		return retryableMethods[resp.Request.Method]
	})
}

// Client wraps the HTTP client for API calls
type Client struct {
	http    *resty.Client
	baseURL string
	apiKey  string
	verbose bool
}

// NewClient creates a new API client
func NewClient() *Client {
	cfg := config.Get()
	creds := config.GetCredentials()

	client := resty.New()
	client.SetTimeout(30 * time.Second)
	configureRetries(client)

	c := &Client{
		http:    client,
		baseURL: cfg.APIURL,
		apiKey:  creds.APIKey,
		verbose: cfg.Verbose,
	}

	// Set default headers
	client.SetHeader("Content-Type", "application/json")
	client.SetHeader("Accept", "application/json")
	client.SetHeader("User-Agent", "afy-cli/1.0")

	// Set auth header if API key is available. The CLI does NOT
	// tell the server which DB to look in — the api-key prefix
	// (afy_test_ vs afy_live_) is the server's signal, the same way
	// Stripe/OpenAI/etc. encode env in the credential.
	if c.apiKey != "" {
		client.SetHeader("Authorization", "Bearer "+c.apiKey)
	}

	return c
}

// NewClientWithURL creates a client with an explicit base URL and API key.
// Used in tests to point the client at a local httptest.Server.
func NewClientWithURL(baseURL, apiKey string) *Client {
	client := resty.New()
	client.SetTimeout(30 * time.Second)
	configureRetries(client)
	client.SetHeader("Content-Type", "application/json")
	client.SetHeader("Accept", "application/json")
	client.SetHeader("User-Agent", "afy-cli/1.0")
	if apiKey != "" {
		client.SetHeader("Authorization", "Bearer "+apiKey)
	}
	return &Client{
		http:    client,
		baseURL: baseURL,
		apiKey:  apiKey,
	}
}

// NewClientWithKey creates a client with a specific API key (for login validation)
func NewClientWithKey(apiKey string) *Client {
	cfg := config.Get()

	client := resty.New()
	client.SetTimeout(30 * time.Second)
	configureRetries(client)
	client.SetHeader("Content-Type", "application/json")
	client.SetHeader("Accept", "application/json")
	client.SetHeader("User-Agent", "afy-cli/1.0")
	client.SetHeader("Authorization", "Bearer "+apiKey)

	return &Client{
		http:    client,
		baseURL: cfg.APIURL,
		apiKey:  apiKey,
		verbose: cfg.Verbose,
	}
}

// SetVerbose enables/disables verbose logging
func (c *Client) SetVerbose(verbose bool) {
	c.verbose = verbose
	if verbose {
		c.http.SetDebug(true)
	}
}

// url builds a full URL from a path
func (c *Client) url(path string) string {
	return c.baseURL + path
}

// Get performs a GET request
func (c *Client) Get(path string, result interface{}) error {
	resp, err := c.http.R().
		SetResult(result).
		Get(c.url(path))

	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return c.handleResponse(resp)
}

// GetRaw performs a GET and returns the raw response body, without JSON
// decoding. Used for endpoints that return non-JSON payloads (e.g. the
// aetherfy.yaml export at /agents/{name}/yaml).
func (c *Client) GetRaw(path string) ([]byte, error) {
	resp, err := c.http.R().Get(c.url(path))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if err := c.handleResponse(resp); err != nil {
		return nil, err
	}
	return resp.Body(), nil
}

// Post performs a POST request
func (c *Client) Post(path string, body interface{}, result interface{}) error {
	resp, err := c.http.R().
		SetBody(body).
		SetResult(result).
		Post(c.url(path))

	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return c.handleResponse(resp)
}

// PostFile uploads a file via POST
func (c *Client) PostFile(path string, fileName string, fileBytes []byte, result interface{}) error {
	resp, err := c.http.R().
		SetFileReader("file", fileName, bytes.NewReader(fileBytes)).
		SetResult(result).
		Post(c.url(path))

	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return c.handleResponse(resp)
}

// Patch performs a PATCH request
func (c *Client) Patch(path string, body interface{}, result interface{}) error {
	resp, err := c.http.R().
		SetBody(body).
		SetResult(result).
		Patch(c.url(path))

	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return c.handleResponse(resp)
}

// Delete performs a DELETE request
func (c *Client) Delete(path string) error {
	resp, err := c.http.R().
		Delete(c.url(path))

	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return c.handleResponse(resp)
}

// DeleteWithResult performs a DELETE request and parses the response body
func (c *Client) DeleteWithResult(path string, result interface{}) error {
	resp, err := c.http.R().
		SetResult(result).
		Delete(c.url(path))

	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return c.handleResponse(resp)
}

// handleResponse checks for errors in the response
func (c *Client) handleResponse(resp *resty.Response) error {
	if resp.IsError() {
		return parseAPIError(resp)
	}
	return nil
}

// CheckConnection verifies API connectivity
func (c *Client) CheckConnection() error {
	resp, err := c.http.R().Get(c.baseURL + "/../health")
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	if resp.IsError() {
		return fmt.Errorf("API returned status %d", resp.StatusCode())
	}
	return nil
}
