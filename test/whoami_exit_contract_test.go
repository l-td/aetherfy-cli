package test

// Pins `afy whoami`'s exit code when there are no credentials.
//
// whoami used to print "Not logged in" and return nil — exit 0 — while every
// other auth-requiring command went through checkAuth() and exited 3 with the
// identical message. Two spellings of one rule; the quieter one was the command
// people reach for precisely to ask "am I logged in?", and a CI shell gating on
// it saw success.
//
// Exit codes are the CLI's contract with scripts, and no Go test can observe
// os.Exit from inside the process. So this builds the real binary and runs it.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const notLoggedInHint = "Not logged in. Run 'afy login' first."

// buildCLI compiles the real binary and returns its path.
func buildCLI(t *testing.T) string {
	t.Helper()
	name := "afy"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)

	build := exec.Command("go", "build", "-o", bin, "./cmd/afy")
	build.Dir = ".." // test/ sits one level under the repo root
	out, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed:\n%s", out)
	return bin
}

// runCLI executes the binary with a scrubbed environment and returns
// stdout, stderr and the exit code.
//
// Scrubbing matters: AETHERFY_API_KEY alone makes IsLoggedIn() true
// (internal/config/credentials.go), so a developer who exports one would see
// this test pass or fail for a reason that has nothing to do with the code.
func runCLI(t *testing.T, bin string, env []string, args ...string) (string, string, int) {
	t.Helper()

	clean := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "AETHERFY_") {
			continue
		}
		clean = append(clean, kv)
	}

	run := exec.Command(bin, args...)
	run.Env = append(clean, env...)

	var stdout, stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	err := run.Run()

	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "could not run %s", bin)
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

func TestWhoamiExitsThreeWhenNotAuthenticated(t *testing.T) {
	bin := buildCLI(t)

	stdout, stderr, code := runCLI(t, bin,
		[]string{"AETHERFY_CONFIG_DIR=" + t.TempDir()}, "whoami")

	assert.Equal(t, 3, code,
		"`afy whoami` with no credentials must exit 3, the same code checkAuth() gives every "+
			"other auth-requiring command. Got %d.\nstdout: %q\nstderr: %q", code, stdout, stderr)
	assert.Empty(t, stdout,
		"the not-logged-in message belongs on stderr; a script doing `afy whoami > out` must get an empty file")
	assert.Contains(t, stderr, notLoggedInHint,
		"stderr must carry the login hint")
}

// Positive control. Without it the test above would still pass if whoami exited
// 3 unconditionally — including if the command were broken outright — so it
// could not tell "correctly refuses" from "never works".
//
// AETHERFY_API_KEY is the documented override and needs no real credentials.
// whoami reads the account from the API on every run (GET /auth/me), so the
// binary is pointed at a stand-in one: no network, and the plan it prints is
// the one that stand-in served.
func TestWhoamiExitsZeroWhenAuthenticated(t *testing.T) {
	bin := buildCLI(t)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" {
			http.Error(w, "unrouted", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"u-1","email":"dev@example.com","tier":"performance"}`))
	}))
	defer api.Close()

	stdout, stderr, code := runCLI(t, bin, []string{
		"AETHERFY_CONFIG_DIR=" + t.TempDir(),
		"AETHERFY_API_KEY=afy_test_0000000000000000000000000000",
		"AETHERFY_API_URL=" + api.URL + "/api/v1",
	}, "whoami")

	assert.Equal(t, 0, code,
		"`afy whoami` with a key present must exit 0.\nstdout: %q\nstderr: %q", stdout, stderr)
	assert.NotEmpty(t, stdout,
		"the authenticated path must still print the status block to stdout")
	assert.Contains(t, stdout, "performance", "the plan printed is the API's, though nothing was saved at login")
	assert.NotContains(t, stdout, notLoggedInHint)
	assert.NotContains(t, stderr, notLoggedInHint)
}
