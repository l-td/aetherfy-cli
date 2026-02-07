package cmd

import (
	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/config"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

var workspacesCmd = &cobra.Command{
	Use:     "workspaces",
	Aliases: []string{"workspace", "ws"},
	Short:   "Manage workspaces",
	Long: `Manage workspaces for multi-agent coordination.

Workspaces are namespaces that contain multiple agents and shared resources
like secrets and vector collections.`,
}

// --- AGENTS ---
var workspacesAgentsCmd = &cobra.Command{
	Use:   "agents <workspace>",
	Short: "List agents in a workspace",
	Long: `List all agents in a workspace.

Shows agent names, types, status, and URLs for multi-agent coordination.`,
	Example: `  # List agents in a workspace
  afy workspaces agents my-workspace

  # List agents in JSON format
  afy workspaces agents my-workspace --output json`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesAgents,
}

func runWorkspacesAgents(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	workspaceName := args[0]

	sp := output.NewSpinner("Fetching workspace agents...")
	sp.Start()

	client := api.NewClient()
	agents, err := client.ListWorkspaceAgents(workspaceName)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to list workspace agents: %v", err)
		return nil
	}

	if len(agents) == 0 {
		output.PrintInfo("No agents found in workspace '%s'", workspaceName)
		output.Println("")
		output.Println("Deploy an agent to this workspace with:")
		output.Println("  afy deploy --workspace " + workspaceName)
		return nil
	}

	// Check output format
	if config.Get().OutputFormat == "json" {
		return output.JSON(agents)
	}

	// Table output
	table := output.Table([]string{"Name", "Type", "Status", "Created"})
	for _, a := range agents {
		agentType := string(a.AgentType)
		if agentType == "" {
			agentType = "SERVICE" // Default
		}

		status := string(a.Status)

		table.Append([]string{
			a.Name,
			agentType,
			status,
			a.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	table.Render()

	output.Println("")
	output.Dim.Printf("Total: %d agent(s)\n", len(agents))
	output.Println("")
	output.Dim.Println("Agents in the same workspace can:")
	output.Dim.Println("  • Share secrets (workspace-scoped)")
	output.Dim.Println("  • Share vector collections")
	output.Dim.Println("  • Communicate via HTTP (AETHERFY_AGENT_<NAME>_URL)")

	return nil
}

func init() {
	workspacesCmd.AddCommand(workspacesAgentsCmd)
}
