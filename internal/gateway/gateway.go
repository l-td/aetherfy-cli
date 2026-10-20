// Package gateway reads the AI Gateway's public catalogue, for `afy gateway
// models`. Everything else `afy gateway` does goes through the control plane
// (internal/api); the catalogue is the one thing the gateway serves itself,
// publicly, so this talks to the gateway host directly and needs no key.
//
// The endpoint is chosen as internal/vectors/resolve.go chooses the vectors
// one: --gateway-url, else AETHERFY_GATEWAY_URL, else DefaultEndpoint.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DefaultEndpoint is the gateway's public host.
const DefaultEndpoint = "https://gateway.aetherfy.com"

// EnvGatewayURL overrides DefaultEndpoint.
const EnvGatewayURL = "AETHERFY_GATEWAY_URL"

// Where the endpoint came from, as the JSON output names it.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceDefault = "default"
)

// Timeout bounds the one catalogue request.
const Timeout = 15 * time.Second

// Resolve picks the endpoint: the flag, then the environment, then the default.
func Resolve(flag string, getenv func(string) string) (endpoint, source string) {
	switch {
	case flag != "":
		endpoint, source = flag, SourceFlag
	case getenv(EnvGatewayURL) != "":
		endpoint, source = getenv(EnvGatewayURL), SourceEnv
	default:
		endpoint, source = DefaultEndpoint, SourceDefault
	}
	return strings.TrimRight(endpoint, "/"), source
}

// Prices are one model's list prices in USD per 1M tokens, as decimal strings
// (exact, as the gateway computes with them); nil where not published.
type Prices struct {
	Input           *string `json:"input"`
	Output          *string `json:"output"`
	CacheRead       *string `json:"cache_read"`
	CacheWrite5m    *string `json:"cache_write_5m"`
	CacheWrite1h    *string `json:"cache_write_1h"`
	ReasoningOutput *string `json:"reasoning_output"`
}

// Tier is a long-context tier: its prices apply above AboveInputTokens.
type Tier struct {
	AboveInputTokens int64  `json:"above_input_tokens"`
	PricesPer1M      Prices `json:"prices_per_1m"`
}

// Model is one catalogue row.
type Model struct {
	Provider        string   `json:"provider"`
	Formats         []string `json:"formats"`
	Mode            string   `json:"mode"`
	ContextWindow   *int64   `json:"context_window"`
	MaxOutputTokens *int64   `json:"max_output_tokens"`
	PricesPer1M     Prices   `json:"prices_per_1m"`
	Tiers           []Tier   `json:"tiers"`
	SourceURL       string   `json:"source_url"`
	AsOf            string   `json:"as_of"`
}

// Catalogue is GET <gateway>/v1/catalogue.
type Catalogue struct {
	PriceVersion string           `json:"price_version"`
	Currency     string           `json:"currency"`
	Unit         string           `json:"unit"`
	Models       map[string]Model `json:"models"`
}

// IDs answers the model ids, sorted.
func (c *Catalogue) IDs() []string {
	ids := make([]string, 0, len(c.Models))
	for id := range c.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Fetch reads the catalogue from endpoint.
func Fetch(ctx context.Context, client *http.Client, endpoint string) (*Catalogue, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/catalogue", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the AI Gateway at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the AI Gateway at %s answered %d for its catalogue", endpoint, resp.StatusCode)
	}
	var c Catalogue
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("the AI Gateway's catalogue is not the expected JSON: %w", err)
	}
	return &c, nil
}
