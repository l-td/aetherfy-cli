package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// THE SHIPPED COMMAND, END TO END: argv through the real root command's flag
// parsing and RunE, against a stand-in control plane. Nothing here calls a
// command's internals.

// cpCall is one request the stand-in control plane received.
type cpCall struct {
	Method string
	Path   string
	Body   map[string]interface{}
}

// fakeCP answers by "METHOD /path" (the path after /api/v1) and records every
// request. An unrouted request answers 599, so a call nobody expected fails
// the command loudly instead of passing as an empty success.
type fakeCP struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []cpCall
	routes map[string]cpAnswer
}

type cpAnswer struct {
	status int
	body   string
}

func newFakeCP(t *testing.T) *fakeCP {
	t.Helper()
	f := &fakeCP{routes: map[string]cpAnswer{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.calls = append(f.calls, cpCall{Method: r.Method, Path: path, Body: body})
		a, ok := f.routes[r.Method+" "+path]
		f.mu.Unlock()
		if !ok {
			a = cpAnswer{599, `{"detail":{"code":"stand-in-unrouted","message":"the stand-in has no answer for ` + r.Method + " " + path + `"}}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCP) on(method, path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method+" "+path] = cpAnswer{status, body}
}

// called returns the requests made to method+path, in order.
func (f *fakeCP) called(method, path string) []cpCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cpCall
	for _, c := range f.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// resetAllFlags puts every flag of the command tree back to its default and
// clears its Changed mark, as a fresh process starts: cobra keeps both between
// executions of the same tree.
func resetAllFlags() {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, fs := range []*pflag.FlagSet{c.Flags(), c.PersistentFlags()} {
			fs.VisitAll(func(f *pflag.Flag) {
				if sv, ok := f.Value.(pflag.SliceValue); ok {
					_ = sv.Replace(nil)
				} else {
					_ = f.Value.Set(f.DefValue)
				}
				f.Changed = false
			})
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// runAfy runs `afy <args>` against cp as a logged-in user and returns what it
// printed on stdout and stderr, and the error Execute returned.
func runAfy(t *testing.T, cp *fakeCP, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("AETHERFY_CONFIG_DIR", t.TempDir())
	t.Setenv("AETHERFY_API_KEY", "afy_test_0123456789abcdef0123456789abcdef")
	t.Setenv("NO_COLOR", "1")
	if cp != nil {
		t.Setenv("AETHERFY_API_URL", cp.srv.URL+"/api/v1")
	}
	resetAllFlags()
	t.Cleanup(func() { resetAllFlags(); rootCmd.SetArgs(nil) })
	rootCmd.SetArgs(args)
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() { err = rootCmd.Execute() })
	})
	return stdout, stderr, err
}

func TestRunAfyDrivesTheRealCommandAndSeesItsRequests(t *testing.T) {
	// Positive control on the harness: a command that reaches the stand-in is
	// recorded, and an unrouted request fails rather than passing as success.
	cp := newFakeCP(t)
	_, stderr, err := runAfy(t, cp, "update", "bot", "--description", "x")
	if err == nil {
		t.Fatal("an unrouted request must fail the command")
	}
	if len(cp.called("PATCH", "/agents/bot")) != 1 {
		t.Fatalf("the command's request was not recorded; calls: %+v", cp.calls)
	}
	if !strings.Contains(stderr, "stand-in-unrouted") && !strings.Contains(stderr, "the stand-in has no answer") {
		t.Errorf("stderr did not carry the stand-in's refusal: %q", stderr)
	}
}
