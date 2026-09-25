package vectors

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// `afy index create` returns only once the index is built, exactly as the
// SDKs' create_field_index does since python-sdk 3993b57/1cbf891/2af3874 and
// js-sdk a66cf63/84e6e30/d698aeb. These tests are the SDK's
// tests/test_field_index.py cases, run on a fake clock so every number below
// is exact and nothing waits.

const (
	completed    = `{"result":{"operation_id":7,"status":"completed"},"status":"ok"}`
	acknowledged = `{"result":{"operation_id":null,"status":"acknowledged"},"status":"ok","time":25.0}`
)

// fakeClock stands in for the monotonic clock: now reads a number, sleep adds
// to it.
type fakeClock struct {
	t      time.Time
	sleeps []time.Duration
}

func (f *fakeClock) now() time.Time { return f.t }
func (f *fakeClock) sleep(d time.Duration) {
	f.sleeps = append(f.sleeps, d)
	f.t = f.t.Add(d)
}
func (f *fakeClock) at() time.Duration { return f.t.Sub(time.Unix(0, 0)) }

type attempt struct {
	method  string
	url     string
	body    string
	at      time.Duration // clock time the attempt started
	timeout time.Duration // HTTP timeout it was given
}

// indexRoute is the index route on the fake clock. Each request answers the
// next body in answers (the last repeats) after cost, or, when the attempt's
// timeout is shorter than cost, gives up at the timeout as the transport would.
//
// RUNAWAY GUARD: past maxCreates it fails the test and answers "completed", so
// a loop that stopped honouring its deadline ends red instead of hanging.
type indexRoute struct {
	t          *testing.T
	clock      *fakeClock
	answers    []string
	statuses   []int
	cost       time.Duration
	attempts   []attempt
	maxCreates int
}

func (r *indexRoute) send(method, url string, body []byte, _ http.Header, timeout time.Duration) (int, []byte, error) {
	r.attempts = append(r.attempts, attempt{method, url, string(body), r.clock.at(), timeout})
	if len(r.attempts) > r.maxCreates {
		r.t.Errorf("RUNAWAY: %d requests to the index route; the wait no longer ends", len(r.attempts))
		return 200, []byte(completed), nil
	}
	if timeout < r.cost {
		r.clock.t = r.clock.t.Add(timeout)
		return 0, nil, deadlineErr{}
	}
	r.clock.t = r.clock.t.Add(r.cost)
	i := len(r.attempts) - 1
	status := 200
	if i < len(r.statuses) {
		status = r.statuses[i]
	}
	if i >= len(r.answers) {
		i = len(r.answers) - 1
	}
	return status, []byte(r.answers[i]), nil
}

// deadlineErr is what net/http returns when a request's context expires.
type deadlineErr struct{}

func (deadlineErr) Error() string   { return "context deadline exceeded" }
func (deadlineErr) Timeout() bool   { return true }
func (deadlineErr) Temporary() bool { return true }

func newIndexClient(t *testing.T, cost time.Duration, answers ...string) (*Client, *indexRoute, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Unix(0, 0)}
	route := &indexRoute{t: t, clock: clock, answers: answers, cost: cost, maxCreates: 200}
	c := New("https://vectors.test", "afy_test_key", "")
	c.now, c.sleep, c.send = clock.now, clock.sleep, route.send
	c.jitter = func() float64 { return 1.0 } // the top of 50-100%, as the SDK's tests pin random()
	return c, route, clock
}

func TestAcknowledgedThenCompletedIsOneMoreCreate(t *testing.T) {
	c, route, _ := newIndexClient(t, 25*time.Second, acknowledged, completed)

	if err := c.CreateFieldIndex("articles", "ts", "integer", 0); err != nil {
		t.Fatalf("expected the index to be reported built, got %v", err)
	}
	// The second create is the same request: re-issuing it is what waits for
	// the running build.
	if len(route.attempts) != 2 {
		t.Fatalf("creates = %d, want 2", len(route.attempts))
	}
	for _, a := range route.attempts {
		if a.method != "PUT" || a.url != "https://vectors.test/api/v1/collections/articles/index" {
			t.Errorf("create went to %s %s", a.method, a.url)
		}
		var body map[string]interface{}
		_ = json.Unmarshal([]byte(a.body), &body)
		if body["field_name"] != "ts" || body["field_schema"] != "integer" || len(body) != 2 {
			t.Errorf("create body = %s, want {field_name: ts, field_schema: integer}", a.body)
		}
	}
}

