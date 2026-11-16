package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A run over the account's limit is accepted into a line: the 202 says it waits,
// at what place, and has no job yet.
func TestRunAgent_DecodesAWaitingRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"deployment_id":"dep-1","version":3,"job_id":null,"status":"waiting","position":4}`))
	}))
	defer srv.Close()

	resp, err := NewClientWithURL(srv.URL, "afy_test_key").RunAgent("nightly", nil)
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if resp.Status != "waiting" || resp.Position == nil || *resp.Position != 4 || resp.JobID != nil {
		t.Errorf("response = %+v, want status waiting, position 4, no job", resp)
	}
}

// CancelRun posts to the run's own route (not the agent's release route) and
// decodes the answer: the run, what the call did, the server's sentence, how many
// runs it cancelled.
func TestCancelRun_PostsToTheRunAndDecodesTheAnswer(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run":{"id":"run-1","agent_id":"a1","version":7,"state":"cancelled",` +
			`"error_message":"Cancelled by its owner.","cancellation_reason":"user_cancel","position":null,` +
			`"created_at":"2026-09-01T10:00:00Z"},"outcome":"cancelled","message":"Run cancelled.","cancelled_count":3}`))
	}))
	defer srv.Close()

	resp, err := NewClientWithURL(srv.URL, "afy_test_key").CancelRun("run-1")
	if err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/deployments/run-1/cancel" {
		t.Errorf("request = %s %s, want POST /deployments/run-1/cancel", gotMethod, gotPath)
	}
	if resp.Outcome != "cancelled" || resp.Message != "Run cancelled." || resp.CancelledCount != 3 ||
		resp.Run.ID != "run-1" || resp.Run.AgentID != "a1" || resp.Run.Status != "cancelled" ||
		resp.Run.CancellationReason != "user_cancel" {
		t.Errorf("response = %+v", resp)
	}
}

// A run's id is one path segment, whatever it holds.
func TestCancelRun_EscapesTheRunID(t *testing.T) {
	var gotRawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run":{},"outcome":"already_finished","message":"m","cancelled_count":0}`))
	}))
	defer srv.Close()

	if _, err := NewClientWithURL(srv.URL, "afy_test_key").CancelRun("a/b"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if gotRawPath != "/deployments/a%2Fb/cancel" {
		t.Errorf("path = %s, want the id escaped into one segment", gotRawPath)
	}
}

// The server's refusal reaches the caller with its code, which the command prints
// as the server wrote it.
func TestCancelRun_SurfacesTheServersRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":{"code":"stand-in-code","message":"the stand-in refuses"}}`))
	}))
	defer srv.Close()

	_, err := NewClientWithURL(srv.URL, "afy_test_key").CancelRun("run-1")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != 404 || apiErr.Code != "stand-in-code" || apiErr.Message != "the stand-in refuses" {
		t.Fatalf("err = %#v, want the server's 404 with its code and message", err)
	}
}
