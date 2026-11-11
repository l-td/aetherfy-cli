package cmd

import (
	"fmt"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var deploymentsCmd = &cobra.Command{
	Use:   "deployments <agent>",
	Short: "List deployment history for an agent",
	Long: `List the deployments (releases) of an agent, ordered newest first.

Shows version, state, creation time, and error message for failed deployments.
A run is not a deployment: list the agent's runs with 'afy runs'.
Use --json for machine-readable output.`,
	Example: `  # List deployments for an agent
  afy deployments my-agent

  # JSON output
  afy deployments my-agent --json`,
	Args: cobra.ExactArgs(1),
	RunE: runDeployments,
}

func runDeployments(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	return printDeployments(api.NewClient(), args[0])
}

// printDeployments is `afy deployments` past the auth check. Takes the client so
// a test can point it at a server of its own, the same seam `afy status` has.
func printDeployments(client *api.Client, agentID string) error {
	sp := output.NewSpinner("Fetching deployment history...")
	sp.Start()
	all, err := client.ListDeployments(agentID)
	sp.Stop()

	if err != nil {
		return printRefusal("Failed to list deployments", err)
	}
	deployments := releasesOnly(all)

	if output.IsJSON() {
		return output.JSON(deployments)
	}

	if len(deployments) == 0 {
		output.PrintInfo("No deployments found for agent '%s'", agentID)
		output.Println("")
		// A deploy goes to the agent a folder links (.aetherfy/agent.json).
		output.Printf("Deploy from its code: afy init --agent %s (once, to link the folder), then afy deploy\n", agentID)
		return nil
	}

	table := output.Table([]string{"Version", "State", "Created", "Error"})
	var cut []api.Deployment // rows whose error the table shortened
	for _, d := range deployments {
		errMsg, shortened := shortenError(d.ErrorMessage)
		if shortened {
			cut = append(cut, d)
		}
		// A degraded deploy stays state=active and converges in the background
		// (control-plane REVIEW_FAQ §63); flag it inline with the N/M region
		// readiness as a separate trailing marker (state value is unchanged).
		// Same DEGRADED term as the dashboard + `afy list`.
		stateCell := formatDeployState(d.Status)
		if tag := formatDegradedTag(d.IsDegraded, d.RegionsReady, d.RegionsTotal); tag != "" {
			stateCell += " " + tag
		}
		table.Append([]string{
			fmt.Sprintf("v%d", d.Version),
			stateCell,
			d.CreatedAt.Format("2006-01-02 15:04"),
			errMsg,
		})
	}
	table.Render()

	// THE FULL SENTENCE OF EVERY ERROR THE TABLE CUT. A refusal is written for
	// the person reading it (a push without aetherfy.yaml names the repo,
	// commit, branch and path it looked in; a frozen account says what to
	// reduce), and its end is where the fix is. The table keeps its width;
	// the sentence is printed whole below it.
	if len(cut) > 0 {
		output.Println("")
		for _, d := range cut {
			output.Printf("v%d error: %s\n", d.Version, d.ErrorMessage)
		}
	}

	output.Println("")
	output.Dim.Printf("Total: %d deployment(s)\n", len(deployments))

	// Show hint if latest is failed
	if len(deployments) > 0 && deployments[0].Status == "failed" {
		output.Println("")
		output.PrintWarning("Latest deployment failed.")
		// The ERROR column is truncated to fit the table, and the mapped
		// sentence it truncates is the same for every build failure anyway.
		// This is the only place `afy deployments` can say which step failed,
		// and someone reading a failed row is asking exactly that.
		printBuildFailureDetail(deployments[0].BuildFailureDetail)
		if d := newestRollbackTarget(deployments[1:]); d != nil {
			output.Printf("Roll back to v%d: afy rollback %s %d\n", d.Version, agentID, d.Version)
			// Rollback re-deploys that version's IMAGE when it still exists, which
			// carries the secrets it was built with. Offer the rebuild alongside it only
			// when the server says that version still has its source (CanRedeploy):
			// the two commands answer different questions, and the failed deployment
			// above may well be a missing secret.
			if d.CanRedeploy {
				output.Printf("Rebuild v%d with current secrets: afy redeploy %s %d\n", d.Version, agentID, d.Version)
			}
		}
	} else if len(deployments) > 0 && deployments[0].IsDegraded {
		// Latest is serving but partially deployed — converging in the
		// background. Surface it so the user knows not every region is live.
		output.Println("")
		output.PrintWarning("Latest deployment is partially deployed — %d/%d regions ready (converging in the background).",
			deployments[0].RegionsReady, deployments[0].RegionsTotal)
		if deployments[0].PendingRegionAlertStage != "" {
			output.Printf("Convergence alert stage: %s\n", deployments[0].PendingRegionAlertStage)
		}
	}

	printWaitingPushLine(client, agentID)
	return nil
}

// printWaitingPushLine names the push waiting for the deploy in progress, if
// any. It is the next entry of this history, and it has no deployment row yet,
// so without this line `afy deployments` would not show it at all. One read of
// the agent, which carries it (`github.waiting_push`); a read that fails only
// leaves the line out — the history above is already printed.
func printWaitingPushLine(client *api.Client, agentID string) {
	agent, err := client.GetAgent(agentID)
	if err != nil || agent == nil || agent.GitHub == nil || agent.GitHub.WaitingPush == nil {
		return
	}
	output.Println("")
	output.KeyValue("Push waiting", formatWaitingPush(agent.GitHub.WaitingPush))
}

// releasesOnly drops the runs from a history. A run is one Deployment row on the
// server (is_ephemeral) with a version number of its own in the same sequence,
// but it is not a deployment: it executes a release, and its failure says
// nothing about the release that would be better to run. Listed here, a failed
// run was "the latest deployment", and the hint beneath the table then offered
// a rollback to the newest release before it -- the release the run had
// executed (benchmark run 5, AFY-8). `afy runs` lists runs.
func releasesOnly(deployments []api.Deployment) []api.Deployment {
	releases := make([]api.Deployment, 0, len(deployments))
	for _, d := range deployments {
		if !d.IsEphemeral {
			releases = append(releases, d)
		}
	}
	return releases
}

// newestRollbackTarget is the last good release, or nil: the first (newest) of
// the releases given that the server says a rollback can target AND that did
// not fail. The server's can_rollback is the rule for whether `afy rollback`
// accepts a version -- its image or its stored code archive still exists -- but
// it accepts a failed release that kept its archive, and a release that failed
// is not one to go back to. The caller passes the releases OLDER than the one
// that failed, so the failed release itself is never offered.
func newestRollbackTarget(deployments []api.Deployment) *api.Deployment {
	for i := range deployments {
		if deployments[i].CanRollback && wentLive(deployments[i].Status) {
			return &deployments[i]
		}
	}
	return nil
}

// wentLive: the release served at some point (it is serving, or something has
// replaced it), as opposed to one that failed or never finished.
func wentLive(state string) bool {
	switch strings.ToLower(state) {
	case "active", "superseded", "rolled_back":
		return true
	}
	return false
}

// formatDeployState adds color/symbols to deployment states for table output.
func formatDeployState(state string) string {
	switch strings.ToLower(state) {
	case "active":
		return output.Success.Sprint("● active")
	case "failed":
		return output.Error.Sprint("✗ failed")
	case "superseded":
		return output.Dim.Sprint("○ superseded")
	case "queued":
		return "· queued"
	case "building":
		return "⟳ building"
	case "deploying":
		return "⟳ deploying"
	case "rolled_back":
		return output.Dim.Sprint("↩ rolled_back")
	case "completed":
		// Terminal end-state of an ephemeral JOB run (CP migration 093). Absent
		// here since that migration shipped, so it fell to `default` and printed
		// undecorated among decorated siblings.
		return output.Dim.Sprint("✓ completed")
	case "archived":
		// The agent was archived, which destroys its app (CP migration 107), so
		// this version stopped serving. Dim like every other terminal state: it
		// is history, not a failure.
		return output.Dim.Sprint("○ archived")
	default:
		return state
	}
}

// errorColumnWidth is how many characters of an error the deployments table
// shows; a longer one is cut there, with an ellipsis, and printed in full
// below the table.
const errorColumnWidth = 60

// shortenError fits msg to the table's error column, cutting on characters
// (never inside a multi-byte one, as a byte cut of "—" would), and says
// whether it cut.
func shortenError(msg string) (string, bool) {
	r := []rune(msg)
	if len(r) <= errorColumnWidth {
		return msg, false
	}
	return string(r[:errorColumnWidth-3]) + "...", true
}