func TestCompletedAtOnceIsOneCreate(t *testing.T) {
	c, route, _ := newIndexClient(t, 10*time.Millisecond, completed)
	if err := c.CreateFieldIndex("articles", "ts", "integer", 0); err != nil {
		t.Fatal(err)
	}
	if len(route.attempts) != 1 {
		t.Fatalf("creates = %d, want 1", len(route.attempts))
	}
}

func TestAlwaysAcknowledgedStopsAtTheDeadlineAndNeverSucceeds(t *testing.T) {
	c, route, clock := newIndexClient(t, 25*time.Second, acknowledged)

	err := c.CreateFieldIndex("articles", "ts", "integer", 60*time.Second)

	// 0 s: given the 45 s attempt cap, acknowledged at 25 s. 25 s: given the
	// 35 s left, acknowledged at 50 s. 50 s: given the last 10 s, and cut by
	// the deadline while the server is still waiting on the build.
	want := []struct{ at, timeout time.Duration }{
		{0, 45 * time.Second}, {25 * time.Second, 35 * time.Second}, {50 * time.Second, 10 * time.Second},
	}
	if len(route.attempts) != len(want) {
		t.Fatalf("creates = %+v, want %d", route.attempts, len(want))
	}
	for i, w := range want {
		if route.attempts[i].at != w.at || route.attempts[i].timeout != w.timeout {
			t.Errorf("create %d at %v given %v, want at %v given %v",
				i, route.attempts[i].at, route.attempts[i].timeout, w.at, w.timeout)
		}
	}
	if clock.at() != 60*time.Second {
		t.Errorf("ended at %v, want the 60 s deadline", clock.at())
	}
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("want a TimeoutError, got %T %v", err, err)
	}
	// The SDKs' words, with the CLI's command where they name their method.
	if err.Error() != "The payload index on 'ts' in collection 'articles' is still building "+
		"after the 60 s deadline. The build carries on server-side; calling afy index create "+
		"again waits for it." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestADeadlineReachedBetweenCreatesIsStillBuilding(t *testing.T) {
	// The deadline runs out as an "acknowledged" arrives, so no further create
	// is sent at all.
	c, route, _ := newIndexClient(t, 25*time.Second, acknowledged)
	err := c.CreateFieldIndex("articles", "ts", "integer", 25*time.Second)
	if err == nil || !strings.Contains(err.Error(), "still building") {
		t.Fatalf("want still building, got %v", err)
	}
	if len(route.attempts) != 1 {
		t.Errorf("creates = %d, want 1", len(route.attempts))
	}
}

func TestADeadlineThatCutsTheFirstCreateDoesNotClaimABuild(t *testing.T) {
	// No answer came back, so it is not known the create was taken.
	c, route, _ := newIndexClient(t, 25*time.Second, completed)
	err := c.CreateFieldIndex("articles", "ts", "integer", 5*time.Second)
	if err == nil || err.Error() != "The payload index create on 'ts' in collection 'articles' "+
		"got no answer within the 5 s deadline, so it is not known whether it was taken. "+
		"Calling afy index create again is safe." {
		t.Fatalf("message = %v", err)
	}
	if len(route.attempts) != 1 || route.attempts[0].timeout != 5*time.Second {
		t.Errorf("attempts = %+v, want one given the 5 s", route.attempts)
	}
}

