package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// afy redeploy and afy rollback print the control plane's refusal sentence as
// written: RELEASE_CONFIG_NOT_ON_RECORD (the release predates recorded
// configurations: deploy it again from its source) and the frozen-account
// refusals (what to reduce). Driven through redeployAgent / rollbackAgent,
// the shipped commands' bodies.
func TestRedeployAndRollbackPrintTheServersRefusalUnaltered(t *testing.T) {
	refusals := []struct {
		status        int
		code, message string
	}{
		{422, "RELEASE_CONFIG_NOT_ON_RECORD", "Version 3's configuration isn't on record; deploy it again from its source."},
		{403, "DUNNING_FROZEN", "Your account is past due, so nothing that adds cost can be deployed. Reduce memory, regions or always-on, or settle the invoice."},
	}
	commands := map[string]func(c *api.Client) int{
		"redeploy": func(c *api.Client) int {
			return redeployAgent(c, []string{"bot", "3"}, true, time.Millisecond, time.Second)
		},
		"rollback": func(c *api.Client) int {
			return rollbackAgent(c, []string{"bot", "3"}, true, time.Millisecond, time.Second)
		},
	}
	for name, run := range commands {
		for _, rf := range refusals {
			t.Run(name+" "+rf.code, func(t *testing.T) {
				cp := newFakeCP(t)
				cp.on("POST", "/agents/bot/deployments/3/"+name, rf.status,
					`{"detail":{"code":"`+rf.code+`","message":"`+rf.message+`"}}`)
				client := api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key")

				code := 0
				stderr := captureStderr(t, func() {
					_ = captureStdout(t, func() { code = run(client) })
				})
				if code != 1 {
					t.Errorf("exit %d, want 1", code)
				}
				if !strings.Contains(stderr, "Error: "+rf.message+"\n") {
					t.Errorf("the server's sentence must be printed on its own, unaltered:\n%s", stderr)
				}
			})
		}
	}
}
