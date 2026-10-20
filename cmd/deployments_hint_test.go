package cmd

import (
	"strings"
	"testing"
)

// An agent with no deployments is told how to deploy one: through a linked
// folder. `afy deploy --agent` no longer exists (a deploy goes to the agent
// the folder links), so the hint must not send anyone to it.
func TestNoDeploymentsHintNamesTheLinkThenDeploy(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/deployments", 200, `[]`)
	stdout, _, err := runAfy(t, cp, "deployments", "bot")
	if err != nil {
		t.Fatalf("deployments: %v", err)
	}
	if !strings.Contains(stdout, "afy init --agent bot") || !strings.Contains(stdout, "then afy deploy") {
		t.Errorf("want the link-then-deploy hint:\n%s", stdout)
	}
	if strings.Contains(stdout, "deploy --agent") {
		t.Errorf("the hint names a removed flag:\n%s", stdout)
	}
}
