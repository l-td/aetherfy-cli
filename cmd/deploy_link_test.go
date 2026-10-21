package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/link"
)

// afy deploy goes to the agent the folder's link names, by id, and never
// creates one. Each case runs the shipped command as its own process (deploy
// ends with os.Exit), against a stand-in control plane.

const (
	prodID    = "11111111-1111-4111-8111-111111111111"
	stagingID = "22222222-2222-4222-8222-222222222222"
)

// agentFolder is a deployable folder: an aetherfy.yaml and some code.
func agentFolder(t *testing.T, yamlBody string) string {
	t.Helper()
	dir := t.TempDir()
	if yamlBody == "" {
		yamlBody = "runtime: python3.11\nentrypoint: main.py\n"
	}
	for name, body := range map[string]string{"aetherfy.yaml": yamlBody, "main.py": "print('hi')\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func linkFolder(t *testing.T, dir string, targets map[string]string) {
	t.Helper()
	for target, id := range targets {
		if err := link.Write(dir, target, id); err != nil {
			t.Fatal(err)
		}
	}
}

// deployCP answers GET for the two linked agents and accepts their deploys.
func deployCP(t *testing.T) *fakeCP {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/"+prodID, 200, `{"id":"`+prodID+`","name":"invoice-parser"}`)
	cp.on("GET", "/agents/"+stagingID, 200, `{"id":"`+stagingID+`","name":"invoice-parser-staging"}`)
	for _, id := range []string{prodID, stagingID} {
		cp.on("POST", "/agents/"+id+"/deploy", 202, `{"id":"d-1","agent_id":"`+id+`","version":3,"state":"pending"}`)
	}
	return cp
}

func deploysSent(cp *fakeCP) int {
	n := 0
	for _, c := range cp.all() {
		if c.Method == "POST" && strings.HasSuffix(c.Path, "/deploy") {
			n++
		}
	}
	return n
}

func TestDeployGoesToTheLinkedAgentByID(t *testing.T) {
	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID, "staging": stagingID})
	cp := deployCP(t)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if len(cp.called("POST", "/agents/"+prodID+"/deploy")) != 1 || deploysSent(cp) != 1 {
		t.Errorf("want exactly one deploy, to the default target's id; calls: %+v", cp.all())
	}
	if !strings.Contains(stdout, "Deploying to invoice-parser...") {
		t.Errorf("the deploy must say which agent it goes to; stdout:\n%s", stdout)
	}
}

func TestDeployTargetPicksAmongTheLinkedAgents(t *testing.T) {
	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID, "staging": stagingID})
	cp := deployCP(t)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--target", "staging", "--detach")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if len(cp.called("POST", "/agents/"+stagingID+"/deploy")) != 1 || deploysSent(cp) != 1 {
		t.Errorf("want one deploy, to staging's id; calls: %+v", cp.all())
	}
	if !strings.Contains(stdout, "Deploying to invoice-parser-staging...") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestDeployRefusesAndCreatesNothing(t *testing.T) {
	for _, c := range []struct {
		name    string
		targets map[string]string
		args    []string
		want    []string
	}{
		{"no link", nil, nil,
			[]string{"not linked to an agent", "afy init --agent <name>", "never creates an agent"}},
		{"no such target", map[string]string{"default": prodID}, []string{"--target", "staging"},
			[]string{"no target 'staging'", "its targets: default", "afy init --agent <name> --target staging"}},
		{"an id the account does not have", map[string]string{"default": "33333333-3333-4333-8333-333333333333"}, nil,
			[]string{"which this account does not have", "afy init --agent <name>"}},
		{"from GitHub into an unlinked folder", nil, []string{"--from-github", "owner/repo"},
			[]string{"not linked to an agent", "afy init --agent <name>"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := agentFolder(t, "")
			linkFolder(t, dir, c.targets)
			cp := deployCP(t)
			cp.on("GET", "/agents/33333333-3333-4333-8333-333333333333", 404,
				`{"detail":{"code":"AGENT_NOT_FOUND","message":"Agent not found"}}`)

			stdout, stderr, code := runAfyProcess(t, cp, nil, append([]string{"deploy", dir, "--detach"}, c.args...)...)
			if code == 0 {
				t.Fatalf("a deploy that cannot find its agent must fail\n%s%s", stdout, stderr)
			}
			for _, w := range c.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr)
				}
			}
			if deploysSent(cp) != 0 || len(cp.called("POST", "/agents")) != 0 {
				t.Errorf("nothing may be deployed or created; calls: %+v", cp.all())
			}
		})
	}
}

func TestDeployHasNoAgentOrCreateFlag(t *testing.T) {
	for _, flag := range []string{"--agent", "--create"} {
		dir := agentFolder(t, "")
		linkFolder(t, dir, map[string]string{"default": prodID})
		cp := deployCP(t)
		args := []string{"deploy", dir, flag}
		if flag == "--agent" {
			args = append(args, "bot")
		}
		_, stderr, code := runAfyProcess(t, cp, nil, args...)
		if code == 0 || !strings.Contains(stderr, "unknown flag: "+flag) {
			t.Errorf("%s: want unknown flag, got exit %d: %s", flag, code, stderr)
		}
		if len(cp.all()) != 0 {
			t.Errorf("%s: sent %+v", flag, cp.all())
		}
	}
}

func TestDeployWarnsWhenGitWillNotCarryTheLink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	for _, c := range []struct {
		name  string
		setup func(dir string)
		warn  string
	}{
		{"untracked", func(dir string) { git(dir, "init", "-q") }, "is not committed"},
		{"ignored", func(dir string) {
			git(dir, "init", "-q")
			_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".aetherfy/\n"), 0o644)
		}, "is ignored by git"},
		{"tracked", func(dir string) { git(dir, "init", "-q"); git(dir, "add", ".aetherfy/agent.json") }, ""},
		{"outside git", func(string) {}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := agentFolder(t, "")
			linkFolder(t, dir, map[string]string{"default": prodID})
			c.setup(dir)
			cp := deployCP(t)
			stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
			if code != 0 {
				t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
			}
			all := stdout + stderr
			if c.warn == "" {
				if strings.Contains(all, ".aetherfy/agent.json is") {
					t.Errorf("no warning expected:\n%s", all)
				}
				return
			}
			if !strings.Contains(all, ".aetherfy/agent.json "+c.warn) {
				t.Errorf("want the %q warning:\n%s", c.warn, all)
			}
			if deploysSent(cp) != 1 {
				t.Error("a warning must not stop the deploy")
			}
		})
	}
}

// A repository on a machine without git: one warning that the check could
// not run, and the deploy goes ahead. The real command, with PATH emptied.
func TestDeployWarnsWhenGitIsMissingInARepository(t *testing.T) {
	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID})
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cp := deployCP(t)
	stdout, stderr, code := runAfyProcess(t, cp, []string{"PATH=" + t.TempDir()}, "deploy", dir, "--detach")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	all := stdout + stderr
	if n := strings.Count(all, "git is not on PATH: could not check that .aetherfy/agent.json is committed"); n != 1 {
		t.Errorf("want the warning once, got %d:\n%s", n, all)
	}
	if deploysSent(cp) != 1 {
		t.Error("the warning must not stop the deploy")
	}
}
