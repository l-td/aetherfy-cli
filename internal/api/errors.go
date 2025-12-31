package api

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
			Code   string      `json:"code"`
		}
		if err := json.Unmarshal(resp.Body(), &errBody); err == nil {
			switch v := errBody.Detail.(type) {
			case string:
				apiErr.Message = v
			case []interface{}:
				// Validation errors (FastAPI format)
				if len(v) > 0 {
					if first, ok := v[0].(map[string]interface{}); ok {
						if msg, ok := first["msg"].(string); ok {
							apiErr.Message = msg
						}
					}
				}
			}
			apiErr.Code = errBody.Code
		}
	}

	// Default messages for common status codes
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

	// Try to parse JSON
	var errBody map[string]interface{}
	if err := json.Unmarshal(body, &errBody); err != nil {
		// Plain text error
		apiErr.Message = string(body)
		return apiErr
	}

	// Try various JSON fields
	if detail, ok := errBody["detail"].(string); ok {
		apiErr.Message = detail
	} else if message, ok := errBody["message"].(string); ok {
		apiErr.Message = message
	} else if errMsg, ok := errBody["error"].(string); ok {
		apiErr.Message = errMsg
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
