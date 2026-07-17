package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// RunAgent must POST to /agents/{name}/run, forward the payload under a
// "payload" key, and decode the 202 {deployment_id, version, job_id} body.
func TestRunAgent_PostsPayload(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"deployment_id":"dep-1","version":3,"job_id":"job-9"}`))
	}))
	defer srv.Close()

	client := NewClientWithURL(srv.URL, "afy_test_key")
	resp, err := client.RunAgent("nightly", map[string]interface{}{"date": "2026-07-17"})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/agents/nightly/run" {
		t.Errorf("path = %s, want /agents/nightly/run", gotPath)
	}
	payload, ok := gotBody["payload"].(map[string]interface{})
	if !ok || payload["date"] != "2026-07-17" {
		t.Errorf("payload not forwarded: %v", gotBody)
	}
	if resp.DeploymentID != "dep-1" || resp.Version != 3 || resp.JobID != "job-9" {
		t.Errorf("response = %+v", resp)
	}
}

// A nil payload must omit the field entirely (optional body), not send
// "payload": null.
func TestRunAgent_NilPayloadOmitsField(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"deployment_id":"d","version":1,"job_id":"j"}`))
	}))
	defer srv.Close()

	client := NewClientWithURL(srv.URL, "afy_test_key")
	if _, err := client.RunAgent("nightly", nil); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if _, present := gotBody["payload"]; present {
		t.Errorf("payload should be omitted when nil, got %v", gotBody)
	}
}

// The new CP-4 run codes must surface with their code intact so the command
// layer can attach an actionable hint (AGENT_NOT_DEPLOYED -> "afy deploy").
func TestRunAgent_SurfacesNotDeployedCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":{"code":"AGENT_NOT_DEPLOYED","message":"Agent 'x' has not been deployed yet. Deploy it before running it."}}`))
	}))
	defer srv.Close()

	client := NewClientWithURL(srv.URL, "afy_test_key")
	_, err := client.RunAgent("x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T", err)
	}
	if apiErr.Code != "AGENT_NOT_DEPLOYED" {
		t.Errorf("code = %q, want AGENT_NOT_DEPLOYED", apiErr.Code)
	}
}

// ListAgentRuns must translate RunsQuery into the documented query params and
// omit empties.
func TestListAgentRuns_BuildsQuery(t *testing.T) {
	cases := []struct {
		name  string
		query RunsQuery
		want  map[string]string
	}{
		{"defaults empty", RunsQuery{}, map[string]string{}},
		{"limit only", RunsQuery{Limit: 50}, map[string]string{"limit": "50"}},
		{"trigger + before + limit", RunsQuery{TriggerSource: "cron", Before: "2026-07-17T00:00:00Z", Limit: 10},
			map[string]string{"trigger_source": "cron", "before": "2026-07-17T00:00:00Z", "limit": "10"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotQuery map[string]string
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotQuery = map[string]string{}
				for k, v := range r.URL.Query() {
					gotQuery[k] = v[0]
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("[]"))
			}))
			defer srv.Close()

			client := NewClientWithURL(srv.URL, "afy_test_key")
			if _, err := client.ListAgentRuns("nightly", tc.query); err != nil {
				t.Fatalf("ListAgentRuns: %v", err)
			}
			if gotPath != "/agents/nightly/runs" {
				t.Errorf("path = %s", gotPath)
			}
			if len(gotQuery) != len(tc.want) {
				t.Fatalf("query = %v, want %v", gotQuery, tc.want)
			}
			for k, want := range tc.want {
				if gotQuery[k] != want {
					t.Errorf("query[%q] = %q, want %q", k, gotQuery[k], want)
				}
			}
		})
	}
}

// ListAgentRuns must decode the documented row shape including the optional
// duration_seconds.
func TestListAgentRuns_ParsesRows(t *testing.T) {
	body := `[{"id":"dep-1","trigger_source":"cron","state":"completed","created_at":"2026-07-17T03:00:00Z","machine_started_at":"2026-07-17T03:00:01Z","machine_stopped_at":"2026-07-17T03:00:11Z","duration_seconds":10}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client := NewClientWithURL(srv.URL, "afy_test_key")
	runs, err := client.ListAgentRuns("nightly", RunsQuery{})
	if err != nil {
		t.Fatalf("ListAgentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	r := runs[0]
	if r.ID != "dep-1" || r.TriggerSource != "cron" || r.State != "completed" {
		t.Errorf("row = %+v", r)
	}
	if r.DurationSeconds == nil || *r.DurationSeconds != 10 {
		t.Errorf("duration = %v", r.DurationSeconds)
	}
}

// PauseSchedule / ResumeSchedule must POST to the right path and decode the
// {cron_paused, cron_next_run_at, changed} response.
func TestPauseResumeSchedule_PathsAndResponse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pause bool
		path  string
	}{
		{"pause", true, "/agents/nightly/schedule/pause"},
		{"resume", false, "/agents/nightly/schedule/resume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotMethod = r.Method
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"cron_paused":true,"cron_next_run_at":"2026-07-18T03:00:00Z","changed":true}`))
			}))
			defer srv.Close()

			client := NewClientWithURL(srv.URL, "afy_test_key")
			var resp *ScheduleStateResponse
			var err error
			if tc.pause {
				resp, err = client.PauseSchedule("nightly")
			} else {
				resp, err = client.ResumeSchedule("nightly")
			}
			if err != nil {
				t.Fatalf("toggle: %v", err)
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %s, want POST", gotMethod)
			}
			if gotPath != tc.path {
				t.Errorf("path = %s, want %s", gotPath, tc.path)
			}
			if !resp.Changed || !resp.CronPaused {
				t.Errorf("resp = %+v", resp)
			}
		})
	}
}

// The --run flag maps to the deployment_id query param on the logs endpoint.
func TestGetAgentLogs_DeploymentIDParam(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("deployment_id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	client := NewClientWithURL(srv.URL, "afy_test_key")
	if _, err := client.GetAgentLogs("nightly", LogQuery{DeploymentID: "dep-42"}); err != nil {
		t.Fatalf("GetAgentLogs: %v", err)
	}
	if got != "dep-42" {
		t.Errorf("deployment_id = %q, want dep-42", got)
	}
}
