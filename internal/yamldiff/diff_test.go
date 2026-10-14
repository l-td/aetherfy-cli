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

func absent(t *testing.T, diffs []FieldDiff, key string) {
	t.Helper()
	for _, d := range diffs {
		if d.Key == key {
			t.Errorf("%s: want no row, got %+v", key, d)
		}
	}
}

func TestDiffCategorizesEachState(t *testing.T) {
	// server = the agent's current configuration (the export); local = the
	// aetherfy.yaml on disk.
	server := mustMap(t, `
runtime: python3.11
type: job
memory_mb: 1024
idle_timeout_minutes: 5
keep_alive: true
schedule: "0 3 * * *"
`)
	local := mustMap(t, `
runtime: python3.11
memory_mb: 512
keep_alive: true
schedule: null
entrypoint: main.py
`)

	diffs := Diff(local, server)

	if d := find(t, diffs, "runtime"); d.Kind != NoOp {
		t.Errorf("runtime kind = %v, want NoOp", d.Kind)
	}
	if d := find(t, diffs, "memory_mb"); d.Kind != Change || d.Current != "1024" || d.Declared != "512" {
		t.Errorf("memory_mb = %+v, want Change 1024→512", d)
	}
	if d := find(t, diffs, "keep_alive"); d.Kind != NoOp {
		t.Errorf("keep_alive kind = %v, want NoOp", d.Kind)
	}
	// Explicit null over a value → Clear.
	if d := find(t, diffs, "schedule"); d.Kind != Clear || d.Current != "0 3 * * *" {
		t.Errorf("schedule = %+v, want Clear", d)
	}
	// Declared, unset on the server → Add.
	if d := find(t, diffs, "entrypoint"); d.Kind != Add || d.Declared != "main.py" {
		t.Errorf("entrypoint = %+v, want Add main.py", d)
	}
}

// NOTHING IS PRESERVED. A field the file leaves out becomes its default on
// deploy: a change when the agent has something else, a no-op when it already
// has the default.
func TestAnOmittedFieldIsComparedAsItsDefault(t *testing.T) {
	server := mustMap(t, "runtime: python3.11\ntype: job\nidle_timeout_minutes: 5\nkeep_alive: true\n")
	diffs := Diff(mustMap(t, "runtime: python3.11\n"), server)

	if d := find(t, diffs, "type"); d.Kind != Change || d.Current != "job" || d.Declared != "service" || !d.Defaulted {
		t.Errorf("type = %+v, want Change job→service (default)", d)
	}
	if d := find(t, diffs, "keep_alive"); d.Kind != Change || d.Declared != "false" || !d.Defaulted {
		t.Errorf("keep_alive = %+v, want Change true→false (default)", d)
	}
	if d := find(t, diffs, "idle_timeout_minutes"); d.Kind != NoOp || !d.Defaulted {
		t.Errorf("idle_timeout_minutes = %+v, want NoOp: the agent already has the default", d)
	}
	// Unset on both sides: nothing to say.
	absent(t, diffs, "schedule")
	absent(t, diffs, "github_dependencies")
	if !HasChanges(diffs) {
		t.Error("type and keep_alive change, so HasChanges must be true")
	}
}

func TestHasChanges(t *testing.T) {
	server := mustMap(t, "runtime: python3.11\ntype: service\nmemory_mb: 512\nidle_timeout_minutes: 5\nkeep_alive: false\n")

	noChange := Diff(mustMap(t, "runtime: python3.11\nmemory_mb: 512\n"), server)
	if HasChanges(noChange) {
		t.Errorf("expected no changes (the omitted fields already hold their defaults), got %+v", noChange)
	}

	changed := Diff(mustMap(t, "runtime: python3.11\nmemory_mb: 1024\n"), server)
	if !HasChanges(changed) {
		t.Errorf("expected changes for memory_mb 512→1024")
	}
}

func TestRenderListsAndMaps(t *testing.T) {
	server := mustMap(t, "runtime: python3.11\n")
	local := mustMap(t, `
runtime: python3.11
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

// regions and github_dependencies are in the export (ccc10c6) and compared
// like every other field: a difference is a change, an equal value is not.
func TestRegionsAndGithubDependenciesAreComparedLikeAnyField(t *testing.T) {
	server := mustMap(t, "runtime: python3.11\nregions: [eu-central-1]\ngithub_dependencies: [org/lib@v1]\n")

	same := Diff(mustMap(t, "runtime: python3.11\nregions: [eu-central-1]\ngithub_dependencies: [org/lib@v1]\n"), server)
	if HasChanges(same) {
		t.Errorf("equal regions and dependencies must not be a change: %+v", same)
	}

	diffs := Diff(mustMap(t, "runtime: python3.11\nregions: [us-east-1]\ngithub_dependencies: [org/lib@v2]\n"), server)
	for _, key := range []string{"regions", "github_dependencies"} {
		if d := find(t, diffs, key); d.Kind != Change {
			t.Errorf("%s = %+v, want Change", key, d)
		}
	}

	// Omitted: they take their defaults (none / no dependencies) -- a change
	// from what the release has.
	omitted := Diff(mustMap(t, "runtime: python3.11\n"), server)
	if d := find(t, omitted, "github_dependencies"); d.Kind != Clear || !d.Defaulted {
		t.Errorf("github_dependencies omitted = %+v, want Clear (default)", d)
	}
}

func TestNullOnAbsentServerFieldIsNoOp(t *testing.T) {
	server := mustMap(t, "runtime: python3.11\n")
	diffs := Diff(mustMap(t, "runtime: python3.11\nschedule: null\n"), server)
	if d := find(t, diffs, "schedule"); d.Kind != NoOp {
		t.Errorf("schedule = %+v, want NoOp (already unset)", d)
	}
}
