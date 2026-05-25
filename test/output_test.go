package test

import (
	"bytes"
	"testing"

	"github.com/aetherfy/cli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToJSON(t *testing.T) {
	data := map[string]interface{}{
		"name":   "test-agent",
		"status": "running",
	}

	result, err := output.ToJSON(data)
	require.NoError(t, err)

	assert.Contains(t, result, `"name"`)
	assert.Contains(t, result, `"test-agent"`)
	assert.Contains(t, result, `"status"`)
	assert.Contains(t, result, `"running"`)
}

func TestToJSON_Pretty(t *testing.T) {
	data := map[string]string{"key": "value"}

	result, err := output.ToJSON(data)
	require.NoError(t, err)

	// Should be indented (pretty printed)
	assert.Contains(t, result, "\n")
	assert.Contains(t, result, "  ")
}

func TestRenderTable(t *testing.T) {
	var buf bytes.Buffer

	headers := []string{"NAME", "STATUS", "REGION"}
	rows := [][]string{
		{"agent-1", "running", "us-east-1"},
		{"agent-2", "stopped", "eu-central-1"},
	}

	output.RenderTable(&buf, headers, rows)
	result := buf.String()

	assert.Contains(t, result, "NAME")
	assert.Contains(t, result, "STATUS")
	assert.Contains(t, result, "agent-1")
	assert.Contains(t, result, "running")
	assert.Contains(t, result, "agent-2")
}

func TestRenderTable_Empty(t *testing.T) {
	var buf bytes.Buffer

	headers := []string{"NAME", "STATUS"}
	rows := [][]string{}

	output.RenderTable(&buf, headers, rows)
	result := buf.String()

	// Should still have headers
	assert.Contains(t, result, "NAME")
}

func TestStatusColor(t *testing.T) {
	tests := []struct {
		status string
		color  string
	}{
		{"running", "green"},
		{"deployed", "green"},
		{"stopped", "yellow"},
		{"pending", "yellow"},
		{"failed", "red"},
		{"error", "red"},
		{"unknown", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			color := output.StatusColor(tt.status)
			// Just verify it doesn't panic and returns a function
			assert.NotNil(t, color)
		})
	}
}

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ab", "***"},          // <= 3 chars: fully masked
		{"abc", "***"},         // <= 3 chars: fully masked
		{"abcd", "abc***"},     // > 3 chars: show first 3
		{"longsecret", "lon***"},
		{"", "***"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := output.MaskSecret(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{30, "30s"},
		{90, "1m 30s"},
		{3600, "1h 0m"},
		{3661, "1h 1m"},
		{86400, "24h 0m"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := output.FormatDuration(tt.seconds)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := output.FormatBytes(tt.bytes)
			assert.Equal(t, tt.want, got)
		})
	}
}
