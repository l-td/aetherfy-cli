package api

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests COUNT REQUESTS, never time: the client's retry wait is shortened to a millisecond, so
// "retried" and "not retried" differ in the number of requests the stand-in saw, and a regression
// cannot hide behind a fast machine.

type agentStandIn struct {
	ID string `json:"id"`
}

func clientFor(t *testing.T, handler http.HandlerFunc) (*Client, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClientWithURL(srv.URL, "afy_test_key")
	c.http.SetRetryWaitTime(time.Millisecond)
	c.http.SetRetryMaxWaitTime(time.Millisecond)
	return c, &hits
}

func TestA200ThatIsNotJSONIsOneRequestAndNamesTheNetwork(t *testing.T) {
	c, hits := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>Sign in to the Wi-Fi</body></html>"))
	})

	var out agentStandIn
	err := c.Get("/agents/x", &out)

	var notAPI *NotAPIResponseError
	if !errors.As(err, &notAPI) {
		t.Fatalf("want a NotAPIResponseError, got %T: %v", err, err)
	}
	if notAPI.ContentType != "text/html" {
		t.Errorf("content type = %q, want text/html", notAPI.ContentType)
	}
	for _, want := range []string{"did not come from the Aetherfy API", "text/html", "network or proxy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err.Error(), want)
		}
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("requests = %d, want 1 (a whole answer that is not the API's is not retried)", got)
	}
}

func TestA200OfTheWrongShapeIsOneRequestAndSaysToUpdate(t *testing.T) {
	c, hits := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 5}`)) // a number where this version reads a string
	})

	var out agentStandIn
	err := c.Get("/agents/x", &out)

	var shape *UnexpectedShapeError
	if !errors.As(err, &shape) {
		t.Fatalf("want an UnexpectedShapeError, got %T: %v", err, err)
	}
	for _, want := range []string{"format this version of afy does not understand", "afy upgrade"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err.Error(), want)
		}
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("requests = %d, want 1 (the same bytes decode the same way)", got)
	}
}

func TestA200WithBrokenJSONIsOneRequest(t *testing.T) {
	c, hits := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": `)) // complete as sent, not JSON: Content-Length matches
	})

	var out agentStandIn
	err := c.Get("/agents/x", &out)

	var shape *UnexpectedShapeError
	if !errors.As(err, &shape) {
		t.Fatalf("want an UnexpectedShapeError, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestABodyCutOffMidReadIsRetriedTwice(t *testing.T) {
	c, hits := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "100") // promises more than it sends
		_, _ = w.Write([]byte(`{"id": "ab`))
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.(net.Conn).Close()
		}
	})

	var out agentStandIn
	err := c.Get("/agents/x", &out)

	if err == nil {
		t.Fatal("a cut-off body produced no error")
	}
	var shape *UnexpectedShapeError
	if errors.As(err, &shape) || isDecodeError(err) {
		t.Fatalf("a transport failure was classified as a decode error: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 3 {
		t.Errorf("requests = %d, want 3 (the first try and two retries)", got)
	}
}

func TestAMutatingRequestIsNeverRetriedWhateverFails(t *testing.T) {
	c, hits := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte(`{"id": "ab`))
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.(net.Conn).Close()
		}
	})

	var out agentStandIn
	if err := c.Post("/agents", map[string]string{"name": "x"}, &out); err == nil {
		t.Fatal("a cut-off body produced no error")
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}
