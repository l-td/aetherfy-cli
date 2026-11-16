package cmd

import (
	"strings"
	"testing"
)

// `afy spawn` prints what the server's 202 says: the run's id (spawn_id), its state
// and, when the account's runs in flight are at their limit and the run waits in
// the line, its place. It used to read fields the server does not send, so its
// Run ID was empty.

func spawnedCP(t *testing.T, body string) *fakeCP {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/parent/spawn", 202, body)
	return cp
}

func TestSpawnPrintsTheRunsIDAndState(t *testing.T) {
	cp := spawnedCP(t, `{"spawn_id":"run-9","job_id":"j-1","child_agent_id":"c-1","workspace":"default",`+
		`"region":"us-east-1","status":"queued","position":null,"estimated_start":"2026-09-01T10:00:00Z"}`)
	stdout, stderr, err := runAfy(t, cp, "spawn", "parent", "child")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, stderr)
	}
	for _, want := range []string{"✓ Agent spawned successfully!", "Run ID:", "run-9", "Status:", "queued", "Region:", "us-east-1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "in line") {
		t.Errorf("a run that is starting is said to wait:\n%s", stdout)
	}
}

func TestSpawnSaysARunThatWaitsInTheLineAndItsPlace(t *testing.T) {
	cp := spawnedCP(t, `{"spawn_id":"run-9","job_id":null,"child_agent_id":"c-1","workspace":"default",`+
		`"region":"us-east-1","status":"waiting","position":4,"estimated_start":"2026-09-01T10:00:00Z"}`)
	stdout, stderr, err := runAfy(t, cp, "spawn", "parent", "child")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "✓ Run accepted; waiting for a slot (#4 in line).") {
		t.Errorf("a waiting spawn does not say it waits, at its place:\n%s", stdout)
	}
	if !strings.Contains(stdout, "waiting (#4 in line)") || !strings.Contains(stdout, "run-9") {
		t.Errorf("the status line and run id are missing:\n%s", stdout)
	}
	if strings.Contains(stdout, "Agent spawned successfully!") {
		t.Errorf("a waiting run is said to have been spawned:\n%s", stdout)
	}
}

func TestSpawnJSONCarriesTheServersAnswerWithNullsAsNulls(t *testing.T) {
	cp := spawnedCP(t, `{"spawn_id":"run-9","job_id":null,"child_agent_id":"c-1","workspace":"default",`+
		`"region":"us-east-1","status":"waiting","position":4,"estimated_start":"2026-09-01T10:00:00Z"}`)
	stdout, stderr, err := runAfy(t, cp, "spawn", "parent", "child", "--json")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, stderr)
	}
	doc, _ := oneJSONDocument(t, "afy spawn --json", stdout).(map[string]interface{})
	if doc["spawn_id"] != "run-9" || doc["status"] != "waiting" || doc["position"] != 4.0 || doc["child_agent_id"] != "c-1" {
		t.Errorf("spawn --json = %v", doc)
	}
	if v, present := doc["job_id"]; !present || v != nil {
		t.Errorf("spawn --json job_id = %v (present %v), want an explicit null", v, present)
	}
}
