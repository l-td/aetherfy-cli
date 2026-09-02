package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rename command's second half: keeping the local aetherfy.yaml pointing at
// the agent that was just renamed, and SAYING what it did.
//
// The failure being prevented is silent and convincing. `afy deploy` resolves
// its target by the `name:` in aetherfy.yaml. After a rename that file names an
// agent that no longer exists, so the deploy gets AGENT_NOT_FOUND and the
// prompt that follows offers to CREATE one under the old name. That offer reads
// like the fix; taking it leaves two agents and no error to explain it.
//
// EVERY BRANCH MUST PRINT. A silent no-op is indistinguishable from a rewrite
// that worked, and the case it hides — the manifest is one directory up — is
// the common one. `captureStdout` (cmd/deploy_url_test.go) redirects both
// writers this package uses; capturing one proves nothing about the other.

func seedManifest(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "aetherfy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding %s: %v", path, err)
	}
	return path
}

func TestRenameReportRewritesAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	path := seedManifest(t, dir, "name: reporter\nruntime: python3.11\n")
	t.Chdir(dir)

	out := captureStdout(t, func() { reportLocalManifestRename("reporter", "daily-reporter") })

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if got, want := string(b), "name: daily-reporter\nruntime: python3.11\n"; got != want {
		t.Errorf("file on disk: %q, want %q", got, want)
	}
	if !strings.Contains(out, "aetherfy.yaml") {
		t.Errorf("the report did not name the file it changed:\n%s", out)
	}
	if !strings.Contains(out, "name:") {
		t.Errorf("the report did not name the field it changed:\n%s", out)
	}
}

func TestRenameReportRefusesAndExplainsWhenTheFileNamesSomeoneElse(t *testing.T) {
	// THE ONE THAT MATTERS. Rewriting this would retarget another project's
	// deploys. Refusing silently would be nearly as bad: the user walks away
	// believing their manifest was fixed.
	dir := t.TempDir()
	original := "name: something-else\nruntime: node20\n"
	path := seedManifest(t, dir, original)
	t.Chdir(dir)

	out := captureStdout(t, func() { reportLocalManifestRename("reporter", "daily-reporter") })

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(b) != original {
		t.Errorf("a file naming a DIFFERENT agent was rewritten: %q", string(b))
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("the refusal was silent; the user has no way to know their manifest is now stale")
	}
	if !strings.Contains(out, "different agent") {
		t.Errorf("the report did not say WHY it declined:\n%s", out)
	}
	if !strings.Contains(out, "daily-reporter") || !strings.Contains(out, "reporter") {
		t.Errorf("the report did not name both the new name and the agent to look for:\n%s", out)
	}
}

func TestRenameReportSpeaksUpWhenThereIsNoManifestHere(t *testing.T) {
	// The common case: the user ran the rename from their home directory. The
	// remedy has to be stated, or nothing tells them their project is stale.
	dir := t.TempDir()
	t.Chdir(dir)

	out := captureStdout(t, func() { reportLocalManifestRename("reporter", "daily-reporter") })

	if strings.TrimSpace(out) == "" {
		t.Fatal("no aetherfy.yaml here produced no output at all")
	}
	if !strings.Contains(out, "aetherfy.yaml") {
		t.Errorf("the report did not name the file the user must update:\n%s", out)
	}
	if !strings.Contains(out, "daily-reporter") {
		t.Errorf("the report did not say what to set the name to:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "aetherfy.yaml")); !os.IsNotExist(err) {
		t.Error("a manifest was invented in a directory that had none")
	}
}

func TestRenameReportIsNotSilentForAManifestWithNoName(t *testing.T) {
	dir := t.TempDir()
	seedManifest(t, dir, "runtime: python3.11\nentrypoint: main.py\n")
	t.Chdir(dir)

	out := captureStdout(t, func() { reportLocalManifestRename("reporter", "daily-reporter") })

	if strings.TrimSpace(out) == "" {
		t.Fatal("a manifest with no name: produced no output")
	}
	if !strings.Contains(out, "name:") {
		t.Errorf("the report did not name the missing field:\n%s", out)
	}
}
