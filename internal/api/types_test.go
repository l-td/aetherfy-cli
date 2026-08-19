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

// The agent's region footprint arrives as a LIST (`regions`), and the
// timestamps as `created_at`/`updated_at`. All three were broken in the same
// way and for the same reason: the struct once declared a singular
// `Region string` that the server has never sent, and the server once omitted
// the timestamps the struct did declare. Both failure modes are silent —
// unmarshalling a missing key yields the zero value, never an error — so
// `Region:` printed blank on every agent and `Created:` printed
// "0001-01-01 00:00:00". Pin the real payload shape on this side.
func TestAgentUnmarshalsRegionsAndTimestamps(t *testing.T) {
	payload := `{
		"id": "a1", "name": "svc", "status": "running",
		"regions": ["us-east-1", "eu-central-1"],
		"pending_regions": ["eu-central-1"],
		"created_at": "2026-08-19T09:34:59+00:00",
		"updated_at": "2026-08-19T09:36:51+00:00"
	}`
	var a Agent
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(a.Regions) != 2 || a.Regions[0] != "us-east-1" {
		t.Errorf("Regions = %v — json tag drift on regions?", a.Regions)
	}
	if len(a.PendingRegions) != 1 || a.PendingRegions[0] != "eu-central-1" {
		t.Errorf("PendingRegions = %v — json tag drift on pending_regions?", a.PendingRegions)
	}
	if a.CreatedAt.IsZero() {
		t.Error("CreatedAt is the zero time — this is the 0001-01-01 bug")
	}
	if a.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is the zero time — this is the 0001-01-01 bug")
	}
}

// An agent with no ACTIVE deployment has no footprint, and the server sends no
// `regions` key at all. That must be an empty slice, never a panic and never a
// phantom value.
func TestAgentWithoutRegionsIsEmptyNotBroken(t *testing.T) {
	var a Agent
	if err := json.Unmarshal([]byte(`{"id":"a1","name":"svc","status":"pending"}`), &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(a.Regions) != 0 {
		t.Errorf("Regions = %v, want empty", a.Regions)
	}
}
