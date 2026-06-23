package api

import (
	"encoding/json"
	"testing"
)

// These guard the WIRE CONTRACT for the partial-deploy degraded fields
// (control-plane AgentResponse / DeploymentResponse; REVIEW_FAQ §63). A typo in
// a `json:"..."` struct tag would silently unmarshal to false/0 and neither
// `go build` nor `go vet` would catch it — tags are opaque strings. These tests
// unmarshal representative server payloads and assert the health fields land.

func TestAgentUnmarshalsDegradedHealth(t *testing.T) {
	payload := `{
		"id": "a1", "name": "svc", "status": "running",
		"is_degraded": true, "regions_total": 3, "regions_ready": 2,
		"degraded_reason": "2/3 regions ready; pending: eu-central-1 (alert: info)"
	}`
	var a Agent
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !a.IsDegraded {
		t.Error("IsDegraded false — json tag drift on is_degraded?")
	}
	if a.RegionsReady != 2 || a.RegionsTotal != 3 {
		t.Errorf("regions = %d/%d, want 2/3", a.RegionsReady, a.RegionsTotal)
	}
	if a.DegradedReason == "" {
		t.Error("DegradedReason empty — json tag drift on degraded_reason?")
	}
}

func TestDeploymentUnmarshalsDegradedHealth(t *testing.T) {
	payload := `{
		"id": "d1", "agent_id": "a1", "version": 3, "state": "active",
		"regions": ["us-east-1","eu-central-1","ap-southeast-1"],
		"is_degraded": true, "regions_total": 3, "regions_ready": 2,
		"pending_region_alert_stage": "info"
	}`
	var d Deployment
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Status != "active" {
		t.Errorf("Status = %q, want active (the `state` tag)", d.Status)
	}
	if !d.IsDegraded || d.RegionsReady != 2 || d.RegionsTotal != 3 {
		t.Errorf("degraded health = %v %d/%d, want true 2/3", d.IsDegraded, d.RegionsReady, d.RegionsTotal)
	}
	if d.PendingRegionAlertStage != "info" {
		t.Errorf("PendingRegionAlertStage = %q, want info", d.PendingRegionAlertStage)
	}
}

// Older server / agent with no deployment omits the fields → must default to
// not-degraded, never a spurious DEGRADED badge.
func TestAgentUnmarshalDefaultsNotDegraded(t *testing.T) {
	var a Agent
	if err := json.Unmarshal([]byte(`{"id":"a1","name":"svc","status":"running"}`), &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.IsDegraded || a.RegionsTotal != 0 || a.RegionsReady != 0 {
		t.Errorf("absent health should default not-degraded/0, got degraded=%v %d/%d",
			a.IsDegraded, a.RegionsReady, a.RegionsTotal)
	}
}
