package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Node 20 reached its end of life on 30 April 2026 and the platform no longer offers it. The CLI
// carries no list of runtimes (afy init reads the server's), so no file here may name the removed
// ones: a fixture or an example that did would teach a refused value.
//
// The names are built from pieces so that this file does not contain what it forbids.
var removedRuntime = regexp.MustCompile(`node` + `20(?:[^0-9]|$)|node:` + `20\b`)

type scannedFile struct{ path, text string }

func removedRuntimeMentions(files []scannedFile) []string {
	var out []string
	for _, f := range files {
		for _, line := range strings.Split(f.text, "\n") {
			if removedRuntime.MatchString(line) {
				out = append(out, f.path+":"+strings.TrimSpace(line))
			}
		}
	}
	return out
}

func trackedFiles(t *testing.T) []scannedFile {
	t.Helper()
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	require.NoError(t, err, "git ls-files")
	var files []scannedFile
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || strings.ContainsRune(string(b), 0) {
			continue // deleted in the working tree, or binary
		}
		files = append(files, scannedFile{rel, string(b)})
	}
	return files
}

func TestNoFileOffersARemovedRuntime(t *testing.T) {
	assert.Empty(t, removedRuntimeMentions(trackedFiles(t)),
		"a runtime the platform no longer offers is named in these files")
}

// The control: the scan finds the removed runtime in each shape it can take, and not a runtime
// that is offered.
func TestTheRemovedRuntimeScanFindsWhatItForbids(t *testing.T) {
	bad := "runtime: node" + "20\n"
	hits := removedRuntimeMentions([]scannedFile{
		{"a.yaml", bad},
		{"b.go", `x := "node` + `20-ts"`},
		{"c.md", "FROM node:" + "20-alpine"},
		{"d.yaml", "runtime: node24\nruntime: node22-ts\nruntime: node200\n"},
	})
	assert.Equal(t, []string{"a.yaml:runtime: node" + "20", `b.go:x := "node` + `20-ts"`, "c.md:FROM node:" + "20-alpine"}, hits)
}
