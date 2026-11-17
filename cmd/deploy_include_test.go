package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/archive"
)

// `afy deploy` of an agent whose aetherfy.yaml includes shared repository paths: the archive is rooted at
// the repository, holds the agent's folder and each include at their repository paths, and the upload
// names the agent's folder (code_root). Without `include:` nothing changes. Each case runs the shipped
// command against the stand-in control plane.

func archiveNames(t *testing.T, data []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			sort.Strings(out)
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// monorepo is a git repository (a .git folder is what the CLI looks for) with two agents and a shared folder.
func monorepo(t *testing.T, botYaml string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "agents", "bot", "aetherfy.yaml"), botYaml)
	writeFile(t, filepath.Join(root, "agents", "bot", "main.py"), "from shared.util import X\n")
	writeFile(t, filepath.Join(root, "shared", "util.py"), "X = 1\n")
	writeFile(t, filepath.Join(root, "agents", "other", "main.py"), "NOT = 'shipped'\n")
	linkFolder(t, filepath.Join(root, "agents", "bot"), map[string]string{"default": prodID})
	return root
}

func theDeploy(t *testing.T, cp *fakeCP) cpCall {
	t.Helper()
	calls := cp.called("POST", "/agents/"+prodID+"/deploy")
	if len(calls) != 1 {
		t.Fatalf("want one deploy; calls: %+v", cp.all())
	}
	return calls[0]
}

func TestDeployWithIncludesSendsTheRepositoryArchiveAndItsCodeRoot(t *testing.T) {
	root := monorepo(t, "runtime: python3.11\ninclude:\n  - ../../shared\n")
	cp := deployCP(t)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", filepath.Join(root, "agents", "bot"), "--detach")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	call := theDeploy(t, cp)
	if call.Form["code_root"] != "agents/bot" {
		t.Errorf("code_root = %q, want agents/bot", call.Form["code_root"])
	}
	got := strings.Join(archiveNames(t, call.Archive), ",")
	want := "agents/bot,agents/bot/.aetherfy,agents/bot/.aetherfy/agent.json,agents/bot/aetherfy.yaml,agents/bot/main.py,shared,shared/util.py"
	if got != want {
		t.Errorf("archive members:\n got %s\nwant %s", got, want)
	}
}

func TestDeployWithoutIncludesSendsTheFolderAloneAndNoCodeRoot(t *testing.T) {
	root := monorepo(t, "runtime: python3.11\n")
	dir := filepath.Join(root, "agents", "bot")
	cp := deployCP(t)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	call := theDeploy(t, cp)
	if _, sent := call.Form["code_root"]; sent {
		t.Errorf("an agent without includes must send no code_root; form: %v", call.Form)
	}
	patterns, err := archive.LoadIgnorePatterns(dir)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := archive.CreateTarball(dir, patterns)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(call.Archive, direct) {
		t.Errorf("the archive of an agent without includes must be CreateTarball's of its folder, byte for byte")
	}
}

func refusedBeforeUpload(t *testing.T, root, wantInStderr string) {
	t.Helper()
	cp := deployCP(t)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", filepath.Join(root, "agents", "bot"), "--detach")
	if code == 0 {
		t.Fatalf("want a refusal; stdout:\n%s", stdout)
	}
	if deploysSent(cp) != 0 {
		t.Errorf("nothing may be uploaded; calls: %+v", cp.all())
	}
	if !strings.Contains(stdout+stderr, wantInStderr) {
		t.Errorf("want %q in the output; got:\n%s%s", wantInStderr, stdout, stderr)
	}
}

func TestAnIncludeLeavingTheRepositoryIsRefusedBeforeTheUpload(t *testing.T) {
	root := monorepo(t, "runtime: python3.11\ninclude:\n  - ../../../outside\n")
	refusedBeforeUpload(t, root, "include '../../../outside' leaves the repository: from 'agents/bot'")
}

func TestAMissingIncludeIsRefusedBeforeTheUpload(t *testing.T) {
	root := monorepo(t, "runtime: python3.11\ninclude:\n  - ../../libs\n")
	refusedBeforeUpload(t, root, "include '../../libs' names 'libs', which is not in this repository.")
}

func TestABadIncludeIsRefusedWithTheServersSentence(t *testing.T) {
	root := monorepo(t, "runtime: python3.11\ninclude:\n  - shared\n")
	refusedBeforeUpload(t, root, "Config validation failed: include: Invalid include entry 'shared'.")
}

func TestOutsideAGitRepositoryAnIncludeIsRefused(t *testing.T) {
	dir := agentFolder(t, "runtime: python3.11\ninclude:\n  - ../shared\n")
	if _, err := archive.RepositoryRoot(dir); err == nil {
		t.Skip("the temporary folder is itself inside a git repository on this host")
	}
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := deployCP(t)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
	if code == 0 || deploysSent(cp) != 0 {
		t.Fatalf("want a refusal before the upload; exit %d calls %+v", code, cp.all())
	}
	if !strings.Contains(stdout+stderr, "not inside a git repository") {
		t.Errorf("the refusal must say why; got:\n%s%s", stdout, stderr)
	}
}
