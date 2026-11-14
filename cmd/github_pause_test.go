package cmd

import (
	"encoding/json"
	"fmt"
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
	// The control plane's answer: the link, plus whether this call changed it.
	pausedLinkBody = `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":null,` +
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,` +
		`"auto_deploy_paused_at":"2026-11-14T09:30:00Z","changed":true}`
	runningLinkBody = `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":null,` +
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,` +
		`"auto_deploy_paused_at":null,"changed":true}`
	// The same links answered to a call that found the state already set.
	alreadyPausedLinkBody = `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":null,` +
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,` +
		`"auto_deploy_paused_at":"2026-11-14T09:30:00Z","changed":false}`
	alreadyOnLinkBody = `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":null,` +
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,` +
		`"auto_deploy_paused_at":null,"changed":false}`
)

func TestGitHubPausePostsToThePauseRouteWithNoBodyAndSaysAutoDeployIsPaused(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/github/pause", 200, pausedLinkBody)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "github", "pause", "bot")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	calls := cp.called("POST", "/agents/bot/github/pause")
	if len(calls) != 1 || calls[0].Body != nil {
		t.Fatalf("the command sent %+v, want one POST with no body", calls)
	}
	// Exactly one request: no read of the link first, and no other route.
	if all := cp.all(); len(all) != 1 {
		t.Errorf("the command made %d requests, want exactly the one POST: %+v", len(all), all)
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
	// The control: this is the pause's text, not the resume's, and not the no-op's.
	if strings.Contains(stdout, "Auto-deploy is on") || strings.Contains(stdout, "already") {
		t.Errorf("pause said something else than a pause:\n%s", stdout)
	}
}

func TestGitHubResumePostsToTheResumeRouteWithNoBodyAndSaysNothingDeploysByItself(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/github/resume", 200, runningLinkBody)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "github", "resume", "bot")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	calls := cp.called("POST", "/agents/bot/github/resume")
	if len(calls) != 1 || calls[0].Body != nil {
		t.Fatalf("the command sent %+v, want one POST with no body", calls)
	}
	if all := cp.all(); len(all) != 1 {
		t.Errorf("the command made %d requests, want exactly the one POST: %+v", len(all), all)
	}
	for _, want := range []string{
		"Auto-deploy is on for bot.",
		"Resuming deploys nothing by itself: the next push to production deploys, or run `afy deploy` to ship the latest code now.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Auto-deploy is paused") || strings.Contains(stdout, "already") {
		t.Errorf("resume said something else than a resume:\n%s", stdout)
	}
}

// A call that changed nothing says so, in the words of the schedule's no-op,
// and prints none of the success lines: those say the state changed.
func TestGitHubPauseAndResumeThatChangedNothingSayTheyWereAlreadyThere(t *testing.T) {
	cases := []struct {
		verb, answer, want string
	}{
		{"pause", alreadyPausedLinkBody, "Auto-deploy for bot was already paused."},
		{"resume", alreadyOnLinkBody, "Auto-deploy for bot was already on."},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.verb, func(t *testing.T) {
			cp := newFakeCP(t)
			cp.on("POST", "/agents/bot/github/"+tc.verb, 200, tc.answer)
			stdout, stderr, code := runAfyProcess(t, cp, nil, "github", tc.verb, "bot")
			if code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Errorf("stdout is missing %q:\n%s", tc.want, stdout)
			}
			for _, unwanted := range []string{"Auto-deploy is paused", "Auto-deploy is on", "build nothing until", "Resuming deploys nothing"} {
				if strings.Contains(stdout, unwanted) {
					t.Errorf("a call that changed nothing printed %q:\n%s", unwanted, stdout)
				}
			}
		})
	}
}

// What is printed is the state the control plane REPORTS, not the verb asked
// for: a pause answered with a link that carries no pause must not print
// "paused", whether or not it changed anything.
func TestGitHubPauseReportsTheStateTheServerAnswersWith(t *testing.T) {
	cases := []struct {
		name, answer, want string
	}{
		{"changed", runningLinkBody, "Auto-deploy is on for bot."},
		{"unchanged", alreadyOnLinkBody, "Auto-deploy for bot was already on."},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cp := newFakeCP(t)
			cp.on("POST", "/agents/bot/github/pause", 200, tc.answer)

			stdout, stderr, code := runAfyProcess(t, cp, nil, "github", "pause", "bot")
			if code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if strings.Contains(stdout, "paused") || !strings.Contains(stdout, tc.want) {
				t.Errorf("the output did not follow the answer's state, want %q:\n%s", tc.want, stdout)
			}
		})
	}
}

func TestGitHubPauseAndResumeJSONPrintTheWholeAnswerLikeTheSchedulesDo(t *testing.T) {
	cases := []struct {
		verb, answer string
		wantAt       interface{}
		wantChanged  bool
	}{
		{"pause", pausedLinkBody, "2026-11-14T09:30:00Z", true},
		{"resume", runningLinkBody, nil, true},
		{"pause", alreadyPausedLinkBody, "2026-11-14T09:30:00Z", false},
		{"resume", alreadyOnLinkBody, nil, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%s changed=%v", tc.verb, tc.wantChanged), func(t *testing.T) {
			cp := newFakeCP(t)
			cp.on("POST", "/agents/bot/github/"+tc.verb, 200, tc.answer)
			stdout, stderr, code := runAfyProcess(t, cp, nil, "github", tc.verb, "bot", "--json")
			if code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			var got, served map[string]interface{}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
			}
			if err := json.Unmarshal([]byte(tc.answer), &served); err != nil {
				t.Fatalf("the fixture is not JSON: %v", err)
			}
			// The whole answer, as the server gave it: every field of the link, and changed.
			for key, want := range served {
				if got[key] != want {
					t.Errorf("%s = %v, want the server's %v: %s", key, got[key], want, stdout)
				}
			}
			if at, present := got["auto_deploy_paused_at"]; !present || at != tc.wantAt {
				t.Errorf("auto_deploy_paused_at = %v (present %v), want %v", at, present, tc.wantAt)
			}
			if changed, present := got["changed"]; !present || changed != tc.wantChanged {
				t.Errorf("changed = %v (present %v), want %v", changed, present, tc.wantChanged)
			}
			// Nothing the command made up: the schedule commands print the answer and nothing beside it.
			for _, invented := range []string{"agent", "auto_deploy_paused"} {
				if _, present := got[invented]; present {
					t.Errorf("--json carries %q, which the control plane did not answer: %s", invented, stdout)
				}
			}
		})
	}
}

// An agent with no GitHub link: the server's own sentence, then the command that
// fixes it.
func TestGitHubPauseOnAnUnlinkedAgentNamesTheLinkCommand(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/github/pause", 422,
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
func TestGitHubResumeOtherRefusalsGetNoLinkAdvice(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/github/resume", 404,
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
