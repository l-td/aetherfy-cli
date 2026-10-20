package cmd

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/link"
	"gopkg.in/yaml.v3"
)

// THE WHOLE INTERACTIVE `afy init`, scripted: every question answered from one
// input, through the real command. Each line below answers one question, in
// the order the flow asks them; a question that lost its line (input
// swallowed by an earlier one) shifts every later answer and the result
// shows it.

// scriptPrompts makes the run interactive and feeds it input; it returns what
// the questions printed.
func scriptPrompts(t *testing.T, input string) *bytes.Buffer {
	t.Helper()
	var asked bytes.Buffer
	prevNew, prevTerm := newPrompter, stdinIsTerminal
	newPrompter = func() *prompter { return &prompter{in: bufio.NewReader(strings.NewReader(input)), out: &asked} }
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { newPrompter, stdinIsTerminal = prevNew, prevTerm })
	return &asked
}

// twoWorkspaceInitCP: two workspaces (so the workspace is asked), both with
// the plan's two regions.
func twoWorkspaceInitCP(t *testing.T) *fakeCP {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces", 200, `[{"id":"w-1","name":"default","regions":["eu-central-1","ap-southeast-1"]},`+
		`{"id":"w-2","name":"acme","regions":["eu-central-1","ap-southeast-1"]}]`)
	cp.on("GET", "/deploy-options", 200, deployOptions)
	cp.on("POST", "/agents", 201, `{"id":"`+prodID+`","name":"invoice-parser","workspace_id":"w-2","workspace_name":"acme"}`)
	return cp
}

func TestInteractiveInitAnswersEveryQuestionFromOneInput(t *testing.T) {
	dir := t.TempDir()
	cp := twoWorkspaceInitCP(t)
	asked := scriptPrompts(t, strings.Join([]string{
		"invoice-parser", // agent name
		"acme",           // workspace, by name
		"python3.12",     // runtime, by name
		"2",              // type: job
		"0 3 * * *",      // schedule
		"eu-central-1",   // region, by name
		"2",              // memory: 1024 MB
		"2",              // billing: always-on
		"worker.py",      // entrypoint
	}, "\n")+"\n")

	if _, stderr, err := runAfy(t, cp, "init", dir); err != nil {
		t.Fatalf("init: %v\n%s\nasked:\n%s", err, stderr, asked)
	}
	posts := cp.called("POST", "/agents")
	if len(posts) != 1 || posts[0].Body["name"] != "invoice-parser" || posts[0].Body["workspace_id"] != "w-2" {
		t.Fatalf("POST /agents = %+v", posts)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(dir, "aetherfy.yaml"))), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"runtime": "python3.12", "type": "job", "schedule": "0 3 * * *", "memory_mb": 1024,
		"keep_alive": true, "entrypoint": "worker.py",
	}
	for k, v := range want {
		if doc[k] != v {
			t.Errorf("%s = %v, want %v (an answer went to the wrong question)", k, doc[k], v)
		}
	}
	if r, _ := doc["regions"].([]interface{}); len(r) != 1 || r[0] != "eu-central-1" {
		t.Errorf("regions = %v", doc["regions"])
	}
}

// Empty answers take each question's default: the folder's name, the first
// workspace, the detected runtime, service, no schedule, the first allowed region, the
// smallest size, auto-sleep.
func TestInteractiveInitDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "billing-bot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := twoWorkspaceInitCP(t)
	scriptPrompts(t, strings.Repeat("\n", 8))

	if _, stderr, err := runAfy(t, cp, "init", dir, "--runtime", "python3.11"); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	posts := cp.called("POST", "/agents")
	if len(posts) != 1 || posts[0].Body["name"] != "billing-bot" || posts[0].Body["workspace_id"] != "w-2" {
		t.Errorf("defaults: POST /agents = %+v (want the folder's name, the first workspace: acme)", posts)
	}
	var doc map[string]interface{}
	_ = yaml.Unmarshal([]byte(readFile(t, filepath.Join(dir, "aetherfy.yaml"))), &doc)
	if doc["type"] != "service" || doc["memory_mb"] != 512 || doc["keep_alive"] != false || doc["schedule"] != nil {
		t.Errorf("defaults: %v", doc)
	}
	if r, _ := doc["regions"].([]interface{}); len(r) != 1 || r[0] != "eu-central-1" {
		t.Errorf("regions = %v, want the first allowed region", doc["regions"])
	}
}

// An answer outside a list is refused (exit 2), and so is input that ends
// before the questions do; either way nothing is created or written.
func TestInteractiveInitRefusesABadAnswerAndEndedInput(t *testing.T) {
	for name, input := range map[string]string{
		"a choice outside the list": "invoice-parser\n7\n",
		"input that ends early":     "invoice-parser\n2\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cp := twoWorkspaceInitCP(t)
			scriptPrompts(t, input)
			_, _, err := runAfy(t, cp, "init", dir)
			if err == nil || ExitCode(err) != exitInputRefused {
				t.Fatalf("want exit %d, got %v", exitInputRefused, err)
			}
			if len(cp.called("POST", "/agents")) != 0 || fileExists(filepath.Join(dir, "aetherfy.yaml")) || fileExists(link.Path(dir)) {
				t.Error("nothing may be created or written")
			}
		})
	}
}

// The overwrite confirmation is a question of the same flow: "n" keeps the
// file and asks nothing more; "y" goes on to the rest from the same input.
func TestInteractiveInitConfirmsAnOverwrite(t *testing.T) {
	for _, c := range []struct {
		answer  string
		created bool
	}{{"n\n", false}, {"y\ninvoice-parser\n2\n\n\n\n\n\n\nmain.py\n", true}} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte("runtime: node22\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cp := twoWorkspaceInitCP(t)
		scriptPrompts(t, c.answer)
		_, stderr, err := runAfy(t, cp, "init", dir, "--runtime", "python3.11")
		if err != nil {
			t.Fatalf("answer %q: %v\n%s", c.answer, err, stderr)
		}
		if got := len(cp.called("POST", "/agents")) == 1; got != c.created {
			t.Errorf("answer %q: created = %v, want %v", c.answer, got, c.created)
		}
		if !c.created && readFile(t, filepath.Join(dir, "aetherfy.yaml")) != "runtime: node22\n" {
			t.Error("a declined overwrite must keep the file")
		}
	}
}

// TYPED AHEAD, all at once: the whole script is already in the input before
// the first question is asked, as a paste or a pipe delivers it. With two
// readers over one stdin, the first buffers past its line and every later
// question reads nothing.
func TestInteractiveInitKeepsTypedAheadAnswers(t *testing.T) {
	dir := t.TempDir()
	cp := twoWorkspaceInitCP(t)
	scriptPrompts(t, "typed-ahead\n1\n\n2\n\n\n\n\nmain.py\n")
	if _, stderr, err := runAfy(t, cp, "init", dir, "--runtime", "python3.11"); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	posts := cp.called("POST", "/agents")
	if len(posts) != 1 || posts[0].Body["name"] != "typed-ahead" || posts[0].Body["workspace_id"] != "w-2" {
		t.Errorf("POST /agents = %+v", posts)
	}
}
