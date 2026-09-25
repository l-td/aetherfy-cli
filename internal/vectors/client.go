// Package vectors talks to the Aetherfy vectors API (vectordb), the service the
// SDKs aetherfy-vectors-python-sdk and aetherfy-vectors-js-sdk wrap.
//
// It is a separate client from internal/api on purpose. That one talks only to
// the control plane: a different host, a different error envelope
// ({"detail": {...}} there, {"error": {...}} here) and a different retry
// policy. Each parser reads only the surface it talks to (control plane
// docs/REVIEW_FAQ.md section 56), so neither one guesses at the other's shape.
//
// Where the SDKs already settled a behaviour (endpoint resolution, the
// workspace default, waiting for a payload index) this package copies it, and
// each copy names the SDK code it copies. The CLI covers a small read-mostly
// subset: bulk writes stay the SDKs' job.
package vectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout is each request's HTTP timeout, the SDKs' DEFAULT_TIMEOUT.
// The payload-index writes get IndexAttemptTimeout instead; see index.go.
const DefaultTimeout = 30 * time.Second

const userAgent = "afy-cli/1.0"

// Client is one resolved connection: an endpoint, a key and a workspace.
type Client struct {
	endpoint  string
	apiKey    string
	workspace string
	timeout   time.Duration

	// Seams, so the index wait runs on a fake clock in tests. now must be
	// monotonic: time.Now carries Go's monotonic reading and Sub uses it, so a
	// wall-clock jump (NTP, a VM resuming) moves neither the deadline nor the
	// held-or-not check. The SDKs use time.monotonic() and performance.now().
	now    func() time.Time
	sleep  func(time.Duration)
	jitter func() float64
	send   sender
}

// sender performs one HTTP exchange bounded by timeout.
type sender func(method, url string, body []byte, headers http.Header, timeout time.Duration) (int, []byte, error)

// New builds a client for an already-resolved endpoint (see Resolve).
// workspace "" means no workspace: collections are not namespaced.
func New(endpoint, apiKey, workspace string) *Client {
	return &Client{
		endpoint:  strings.TrimRight(endpoint, "/"),
		apiKey:    apiKey,
		workspace: workspace,
		timeout:   DefaultTimeout,
		now:       time.Now,
		sleep:     time.Sleep,
		jitter:    rand.Float64,
		send:      httpSend,
	}
}

// Endpoint is the base URL requests go to, without /api/v1.
func (c *Client) Endpoint() string { return c.endpoint }

// Workspace is the workspace collection names are scoped to, "" for none.
func (c *Client) Workspace() string { return c.workspace }

func httpSend(method, target string, body []byte, headers http.Header, timeout time.Duration) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header = headers
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, data, nil
}

// apiURL joins the endpoint and a path under /api/v1, the SDKs' build_api_url.
func apiURL(endpoint, path string) string {
	return strings.TrimRight(endpoint, "/") + "/api/v1/" + strings.TrimLeft(path, "/")
}

// collectionPath is the SDKs' _build_collection_path: the nested
// workspaces/{ws}/collections/{name} form when a workspace is set, else the
// flat one. Every segment is escaped whole, "/" included, as quote(safe="")
// does, so a name cannot split into two segments.
func (c *Client) collectionPath(name, suffix string) string {
	enc := url.PathEscape(name)
	if c.workspace != "" {
		return "workspaces/" + url.PathEscape(c.workspace) + "/collections/" + enc + suffix
	}
	return "collections/" + enc + suffix
}

// collectionsPath is the SDKs' _build_collections_list_path.
func (c *Client) collectionsPath() string {
	if c.workspace != "" {
		return "workspaces/" + url.PathEscape(c.workspace) + "/collections"
	}
	return "collections"
}

func (c *Client) headers(withBody bool) http.Header {
	h := http.Header{}
	h.Set("Accept", "application/json")
	h.Set("User-Agent", userAgent)
	if withBody {
		h.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		h.Set("Authorization", "Bearer "+c.apiKey)
	}
	return h
}

