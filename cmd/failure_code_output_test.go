package cmd

// A FAILED DEPLOYMENT OR RUN SHOWS ITS CODE BESIDE ITS SENTENCE (the server's failure_code, beside error_message):
// `afy deploy`, `afy run --wait`, `afy status` and `afy deployments` print a "Failure code:" line under the reason,
// and with --json the one error object carries the code as its `code`, where a script reads a code. A cancelled run
// carries none (its reason is cancellation_reason), and a deployment that failed before the server recorded codes
// prints the sentence alone. Each test has a control that must not show a code.
//
// The codes are ones the control plane's codes snapshot holds (test/cp_error_codes_test.go admits no other literal):
// a run refused at its turn carries its gate's code, a refused push the upload's.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

const (
	runFailureCode  = "PLAN_LIMIT_EXCEEDED"
	pushFailureCode = "DEPLOYMENT_ARCHIVE_TOO_LARGE"
)

// failedDeploymentServer answers GET /deployments/d-4 with a failed deployment carrying `code` ("" for none).
func failedDeploymentServer(t *testing.T, code string) *api.Client {
	t.Helper()
	extra := ""
	if code != "" {
		extra = `,"failure_code":"` + code + `"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/deployments/d-4" {
			_, _ = w.Write([]byte(`{"id":"d-4","version":4,"state":"failed","error_message":"the archive is too large"` + extra + `}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return api.NewClientWithURL(srv.URL, "afy_test_key")
}

func TestADeployWaitPrintsTheFailureCodeUnderTheReason(t *testing.T) {
	var err error
	stderr := captureStderr(t, func() {
		err = watchDeployment(failedDeploymentServer(t, pushFailureCode), "tick", "d-4", fastPoll, time.Minute)
	})
	if !errors.Is(err, errDeploymentFailed) {
		t.Fatalf("want errDeploymentFailed, got %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Reason: the archive is too large") || !strings.Contains(stderr, "Failure code: "+pushFailureCode) {
		t.Errorf("the reason and the code belong on stderr, got:\n%s", stderr)
	}

	// CONTROL: a deployment that failed before the server recorded codes prints the sentence alone.
	stderr = captureStderr(t, func() {
		_ = watchDeployment(failedDeploymentServer(t, ""), "tick", "d-4", fastPoll, time.Minute)
	})
	if strings.Contains(stderr, "Failure code") {
		t.Errorf("no code was sent, none may be printed:\n%s", stderr)
	}
}

func TestADeployWaitsJSONErrorCarriesTheFailureCodeAsItsCode(t *testing.T) {
	jsonRun(t, true)
	stderr := captureStderr(t, func() {
		_ = watchDeployment(failedDeploymentServer(t, pushFailureCode), "tick", "d-4", fastPoll, time.Minute)
	})
	e := jsonErrorOnStderr(t, "afy deploy --json", stderr)
	if e["code"] != pushFailureCode {
		t.Errorf("the error object's code is %v, want %s: %v", e["code"], pushFailureCode, e)
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, "the archive is too large") {
		t.Errorf("the message keeps the sentence: %v", e)
	}
}

func TestARunWaitPrintsAFailedRunsCode(t *testing.T) {
	stdout, stderr, err := runWaitWith(t, 10, acceptedWaiting, nil,
		dRun("failed", `,"error_message":"your plan does not allow it","failure_code":"`+runFailureCode+`"`))
	if exitCodeOf(err) != exitRequestFailed {
		t.Fatalf("exit %d, want 1\n%s%s", exitCodeOf(err), stdout, stderr)
	}
	if out := stdout + stderr; !strings.Contains(out, "Failure code: "+runFailureCode) {
		t.Errorf("the run's code is not printed:\n%s", out)
	}
}

func TestARunWaitsJSONErrorCarriesTheCodeAndACancelledRunsCarriesNone(t *testing.T) {
	_, stderr, _ := runWaitWith(t, 10, acceptedWaiting, []string{"--json"},
		dRun("failed", `,"error_message":"your plan does not allow it","failure_code":"`+runFailureCode+`"`))
	if e := jsonErrorOnStderr(t, "afy run --wait --json", stderr); e["code"] != runFailureCode {
		t.Errorf("the error object's code is %v, want %s: %v", e["code"], runFailureCode, e)
	}

	// CONTROL: a cancelled run carries no failure code, even if one were sent.
	_, stderr, _ = runWaitWith(t, 10, acceptedWaiting, []string{"--json"},
		dRun("cancelled", `,"error_message":"stopped by its owner","failure_code":"`+runFailureCode+`"`))
	if e := jsonErrorOnStderr(t, "afy run --wait --json", stderr); e["code"] != nil {
		t.Errorf("a cancelled run's error carries a code: %v", e)
	}
}

func TestStatusShowsTheLastDeploysFailureCode(t *testing.T) {
	out := statusAgainst(t, `[
		{"id":"d3","agent_id":"a1","version":3,"state":"failed","error_message":"the archive is too large",
		 "failure_code":"`+pushFailureCode+`","created_at":"2026-10-14T10:00:00Z"}]`)
	if !strings.Contains(out, "Deploy error") || !strings.Contains(out, pushFailureCode) {
		t.Errorf("afy status must show the failed deploy's code beside its sentence:\n%s", out)
	}
	// CONTROL: no code sent, no code line.
	out = statusAgainst(t, `[
		{"id":"d3","agent_id":"a1","version":3,"state":"failed","error_message":"the archive is too large",
		 "created_at":"2026-10-14T10:00:00Z"}]`)
	if strings.Contains(out, "Failure code") {
		t.Errorf("no code was sent, none may be printed:\n%s", out)
	}
}

func TestTheLastRunNamesAFailedRunsCodeAndNoOther(t *testing.T) {
	failed := &api.LastRun{State: "failed", ErrorMessage: "your plan does not allow it", FailureCode: runFailureCode,
		CreatedAt: time.Now()}
	if got := formatAgentLastRun(failed); !strings.Contains(got, "["+runFailureCode+"]") {
		t.Errorf("a failed run's code is not on its line: %q", got)
	}
	// CONTROL: the same code on a cancelled run is not shown.
	cancelled := &api.LastRun{State: "cancelled", ErrorMessage: "stopped by its owner", FailureCode: runFailureCode,
		CreatedAt: time.Now()}
	if got := formatAgentLastRun(cancelled); strings.Contains(got, runFailureCode) {
		t.Errorf("a cancelled run shows a failure code: %q", got)
	}
}

func TestDeploymentsPrintsACutErrorsCode(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/deployments", 200, deploymentsPage(`[
		{"id":"d3","agent_id":"a1","version":3,"state":"failed","error_message":"`+pushWithoutYAML+`",
		 "failure_code":"`+pushFailureCode+`","created_at":"2026-10-14T10:00:00Z"}]`))
	stdout, _, err := runAfy(t, cp, "deployments", "bot")
	if err != nil {
		t.Fatalf("afy deployments: %v", err)
	}
	if !strings.Contains(stdout, "v3 failure code: "+pushFailureCode) {
		t.Errorf("the cut error's code is not printed under it:\n%s", stdout)
	}
}
