package cmd

import (
	"fmt"
	"strings"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/config"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

var deploymentsCmd = &cobra.Command{
	Use:   "deployments <agent>",
	Short: "List deployment history for an agent",
	Long: `List all deployments for an agent, ordered newest first.

Shows version, state, creation time, and error message for failed deployments.
Use --json for machine-readable output.`,
	Example: `  # List deployments for an agent
  afy deployments my-agent

  # JSON output
  afy deployments my-agent --output json`,
	Args: cobra.ExactArgs(1),
	RunE: runDeployments,
}

func runDeployments(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	agentID := args[0]
	client := api.NewClient()

	sp := output.NewSpinner("Fetching deployment history...")
	sp.Start()
	deployments, err := client.ListDeployments(agentID)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to list deployments: %v", err)
		return nil
	}

	if config.Get().OutputFormat == "json" {
		return output.JSON(deployments)
	}

	if len(deployments) == 0 {
		output.PrintInfo("No deployments found for agent '%s'", agentID)
		output.Println("")
		output.Printf("Deploy with: afy deploy --agent %s\n", agentID)
		return nil
	}

	table := output.Table([]string{"Version", "State", "Created", "Error"})
	for _, d := range deployments {
		errMsg := ""
		if d.ErrorMessage != "" {
			// Truncate long errors for the table
			errMsg = d.ErrorMessage
			if len(errMsg) > 60 {
				errMsg = errMsg[:57] + "..."
			}
		}
		table.Append([]string{
			fmt.Sprintf("v%d", d.Version),
			formatDeployState(d.Status),
			d.CreatedAt.Format("2006-01-02 15:04"),
			errMsg,
		})
	}
	table.Render()

	output.Println("")
	output.Dim.Printf("Total: %d deployment(s)\n", len(deployments))

	// Show hint if latest is failed
	if len(deployments) > 0 && deployments[0].Status == "failed" {
		output.Println("")
		output.PrintWarning("Latest deployment failed.")
		if len(deployments) > 1 {
			// Find the most recent version with a usable image
			for _, d := range deployments[1:] {
				if d.Status == "active" || d.Status == "superseded" {
					output.Printf("Roll back to v%d: afy rollback %s %d\n", d.Version, agentID, d.Version)
					break
				}
			}
		}
	}

	return nil
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
	default:
		return state
	}
}
