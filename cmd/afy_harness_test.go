package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

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

// all returns every request received, in order, under the lock (the server
// records from its own goroutines).
func (f *fakeCP) all() []cpCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cpCall(nil), f.calls...)
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
		stdout = captureStdout(t, func() { err = Execute() })
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
		t.Fatalf("the command's request was not recorded; calls: %+v", cp.all())
	}
	if !strings.Contains(stderr, "stand-in-unrouted") && !strings.Contains(stderr, "the stand-in has no answer") {
		t.Errorf("stderr did not carry the stand-in's refusal: %q", stderr)
	}
}

// afyProcessArgs carries the argv of a re-executed test binary that is to act
// as afy (see runAfyProcess). Unit separator: an argument may contain spaces.
const afyProcessArgs = "AFY_TEST_PROCESS_ARGS"

// afyProcessBound is how long runAfyProcess lets one afy run.
const afyProcessBound = 60 * time.Second

// TestMain lets the test binary act as afy itself when re-executed by
// runAfyProcess: some commands end the process (os.Exit with their exit code),
// which in-process would end the whole test run.
func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv(afyProcessArgs); ok {
		rootCmd.SetArgs(strings.Split(args, "\x1f"))
		if err := Execute(); err != nil {
			os.Exit(ExitCode(err))
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runAfyProcess runs `afy <args>` as its own process (this test binary,
// re-executed) against cp, logged in, and returns stdout, stderr and the exit
// code: the shipped command as a user runs it, exit code included.
//
// BOUNDED. A command that wrongly gets further than a case expects (a deploy
// that should have been refused, then waits on a deployment the stand-in
// cannot answer) would otherwise hold the whole run until go test's own
// timeout. It is killed at afyProcessBound and the case fails saying so.
func runAfyProcess(t *testing.T, cp *fakeCP, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), afyProcessBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(),
		afyProcessArgs+"="+strings.Join(args, "\x1f"),
		"AETHERFY_CONFIG_DIR="+t.TempDir(),
		"AETHERFY_API_KEY=afy_test_0123456789abcdef0123456789abcdef",
		"NO_COLOR=1",
	)
	if cp != nil {
		cmd.Env = append(cmd.Env, "AETHERFY_API_URL="+cp.srv.URL+"/api/v1")
	}
	cmd.Env = append(cmd.Env, env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("afy %s did not finish within %s (killed); it got further than this case expects.\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), afyProcessBound, out.String(), errOut.String())
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("running afy: %v", err)
	}
	return out.String(), errOut.String(), code
}

func TestRunAfyProcessReportsTheExitCode(t *testing.T) {
	// Positive control: a refused input exits 2, and a good one 0, as a user
	// would see them.
	if _, stderr, code := runAfyProcess(t, nil, nil, "access", "bot", "--add", "--yes"); code != exitInputRefused {
		t.Errorf("exit %d, want %d; stderr %q", code, exitInputRefused, stderr)
	}
	if _, _, code := runAfyProcess(t, nil, nil, "version"); code != 0 {
		t.Errorf("afy version exited %d", code)
	}
}

// Execute prints an error no command printed (here cobra's own, for an unknown
// flag) exactly once, in the "Error: ..." form cobra used before
// SilenceErrors -- the control for the refusal tests' "shown once".
func TestExecutePrintsAnUnprintedErrorOnce(t *testing.T) {
	_, stderr, err := runAfy(t, newFakeCP(t), "list", "--bogus")
	if err == nil {
		t.Fatal("an unknown flag must fail")
	}
	if n := strings.Count(stderr, "Error: unknown flag: --bogus"); n != 1 {
		t.Errorf("want the error once, got %d:\n%s", n, stderr)
	}
}
