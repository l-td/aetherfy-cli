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
schedule:
  cron: "0 3 * * *"
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
	if d := find(t, diffs, "schedule"); d.Kind != Clear || d.Current != "{cron: 0 3 * * *}" {
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

// regions is a SET: the same regions in another order are not a change; a
// different set is.
func TestRegionsReorderedAreNotAChange(t *testing.T) {
	server := mustMap(t, "runtime: python3.11\nregions: [eu-central-1, us-east-1]\n")
	diffs := Diff(mustMap(t, "runtime: python3.11\nregions: [us-east-1, eu-central-1]\n"), server)
	if d := find(t, diffs, "regions"); d.Kind != NoOp {
		t.Errorf("the same regions reordered = %+v, want NoOp", d)
	}
	if HasChanges(diffs) {
		t.Error("a reordering is never a change")
	}
	diffs = Diff(mustMap(t, "runtime: python3.11\nregions: [us-east-1]\n"), server)
	if d := find(t, diffs, "regions"); d.Kind != Change {
		t.Errorf("a different set = %+v, want Change", d)
	}
}

// THE SCHEDULE IS A BLOCK. The same block is no change, whether the export writes
// a null deadline or leaves the key out; a different expression, or a deadline
// added, changed or dropped, is a change.
func TestTheScheduleBlockIsComparedAsABlockWhoseNullKeysAreAbsent(t *testing.T) {
	base := "runtime: python3.11\n"
	for name, c := range map[string]struct {
		local, server string
		want          Kind
	}{
		"the same block":                      {"schedule:\n  cron: \"0 3 * * *\"\n", "schedule:\n  cron: \"0 3 * * *\"\n", NoOp},
		"a null deadline is the key left out": {"schedule:\n  cron: \"0 3 * * *\"\n", "schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: null\n", NoOp},
		"a null deadline on the file's side":  {"schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: null\n", "schedule:\n  cron: \"0 3 * * *\"\n", NoOp},
		"the same block with a deadline":      {"schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: 30\n", "schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: 30\n", NoOp},
		"another expression":                  {"schedule:\n  cron: \"0 4 * * *\"\n", "schedule:\n  cron: \"0 3 * * *\"\n", Change},
		"a deadline added":                    {"schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: 30\n", "schedule:\n  cron: \"0 3 * * *\"\n", Change},
		"a deadline changed":                  {"schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: 45\n", "schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: 30\n", Change},
		"a deadline dropped":                  {"schedule:\n  cron: \"0 3 * * *\"\n", "schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: 30\n", Change},
		"a schedule added":                    {"schedule:\n  cron: \"0 3 * * *\"\n", "", Add},
	} {
		diffs := Diff(mustMap(t, base+c.local), mustMap(t, base+c.server))
		if d := find(t, diffs, "schedule"); d.Kind != c.want {
			t.Errorf("%s: schedule = %+v, want kind %v", name, d, c.want)
		}
	}
	// The block renders as the block.
	diffs := Diff(mustMap(t, base+"schedule:\n  cron: \"0 3 * * *\"\n  deadline_minutes: 30\n"), mustMap(t, base))
	if d := find(t, diffs, "schedule"); d.Declared != "{cron: 0 3 * * *, deadline_minutes: 30}" {
		t.Errorf("schedule render = %q", d.Declared)
	}
}

func TestNullOnAbsentServerFieldIsNoOp(t *testing.T) {
	server := mustMap(t, "runtime: python3.11\n")
	diffs := Diff(mustMap(t, "runtime: python3.11\nschedule: null\n"), server)
	if d := find(t, diffs, "schedule"); d.Kind != NoOp {
		t.Errorf("schedule = %+v, want NoOp (already unset)", d)
	}
}

// drain_timeout_minutes is a yaml-owned field whose default is 30: an omitted line
// is the default, so a service running 30 shows no change, one running 90 shows
// the file would reset it, and a declared value that differs is a change.
func TestTheDrainTimeoutIsComparedAsItsDefault(t *testing.T) {
	base := "runtime: python3.11\ntype: service\n"

	d := find(t, Diff(mustMap(t, base), mustMap(t, base+"drain_timeout_minutes: 30\n")), "drain_timeout_minutes")
	if d.Kind != NoOp || !d.Defaulted {
		t.Errorf("an omitted line against a running 30 = %+v, want NoOp (the default)", d)
	}

	d = find(t, Diff(mustMap(t, base), mustMap(t, base+"drain_timeout_minutes: 90\n")), "drain_timeout_minutes")
	if d.Kind != Change || d.Declared != "30" || !d.Defaulted {
		t.Errorf("an omitted line against a running 90 = %+v, want Change 90→30 (default)", d)
	}

	d = find(t, Diff(mustMap(t, base+"drain_timeout_minutes: 120\n"), mustMap(t, base+"drain_timeout_minutes: 30\n")), "drain_timeout_minutes")
	if d.Kind != Change || d.Declared != "120" || d.Defaulted {
		t.Errorf("a declared 120 against a running 30 = %+v, want Change 30→120", d)
	}
}
