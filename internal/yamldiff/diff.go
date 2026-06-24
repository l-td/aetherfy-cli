// Package yamldiff computes the per-field difference between a local
// aetherfy.yaml and an agent's current server state, under JSON Merge Patch
// (RFC 7396) semantics — the model `afy deploy` applies (REVIEW_FAQ §64).
//
// Both inputs are AetherfyConfig-shaped maps parsed by yaml.v3:
//   - a key PRESENT with a non-nil value → the field is declared
//   - a key PRESENT with a nil value     → declared as explicit `null` (clear)
//   - a key ABSENT                        → omitted (deploy preserves it)
//
// The server map comes from the control-plane GET /agents/{name}/yaml export
// (serialize_agent_to_yaml), so it contains the current value for every set
// field and omits unset nullable fields — exactly the shape the local file
// uses. Keeping the comparison purely over maps makes it trivially testable.
package yamldiff

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Kind classifies what `afy deploy` would do to a field.
type Kind int

const (
	// NoOp: declared locally with the same value the server already has.
	NoOp Kind = iota
	// Change: declared locally with a value different from the server's.
	Change
	// Add: declared locally; the server has no value for it yet.
	Add
	// Clear: declared locally as `null`; the server has a value to clear.
	Clear
	// Preserve: omitted locally; the server has a value deploy will keep.
	Preserve
)

// FieldDiff is one field's classification, with rendered values for display.
type FieldDiff struct {
	Key      string
	Kind     Kind
	Current  string // rendered current (server) value; "" if none
	Declared string // rendered local value; "" if omitted; "null" if explicit null
}

// canonicalOrder mirrors a hand-written aetherfy.yaml / serialize_agent_to_yaml
// so the diff reads top-to-bottom like the file. Unknown keys follow, sorted.
var canonicalOrder = []string{
	"name", "runtime", "type", "description",
	"memory_mb", "idle_timeout_minutes", "keep_alive",
	"workspace", "database_collection", "entrypoint", "spawn", "regions",
}

// Diff classifies every field present in either map.
func Diff(local, server map[string]interface{}) []FieldDiff {
	var out []FieldDiff
	for _, key := range orderedKeys(local, server) {
		lv, lp := local[key]
		sv, sp := server[key]

		switch {
		case !lp && sp:
			// Omitted locally, server has it → preserved on deploy.
			out = append(out, FieldDiff{key, Preserve, render(sv), ""})
		case lp && lv == nil:
			// Explicit `null` → clear (only meaningful when the server has a
			// value; clearing an already-absent field is a no-op).
			if sp {
				out = append(out, FieldDiff{key, Clear, render(sv), "null"})
			} else {
				out = append(out, FieldDiff{key, NoOp, "", "null"})
			}
		case equalValues(lv, sv):
			out = append(out, FieldDiff{key, NoOp, render(sv), render(lv)})
		case sp:
			out = append(out, FieldDiff{key, Change, render(sv), render(lv)})
		default:
			out = append(out, FieldDiff{key, Add, "", render(lv)})
		}
	}
	return out
}

// HasChanges reports whether any field would change on deploy (Change / Add /
// Clear). NoOp and Preserve are not changes. Drives the CI-friendly exit code.
func HasChanges(diffs []FieldDiff) bool {
	for _, d := range diffs {
		switch d.Kind {
		case Change, Add, Clear:
			return true
		}
	}
	return false
}

func orderedKeys(local, server map[string]interface{}) []string {
	set := map[string]bool{}
	for k := range local {
		set[k] = true
	}
	for k := range server {
		set[k] = true
	}
	used := map[string]bool{}
	var keys []string
	for _, k := range canonicalOrder {
		if set[k] {
			keys = append(keys, k)
			used[k] = true
		}
	}
	var extras []string
	for k := range set {
		if !used[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	return append(keys, extras...)
}

func equalValues(a, b interface{}) bool {
	return reflect.DeepEqual(a, b)
}

// render produces a compact one-line representation of a YAML value for the
// diff display (scalars as-is, lists as [a, b], maps as {k: v}).
func render(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case []interface{}:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = render(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(t))
		for _, k := range keys {
			parts = append(parts, k+": "+render(t[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", t)
	}
}
