package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// A rollback to a version whose image is gone is rebuilt from that version's
// stored source, which is not the exact artifact that ran before. The server
// says so on the response; `afy rollback` has to say it too, in the terminal a
// rollback is usually run from in a hurry.
func rollbackWithNotice(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rollback") {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	var code int
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			code = rollbackAgent(client, []string{"ranker", "2"}, true, fastPoll, time.Minute)
		})
	})
	if code != 0 {
		t.Fatalf("a queued rollback exited %d", code)
	}
	// The notice is a warning, and warnings may go to either stream.
	return stdout + stderr
}

func TestRollbackPrintsTheServersRebuildNotice(t *testing.T) {
	const notice = "a notice this test made up"
	out := rollbackWithNotice(t, `{"id":"d-4","version":4,"state":"queued","rollback_notice":"`+notice+`"}`)

	if !strings.Contains(out, notice) {
		t.Errorf("afy rollback did not print the server's rebuild notice:\n%s", out)
	}
}

// THE CONTROL: an exact rollback carries no notice and prints none.
func TestAnExactRollbackPrintsNoRebuildNotice(t *testing.T) {
	out := rollbackWithNotice(t, `{"id":"d-4","version":4,"state":"queued"}`)

	if strings.Contains(strings.ToLower(out), "rebuilt") {
		t.Errorf("an exact rollback printed a rebuild notice:\n%s", out)
	}
}
