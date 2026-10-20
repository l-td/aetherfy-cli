package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/archive"
)

// A linked folder with no aetherfy.yaml: refused before anything is uploaded,
// with the control plane's own sentence (archive.MissingConfigMessage, held to
// MISSING_CONFIG_MESSAGE) after the path it looked at.
func TestDeployWithoutAYamlSaysWhereItLookedInTheServersWords(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := deployCP(t)

	_, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
	if code == 0 {
		t.Fatal("a deploy without aetherfy.yaml must fail")
	}
	want := filepath.Join(dir, "aetherfy.yaml") + ": " + archive.MissingConfigMessage
	if !strings.Contains(stderr, want) {
		t.Errorf("want %q in:\n%s", want, stderr)
	}
	if !strings.Contains(archive.MissingConfigMessage, "at least `runtime:`") {
		t.Errorf("the sentence must say what the file needs: %q", archive.MissingConfigMessage)
	}
	if deploysSent(cp) != 0 {
		t.Error("nothing may be uploaded")
	}
}
