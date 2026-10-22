package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE EXIT RULE for every question a command asks before it acts: no terminal
// and no skip flag is exit 2 before asking; a "no" is exit 1, never 0. The
// destructive deletes, afy access and afy collections regions are covered where
// they live (destructive_confirm_test.go, agents_access_test.go,
// collection_regions_test.go); these are the two that ask through the process,
// run as their own process so the exit code is what a script sees.

func overageCP(t *testing.T) *fakeCP {
	cp := deployCP(t)
	cp.on("POST", "/agents/"+prodID+"/deploy", 402,
		`{"detail":{"code":"OVERAGE_CONFIRM_REQUIRED","message":"confirm","additional_monthly_usd":5.5}}`)
	return cp
}

func TestDeployOverageWithoutATerminalOrYesIsExitTwoBeforeAsking(t *testing.T) {
	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := overageCP(t)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
	if code != exitInputRefused {
		t.Fatalf("exit %d, want %d\n%s%s", code, exitInputRefused, stdout, stderr)
	}
	if !strings.Contains(stderr, "This deployment adds ~$5.50/mo of usage; pass --yes to confirm without a prompt.") {
		t.Errorf("stderr:\n%s", stderr)
	}
	if strings.Contains(stdout+stderr, "[y/N]") {
		t.Errorf("it must refuse before asking:\n%s%s", stdout, stderr)
	}
	if n := len(cp.called("POST", "/agents/"+prodID+"/deploy")); n != 1 {
		t.Errorf("want only the first attempt, got %d deploy request(s)", n)
	}

	// --json: the usage-error document, nothing on stdout.
	stdout, stderr, code = runAfyProcess(t, cp, nil, "deploy", dir, "--detach", "--json")
	if code != exitInputRefused || strings.TrimSpace(stdout) != "" {
		t.Fatalf("--json: exit %d, stdout %q", code, stdout)
	}
	if doc := jsonErrorOnStderr(t, "overage", stderr); doc["code"] != usageErrorCode {
		t.Errorf("code = %v", doc["code"])
	}
}

func TestDeployOverageWithYesSendsTheConfirmation(t *testing.T) {
	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := overageCP(t)

	_, _, _ = runAfyProcess(t, cp, nil, "deploy", dir, "--detach", "--yes")
	posts := cp.called("POST", "/agents/"+prodID+"/deploy")
	if len(posts) != 2 || !strings.Contains(posts[1].Query, "confirm_overage=true") || strings.Contains(posts[0].Query, "confirm_overage=true") {
		t.Errorf("want the plain attempt, then the confirmed one; got %+v", posts)
	}
}

func TestInitOverwriteWithoutATerminalOrForceIsExitTwoBeforeAsking(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte("runtime: node22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := twoWorkspaceInitCP(t)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "init", dir, "--runtime", "python3.11")
	if code != exitInputRefused {
		t.Fatalf("exit %d, want %d\n%s%s", code, exitInputRefused, stdout, stderr)
	}
	if !strings.Contains(stderr, "This overwrites aetherfy.yaml; pass --force to confirm without a prompt.") {
		t.Errorf("stderr:\n%s", stderr)
	}
	if strings.Contains(stdout+stderr, "[y/N]") || len(cp.called("POST", "/agents")) != 0 {
		t.Errorf("it must refuse before asking or creating anything:\n%s%s", stdout, stderr)
	}
	if got := readFile(t, filepath.Join(dir, "aetherfy.yaml")); got != "runtime: node22\n" {
		t.Errorf("the file was touched: %q", got)
	}
}
