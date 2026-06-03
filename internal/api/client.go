package api

import (
	"bytes"
	"fmt"
	"time"

	"github.com/aetherfy/cli/internal/config"
	"github.com/go-resty/resty/v2"
)

// Client wraps the HTTP client for API calls
type Client struct {
	http    *resty.Client
	baseURL string
	apiKey  string
	verbose bool
}

// testDBHeaderValue returns "1" if the key is a test key (afy_test_…),
// "0" otherwise. The control-plane's auth path looks at this header to
// pick the prod or test database for the key lookup — it no longer
// tries one then the other. Deriving the flag from the key's prefix
// keeps the contract local to the CLI and out of user config: the key
// itself already encodes which env it belongs to.
func testDBHeaderValue(apiKey string) string {
	if config.IsTestKey(apiKey) {
		return "1"
	}
	return "0"
}

// applyAuthHeaders sets Authorization + X-Test-DB on a resty client.
// Centralized so every constructor sends the same surface — adding a
// new constructor without these headers would silently route to the
// wrong DB.
func applyAuthHeaders(client *resty.Client, apiKey string) {
	if apiKey == "" {
		return
	}
	client.SetHeader("Authorization", "Bearer "+apiKey)
	client.SetHeader("X-Test-DB", testDBHeaderValue(apiKey))
}

// NewClient creates a new API client
func NewClient() *Client {
	cfg := config.Get()
	creds := config.GetCredentials()

	client := resty.New()
	client.SetTimeout(30 * time.Second)
	client.SetRetryCount(2)
	client.SetRetryWaitTime(1 * time.Second)

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

	applyAuthHeaders(client, c.apiKey)

	return c
}

// NewClientWithURL creates a client with an explicit base URL and API key.
// Used in tests to point the client at a local httptest.Server.
func NewClientWithURL(baseURL, apiKey string) *Client {
	client := resty.New()
	client.SetTimeout(30 * time.Second)
	client.SetHeader("Content-Type", "application/json")
	client.SetHeader("Accept", "application/json")
	client.SetHeader("User-Agent", "afy-cli/1.0")
	applyAuthHeaders(client, apiKey)
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
	client.SetHeader("Content-Type", "application/json")
	client.SetHeader("Accept", "application/json")
	client.SetHeader("User-Agent", "afy-cli/1.0")
	applyAuthHeaders(client, apiKey)

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
