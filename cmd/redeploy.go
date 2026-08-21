package cmd

import (
	"fmt"
	"strconv"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

// Top-level, deliberately — the sibling of `afy rollback`, not a subcommand of
// `afy deployments`. The two operations answer the same question ("put a
// different build of this agent in front of traffic") and take the same
// `<agent> [version]` arguments, so they belong at the same level.
//
// Nesting it under `deployments` was tried and rejected: `afy deployments` is a
// leaf that takes an AGENT NAME positionally, so adding subcommands to it makes
// `afy deployments redeploy` ambiguous to a reader — is "redeploy" a subcommand
// or an agent called redeploy? Cobra resolves it (subcommand wins, which also
// makes an agent named "redeploy" unreachable), but the docs CLI guard flagged
// the same ambiguity from the other side: every `afy deployments <agent>`
// example in the docs started reading as an unknown subcommand.
var redeployCmd = &cobra.Command{
	Use:   "redeploy <agent> [version]",
	Short: "Rebuild a deployment from its source, with current secrets",
	Long: `Re-run a version's build from the source archive Aetherfy stored for it.

This is not a rollback. Rollback re-deploys an image that was already built, so
it cannot pick up a secret written since. Secrets are injected while the machine
is built, which makes a fresh build the way a stored secret reaches a running
service — the same model as 'afy deploy' and a git push, without needing the
source locally.

Your agent keeps its current configuration: the archive is not re-read for
memory, regions or anything else, so redeploying an older version never reverts
a change you made after it shipped.

If version is omitted, the active deployment is used.

Archives are kept for the three most recent successful deployments, and deleted
when a build fails, so older versions may no longer be rebuildable.`,
	Example: `  # Rebuild the active deployment (applies secrets set since it deployed)
  afy redeploy my-agent

  # Rebuild a specific version
  afy redeploy my-agent 3

  # Queue it and return immediately
  afy redeploy my-agent --detach`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runRedeploy,
}

var redeployDetach bool

func init() {
	redeployCmd.Flags().BoolVarP(&redeployDetach, "detach", "d", false, "Return immediately without waiting for completion")
}

func runRedeploy(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	agentID := args[0]
	client := api.NewClient()

	version := 0
	if len(args) == 2 {
		parsed, err := strconv.Atoi(args[1])
		if err != nil || parsed < 1 {
			output.PrintError("Version must be a positive integer, got: %s", args[1])
			return nil
		}
		version = parsed
	} else {
		// Default to the active deployment — the one whose environment a newly
		// stored secret is missing from, which is the reason to reach for this
		// command at all. Ephemeral rows are skipped: a spawned or scheduled
		// run is an invocation of an already-built image, never its own build.
		deployments, err := client.ListDeployments(agentID)
		if err != nil {
			output.PrintError("Failed to list deployments: %v", err)
			return nil
		}
		for _, d := range deployments {
			if d.Status == "active" && !d.IsEphemeral {
				version = d.Version
				break
			}
		}
		if version == 0 {
			output.PrintError("Agent '%s' has no active deployment to redeploy.", agentID)
			output.Printf("Pick a version explicitly: afy redeploy %s <version>\n", agentID)
			output.Printf("Or see what exists: afy deployments %s\n", agentID)
			return nil
		}
	}

	sp := output.NewSpinner(fmt.Sprintf("Redeploying %s v%d...", agentID, version))
	sp.Start()
	resp, err := client.Redeploy(agentID, version)
	sp.Stop()

	if err != nil {
		output.PrintError("Redeploy failed: %v", err)
		return nil
	}

	output.KeyValue("Deployment ID", resp.ID)
	output.KeyValue("New Version", strconv.Itoa(resp.Version))
	output.Println("")

	if redeployDetach {
		output.PrintSuccess("Redeploy queued (rebuilding v%d from source).", version)
		output.Printf("Run 'afy logs %s' to follow progress.\n", agentID)
	} else {
		watchDeployment(client, agentID, resp.ID)
	}

	return nil
}
