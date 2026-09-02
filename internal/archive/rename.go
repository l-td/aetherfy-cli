package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RewriteAgentName keeps a project directory pointing at the agent it just
// renamed.
//
// WHY THIS EXISTS AT ALL. `afy deploy` resolves its target from the `name:` in
// the local aetherfy.yaml. Rename an agent and that file still names the old
// one, so the next deploy from the directory gets AGENT_NOT_FOUND — and then
// offers to CREATE an agent under the old name, which reads like the fix and is
// not. Most people say yes, and end up with two agents.
//
// A rename that leaves the file behind is only half a rename. This is the other
// half, and it is why renaming lives in the CLI: the dashboard cannot reach
// the user's disk, so it cannot do this and its rename was removed.
//
// IT REWRITES ONE FIELD AND NOTHING ELSE. Not a YAML round-trip: unmarshalling
// and re-marshalling would silently discard every comment in the file and
// reorder the keys, which is a far bigger edit than the user asked for. The
// value is replaced in place, preserving the line's indentation, its spacing,
// its quoting style, any trailing comment and the file's line endings.
//
// IT NEVER TOUCHES A FILE THAT NAMES A DIFFERENT AGENT. That is the whole
// safety property: a directory whose aetherfy.yaml declares some other agent is
// some other project, and rewriting it would retarget that project's deploys at
// an agent nobody asked it to deploy. Mismatch is reported, never resolved.

// RewriteOutcome says what RewriteAgentName did, so the caller can say it too.
type RewriteOutcome int

const (
	// RewriteNoFile: no aetherfy.yaml in the directory. Nothing was touched.
	RewriteNoFile RewriteOutcome = iota
	// RewriteUnreadable: a file is there but could not be read or parsed.
	// Nothing was touched.
	RewriteUnreadable
	// RewriteNameMismatch: the file's `name:` points at a different agent.
	// Nothing was touched, deliberately.
	RewriteNameMismatch
	// RewriteNoNameField: the file parses but declares no top-level `name:`
	// (the deploy target came from --agent). Nothing was touched.
	RewriteNoNameField
	// RewriteDone: the field was rewritten.
	RewriteDone
)

// RewriteAgentName rewrites the top-level `name:` in dir/aetherfy.yaml from
// oldName to newName, but only if it currently reads exactly oldName.
//
// Returns the outcome and the path it looked at (always dir/aetherfy.yaml, so
// the caller can name it either way). The error is non-nil only when a write
// was attempted and failed — every "did not apply" case is an outcome, not an
// error, because none of them is a failure of the rename itself.
func RewriteAgentName(dir, oldName, newName string) (RewriteOutcome, string, error) {
	path := filepath.Join(dir, "aetherfy.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RewriteNoFile, path, nil
		}
		return RewriteUnreadable, path, nil
	}

	// Parse first, so "what does this file declare" is answered by the same
	// parser `afy deploy` uses rather than by the line scan below. The scan
	// then has to AGREE with it; if it cannot, nothing is written.
	cfg, err := ParseAetherfyConfig(dir)
	if err != nil {
		return RewriteUnreadable, path, nil
	}
	if cfg.Name == "" {
		return RewriteNoNameField, path, nil
	}
	if cfg.Name != oldName {
		return RewriteNameMismatch, path, nil
	}

	rewritten, ok := replaceTopLevelName(string(data), oldName, newName)
	if !ok {
		// The parser saw the old name but the line scan could not find it in a
		// shape it is willing to edit — a flow mapping, a multi-line value, an
		// anchor. Refusing beats guessing at someone's file.
		return RewriteUnreadable, path, nil
	}

	// Preserve the file's mode. os.WriteFile's perm argument is ignored for an
	// existing file, but a Stat failure should not silently downgrade it.
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(rewritten), mode); err != nil {
		return RewriteUnreadable, path, fmt.Errorf("could not write %s: %w", path, err)
	}
	return RewriteDone, path, nil
}

// replaceTopLevelName swaps the value of the first column-zero `name:` line,
// leaving everything around it byte-identical.
//
// Column zero is what makes it top-level, and that matters: a `name:` nested
// under another key is indented, so an indented match is somebody else's field
// and is skipped.
//
// Returns ok=false when no such line carries exactly oldName as its value — the
// caller then writes nothing.
func replaceTopLevelName(content, oldName, newName string) (string, bool) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		// Keep any \r with the tail so CRLF files round-trip unchanged.
		body, cr := line, ""
		if strings.HasSuffix(body, "\r") {
			body, cr = body[:len(body)-1], "\r"
		}

		key, rest, found := strings.Cut(body, ":")
		if !found || strings.TrimRight(key, " \t") != "name" {
			continue // indented, a different key, or not a mapping line at all
		}

		// Split the remainder into leading space, the value, and the tail (a
		// trailing comment plus whatever whitespace was there).
		lead := rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
		afterLead := rest[len(lead):]

		value, tail := afterLead, ""
		if hash := strings.Index(afterLead, "#"); hash > 0 {
			value, tail = afterLead[:hash], afterLead[hash:]
		}
		trimmed := strings.TrimRight(value, " \t")
		tail = value[len(trimmed):] + tail

		quote := ""
		bare := trimmed
		if len(bare) >= 2 {
			if (bare[0] == '"' && bare[len(bare)-1] == '"') || (bare[0] == '\'' && bare[len(bare)-1] == '\'') {
				quote = string(bare[0])
				bare = bare[1 : len(bare)-1]
			}
		}
		if bare != oldName {
			return "", false // the line does not say what the parser said it says
		}

		lines[i] = key + ":" + lead + quote + newName + quote + tail + cr
		return strings.Join(lines, "\n"), true
	}
	return "", false
}
