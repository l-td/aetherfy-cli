// Package yamldiff computes the per-field difference between a local
// aetherfy.yaml and an agent's current configuration: what `afy deploy` of
// that file would change.
//
// aetherfy.yaml owns how the agent runs, and a deploy applies the WHOLE file:
//   - a key PRESENT with a value      → the field becomes that value;
//   - a key PRESENT as `null`         → the field is unset;
//   - a key ABSENT                    → the field becomes its DEFAULT (Defaults),
//     never the value the agent had before.
//
// There is no "preserved" field: nothing outside the file survives a deploy.
//
// The server map comes from the control-plane GET /agents/{id}/yaml export
// (serialize_agent_to_yaml): the current value of every set field, unset
// nullable fields omitted.
package yamldiff

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Defaults is what a field left out of aetherfy.yaml becomes on deploy: the
// control plane's AetherfyConfig defaults (shared/config_parser.py), decoded
// as yaml.v3 decodes the same value in a file. A COPY, held to the model by
// archive's TestKnownFieldsEqualTheControlPlaneModel (read by test/cpyaml), so
// a default changed there turns this repo's tests red. runtime is required and
// has none.
var Defaults = map[string]interface{}{
	"type":                 "service",
	"spawn":                nil,
	"regions":              nil,
	"memory_mb":            256,
	"idle_timeout_minutes": 5,
	"keep_alive":           false,
	"entrypoint":           nil,
	"schedule":             nil,
	"github_dependencies":  []interface{}{},
}

// Kind classifies what `afy deploy` would do to a field.
type Kind int

const (
	// NoOp: the deploy leaves the field as it is.
	NoOp Kind = iota
	// Change: the field gets a different value.
	Change
	// Add: the field is unset now and gets a value.
	Add
	// Clear: the field has a value and becomes unset.
	Clear
	// NotCompared: declared in the file, but the agent's current value cannot
	// be read (NotExported), so nothing can be said about it. Not a change.
	NotCompared
)

// NotExported are fields the control plane's export (serialize_agent_to_yaml)
// never carries and no other read gives the CLI. A declared one is
// NotCompared rather than shown as an Add on every run. (regions is not here:
// the caller fills it from the agent's deployed footprint.)
var NotExported = map[string]bool{"github_dependencies": true}

// FieldDiff is one field's classification, with rendered values for display.
// Defaulted says the file leaves the field out, so Declared is its default.
type FieldDiff struct {
	Key       string
	Kind      Kind
	Current   string // rendered current (server) value; "" if unset
	Declared  string // rendered value the deploy gives it; "null" if unset
	Defaulted bool
}

// canonicalOrder mirrors serialize_agent_to_yaml so the diff reads
// top-to-bottom like the file. Unknown keys follow, sorted.
var canonicalOrder = []string{
	"runtime", "type", "memory_mb", "idle_timeout_minutes", "keep_alive",
	"entrypoint", "schedule", "spawn", "regions", "github_dependencies",
}

// Diff classifies every field present in either map.
func Diff(local, server map[string]interface{}) []FieldDiff {
	var out []FieldDiff
	for _, key := range orderedKeys(local, server) {
		lv, lp := local[key]
		sv, sp := server[key]

		defaulted := false
		if !lp {
			def, has := Defaults[key]
			if !sp || !has {
				// Unset on both sides, or a field with no default (runtime)
				// left out: nothing this file would change.
				continue
			}
			lv, defaulted = def, true
		}

		d := FieldDiff{Key: key, Declared: render(lv), Defaulted: defaulted}
		if sp {
			d.Current = render(sv)
		}
		switch {
		case !sp && NotExported[key] && !isUnset(lv):
			d.Kind = NotCompared
		case sp && equalValue(key, lv, sv), !sp && isUnset(lv):
			d.Kind = NoOp
		case !sp:
			d.Kind = Add
		case isUnset(lv):
			d.Kind = Clear
		default:
			d.Kind = Change
		}
		out = append(out, d)
	}
	return out
}

// HasChanges reports whether any field would change on deploy (Change / Add /
// Clear). Drives the CI-friendly exit code.
func HasChanges(diffs []FieldDiff) bool {
	for _, d := range diffs {
		switch d.Kind {
		case Change, Add, Clear:
			return true
		}
	}
	return false
}

// isUnset: a value that leaves the field unset (null, or an empty list).
func isUnset(v interface{}) bool {
	if v == nil {
		return true
	}
	if l, ok := v.([]interface{}); ok && len(l) == 0 {
		return true
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

// equalValue compares one field's two values. regions is a SET: the deployed
// footprint is not in the order the file lists it.
func equalValue(key string, a, b interface{}) bool {
	if key == "regions" {
		as, aok := stringSet(a)
		bs, bok := stringSet(b)
		if aok && bok {
			return reflect.DeepEqual(as, bs)
		}
	}
	return equalValues(a, b)
}

func stringSet(v interface{}) ([]string, bool) {
	l, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(l))
	for _, e := range l {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out, true
}

func equalValues(a, b interface{}) bool {
	if isUnset(a) && isUnset(b) {
		return true
	}
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
