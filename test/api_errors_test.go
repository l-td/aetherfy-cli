package test

import (
	"testing"

	"github.com/aetherfy/cli/internal/api"
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
	}{
		{
			name:       "json error with detail",
			body:       `{"detail": "Invalid API key"}`,
			statusCode: 401,
			wantMsg:    "Invalid API key",
		},
		{
			name:       "json error with message",
			body:       `{"message": "Not found"}`,
			statusCode: 404,
			wantMsg:    "Not found",
		},
		{
			name:       "json error with error field",
			body:       `{"error": "Something went wrong"}`,
			statusCode: 500,
			wantMsg:    "Something went wrong",
		},
		{
			name:       "plain text error",
			body:       "Bad Request",
			statusCode: 400,
			wantMsg:    "Bad Request",
		},
		{
			name:       "empty body",
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
		})
	}
}
