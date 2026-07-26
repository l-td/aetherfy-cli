package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

// dropServer answers every request by hijacking and slamming the connection
// shut, which surfaces in the client as a transport error — the class resty
// retries on. It records how many times each method actually reached it.
func dropServer(t *testing.T) (*httptest.Server, func(string) int) {
	t.Helper()

	var mu sync.Mutex
	counts := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.Method]++
		mu.Unlock()

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("ResponseWriter is not a Hijacker; cannot simulate a drop")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack failed: %v", err)
			return
		}
		_ = conn.Close()
	}))

	return srv, func(method string) int {
		mu.Lock()
		defer mu.Unlock()
		return counts[method]
	}
}

func retryTestClient() *resty.Client {
	client := resty.New()
	configureRetries(client)
	// Keep the backoff out of the test's wall-clock; the policy under test is
	// WHICH methods retry, not how long it waits between attempts.
	client.SetRetryWaitTime(1 * time.Millisecond)
	client.SetRetryMaxWaitTime(2 * time.Millisecond)
	return client
}

// A transport error on a safe method is retried: nothing on the server can have
// happened, so re-sending is free.
func TestTransportErrorRetriesSafeMethods(t *testing.T) {
	srv, count := dropServer(t)
	defer srv.Close()

	client := retryTestClient()
	if _, err := client.R().Get(srv.URL); err == nil {
		t.Fatal("expected a transport error from the dropped connection")
	}

	// SetRetryCount(2) => 1 initial attempt + 2 retries.
	if got := count(http.MethodGet); got != 3 {
		t.Errorf("GET reached the server %d time(s); want 3 (1 attempt + 2 retries)", got)
	}
}

// THE REGRESSION PIN (nightly #30). A transport error on a mutating method is
// NOT retried: the client cannot distinguish "never arrived" from "arrived and
// is still executing", and re-sending double-executes the operation. Resty's
// default (no retry conditions) retries every method, which is what re-sent
// `agents stop` into its own agent-row lock and made the CLI 409 against
// itself. Delete the retry condition in client.go and these go red.
func TestTransportErrorDoesNotRetryMutatingMethods(t *testing.T) {
	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			srv, count := dropServer(t)
			defer srv.Close()

			client := retryTestClient()
			_, err := client.R().Execute(method, srv.URL)
			if err == nil {
				t.Fatal("expected a transport error from the dropped connection")
			}

			if got := count(method); got != 1 {
				t.Errorf(
					"%s reached the server %d time(s); want exactly 1 — a mutating "+
						"request must never be re-sent on a transport error (it may "+
						"already have taken effect)",
					method, got,
				)
			}
		})
	}
}

// The retry condition nil-guards resp and resp.Request. If resty did not
// populate those on a transport error, that guard would return false for EVERY
// method and would have silently killed GET retries as well — a safe failure
// direction, but the code's claim would not match its behaviour. resty builds
// the Response with Request set BEFORE it inspects the error
// (v2.11.0 client.go:1189-1197), so the guard never trips. Pinned rather than
// trusted, because a resty upgrade could change it and nothing else would say so.
func TestRetryConditionReceivesRequestOnTransportError(t *testing.T) {
	srv, _ := dropServer(t)
	defer srv.Close()

	var sawResp, sawRequest bool
	var sawMethod string

	client := resty.New()
	client.SetRetryCount(1)
	client.SetRetryWaitTime(1 * time.Millisecond)
	client.SetRetryMaxWaitTime(2 * time.Millisecond)
	client.AddRetryCondition(func(resp *resty.Response, err error) bool {
		if err == nil {
			return false
		}
		if resp != nil {
			sawResp = true
			if resp.Request != nil {
				sawRequest = true
				sawMethod = resp.Request.Method
			}
		}
		return false
	})

	if _, err := client.R().Get(srv.URL); err == nil {
		t.Fatal("expected a transport error from the dropped connection")
	}

	if !sawResp {
		t.Error("retry condition got a nil *Response on a transport error; the " +
			"nil-guard in client.go would disable ALL retries, including GET")
	}
	if !sawRequest {
		t.Error("retry condition got a nil resp.Request on a transport error; the " +
			"method check in client.go could never match, disabling GET retries")
	}
	if sawMethod != http.MethodGet {
		t.Errorf("retry condition saw method %q; want %q", sawMethod, http.MethodGet)
	}
}

// The production constructor must carry the policy, not just the test helper.
func TestNewClientWithURLDoesNotBlindlyRetryPosts(t *testing.T) {
	srv, count := dropServer(t)
	defer srv.Close()

	c := NewClientWithURL(srv.URL, "afy_test_key")
	// Exercise the real Post path (Client.Post → handleResponse).
	if err := c.Post("/agents/x/stop", nil, nil); err == nil {
		t.Fatal("expected a transport error from the dropped connection")
	}

	if got := count(http.MethodPost); got != 1 {
		t.Errorf("POST reached the server %d time(s); want exactly 1", got)
	}
}
