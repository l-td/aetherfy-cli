// Package cpreadiness extracts the control plane's resume-readiness vocabulary
// -- the values POST /agents/{id}/start answers in `readiness` -- from the
// sibling aetherfy-control-plane checkout.
//
// WHY IT EXISTS. `afy start` switches on those values. If the control plane
// renamed one, the CLI would fall into its catch-all branch and print "did not
// confirm" about an agent that is serving, and nothing would go red: the CLI's
// tests feed it the values it already knows. So the CLI's set is compared with
// the control plane's own constants wherever that checkout exists -- a dev box
// with the sibling, and the e2e nightly, which sets cperrors.RequireEnv.
//
// NO COMMITTED SNAPSHOT, unlike cperrors and cplink, and on purpose: the set is
// four words, nothing in this repo needs it where the control plane is absent,
// and a snapshot would add a generator and a push gate to pin what a live read
// already answers. The cost is that this repo's own CI, which checks out no
// sibling, does not run the comparison; the nightly does.
package cpreadiness

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SourcePath is where the constants live, CP-repo-relative.
const SourcePath = "orchestrator/fly_manager.py"

// A readiness constant: `READINESS_X = "value"` or `READINESS_X = OTHER_NAME`.
// Module level only (column 0). The private `_READINESS_RANK` does not match.
var readinessDecl = regexp.MustCompile(`^(READINESS_[A-Z0-9_]+) = (?:"([^"]*)"|([A-Za-z_][A-Za-z0-9_]*))\s*(?:#.*)?$`)

// Any module-level string constant, for resolving an alias one hop.
var stringConst = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*) = "([^"]*)"\s*(?:#.*)?$`)

// Value is one readiness constant.
type Value struct {
	Name  string
	Value string
}

// Extract reads the readiness constants from cpRoot. An alias is resolved to the
// string constant it names in the same file; an alias that resolves to nothing
// is an error, never a silently dropped value.
func Extract(cpRoot string) ([]Value, error) {
	raw, err := os.ReadFile(filepath.Join(cpRoot, filepath.FromSlash(SourcePath)))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	consts := map[string]string{}
	for _, l := range lines {
		if m := stringConst.FindStringSubmatch(l); m != nil {
			consts[m[1]] = m[2]
		}
	}

	var out []Value
	for _, l := range lines {
		m := readinessDecl.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if m[3] == "" {
			out = append(out, Value{Name: m[1], Value: m[2]})
			continue
		}
		v, ok := consts[m[3]]
		if !ok {
			return nil, fmt.Errorf("%s = %s, which is not a string constant in %s", m[1], m[3], SourcePath)
		}
		out = append(out, Value{Name: m[1], Value: v})
	}
	return out, nil
}

// Validate refuses an extraction that could agree with anything: nothing found,
// an empty value, or two names spelling the same value.
func Validate(vals []Value) error {
	if len(vals) < 2 {
		return fmt.Errorf("found %d READINESS_* constant(s) in %s -- the parser has rotted or the "+
			"constants moved; refusing to treat that as agreement", len(vals), SourcePath)
	}
	seen := map[string]string{}
	for _, v := range vals {
		if v.Value == "" {
			return fmt.Errorf("%s is empty", v.Name)
		}
		if other, dup := seen[v.Value]; dup {
			return fmt.Errorf("%s and %s both spell %q", other, v.Name, v.Value)
		}
		seen[v.Value] = v.Name
	}
	return nil
}

// Set is the sorted list of values.
func Set(vals []Value) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, v.Value)
	}
	sort.Strings(out)
	return out
}
