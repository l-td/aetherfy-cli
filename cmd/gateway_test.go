package cmd

// `afy gateway` against an httptest control plane and an httptest gateway.
// What matters most is what goes on the wire: `set` must send exactly the
// fields the user named (a field left out is "unchanged" to the control plane,
// a null is "clear it"), money as a JSON number, and every refusal must happen
// before any request (exit 2), so a typo never half-applies.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/gateway"
)

// cpCall is one request the stand-in control plane saw.
type cpCall struct {
	Method, Path, RawQuery string
	Body                   map[string]json.RawMessage
}

// gatewayCP answers the /gateway/* routes with fixed bodies and records every
// call, so a test can assert both the rendering and the wire.
func gatewayCP(t *testing.T) (*api.Client, func() []cpCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []cpCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := cpCall{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.Body); err != nil {
				t.Errorf("request body is not a JSON object: %s", raw)
			}
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/gateway/settings":
			_, _ = w.Write([]byte(`{"monthly_budget_usd":50.0,"month_to_date_usd":0.00012345,"month_start":"2026-09-01T00:00:00Z"}`))
		case r.URL.Path == "/gateway/agents":
			_, _ = w.Write([]byte(`{"month_start":"2026-09-01T00:00:00Z","agents":[` +
				`{"agent_id":"a1","name":"reporter","monthly_budget_usd":10.5,"allowed_models":["gpt-4o-mini"],"month_to_date_usd":1.25},` +
				`{"agent_id":"a2","name":"scraper","monthly_budget_usd":null,"allowed_models":null,"month_to_date_usd":0}]}`))
		case strings.HasPrefix(r.URL.Path, "/gateway/agents/"):
			_, _ = w.Write([]byte(`{"agent_id":"a1","name":"reporter","monthly_budget_usd":null,"allowed_models":null,"month_to_date_usd":1.25}`))
		case r.URL.Path == "/gateway/usage":
			_, _ = w.Write([]byte(`{"since":"2026-09-01T00:00:00Z","until":"2026-09-28T00:00:00Z","group_by":"agent","agent_id":null,` +
				`"rows":[{"key":"a1","agent_name":"reporter","requests":3,"input_tokens":1200,"output_tokens":80,` +
				`"cache_read_input_tokens":1000,"cache_write_input_tokens":0,"reasoning_output_tokens":0,"cost_usd":0.0042,` +
				`"unpriced_requests":1,"aborted_requests":0,"error_requests":2},` +
				`{"key":null,"agent_name":null,"requests":1,"input_tokens":10,"output_tokens":5,` +
				`"cache_read_input_tokens":0,"cache_write_input_tokens":0,"reasoning_output_tokens":0,"cost_usd":0.0001,` +
				`"unpriced_requests":0,"aborted_requests":1,"error_requests":0}],` +
				`"totals":{"key":null,"agent_name":null,"requests":4,"input_tokens":1210,"output_tokens":85,` +
				`"cache_read_input_tokens":1000,"cache_write_input_tokens":0,"reasoning_output_tokens":0,"cost_usd":0.0043,` +
				`"unpriced_requests":1,"aborted_requests":1,"error_requests":2}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":{"code":"NOT_FOUND","message":"no such route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return api.NewClientWithURL(srv.URL, "afy_test_key"), func() []cpCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]cpCall(nil), calls...)
	}
}

func withOutputFormat(t *testing.T, format string) {
	t.Helper()
	previous := config.Get().OutputFormat
	config.SetOutputFormat(format)
	t.Cleanup(func() { config.SetOutputFormat(previous) })
}

// setFlags sets the package-level `set` flag values for one test.
func setFlags(t *testing.T, budget, models string) {
	t.Helper()
	oldB, oldM := gatewayBudget, gatewayModels
	gatewayBudget, gatewayModels = budget, models
	t.Cleanup(func() { gatewayBudget, gatewayModels = oldB, oldM })
}

func isRefusal(err error) bool {
	var in *inputError
	return errors.As(err, &in)
}

// --- status -------------------------------------------------------------------

func TestGatewayStatusShowsTheAccountAndEveryAgent(t *testing.T) {
	client, _ := gatewayCP(t)
	withOutputFormat(t, "text")
	var err error
	out := captureStdout(t, func() { err = runGatewayStatus(client, nil) })
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{
		"$50.00",      // the account budget
		"$0.00012345", // a tiny spend keeps its digits, never $0.00
		"list-price estimate",
		"reporter", "$10.50", "gpt-4o-mini",
		"scraper", "none", "all", // no budget, every model
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output lacks %q:\n%s", want, out)
		}
	}
}

func TestGatewayStatusOfOneAgentAsJSONKeepsNulls(t *testing.T) {
	client, _ := gatewayCP(t)
	withOutputFormat(t, "json")
	var err error
	out := captureStdout(t, func() { err = runGatewayStatus(client, []string{"scraper"}) })
	if err != nil {
		t.Fatalf("status scraper: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out)
	}
	// A null budget means "no budget": dropping the key would make it
	// indistinguishable from a field the CLI forgot.
	for _, key := range []string{"monthly_budget_usd", "allowed_models"} {
		v, ok := got[key]
		if !ok || v != nil {
			t.Errorf("%s = %v (present %v), want an explicit null", key, v, ok)
		}
	}
}

func TestGatewayStatusOfAnUnknownAgentFails(t *testing.T) {
	client, _ := gatewayCP(t)
	withOutputFormat(t, "text")
	var err error
	_ = captureStdout(t, func() { err = runGatewayStatus(client, []string{"ghost"}) })
	if err == nil || isRefusal(err) {
		t.Fatalf("err = %v, want a request-side failure (exit 1)", err)
	}
}

// --- set ----------------------------------------------------------------------

func TestGatewaySetSendsExactlyTheNamedFields(t *testing.T) {
	cases := []struct {
		name                string
		args                []string
		budget, models      string
		budgetSet, modelSet bool
		path                string
		want                map[string]string // field -> raw JSON
	}{
		{"account budget", nil, "25.5", "", true, false, "/gateway/settings",
			map[string]string{"monthly_budget_usd": "25.5"}},
		{"account budget removed", nil, "none", "", true, false, "/gateway/settings",
			map[string]string{"monthly_budget_usd": "null"}},
		{"agent models only", []string{"reporter"}, "", "gpt-4o-mini, claude-opus-5-5", false, true, "/gateway/agents/reporter",
			map[string]string{"allowed_models": `["gpt-4o-mini","claude-opus-5-5"]`}},
		{"agent budget only", []string{"reporter"}, "0", "", true, false, "/gateway/agents/reporter",
			map[string]string{"monthly_budget_usd": "0"}},
		{"agent both cleared", []string{"reporter"}, "none", "all", true, true, "/gateway/agents/reporter",
			map[string]string{"monthly_budget_usd": "null", "allowed_models": "null"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, calls := gatewayCP(t)
			withOutputFormat(t, "text")
			setFlags(t, tc.budget, tc.models)
			var err error
			_ = captureStdout(t, func() { err = runGatewaySet(client, tc.args, tc.budgetSet, tc.modelSet) })
			if err != nil {
				t.Fatalf("set: %v", err)
			}
			got := calls()
			if len(got) != 1 || got[0].Method != http.MethodPut || got[0].Path != tc.path {
				t.Fatalf("calls = %+v, want one PUT %s", got, tc.path)
			}
			if len(got[0].Body) != len(tc.want) {
				t.Errorf("body has fields %v, want exactly %v", keys(got[0].Body), tc.want)
			}
			for field, raw := range tc.want {
				if string(got[0].Body[field]) != raw {
					t.Errorf("%s = %s, want %s", field, got[0].Body[field], raw)
				}
			}
		})
	}
}

func keys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestGatewaySetRefusesBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name                string
		args                []string
		budget, models      string
		budgetSet, modelSet bool
	}{
		{"nothing to set", []string{"reporter"}, "", "", false, false},
		{"models without an agent", nil, "", "gpt-4o-mini", false, true},
		{"fractions of a cent", nil, "12.345", "", true, false},
		{"negative", nil, "-1", "", true, false},
		{"a word", nil, "lots", "", true, false},
		{"over the column", nil, "10000000000", "", true, false},
		{"an empty model id", []string{"reporter"}, "", "gpt-4o-mini,,x", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, calls := gatewayCP(t)
			setFlags(t, tc.budget, tc.models)
			err := runGatewaySet(client, tc.args, tc.budgetSet, tc.modelSet)
			if !isRefusal(err) {
				t.Errorf("err = %v, want a refusal (exit 2)", err)
			}
			if n := len(calls()); n != 0 {
				t.Errorf("%d request(s) sent; a refusal must send none", n)
			}
		})
	}
}

// The negative control for the refusal table: a valid value on the same path
// does reach the server, so "no request" above is the refusal, not a harness
// that never sends.
func TestGatewaySetAcceptsWholeCents(t *testing.T) {
	client, calls := gatewayCP(t)
	withOutputFormat(t, "text")
	setFlags(t, "12.34", "")
	_ = captureStdout(t, func() {
		if err := runGatewaySet(client, nil, true, false); err != nil {
			t.Errorf("set 12.34: %v", err)
		}
	})
	if n := len(calls()); n != 1 {
		t.Errorf("%d request(s), want 1", n)
	}
}

// --- usage --------------------------------------------------------------------

func TestGatewayUsageAsksForTheWindowAndRendersTotals(t *testing.T) {
	client, calls := gatewayCP(t)
	withOutputFormat(t, "text")
	oldBy, oldSince, oldAgent := gatewayUsageBy, gatewayUsageSince, gatewayUsageAgent
	gatewayUsageBy, gatewayUsageSince, gatewayUsageAgent = "agent", "7d", "reporter"
	t.Cleanup(func() { gatewayUsageBy, gatewayUsageSince, gatewayUsageAgent = oldBy, oldSince, oldAgent })

	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	var err error
	out := captureStdout(t, func() { err = runGatewayUsage(client, now) })
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	got := calls()
	if len(got) != 1 {
		t.Fatalf("calls = %+v", got)
	}
	q := got[0].RawQuery
	for _, want := range []string{"group_by=agent", "agent=reporter", "since=2026-09-21T12%3A00%3A00Z"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %q", q, want)
		}
	}
	for _, want := range []string{"reporter", "(account key)", "Total", "$0.0043", "1 call(s) unpriced", "1 aborted", "2 refused or failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage output lacks %q:\n%s", want, out)
		}
	}
}

func TestGatewayUsageRefusesAnUnknownGrouping(t *testing.T) {
	client, calls := gatewayCP(t)
	old := gatewayUsageBy
	gatewayUsageBy = "provider"
	t.Cleanup(func() { gatewayUsageBy = old })
	if err := runGatewayUsage(client, time.Now()); !isRefusal(err) {
		t.Errorf("err = %v, want a refusal", err)
	}
	if n := len(calls()); n != 0 {
		t.Errorf("%d request(s) sent", n)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"30d":                       "2026-08-29T12:00:00Z",
		"12h":                       "2026-09-28T00:00:00Z",
		"90m":                       "2026-09-28T10:30:00Z",
		"2026-09-01":                "2026-09-01T00:00:00Z",
		"2026-09-01T02:00:00+02:00": "2026-09-01T00:00:00Z",
	} {
		got, err := parseSince(in, now)
		if err != nil || got != want {
			t.Errorf("parseSince(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "7", "7w", "yesterday", "-3d"} {
		if _, err := parseSince(bad, now); !isRefusal(err) {
			t.Errorf("parseSince(%q) err = %v, want a refusal", bad, err)
		}
	}
}

// --- models -------------------------------------------------------------------

const catalogueJSON = `{"price_version":"litellm@abc+overrides@12345678","currency":"USD","unit":"per_1m_tokens","models":{` +
	`"gpt-4o-mini":{"provider":"openai","formats":["openai"],"mode":"chat","context_window":128000,"max_output_tokens":16384,` +
	`"prices_per_1m":{"input":"0.15","output":"0.6","cache_read":"0.075","cache_write_5m":null,"cache_write_1h":null,"reasoning_output":null},` +
	`"tiers":[],"source_url":"https://openai.com/api/pricing","as_of":"2026-09-28"}}}`

func catalogueServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/catalogue" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-aetherfy-api-key") != "" {
			t.Errorf("the public catalogue was sent a credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalogueJSON))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGatewayModelsReadsThePublicCatalogue(t *testing.T) {
	srv := catalogueServer(t)
	withOutputFormat(t, "json")
	old := gatewayURL
	gatewayURL = srv.URL + "/"
	t.Cleanup(func() { gatewayURL = old })

	var err error
	out := captureStdout(t, func() {
		err = runGatewayModels(srv.Client(), func(string) string { return "http://env.invalid" })
	})
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	var got struct {
		Endpoint       string            `json:"endpoint"`
		EndpointSource string            `json:"endpoint_source"`
		Catalogue      gateway.Catalogue `json:"catalogue"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out)
	}
	if got.Endpoint != srv.URL || got.EndpointSource != gateway.SourceFlag {
		t.Errorf("endpoint = %q from %q, want %q from the flag", got.Endpoint, got.EndpointSource, srv.URL)
	}
	if p := got.Catalogue.Models["gpt-4o-mini"].PricesPer1M.Input; p == nil || *p != "0.15" {
		t.Errorf("input price = %v, want the exact decimal string 0.15", p)
	}
}

