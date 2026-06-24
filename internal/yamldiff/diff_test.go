package yamldiff

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// mustMap parses YAML into a map, preserving the key-present/absent and
// explicit-null distinctions the diff relies on.
func mustMap(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

// find returns the FieldDiff for key, or fails.
func find(t *testing.T, diffs []FieldDiff, key string) FieldDiff {
	t.Helper()
	for _, d := range diffs {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("no diff for key %q", key)
	return FieldDiff{}
}

func TestDiffCategorizesEachState(t *testing.T) {
	// server = current state; local = the aetherfy.yaml on disk.
	server := mustMap(t, `
name: svc
runtime: python3.11
type: service
memory_mb: 1024
keep_alive: true
workspace: ws-a
`)
	local := mustMap(t, `
name: svc
runtime: python3.11
memory_mb: 512
keep_alive: true
workspace: null
database_collection: coll-new
`)

	diffs := Diff(local, server)

	// name: declared, same → NoOp
	if d := find(t, diffs, "name"); d.Kind != NoOp {
		t.Errorf("name kind = %v, want NoOp", d.Kind)
	}
	// memory_mb: declared 512 vs server 1024 → Change
	if d := find(t, diffs, "memory_mb"); d.Kind != Change || d.Current != "1024" || d.Declared != "512" {
		t.Errorf("memory_mb = %+v, want Change 1024→512", d)
	}
	// keep_alive: declared true, server true → NoOp
	if d := find(t, diffs, "keep_alive"); d.Kind != NoOp {
		t.Errorf("keep_alive kind = %v, want NoOp", d.Kind)
	}
	// type: omitted locally, server has it → Preserve
	if d := find(t, diffs, "type"); d.Kind != Preserve || d.Current != "service" {
		t.Errorf("type = %+v, want Preserve service", d)
	}
	// workspace: explicit null, server has ws-a → Clear
	if d := find(t, diffs, "workspace"); d.Kind != Clear || d.Current != "ws-a" {
		t.Errorf("workspace = %+v, want Clear ws-a", d)
	}
	// database_collection: declared, server absent → Add
	if d := find(t, diffs, "database_collection"); d.Kind != Add || d.Declared != "coll-new" {
		t.Errorf("database_collection = %+v, want Add coll-new", d)
	}
}

func TestHasChanges(t *testing.T) {
	server := mustMap(t, "name: svc\nruntime: python3.11\nmemory_mb: 512\n")

	// Identical declaration (plus an omitted field that's preserved) → no changes.
	noChange := Diff(mustMap(t, "name: svc\nmemory_mb: 512\n"), server)
	if HasChanges(noChange) {
		t.Errorf("expected no changes, got %+v", noChange)
	}

	// A differing value → changes.
	changed := Diff(mustMap(t, "name: svc\nmemory_mb: 1024\n"), server)
	if !HasChanges(changed) {
		t.Errorf("expected changes for memory_mb 512→1024")
	}
}

func TestRenderListsAndMaps(t *testing.T) {
	server := mustMap(t, "name: svc\n")
	local := mustMap(t, `
name: svc
regions:
  - us-east-1
  - eu-central-1
spawn:
  enabled: true
  workers:
    - w1
`)
	diffs := Diff(local, server)
	if d := find(t, diffs, "regions"); d.Declared != "[us-east-1, eu-central-1]" {
		t.Errorf("regions render = %q", d.Declared)
	}
	if d := find(t, diffs, "spawn"); d.Declared != "{enabled: true, workers: [w1]}" {
		t.Errorf("spawn render = %q", d.Declared)
	}
}

func TestNullOnAbsentServerFieldIsNoOp(t *testing.T) {
	// `workspace: null` when the agent is already workspaceless → no-op clear.
	server := mustMap(t, "name: svc\n")
	local := mustMap(t, "name: svc\nworkspace: null\n")
	diffs := Diff(local, server)
	if d := find(t, diffs, "workspace"); d.Kind != NoOp {
		t.Errorf("workspace null-on-absent = %v, want NoOp", d.Kind)
	}
	if HasChanges(diffs) {
		t.Errorf("null-on-absent should not count as a change")
	}
}
