package archive

import (
	"os"
	"path/filepath"
	"testing"
)

// A rename that does not follow through into aetherfy.yaml is half a rename:
// `afy deploy` resolves its target from that file's `name:`, so the next deploy
// gets AGENT_NOT_FOUND and then offers to CREATE an agent under the old name.
//
// THE TEST THAT MATTERS IS TestLeavesADifferentAgentsFileByteIdentical. Getting
// the happy path wrong is loud — the user sees the wrong name in their file.
// Getting the refusal wrong is silent and destructive: it rewrites somebody
// else's project so that its deploys start landing on an agent nobody pointed
// it at.

func write(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "aetherfy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding %s: %v", path, err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func TestRewritesAMatchingName(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "name: reporter\nruntime: python3.11\nentrypoint: main.py\n")

	outcome, gotPath, err := RewriteAgentName(dir, "reporter", "daily-reporter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != RewriteDone {
		t.Fatalf("outcome = %v, want RewriteDone", outcome)
	}
	if gotPath != path {
		t.Errorf("path = %q, want %q", gotPath, path)
	}

	got := read(t, path)
	want := "name: daily-reporter\nruntime: python3.11\nentrypoint: main.py\n"
	if got != want {
		t.Errorf("file on disk:\n%q\nwant:\n%q", got, want)
	}
}

func TestLeavesADifferentAgentsFileByteIdentical(t *testing.T) {
	// THE ONE THAT MATTERS. This directory belongs to another project. Rewriting
	// it would silently retarget that project's deploys at an agent nobody
	// pointed it at — a worse outcome than the problem being fixed, and one
	// nothing would report.
	dir := t.TempDir()
	original := "name: something-else\nruntime: node20\n# keep me\n"
	path := write(t, dir, original)

	outcome, _, err := RewriteAgentName(dir, "reporter", "daily-reporter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != RewriteNameMismatch {
		t.Fatalf("outcome = %v, want RewriteNameMismatch", outcome)
	}
	if got := read(t, path); got != original {
		t.Errorf("a file naming a DIFFERENT agent was modified:\ngot:  %q\nwant: %q", got, original)
	}
}

func TestNoFileIsNotAnErrorAndCreatesNothing(t *testing.T) {
	dir := t.TempDir()

	outcome, path, err := RewriteAgentName(dir, "reporter", "daily-reporter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != RewriteNoFile {
		t.Fatalf("outcome = %v, want RewriteNoFile", outcome)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("a file was created at %s; renaming must not invent a manifest", path)
	}
}

func TestPreservesCommentsAndOtherKeys(t *testing.T) {
	// A YAML round-trip would drop every comment and reorder the keys — a far
	// bigger edit than the user asked for, in a file they wrote by hand.
	dir := t.TempDir()
	path := write(t, dir, ""+
		"# my agent\n"+
		"name: reporter   # the deploy target\n"+
		"runtime: python3.11\n"+
		"\n"+
		"# regions matter\n"+
		"regions:\n"+
		"  - us-east-1\n")

	if _, _, err := RewriteAgentName(dir, "reporter", "daily-reporter"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "" +
		"# my agent\n" +
		"name: daily-reporter   # the deploy target\n" +
		"runtime: python3.11\n" +
		"\n" +
		"# regions matter\n" +
		"regions:\n" +
		"  - us-east-1\n"
	if got := read(t, path); got != want {
		t.Errorf("file on disk:\n%q\nwant:\n%q", got, want)
	}
}

func TestPreservesQuotingStyle(t *testing.T) {
	for _, q := range []string{`"`, `'`} {
		dir := t.TempDir()
		path := write(t, dir, "name: "+q+"reporter"+q+"\nruntime: node20\n")

		if _, _, err := RewriteAgentName(dir, "reporter", "daily-reporter"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "name: " + q + "daily-reporter" + q + "\nruntime: node20\n"
		if got := read(t, path); got != want {
			t.Errorf("quote %s: got %q, want %q", q, got, want)
		}
	}
}

func TestPreservesCRLF(t *testing.T) {
	// The CLI's users are on Windows too, and rewriting one field must not
	// rewrite every line ending in the file — that turns a one-line change into
	// a whole-file diff in their repository.
	dir := t.TempDir()
	path := write(t, dir, "name: reporter\r\nruntime: python3.11\r\n")

	if _, _, err := RewriteAgentName(dir, "reporter", "daily-reporter"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "name: daily-reporter\r\nruntime: python3.11\r\n"
	if got := read(t, path); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIgnoresANestedNameKey(t *testing.T) {
	// Only the column-zero `name:` is the deploy target. An indented one belongs
	// to whatever block it sits in.
	dir := t.TempDir()
	path := write(t, dir, ""+
		"name: reporter\n"+
		"runtime: python3.11\n"+
		"spawn:\n"+
		"  workers:\n"+
		"    - name: reporter\n")

	if _, _, err := RewriteAgentName(dir, "reporter", "daily-reporter"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "" +
		"name: daily-reporter\n" +
		"runtime: python3.11\n" +
		"spawn:\n" +
		"  workers:\n" +
		"    - name: reporter\n"
	if got := read(t, path); got != want {
		t.Errorf("a nested name: was rewritten:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestAFileWithNoNameFieldIsUntouched(t *testing.T) {
	// Reachable: `afy deploy --agent <name>` needs no `name:` at all.
	dir := t.TempDir()
	original := "runtime: python3.11\nentrypoint: main.py\n"
	path := write(t, dir, original)

	outcome, _, err := RewriteAgentName(dir, "reporter", "daily-reporter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != RewriteNoNameField {
		t.Fatalf("outcome = %v, want RewriteNoNameField", outcome)
	}
	if got := read(t, path); got != original {
		t.Errorf("got %q, want it untouched %q", got, original)
	}
}

func TestUnparseableYamlIsRefusedNotRewritten(t *testing.T) {
	// Failing towards "touch nothing" is the only safe direction here: the file
	// is the user's, and we cannot repair what we cannot read.
	dir := t.TempDir()
	original := "name: [unclosed\nruntime: python3.11\n"
	path := write(t, dir, original)

	outcome, _, err := RewriteAgentName(dir, "reporter", "daily-reporter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != RewriteUnreadable {
		t.Fatalf("outcome = %v, want RewriteUnreadable", outcome)
	}
	if got := read(t, path); got != original {
		t.Errorf("an unparseable file was modified:\ngot:  %q\nwant: %q", got, original)
	}
}

func TestRenamingToTheSameNameStillOnlyTouchesTheOneField(t *testing.T) {
	// The command refuses this before it gets here, but the function is the one
	// that touches the disk, so it must be safe on its own.
	dir := t.TempDir()
	path := write(t, dir, "name: reporter\nruntime: node20\n")

	if _, _, err := RewriteAgentName(dir, "reporter", "reporter"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := read(t, path), "name: reporter\nruntime: node20\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
