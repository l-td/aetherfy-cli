package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A resume that takes longer than the client's usual bound -- the control
// plane retrying a full host, then recreating the machine -- is waited for,
// not reported as a failure; and the client's bound is back afterwards.
// Scaled down: the usual bound is 50ms here and the resume takes 300ms.
func TestAResumeLongerThanTheUsualBoundIsWaitedFor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"running","agent_id":"a","readiness":"slow_start","machines_recreated":1}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClientWithURL(srv.URL, "afy_test_key")
	c.http.SetTimeout(50 * time.Millisecond)
	saved := StartAgentTimeout
	StartAgentTimeout = 5 * time.Second
	t.Cleanup(func() { StartAgentTimeout = saved })

	result, err := c.StartAgent("api")

	if err != nil {
		t.Fatalf("a resume slower than the usual bound was reported as failed: %v", err)
	}
	if result.MachinesRecreated != 1 {
		t.Errorf("machines_recreated = %d, want 1", result.MachinesRecreated)
	}
	if got := c.http.GetClient().Timeout; got != 50*time.Millisecond {
		t.Errorf("the client's bound was left at %v after the resume", got)
	}
}
