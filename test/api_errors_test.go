package test

import (
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestAPIError_Error(t *testing.T) {
	err := &api.APIError{
		StatusCode: 404,
		Message:    "Agent not found",
		Details:    "No agent with ID 'test-123'",
	}

	errStr := err.Error()
	assert.Contains(t, errStr, "404")
	assert.Contains(t, errStr, "Agent not found")
}

func TestAPIError_Is401(t *testing.T) {
	err := &api.APIError{StatusCode: 401, Message: "Unauthorized"}
	assert.True(t, err.IsUnauthorized())
	assert.False(t, err.IsNotFound())
	assert.False(t, err.IsRateLimited())
}

func TestAPIError_Is404(t *testing.T) {
	err := &api.APIError{StatusCode: 404, Message: "Not found"}
	assert.True(t, err.IsNotFound())
	assert.False(t, err.IsUnauthorized())
}

func TestAPIError_Is429(t *testing.T) {
	err := &api.APIError{StatusCode: 429, Message: "Rate limited"}
	assert.True(t, err.IsRateLimited())
	assert.False(t, err.IsNotFound())
}

func TestAPIError_IsServerError(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{200, false},
		{400, false},
		{404, false},
		{500, true},
		{502, true},
		{503, true},
	}

	for _, tt := range tests {
		err := &api.APIError{StatusCode: tt.code}
		assert.Equal(t, tt.want, err.IsServerError())
	}
}

func TestParseErrorResponse(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		statusCode int
		wantMsg    string
		wantCode   string
	}{
		{
			name:       "canonical envelope with code and message",
			body:       `{"detail":{"code":"INVALID_API_KEY","message":"Invalid API key"}}`,
			statusCode: 401,
			wantMsg:    "Invalid API key",
			wantCode:   "INVALID_API_KEY",
		},
		{
			name:       "canonical envelope with extras",
			body:       `{"detail":{"code":"AGENT_NOT_FOUND","message":"No agent with id 'a1'","agent_id":"a1"}}`,
			statusCode: 404,
			wantMsg:    "No agent with id 'a1'",
			wantCode:   "AGENT_NOT_FOUND",
		},
		{
			name:       "canonical validation envelope (422 wrapped by exception_handler)",
			body:       `{"detail":{"code":"VALIDATION_ERROR","message":"name is required","violations":[{"loc":["body","name"],"msg":"name is required"}]}}`,
			statusCode: 422,
			wantMsg:    "name is required",
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "plain text body (transport-level, e.g. proxy error)",
			body:       "Bad Request",
			statusCode: 400,
			wantMsg:    "Bad Request",
		},
		{
			name:       "empty body falls back to status-code message",
			body:       "",
			statusCode: 500,
			wantMsg:    "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := api.ParseErrorResponse(tt.statusCode, []byte(tt.body))
			assert.Equal(t, tt.statusCode, err.StatusCode)
			assert.Contains(t, err.Message, tt.wantMsg)
			if tt.wantCode != "" {
				assert.Equal(t, tt.wantCode, err.Code)
			}
		})
	}
}
