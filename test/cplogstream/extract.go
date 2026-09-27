// Package cplogstream extracts the control plane's platform log stream -- the
// stream name its own lines and the supervisor's are filed under -- from the
// sibling aetherfy-control-plane checkout.
//
// WHY IT EXISTS. Platform lines carry no "[SYSTEM] " tag in their message any
// more; the stream is the only thing that tells one from the customer's own
// output, and `afy logs` prints a platform ERROR as "[SYSTEM ERROR]" by
// comparing it (cmd/logs.go systemStream). If the control plane renamed the
// stream, platform errors would print as plain "[ERROR]" and nothing would go
// red: the CLI's tests feed it the name it already knows.
//
// NO COMMITTED SNAPSHOT, like cpreadiness and for the same reason: one word,
// read live wherever the checkout exists -- a dev box with the sibling, and the
// e2e nightly (cperrors.RequireEnv).
package cplogstream

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SourcePath is where the constant lives, CP-repo-relative.
const SourcePath = "models/agent_log.py"

// Module level only (column 0).
var decl = regexp.MustCompile(`^SYSTEM_LOG_STREAM = "([^"]*)"\s*(?:#.*)?$`)

// Extract returns the value of SYSTEM_LOG_STREAM. Exactly one declaration, and a
// non-empty value, or an error: an extraction that found nothing must never
// read as agreement.
func Extract(cpRoot string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(cpRoot, filepath.FromSlash(SourcePath)))
	if err != nil {
		return "", err
	}
	var found []string
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if m := decl.FindStringSubmatch(l); m != nil {
			found = append(found, m[1])
		}
	}
	if len(found) != 1 || found[0] == "" {
		return "", fmt.Errorf("expected exactly one non-empty SYSTEM_LOG_STREAM in %s, found %q", SourcePath, found)
	}
	return found[0], nil
}