func TestAnyOtherAnswerIsAnErrorNeverSuccess(t *testing.T) {
	for name, body := range map[string]string{
		"no result":      `{}`,
		"bare true":      `{"result":true}`,
		"another status": `{"result":{"status":"clock_rejected"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, route, _ := newIndexClient(t, 10*time.Millisecond, body)
			err := c.CreateFieldIndex("articles", "ts", "integer", 0)
			if err == nil || !strings.Contains(err.Error(), "the index is not confirmed built") {
				t.Fatalf("want 'not confirmed built', got %v", err)
			}
			if len(route.attempts) != 1 {
				t.Errorf("creates = %d, want 1", len(route.attempts))
			}
		})
	}
}

func TestAnUnheldAcknowledgedIsPacedAndTheDefaultDeadlineEndsIt(t *testing.T) {
	// A vectordb from before db26396 answers every create "acknowledged" at
	// once. Without the floor this loop re-sends in a tight loop, and without
	// the default deadline it never ends; the runaway guard reds either.
	c, route, clock := newIndexClient(t, 0, acknowledged)

	err := c.CreateFieldIndex("articles", "ts", "integer", 0)

	firstMinute := 0
	for _, a := range route.attempts {
		if a.at < time.Minute {
			firstMinute++
		}
	}
	// Sent at 0, 1, 3, 7, 15, 25, 35, 45, 55 s: the pause doubles from 1 s
	// and stops at 10 s.
	if firstMinute != 9 {
		t.Errorf("creates in the first minute = %d, want 9", firstMinute)
	}
	wantPauses := []time.Duration{1, 2, 4, 8, 10, 10}
	for i, w := range wantPauses {
		if i >= len(clock.sleeps) || clock.sleeps[i] != w*time.Second {
			t.Fatalf("pauses = %v, want to start %v s", clock.sleeps, wantPauses)
		}
	}
	if clock.at() != IndexDefaultDeadline {
		t.Errorf("ended at %v, want the default deadline %v", clock.at(), IndexDefaultDeadline)
	}
	if err == nil || !strings.Contains(err.Error(), "still building after the 600 s deadline") {
		t.Errorf("want the still-building error at 600 s, got %v", err)
	}
}

func TestAHeldAcknowledgedIsResentAtOnce(t *testing.T) {
	c, route, clock := newIndexClient(t, 25*time.Second, acknowledged, acknowledged, completed)
	if err := c.CreateFieldIndex("articles", "ts", "integer", 0); err != nil {
		t.Fatal(err)
	}
	if len(route.attempts) != 3 || len(clock.sleeps) != 0 {
		t.Errorf("creates = %d, pauses = %v; a create the server held needs no pause", len(route.attempts), clock.sleeps)
	}
}

func TestEachCreateGetsTheIndexAttemptTimeout(t *testing.T) {
	c, route, _ := newIndexClient(t, 25*time.Second, acknowledged, completed)
	if err := c.CreateFieldIndex("articles", "ts", "integer", 0); err != nil {
		t.Fatal(err)
	}
	for _, a := range route.attempts {
		if a.timeout != IndexAttemptTimeout {
			t.Errorf("a create was given %v, want IndexAttemptTimeout %v", a.timeout, IndexAttemptTimeout)
		}
	}

	// A longer client timeout is kept, as the SDKs keep their constructor's.
	c, route, _ = newIndexClient(t, time.Second, completed)
	c.timeout = 90 * time.Second
	if err := c.CreateFieldIndex("articles", "ts", "integer", 0); err != nil {
		t.Fatal(err)
	}
	if route.attempts[0].timeout != 90*time.Second {
		t.Errorf("given %v, want the longer 90 s", route.attempts[0].timeout)
	}
}

func TestATransientFailureIsRetriedWithBackoff(t *testing.T) {
	// The SDKs send a create through retry_with_backoff: a 503 is retried
	// after 1 s (jitter pinned to its top), not reported.
	c, route, clock := newIndexClient(t, time.Second, `{"error":{"code":"SERVICE_UNAVAILABLE","message":"busy"}}`, completed)
	route.statuses = []int{503, 200}
	if err := c.CreateFieldIndex("articles", "ts", "integer", 0); err != nil {
		t.Fatalf("a 503 then completed should succeed, got %v", err)
	}
	if len(route.attempts) != 2 || len(clock.sleeps) != 1 || clock.sleeps[0] != time.Second {
		t.Errorf("attempts = %d, pauses = %v; want 2 and one 1 s backoff", len(route.attempts), clock.sleeps)
	}
}

func TestARefusalIsReportedWithItsCodeNotRetried(t *testing.T) {
	c, route, _ := newIndexClient(t, time.Second, `{"error":{"code":"NOT_FOUND","message":"Collection 'articles' not found"}}`)
	route.statuses = []int{404}
	err := c.CreateFieldIndex("articles", "ts", "integer", 0)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 404 || ae.Code != "NOT_FOUND" || ae.Message != "Collection 'articles' not found" {
		t.Fatalf("want the API's 404 unchanged, got %#v", err)
	}
	if len(route.attempts) != 1 {
		t.Errorf("attempts = %d, want 1", len(route.attempts))
	}
}

func TestIndexDeleteIsOneRequestWithTheIndexAttemptTimeout(t *testing.T) {
	c, route, _ := newIndexClient(t, time.Second, `{"error":{"code":"PROXY_ERROR","message":"upstream"}}`)
	route.statuses = []int{503}
	c.workspace = "team alpha"

	err := c.DeleteFieldIndex("articles", "metadata/tag")

	if err == nil {
		t.Fatal("a 503 must be reported")
	}
	if len(route.attempts) != 1 {
		t.Fatalf("attempts = %d; a delete is one request, never retried", len(route.attempts))
	}
	a := route.attempts[0]
	if a.method != "DELETE" || a.timeout != IndexAttemptTimeout {
		t.Errorf("sent %s with %v, want DELETE with %v", a.method, a.timeout, IndexAttemptTimeout)
	}
	// Every segment escaped whole, so neither the space nor the "/" splits it.
	if a.url != "https://vectors.test/api/v1/workspaces/team%20alpha/collections/articles/index/metadata%2Ftag" {
		t.Errorf("url = %s", a.url)
	}
}

func TestTheIndexConstantsAreTheSDKs(t *testing.T) {
	// Copies of aetherfy-vectors-python-sdk aetherfy_vectors/client.py
	// INDEX_WAIT_BUDGET_S = 25.0, INDEX_FORWARD_MARGIN_S = 5.0,
	// INDEX_ATTEMPT_TIMEOUT_S = 45.0, INDEX_DEFAULT_DEADLINE_S = 600.0,
	// INDEX_RESEND_PAUSE_FIRST_S = 1.0, INDEX_RESEND_PAUSE_MAX_S = 10.0, and
	// the *_MS twins in aetherfy-vectors-js-sdk src/client.ts. All six are
	// read from source by aetherfy-e2e-tests
	// tests/pyunit/test_index_timeouts_pair.py: the first three against
	// vectordb's, the pacing three against both SDKs'. This local copy is what
	// reds in this repo's own CI, which checks out no sibling.
	for name, got := range map[string][2]time.Duration{
		"IndexWaitBudget":       {IndexWaitBudget, 25 * time.Second},
		"IndexForwardMargin":    {IndexForwardMargin, 5 * time.Second},
		"IndexAttemptTimeout":   {IndexAttemptTimeout, 45 * time.Second},
		"IndexDefaultDeadline":  {IndexDefaultDeadline, 600 * time.Second},
		"IndexResendPauseFirst": {IndexResendPauseFirst, 1 * time.Second},
		"IndexResendPauseMax":   {IndexResendPauseMax, 10 * time.Second},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %v, the SDKs have %v", name, got[0], got[1])
		}
	}
	// The attempt outlasts the server's hold plus a forward, with room for
	// this client's own hop; the 30 s default alone does not, which is why a
	// create does not use it.
	held := IndexWaitBudget + IndexForwardMargin
	if IndexAttemptTimeout < held+10*time.Second {
		t.Errorf("IndexAttemptTimeout %v leaves no room over the %v the server may hold", IndexAttemptTimeout, held)
	}
	if DefaultTimeout >= held+10*time.Second {
		t.Errorf("DefaultTimeout %v would be enough; then IndexAttemptTimeout is not needed", DefaultTimeout)
	}
}

func TestATimeoutThatIsNotAFiniteNumberAboveZeroIsRefused(t *testing.T) {
	for _, raw := range []string{"0", "-1", "-0.5", "NaN", "Inf", "+Inf", "abc", "10m", ""} {
		_, err := ParseIndexTimeout(raw)
		if err == nil {
			t.Errorf("--timeout %q was accepted", raw)
			continue
		}
		want := "--timeout must be a finite number of seconds above 0, got '" + raw + "'"
		if err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	}
	for raw, want := range map[string]time.Duration{"1.5": 1500 * time.Millisecond, "600": 600 * time.Second} {
		got, err := ParseIndexTimeout(raw)
		if err != nil || got != want {
			t.Errorf("--timeout %q = %v, %v; want %v", raw, got, err, want)
		}
	}
}
