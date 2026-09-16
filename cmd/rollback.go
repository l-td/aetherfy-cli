package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback <agent> [version]",
	Short: "Roll back an agent to a previous deployment version",
	Long: `Roll back an agent to a previously deployed version.

Skips the build step — the image from the target version is re-deployed
directly. Only versions that were successfully built (active or superseded)
can be used as rollback targets.

If version is omitted, the deployment history is printed so you can choose.`,
	Example: `  # Show deployment history to pick a version
  afy rollback my-agent

  # Roll back to version 3
  afy rollback my-agent 3

  # Roll back and return immediately without watching
  afy rollback my-agent 3 --detach`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runRollback,
}

var rollbackDetach bool

func init() {
	rollbackCmd.Flags().BoolVarP(&rollbackDetach, "detach", "d", false, "Return immediately without waiting for completion")
}

func runRollback(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}
	if code := rollbackAgent(api.NewClient(), args, rollbackDetach, deploymentPollInterval, deploymentWatchTimeout); code != 0 {
		os.Exit(code)
	}
	return nil
}

// rollbackAgent runs `afy rollback` and returns its exit code: 0 when the
// rollback went live, was queued with --detach, or there was only a history to
// print. Values rather than os.Exit calls, so a test can drive the command
// against a server and read the code a script would see.
func rollbackAgent(client *api.Client, args []string, detach bool, pollInterval, timeout time.Duration) int {
	agentID := args[0]

	// No version supplied — list deployments so the user can choose.
	if len(args) == 1 {
		deployments, err := client.ListDeployments(agentID)
		if err != nil {
			output.PrintError("Failed to list deployments: %v", err)
			return 1
		}
		if len(deployments) == 0 {
			output.PrintInfo("No deployments found for agent '%s'", agentID)
			return 0
		}

		output.Println("Deployment history (newest first):")
		output.Println("")
		table := output.Table([]string{"Version", "State", "Created"})
		for _, d := range deployments {
			table.Append([]string{
				strconv.Itoa(d.Version),
				formatDeploymentState(d.Status),
				d.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		table.Render()
		output.Println("")
		output.Println("Re-run with a version number to roll back:")
		output.Printf("  afy rollback %s <version>\n", agentID)
		return 0
	}

	// Parse version argument.
	version, err := strconv.Atoi(args[1])
	if err != nil || version < 1 {
		output.PrintError("Version must be a positive integer, got: %s", args[1])
		return 1
	}

	sp := output.NewSpinner(fmt.Sprintf("Rolling back %s to version %d...", agentID, version))
	sp.Start()
	resp, err := client.Rollback(agentID, version)
	sp.Stop()

	if err != nil {
		output.PrintError("Rollback failed: %v", err)
		return 1
	}

	output.KeyValue("Deployment ID", resp.ID)
	output.KeyValue("New Version", strconv.Itoa(resp.Version))
	// A version whose image is gone is rebuilt from its stored source, which is
	// not the exact artifact that ran before. The server says so, and so must
	// the terminal: a rollback is usually run in a hurry, by someone trusting it.
	if resp.RollbackNotice != "" {
		output.PrintWarning("Rollback to v%d: %s", version, resp.RollbackNotice)
	}
	output.Println("")

	if detach {
		output.PrintSuccess("Rollback to v%d queued.", version)
		output.Printf("Run 'afy logs %s' to follow progress.\n", agentID)
		return 0
	}
	if watchDeployment(client, agentID, resp.ID, pollInterval, timeout) != nil {
		return 1
	}
	return 0
}
