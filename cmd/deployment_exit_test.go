package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// A command that waits for a deployment is a command a script trusts with its
// exit status. On 2026-09-14 a failed `afy deploy`, `afy redeploy` and
// `afy rollback` each printed "Deployment failed" and exited 0, so the fallback
// a script had written for exactly that case never ran. Each test drives the
// command against a server whose deployment ends in the state named, and reads
// the exit code a shell would see.

const imageGone = "The image for v2 is no longer available in the registry"

// Short enough that a test settles in milliseconds; the watch's first read
// happens one interval in.
const fastPoll = time.Millisecond

// deploymentServer answers the rollback and redeploy POSTs with deployment d-4
// (or refuses them with a 422), and every read of d-4 with the given state.
// Anything else is a 404, which the watch reads as "no logs" and "no URL".
func deploymentServer(t *testing.T, state, errorMessage string, refuse bool) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost &&
			(strings.HasSuffix(r.URL.Path, "/rollback") || strings.HasSuffix(r.URL.Path, "/redeploy")):
			if refuse {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"detail":{"code":"DEPLOYMENT_ROLLBACK_TARGET_INVALID","message":"` + imageGone + `"}}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"d-4","version":4,"state":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/deployments/d-4":
			_, _ = w.Write([]byte(`{"id":"d-4","version":4,"state":"` + state + `","error_message":"` + errorMessage + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return api.NewClientWithURL(srv.URL, "afy_test_key")
}

func TestRollbackExitsNonZeroWhenTheDeploymentFails(t *testing.T) {
	client := deploymentServer(t, "failed", imageGone, false)

	var code int
	stderr := captureStderr(t, func() {
		code = rollbackAgent(client, []string{"ranker", "2"}, false, fastPoll, time.Minute)
	})

	if code == 0 {
		t.Fatalf("a rollback whose deployment failed exited 0; a script would read it as green\nstderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Deployment failed") || !strings.Contains(stderr, imageGone) {
		t.Errorf("the failure and its reason belong on stderr, got:\n%s", stderr)
	}
}

func TestRedeployExitsNonZeroWhenTheDeploymentFails(t *testing.T) {
	client := deploymentServer(t, "failed", imageGone, false)

	var code int
	stderr := captureStderr(t, func() {
		code = redeployAgent(client, []string{"brief-api", "1"}, false, fastPoll, time.Minute)
	})

	if code == 0 {
		t.Fatalf("a redeploy whose deployment failed exited 0\nstderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, imageGone) {
		t.Errorf("the reason belongs on stderr, got:\n%s", stderr)
	}
}

// `afy deploy` builds its archive from the filesystem before it waits, so the
// part under test is the wait it hands over to -- the same function the other
// two commands use, and the one that used to return nothing at all.
func TestDeployWaitReportsAFailedDeploymentAsAnError(t *testing.T) {
	client := deploymentServer(t, "failed", imageGone, false)

	var err error
	stderr := captureStderr(t, func() {
		err = watchDeployment(client, "tick", "d-4", fastPoll, time.Minute)
	})

	if !errors.Is(err, errDeploymentFailed) {
		t.Fatalf("want errDeploymentFailed, got %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, imageGone) {
		t.Errorf("the reason belongs on stderr, got:\n%s", stderr)
	}
}

// THE POSITIVE CONTROL. Every test above would pass against a command that
// exited 1 unconditionally.
func TestRollbackExitsZeroWhenTheDeploymentGoesLive(t *testing.T) {
	client := deploymentServer(t, "active", "", false)

	var code int
	stderr := captureStderr(t, func() {
		code = rollbackAgent(client, []string{"ranker", "2"}, false, fastPoll, time.Minute)
	})

	if code != 0 {
		t.Fatalf("a rollback that went live exited %d\nstderr:\n%s", code, stderr)
	}
}

// A refusal before anything was created is a failed command too -- it is what
// a rollback to a vanished image now answers.
func TestRollbackExitsNonZeroWhenTheServerRefusesIt(t *testing.T) {
	client := deploymentServer(t, "", "", true)

	var code int
	stderr := captureStderr(t, func() {
		code = rollbackAgent(client, []string{"ranker", "2"}, false, fastPoll, time.Minute)
	})

	if code == 0 {
		t.Fatalf("a refused rollback exited 0\nstderr:\n%s", stderr)
	}
}

// A wait that runs out confirmed nothing, so it cannot read as success.
func TestRedeployExitsNonZeroWhenTheWaitRunsOut(t *testing.T) {
	client := deploymentServer(t, "deploying", "", false)

	var code int
	stderr := captureStderr(t, func() {
		code = redeployAgent(client, []string{"brief-api", "1"}, false, fastPoll, 50*time.Millisecond)
	})

	if code == 0 {
		t.Fatalf("a redeploy that never settled exited 0\nstderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "timed out") {
		t.Errorf("stderr does not say the wait ran out:\n%s", stderr)
	}
}

// The two failures are distinct errors, and this is what reads them apart: a
// deployment that FAILED is not one we stopped waiting for, and a caller (or a
// later exit-code split) must be able to tell. Without this the distinction is
// written down and never checked.
func TestTheWaitSaysWhichFailureItHad(t *testing.T) {
	never := deploymentServer(t, "deploying", "", false)
	var timedOut error
	captureStderr(t, func() {
		timedOut = watchDeployment(never, "tick", "d-4", fastPoll, 50*time.Millisecond)
	})
	if !errors.Is(timedOut, errDeploymentWatchTimedOut) {
		t.Errorf("a deployment that never settled: want errDeploymentWatchTimedOut, got %v", timedOut)
	}

	failed := deploymentServer(t, "failed", imageGone, false)
	var didFail error
	captureStderr(t, func() {
		didFail = watchDeployment(failed, "tick", "d-4", fastPoll, time.Minute)
	})
	if !errors.Is(didFail, errDeploymentFailed) {
		t.Errorf("a deployment that failed: want errDeploymentFailed, got %v", didFail)
	}
}
