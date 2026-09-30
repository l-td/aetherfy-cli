// Package cpresume extracts the control plane's published reasons a start
// asked for during a stop was dropped -- AgentResponse.resume.reason, the
// RESUME_DROP_REASONS tuple -- from the sibling aetherfy-control-plane checkout.
//
// WHY IT EXISTS. `afy start --wait` and `afy status` print a sentence per
// reason. A reason added or renamed there would print the catch-all sentence
// here with every CLI test green -- they feed the CLI reasons it already knows.
// So the CLI's set is compared with the control plane's own tuple wherever that
// checkout exists, as cpreadiness does for the readiness values: live, no
// committed snapshot, skipped in this repo's CI, run by the e2e nightly.
package cpresume

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SourcePath is where the tuple lives, CP-repo-relative.
const SourcePath = "workers/lifecycle_decisions.py"

var (
	tupleStart = regexp.MustCompile(`^RESUME_DROP_REASONS = \($`)
	tupleEntry = regexp.MustCompile(`^\s+"([a-z_]+)",\s*(?:#.*)?$`)
	tupleEnd   = regexp.MustCompile(`^\)\s*$`)
)

// Extract reads the tuple's entries from cpRoot, in order. A tuple that is not
// found, or not closed, is an error -- never an empty agreement.
func Extract(cpRoot string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(cpRoot, filepath.FromSlash(SourcePath)))
	if err != nil {
		return nil, err
	}
	var out []string
	inside, closed := false, false
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		switch {
		case !inside && tupleStart.MatchString(l):
			inside = true
		case inside && tupleEnd.MatchString(l):
			inside, closed = false, true
		case inside:
			m := tupleEntry.FindStringSubmatch(l)
			if m == nil {
				return nil, fmt.Errorf("a RESUME_DROP_REASONS entry this reader cannot vouch for: %q", l)
			}
			out = append(out, m[1])
		}
		if closed {
			break
		}
	}
	if !closed {
		return nil, fmt.Errorf("no closed RESUME_DROP_REASONS = ( ... ) tuple in %s", SourcePath)
	}
	return out, nil
}

// Validate refuses an extraction that could agree with anything.
func Validate(reasons []string) error {
	if len(reasons) < 2 {
		return fmt.Errorf("found %d reason(s) in %s -- refusing to treat that as agreement", len(reasons), SourcePath)
	}
	seen := map[string]bool{}
	for _, r := range reasons {
		if seen[r] {
			return fmt.Errorf("%q is listed twice in RESUME_DROP_REASONS", r)
		}
		seen[r] = true
	}
	return nil
}

// Set is the sorted list.
func Set(reasons []string) []string {
	out := append([]string(nil), reasons...)
	sort.Strings(out)
	return out
}
