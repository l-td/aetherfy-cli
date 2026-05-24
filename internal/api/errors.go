package api

// Canonical control-plane error envelope:
//
//	{"detail": {"code": "STABLE_CODE", "message": "...", **extras}}
//
// All API error responses from the control-plane — including FastAPI's
// pydantic 422 validation errors (wrapped by a RequestValidationError
// exception_handler in main.py that emits
// `{"detail": {"code": "VALIDATION_ERROR", "message": "...", "violations": [...]}}`)
// — use this nested dict shape. `code` is one of the stable strings
// defined in aetherfy-control-plane/shared/error_codes.py (e.g.
// AGENT_NOT_FOUND, WORKSPACE_NAME_TAKEN, AUTH_INVALID_API_KEY) and is
// append-only — clients pin literal strings and switch on them.
//
// The CLI therefore ASSUMES `detail` is a JSON object. The only path
// that does not produce a JSON object body is a transport-level failure
// (e.g. Cloudflare returning a 502 HTML page, an empty body, or a
// non-JSON gateway response) — for those we fall back to a default
// status-code message via statusCodeMessage.

import (
	"encoding/json"
	"fmt"

	"github.com/go-resty/resty/v2"
)

// APIError represents an error response from the API
type APIError struct {
	StatusCode int    `json:"-"`
	Message    string `json:"detail"`
	Code       string `json:"code,omitempty"`
	Details    string `json:"details,omitempty"`
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("[%d] %s (%s)", e.StatusCode, e.Message, e.Code)
	}
	return fmt.Sprintf("[%d] %s", e.StatusCode, e.Message)
}

// IsUnauthorized returns true if the error is a 401
func (e *APIError) IsUnauthorized() bool {
	return e.StatusCode == 401
}

// IsNotFound returns true if the error is a 404
func (e *APIError) IsNotFound() bool {
	return e.StatusCode == 404
}

// IsRateLimited returns true if the error is a 429
func (e *APIError) IsRateLimited() bool {
	return e.StatusCode == 429
}

// IsServerError returns true if the error is a 5xx
func (e *APIError) IsServerError() bool {
	return e.StatusCode >= 500 && e.StatusCode < 600
}

// parseAPIError extracts error details from a response
func parseAPIError(resp *resty.Response) error {
	apiErr := &APIError{
		StatusCode: resp.StatusCode(),
		Message:    "Unknown error",
	}

	// Try to parse error body
	if len(resp.Body()) > 0 {
		var errBody struct {
			Detail interface{} `json:"detail"`
		}
		if err := json.Unmarshal(resp.Body(), &errBody); err == nil {
			switch v := errBody.Detail.(type) {
			case map[string]interface{}:
				// Canonical control-plane envelope:
				// {"detail": {"code": "...", "message": "...", **extras}}
				if msg, ok := v["message"].(string); ok && msg != "" {
					apiErr.Message = msg
				} else if raw, err := json.Marshal(v); err == nil {
					// Fall back to serialized dict so display still shows something useful
					apiErr.Message = string(raw)
				}
				if code, ok := v["code"].(string); ok && code != "" {
					apiErr.Code = code
				}
			default:
				// Unexpected shape — serialize whatever we got so it isn't
				// dropped silently. The control-plane should never emit
				// this; if we see it, something is misconfigured upstream.
				if v != nil {
					if raw, err := json.Marshal(v); err == nil {
						apiErr.Message = string(raw)
					}
				}
			}
		}
	}

	// Default messages for common status codes — used when the body is
	// empty or non-JSON (transport-level errors: Cloudflare 502 HTML,
	// gateway timeouts, network failures, etc.)
	if apiErr.Message == "Unknown error" {
		switch resp.StatusCode() {
		case 400:
			apiErr.Message = "Bad request"
		case 401:
			apiErr.Message = "Authentication required - run 'afy login'"
		case 403:
			apiErr.Message = "Access denied - check your permissions"
		case 404:
			apiErr.Message = "Resource not found"
		case 409:
			apiErr.Message = "Resource already exists"
		case 422:
			apiErr.Message = "Validation failed"
		case 429:
			apiErr.Message = "Rate limit exceeded - please wait and try again"
		case 500:
			apiErr.Message = "Server error - please try again later"
		case 502, 503, 504:
			apiErr.Message = "Service temporarily unavailable"
		}
	}

	return apiErr
}

// IsNotFound returns true if the error is a 404
func IsNotFound(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == 404
	}
	return false
}

// IsUnauthorized returns true if the error is a 401
func IsUnauthorized(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == 401
	}
	return false
}

// IsRateLimited returns true if the error is a 429
func IsRateLimited(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == 429
	}
	return false
}

// ParseErrorResponse creates an APIError from status code and body
func ParseErrorResponse(statusCode int, body []byte) *APIError {
	apiErr := &APIError{
		StatusCode: statusCode,
		Message:    "Unknown error",
	}

	if len(body) == 0 {
		apiErr.Message = statusCodeMessage(statusCode)
		return apiErr
	}

	// Try to parse JSON. Non-JSON bodies are transport-level (Cloudflare
	// HTML error pages, plain-text proxy errors, etc.) — pass them through.
	var errBody map[string]interface{}
	if err := json.Unmarshal(body, &errBody); err != nil {
		apiErr.Message = string(body)
		return apiErr
	}

	// Canonical control-plane envelope:
	// {"detail": {"code": "...", "message": "...", **extras}}
	switch v := errBody["detail"].(type) {
	case map[string]interface{}:
		if msg, ok := v["message"].(string); ok && msg != "" {
			apiErr.Message = msg
		} else if raw, err := json.Marshal(v); err == nil {
			apiErr.Message = string(raw)
		}
		if code, ok := v["code"].(string); ok && code != "" {
			apiErr.Code = code
		}
	default:
		// Unexpected shape — serialize whatever we got so it isn't
		// dropped silently. The control-plane should never emit this.
		if v != nil {
			if raw, err := json.Marshal(v); err == nil {
				apiErr.Message = string(raw)
			}
		}
	}

	return apiErr
}

func statusCodeMessage(code int) string {
	switch code {
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 429:
		return "Rate Limited"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	default:
		return fmt.Sprintf("HTTP %d", code)
	}
}
