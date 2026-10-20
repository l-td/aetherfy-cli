package cmd

import (
	"strings"
	"testing"
)

// AGENT_SAFETY_LIMIT_EXCEEDED (403) is the control plane's refusal for an
// account with NO plan agent cap that already holds the most agents one
// account can have. Its sentence says the way out is support; it carries no
// upgrade_url because no plan raises it. The CLI renders it like any other
// refusal: the control plane's sentence as written, the code under --json, and
// no "upgrade your plan" wording or link of its own.

const safetyLimitSentence = "This account already has 500 deployed agents, the most one account can have. " +
	"Delete or archive a deployed agent, or contact support to raise the limit."

const safetyLimitBody = `{"detail":{"code":"AGENT_SAFETY_LIMIT_EXCEEDED","message":"` + safetyLimitSentence + `",` +
	`"limit":"agents","support_url":"https://example.invalid/support"}}`

func TestAgentSafetyLimitIsTheControlPlanesSentenceAndNothingMore(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		route route
	}{
		{"create", []string{"create", "bot", "--workspace", "acme"}, route{"POST", "/agents", 403, safetyLimitBody}},
		{"restore", []string{"restore", "bot"}, route{"POST", "/agents/bot/restore", 403, safetyLimitBody}},
		{"start", []string{"start", "bot"}, route{"POST", "/agents/bot/start", 403, safetyLimitBody}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			routes := []route{tc.route, {"GET", "/workspaces/acme", 200, wsRow}}
			stdout, stderr, code := runAfyProcess(t, newCasesCP(t, routes), nil, tc.args...)
			if code != exitRequestFailed || strings.TrimSpace(stdout) != "" {
				t.Fatalf("exit %d, stdout %q\n%s", code, stdout, stderr)
			}
			if strings.TrimSpace(stderr) != "Error: "+safetyLimitSentence {
				t.Errorf("want the control plane's sentence alone, as written:\n%s", stderr)
			}
			for _, banned := range []string{"upgrade", "Upgrade", "http", "support_url", "plan"} {
				if strings.Contains(stderr, banned) {
					t.Errorf("the refusal carries %q, which the control plane's sentence does not:\n%s", banned, stderr)
				}
			}

			stdout, stderr, code = runAfyProcess(t, newCasesCP(t, routes), nil, append(append([]string{}, tc.args...), "--json")...)
			if code != exitRequestFailed || strings.TrimSpace(stdout) != "" {
				t.Fatalf("--json: exit %d, stdout %q", code, stdout)
			}
			e := jsonErrorOnStderr(t, tc.name, stderr)
			if e["code"] != "AGENT_SAFETY_LIMIT_EXCEEDED" || e["status"] != 403.0 || e["message"] != safetyLimitSentence {
				t.Errorf("--json error = %v, want the code, 403 and the sentence", e)
			}
		})
	}
}
