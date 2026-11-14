package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// `afy github pause <agent>` and `afy github resume <agent>` switch auto-deploy
// off and on for an agent's GitHub link, without unlinking. Each test runs the
// SHIPPED command as a process against the stand-in control plane, so what is
// asserted is what the person sees and what reached the wire.
//
// The word is "auto-deploy" throughout: "pause" already names the agent
// lifecycle (`afy stop`, the dashboard's agent pause), which this does not touch.

const (
	pausedLinkBody = `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":null,` +
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,` +
		`"auto_deploy_paused_at":"2026-11-14T09:30:00Z"}`
	runningLinkBody = `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":null,` +
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,` +
		`"auto_deploy_paused_at":null}`
)

func TestGitHubPauseSendsAPausedPatchAndSaysAutoDeployIsPaused(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("PATCH", "/agents/bot/github", 200, pausedLinkBody)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "github", "pause", "bot")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	calls := cp.called("PATCH", "/agents/bot/github")
	if len(calls) != 1 || calls[0].Body["auto_deploy_paused"] != true || len(calls[0].Body) != 1 {
		t.Fatalf("the command sent %+v, want one PATCH with exactly {auto_deploy_paused: true}", calls)
	}
	// Exactly one request: no read of the link first.
	if all := cp.all(); len(all) != 1 {
		t.Errorf("the command made %d requests, want exactly the one PATCH: %+v", len(all), all)
	}
	for _, want := range []string{
		"Auto-deploy is paused for bot.",
		"Pushes to production build nothing until you run `afy github resume bot`.",
		"`afy deploy` still deploys your code.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}
	// The control: this is the pause's text, not the resume's.
	if strings.Contains(stdout, "Auto-deploy is on") {
		t.Errorf("pause said auto-deploy is on:\n%s", stdout)
	}
}

func TestGitHubResumeSendsAnUnpausedPatchAndSaysNothingDeploysByItself(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("PATCH", "/agents/bot/github", 200, runningLinkBody)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "github", "resume", "bot")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	calls := cp.called("PATCH", "/agents/bot/github")
	// false is sent, not omitted: an empty body would be refused.
	if len(calls) != 1 || calls[0].Body["auto_deploy_paused"] != false || len(calls[0].Body) != 1 {
		t.Fatalf("the command sent %+v, want one PATCH with exactly {auto_deploy_paused: false}", calls)
	}
	for _, want := range []string{
		"Auto-deploy is on for bot.",
		"Resuming deploys nothing by itself: the next push to production deploys, or run `afy deploy` to ship the latest code now.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Auto-deploy is paused") {
		t.Errorf("resume said auto-deploy is paused:\n%s", stdout)
	}
}

// What is printed is the state the control plane REPORTS, not the state asked
// for: a pause answered with a link that carries no pause must not print
// "paused".
func TestGitHubPauseReportsTheStateTheServerAnswersWith(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("PATCH", "/agents/bot/github", 200, runningLinkBody)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "github", "pause", "bot")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "Auto-deploy is paused") || !strings.Contains(stdout, "Auto-deploy is on for bot.") {
		t.Errorf("the output did not follow the answer's state:\n%s", stdout)
	}
}

func TestGitHubPauseAndResumeJSONCarryTheState(t *testing.T) {
	cases := []struct {
		verb, answer string
		wantPaused   bool
		wantAt       interface{}
	}{
		{"pause", pausedLinkBody, true, "2026-11-14T09:30:00Z"},
		{"resume", runningLinkBody, false, nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.verb, func(t *testing.T) {
			cp := newFakeCP(t)
			cp.on("PATCH", "/agents/bot/github", 200, tc.answer)
			stdout, stderr, code := runAfyProcess(t, cp, nil, "github", tc.verb, "bot", "--json")
			if code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			var got map[string]interface{}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
			}
			if got["agent"] != "bot" || got["auto_deploy_paused"] != tc.wantPaused {
				t.Errorf("got %v, want agent bot and auto_deploy_paused %v", got, tc.wantPaused)
			}
			// Present, and null when on: a script cannot read a key that is absent.
			at, present := got["auto_deploy_paused_at"]
			if !present || at != tc.wantAt {
				t.Errorf("auto_deploy_paused_at = %v (present %v), want %v", at, present, tc.wantAt)
			}
		})
	}
}

// An agent with no GitHub link: the server's own sentence, then the command that
// fixes it.
func TestGitHubPauseOnAnUnlinkedAgentNamesTheLinkCommand(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("PATCH", "/agents/bot/github", 409,
		`{"detail":{"code":"GITHUB_NOT_LINKED","message":"This agent is not linked to a GitHub repository."}}`)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "github", "pause", "bot")
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "This agent is not linked to a GitHub repository.") {
		t.Errorf("the server's sentence did not reach stderr:\n%s", stderr)
	}
	if !strings.Contains(stdout, "afy github link bot <owner/repo>") {
		t.Errorf("the refusal did not name the command that links the agent:\n%s", stdout)
	}
	if strings.Contains(stdout, "Auto-deploy is") {
		t.Errorf("a refused pause printed a success line:\n%s", stdout)
	}
}

// THE CONTROL for the hint above: it follows the CODE. Any other refusal gets
// the server's sentence and no advice to link.
func TestGitHubPauseOtherRefusalsGetNoLinkAdvice(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("PATCH", "/agents/bot/github", 404,
		`{"detail":{"code":"AGENT_NOT_FOUND","message":"Agent not found."}}`)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "github", "resume", "bot")
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Agent not found.") {
		t.Errorf("the server's sentence did not reach stderr:\n%s", stderr)
	}
	if strings.Contains(stdout, "afy github link") {
		t.Errorf("a refusal that is not GITHUB_NOT_LINKED advised linking:\n%s", stdout)
	}
}

func TestGitHubPauseAndResumeTakeExactlyOneAgent(t *testing.T) {
	for _, verb := range []string{"pause", "resume"} {
		for _, args := range [][]string{{"github", verb}, {"github", verb, "a", "b"}} {
			cp := newFakeCP(t)
			_, _, code := runAfyProcess(t, cp, nil, args...)
			if code != exitInputRefused {
				t.Errorf("afy %s exited %d, want %d", strings.Join(args, " "), code, exitInputRefused)
			}
			if n := len(cp.all()); n != 0 {
				t.Errorf("afy %s sent %d request(s) before refusing its arguments", strings.Join(args, " "), n)
			}
		}
	}
}
