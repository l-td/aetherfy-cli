package vectors

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Where `afy collections|index|points` send their requests, and which
// workspace the names belong to, in the SDKs' order
// (aetherfy_vectors/client.py AetherfyVectorsClient.__init__).

type discoveryStub struct {
	status int
	body   string
	calls  []string
	auth   []string
}

func (d *discoveryStub) send(method, url string, _ []byte, h http.Header, _ time.Duration) (int, []byte, error) {
	d.calls = append(d.calls, method+" "+url)
	d.auth = append(d.auth, h.Get("Authorization"))
	return d.status, []byte(d.body), nil
}

func resolver(env map[string]string, d *discoveryStub) *Resolver {
	return &Resolver{
		Getenv:        func(k string) string { return env[k] },
		DiscoveryBase: "https://global.test",
		APIKey:        "afy_test_key",
		send:          d.send,
	}
}

const regionsBody = `{"us-east-1":"https://use1.test","eu-central-1":"https://euc1.test/"}`

func TestTheEndpointIsFlagThenEnvThenRegionThenDefault(t *testing.T) {
	cases := []struct {
		name       string
		opts       Options
		env        map[string]string
		endpoint   string
		source     string
		discovered bool
	}{
		{"flag beats env and region", Options{Endpoint: "https://flag.test/", APIRegion: "us-east-1"},
			map[string]string{EnvVectorsURL: "https://env.test"}, "https://flag.test", SourceFlag, false},
		{"env beats region", Options{APIRegion: "us-east-1"},
			map[string]string{EnvVectorsURL: "https://env.test"}, "https://env.test", SourceEnv, false},
		{"region flag is discovered", Options{APIRegion: "eu-central-1"},
			nil, "https://euc1.test", SourceRegion, true},
		{"region env is discovered", Options{},
			map[string]string{EnvAPIRegion: "us-east-1"}, "https://use1.test", SourceRegion, true},
		{"region flag beats region env", Options{APIRegion: "eu-central-1"},
			map[string]string{EnvAPIRegion: "us-east-1"}, "https://euc1.test", SourceRegion, true},
		{"nothing is the default", Options{}, nil, DefaultEndpoint, SourceDefault, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &discoveryStub{status: 200, body: regionsBody}
			got, err := resolver(tc.env, d).Resolve(tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got.Endpoint != tc.endpoint || got.EndpointSource != tc.source {
				t.Errorf("resolved %s (%s), want %s (%s)", got.Endpoint, got.EndpointSource, tc.endpoint, tc.source)
			}
			if tc.discovered != (len(d.calls) == 1) {
				t.Errorf("discovery calls = %v, want discovery %v", d.calls, tc.discovered)
			}
			if tc.discovered && (d.calls[0] != "GET https://global.test/api/v1/regions" || d.auth[0] != "Bearer afy_test_key") {
				t.Errorf("discovery = %v with %v, want GET /api/v1/regions on the global endpoint with the key", d.calls, d.auth)
			}
		})
	}
}

func TestAnEnvURLOverAnAPIRegionWarns(t *testing.T) {
	got, err := resolver(map[string]string{EnvVectorsURL: "https://env.test"}, &discoveryStub{}).
		Resolve(Options{APIRegion: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Warning, "Both AETHERFY_VECTORS_URL and api_region=us-east-1 are set; using AETHERFY_VECTORS_URL") {
		t.Errorf("warning = %q", got.Warning)
	}
}

func TestAnUnknownAPIRegionIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, o := range []struct {
		opts Options
		env  map[string]string
	}{
		{Options{APIRegion: "us-west-2"}, nil},
		{Options{}, map[string]string{EnvAPIRegion: "us-west-2"}},
	} {
		d := &discoveryStub{status: 200, body: regionsBody}
		_, err := resolver(o.env, d).Resolve(o.opts)
		var ir *InvalidRegionError
		if !errors.As(err, &ir) || err.Error() != "api_region must be one of us-east-1, eu-central-1, ap-southeast-1, got 'us-west-2'" {
			t.Errorf("err = %v", err)
		}
		if len(d.calls) != 0 {
			t.Errorf("a refused region still asked discovery: %v", d.calls)
		}
	}
}

func TestDiscoveryFailuresSayWhatFailed(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"refused":        {401, `{}`, "Region discovery returned 401 from https://global.test/api/v1/regions."},
		"not json":       {200, `<html>`, "Region discovery returned non-JSON body"},
		"region missing": {200, `{"us-east-1":"https://use1.test"}`, "Region 'eu-central-1' not configured at the discovery endpoint (available: [us-east-1])."},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolver(nil, &discoveryStub{status: tc.status, body: tc.body}).Resolve(Options{APIRegion: "eu-central-1"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestTheWorkspaceIsFlagThenEnvThenNone(t *testing.T) {
	env := map[string]string{EnvWorkspace: "from-env"}
	for name, tc := range map[string]struct {
		opts Options
		env  map[string]string
		want string
	}{
		"flag":                        {Options{Workspace: "from-flag", WorkspaceSet: true}, env, "from-flag"},
		"env, the SDKs' auto":         {Options{}, env, "from-env"},
		"none":                        {Options{}, nil, ""},
		"an empty flag forces none":   {Options{Workspace: "", WorkspaceSet: true}, env, ""},
		"an unset flag is not a flag": {Options{Workspace: "", WorkspaceSet: false}, env, "from-env"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := resolver(tc.env, &discoveryStub{}).Resolve(tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got.Workspace != tc.want {
				t.Errorf("workspace = %q, want %q", got.Workspace, tc.want)
			}
		})
	}
}

func TestTheDefaultEndpointIsTheSDKs(t *testing.T) {
	// aetherfy_vectors/client.py DEFAULT_ENDPOINT and VALID_REGIONS. The CLI
	// hard-codes no other vectors host: every regional one comes from
	// discovery.
	if DefaultEndpoint != "https://vectors.aetherfy.com" {
		t.Errorf("DefaultEndpoint = %s", DefaultEndpoint)
	}
	if strings.Join(ValidRegions, ",") != "us-east-1,eu-central-1,ap-southeast-1" {
		t.Errorf("ValidRegions = %v", ValidRegions)
	}
}

func TestErrorBodiesAreReadInEveryShapeTheSDKReads(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		body    string
		code    string
		message string
	}{
		"canonical":     {404, `{"error":{"code":"NOT_FOUND","message":"Collection 'a' not found"}}`, "NOT_FOUND", "Collection 'a' not found"},
		"string-shaped": {400, `{"error":"bad filter","error_code":"VALIDATION_ERROR"}`, "VALIDATION_ERROR", "bad filter"},
		"flat":          {429, `{"message":"slow down","error_code":"RATE_LIMIT_EXCEEDED"}`, "RATE_LIMIT_EXCEEDED", "slow down"},
		"a gateway":     {502, `<html>Bad Gateway</html>`, "", "<html>Bad Gateway</html>"},
		"empty":         {503, ``, "", "HTTP 503"},
	} {
		t.Run(name, func(t *testing.T) {
			e := ParseError(tc.status, []byte(tc.body))
			if e.Status != tc.status || e.Code != tc.code || e.Message != tc.message {
				t.Errorf("parsed %#v, want %d %q %q", e, tc.status, tc.code, tc.message)
			}
		})
	}
}
