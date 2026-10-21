// Package changelog extracts one release's section from CHANGELOG.md, so the
// release workflow can publish it as the release body.
package changelog

import (
	"fmt"
	"strings"
)

// Section returns the body of the "## <tag>" section of a changelog: the lines
// after that heading, up to the next "## " heading or the end, trimmed. The heading
// may carry text after the tag ("## v0.4.2 (not tagged yet)"), but the tag must be
// the whole first word: "## v0.4.2-rc1" is not the section of v0.4.2.
//
// It FAILS, and never returns an empty or guessed body, when the tag has no
// section, the section is empty, or two sections claim the tag: a release must not
// publish an empty page or a commit list because its notes were not written.
// A "## " inside a fenced code block is code, not a heading.
func Section(changelog, tag string) (string, error) {
	if strings.TrimSpace(tag) == "" {
		return "", fmt.Errorf("no tag given")
	}
	var (
		body     []string
		inside   bool
		found    int
		inFence  bool
		headings []string
	)
	for _, line := range strings.Split(strings.ReplaceAll(changelog, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			inside = false
			headings = append(headings, strings.TrimSpace(line[3:]))
			fields := strings.Fields(line[3:])
			if len(fields) > 0 && fields[0] == tag {
				inside = true
				found++
				continue
			}
		}
		if inside {
			body = append(body, line)
		}
	}
	switch {
	case found == 0:
		return "", fmt.Errorf("CHANGELOG.md has no section for %s: add a \"## %s\" heading with the release notes (its sections: %s)",
			tag, tag, strings.Join(headings, "; "))
	case found > 1:
		return "", fmt.Errorf("CHANGELOG.md has %d sections for %s: a release has one", found, tag)
	}
	text := strings.TrimSpace(strings.Join(body, "\n"))
	if text == "" {
		return "", fmt.Errorf("the \"## %s\" section of CHANGELOG.md is empty: write the release notes", tag)
	}
	return text + "\n", nil
}
