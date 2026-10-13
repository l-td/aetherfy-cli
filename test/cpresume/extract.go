// Package cpresume extracts the control plane's published vocabulary of an
// accepted start -- the reasons one was dropped (AgentResponse.resume.reason,
// the RESUME_DROP_REASONS tuple) and why one was accepted rather than carried
// out in the request (resume.cause, RESUME_CAUSES) -- from the sibling
// aetherfy-control-plane checkout.
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
	tupleEntry = regexp.MustCompile(`^\s+"([a-z_]+)",\s*(?:#.*)?$`)
	tupleEnd   = regexp.MustCompile(`^\)\s*$`)
)

// Extract reads RESUME_DROP_REASONS' entries from cpRoot, in order. A tuple
// that is not found, or not closed, is an error -- never an empty agreement.
func Extract(cpRoot string) ([]string, error) {
	return extractTuple(cpRoot, "RESUME_DROP_REASONS")
}

// ExtractCauses reads RESUME_CAUSES' entries, the same way.
func ExtractCauses(cpRoot string) ([]string, error) {
	return extractTuple(cpRoot, "RESUME_CAUSES")
}

func extractTuple(cpRoot, name string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(cpRoot, filepath.FromSlash(SourcePath)))
	if err != nil {
		return nil, err
	}
	start := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + ` = \($`)
	var out []string
	inside, closed := false, false
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		switch {
		case !inside && start.MatchString(l):
			inside = true
		case inside && tupleEnd.MatchString(l):
			inside, closed = false, true
		case inside:
			m := tupleEntry.FindStringSubmatch(l)
			if m == nil {
				return nil, fmt.Errorf("a %s entry this reader cannot vouch for: %q", name, l)
			}
			out = append(out, m[1])
		}
		if closed {
			break
		}
	}
	if !closed {
		return nil, fmt.Errorf("no closed %s = ( ... ) tuple in %s", name, SourcePath)
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
