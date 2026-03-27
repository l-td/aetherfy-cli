package cmd

import (
	"fmt"
	"strconv"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/output"
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

	agentID := args[0]
	client := api.NewClient()

	// No version supplied — list deployments so the user can choose.
	if len(args) == 1 {
		deployments, err := client.ListDeployments(agentID)
		if err != nil {
			output.PrintError("Failed to list deployments: %v", err)
			return nil
		}
		if len(deployments) == 0 {
			output.PrintInfo("No deployments found for agent '%s'", agentID)
			return nil
		}

		output.Println("Deployment history (newest first):")
		output.Println("")
		table := output.Table([]string{"Version", "State", "Created"})
		for _, d := range deployments {
			table.Append([]string{
				strconv.Itoa(d.Version),
				d.Status,
				d.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		table.Render()
		output.Println("")
		output.Println("Re-run with a version number to roll back:")
		output.Printf("  afy rollback %s <version>\n", agentID)
		return nil
	}

	// Parse version argument.
	version, err := strconv.Atoi(args[1])
	if err != nil || version < 1 {
		output.PrintError("Version must be a positive integer, got: %s", args[1])
		return nil
	}

	sp := output.NewSpinner(fmt.Sprintf("Rolling back %s to version %d...", agentID, version))
	sp.Start()
	resp, err := client.Rollback(agentID, version)
	sp.Stop()

	if err != nil {
		output.PrintError("Rollback failed: %v", err)
		return nil
	}

	output.KeyValue("Deployment ID", resp.ID)
	output.KeyValue("New Version", strconv.Itoa(resp.Version))
	output.Println("")

	if rollbackDetach {
		output.PrintSuccess("Rollback queued (rolling back to v%d image).", version)
		output.Printf("Run 'afy logs %s' to follow progress.\n", agentID)
	} else {
		watchDeployment(client, agentID, resp.ID)
	}

	return nil
}