func TestGatewayModelsFailsOnANon200(t *testing.T) {
	srv := catalogueServer(t)
	withOutputFormat(t, "text")
	old := gatewayURL
	gatewayURL = srv.URL + "/nowhere"
	t.Cleanup(func() { gatewayURL = old })
	var err error
	_ = captureStdout(t, func() { err = runGatewayModels(srv.Client(), func(string) string { return "" }) })
	if err == nil || isRefusal(err) {
		t.Errorf("err = %v, want a request-side failure", err)
	}
}

func TestGatewayEndpointPrecedence(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == gateway.EnvGatewayURL {
				return v
			}
			return ""
		}
	}
	for _, tc := range []struct{ flag, env, want, source string }{
		{"https://flag.example/", "https://env.example", "https://flag.example", gateway.SourceFlag},
		{"", "https://env.example", "https://env.example", gateway.SourceEnv},
		{"", "", gateway.DefaultEndpoint, gateway.SourceDefault},
	} {
		got, source := gateway.Resolve(tc.flag, env(tc.env))
		if got != tc.want || source != tc.source {
			t.Errorf("Resolve(%q, env %q) = %q, %q; want %q, %q", tc.flag, tc.env, got, source, tc.want, tc.source)
		}
	}
}

// --- formatting -----------------------------------------------------------------

func TestUSDNeverRoundsASpendAway(t *testing.T) {
	for in, want := range map[float64]string{
		0:          "$0.00",
		1.5:        "$1.50",
		12:         "$12.00",
		0.00012345: "$0.00012345",
		0.1:        "$0.10",
	} {
		if got := usd(in); got != want {
			t.Errorf("usd(%v) = %q, want %q", in, got, want)
		}
	}
}