// do sends one request with the given HTTP timeout and decodes a 2xx body into
// out (when out is not nil). A non-2xx answer is an *APIError; no answer is a
// *TimeoutError or a *NetworkError. Nothing is retried here.
func (c *Client) do(method, path string, body interface{}, timeout time.Duration, out interface{}) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encoding the request: %w", err)
		}
	}
	status, data, err := c.send(method, apiURL(c.endpoint, path), payload, c.headers(body != nil), timeout)
	if err != nil {
		if isTimeout(err) {
			return &TimeoutError{Message: fmt.Sprintf("Request to %s timed out after %s seconds", path, seconds(timeout))}
		}
		return &NetworkError{Message: fmt.Sprintf("Network connection failed: %v", err)}
	}
	if status < 200 || status > 299 {
		return ParseError(status, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the vectors API answered %d with a body that is not the JSON expected: %w", status, err)
	}
	return nil
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// seconds formats a duration the way the SDKs' messages print one: "600",
// "1.5", "45".
func seconds(d time.Duration) string {
	return fmt.Sprintf("%g", d.Seconds())
}

// TimeoutError is a request that got no answer within its timeout or deadline:
// the SDKs' RequestTimeoutError.
type TimeoutError struct{ Message string }

func (e *TimeoutError) Error() string { return e.Message }

// NetworkError is a request that could not be sent or answered at all: the
// SDKs' NetworkError.
type NetworkError struct{ Message string }

func (e *NetworkError) Error() string { return e.Message }

// APIError is a non-2xx answer from the vectors API, its code and message
// copied through unchanged.
//
// There is NO vectordb error registry to pin these codes against, unlike the
// control plane's shared/error_codes.py that test/cp_error_codes_test.go reads.
// vectordb writes its codes as literals in the route that raises them (about
// twenty files under backend/); middleware/errorHandler.js has an ERROR_CODES
// map, but only its global handler uses it, and most codes a route answers
// (NOT_FOUND, COLLECTION_LIST_ERROR, INVALID_POINT_ID, ...) are not in it. So
// nothing here switches on a code: the CLI prints it and exits non-zero.
type APIError struct {
	Status  int
	Code    string
	Message string
	// Details is the error object as the server sent it, extras included.
	Details map[string]interface{}
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	}
	return e.Message
}

// ParseError reads a vectordb error body, in the shapes the Python SDK's
// parse_error_response reads:
//
//	{"error": {"code": "...", "message": "...", **extras}}  canonical
//	{"error": "<message>", "error_code": "..."}             string-shaped
//	{"message": "...", "error_code": "..."}                 flat
//
// and anything else (a gateway's HTML page, an empty body) as its text, or
// "HTTP <status>" when there is none.
func ParseError(status int, body []byte) *APIError {
	e := &APIError{Status: status}
	var obj map[string]interface{}
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		text := strings.TrimSpace(string(body))
		if text == "" {
			text = fmt.Sprintf("HTTP %d", status)
		}
		e.Message = text
		return e
	}
	switch v := obj["error"].(type) {
	case map[string]interface{}:
		e.Message, _ = v["message"].(string)
		e.Code, _ = v["code"].(string)
		e.Details = v
	case string:
		e.Message = v
		e.Code, _ = obj["error_code"].(string)
	default:
		e.Message, _ = obj["message"].(string)
		e.Code, _ = obj["error_code"].(string)
	}
	if e.Message == "" {
		e.Message = "Unknown error"
	}
	return e
}

// retryable is the SDKs' is_retryable_error: a service that is unavailable, a
// request that timed out or never connected, or a 429 that says when to come
// back.
func retryable(err error) bool {
	var te *TimeoutError
	var ne *NetworkError
	if errors.As(err, &te) || errors.As(err, &ne) {
		return true
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Status {
	case 502, 503, 504:
		return true
	case 429:
		// A storage quota is not a rate limit, and waiting does not lift it:
		// the SDK raises QuotaExceededError for it, which it never retries.
		if ae.Code == "STORAGE_LIMIT_EXCEEDED" {
			return false
		}
		return ae.Details["retry_after"] != nil
	}
	return false
}
