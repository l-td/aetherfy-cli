package cmd

import (
	"fmt"
	"strings"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/config"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

var agentsCmd = &cobra.Command{
	Use:     "agents",
	Aliases: []string{"agent", "a"},
	Short:   "Manage agents",
	Long:    "Create, list, delete, and manage your Aetherfy agents.",
}

// --- LIST ---
var agentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all agents",
	Long:  "List all agents in your account.",
	RunE:  runAgentsList,
}

func runAgentsList(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	sp := output.NewSpinner("Fetching agents...")
	sp.Start()

	client := api.NewClient()
	agents, err := client.ListAgents()
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to list agents: %v", err)
		return err
	}

	// Check output format first
	if config.Get().OutputFormat == "json" {
		return output.JSON(agents)
	}

	if len(agents) == 0 {
		output.PrintInfo("No agents found. Create one with 'afy agents create <name>'")
		return nil
	}

	// Table output
	table := output.Table([]string{"Name", "Type", "Status", "Region", "ID"})
	for _, a := range agents {
		status := formatStatus(a.Status)
		table.Append([]string{a.Name, a.AgentType, status, a.Region, a.ID})
	}
	table.Render()

	output.Println("")
	output.Dim.Printf("Total: %d agent(s)\n", len(agents))

	return nil
}

// --- CREATE ---
var agentsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new agent",
	Long:  "Create a new agent with the specified name.",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentsCreate,
}

var (
	agentDescription string
	agentType        string
	agentRuntime     string
	spawnEnabled     bool
)

func init() {
	agentsCreateCmd.Flags().StringVarP(&agentDescription, "description", "d", "", "Agent description")
	agentsCreateCmd.Flags().StringVarP(&agentType, "type", "t", "SERVICE", "Agent type: SERVICE or JOB")
	agentsCreateCmd.Flags().StringVarP(&agentRuntime, "runtime", "r", "python3.11", "Runtime: python3.11, node20, or bun")
	agentsCreateCmd.Flags().BoolVar(&spawnEnabled, "spawn-enabled", false, "Enable spawning for this agent")
}

func runAgentsCreate(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	// Validate agent type
	agentType = strings.ToUpper(agentType)
	if agentType != "SERVICE" && agentType != "JOB" {
		output.PrintError("Invalid agent type. Must be SERVICE or JOB")
		return fmt.Errorf("invalid agent type: must be SERVICE or JOB")
	}

	sp := output.NewSpinner(fmt.Sprintf("Creating agent '%s'...", name))
	sp.Start()

	client := api.NewClient()
	agent, err := client.CreateAgent(&api.AgentCreateRequest{
		Name:         name,
		Description:  agentDescription,
		AgentType:    strings.ToLower(agentType), // Backend expects lowercase
		Runtime:      agentRuntime,
		SpawnEnabled: spawnEnabled,
	})
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to create agent: %v", err)
		return err
	}

	output.PrintSuccess("Agent '%s' created successfully!", agent.Name)
	output.Println("")
	output.KeyValue("ID", agent.ID)
	output.KeyValue("Name", agent.Name)
	output.KeyValue("Type", agent.AgentType)
	output.KeyValue("Status", agent.Status)

	output.Println("")
	output.Println("Next steps:")
	output.Println("  1. Add your code to a directory with aetherfy.yaml")
	output.Println("  2. Run 'afy deploy' to deploy your agent")

	return nil
}

// --- DELETE ---
var agentsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an agent",
	Long:  "Delete an agent and all its deployments.",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentsDelete,
}

var forceDelete bool

func init() {
	agentsDeleteCmd.Flags().BoolVarP(&forceDelete, "force", "f", false, "Skip confirmation prompt")
}

func runAgentsDelete(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	idOrName := args[0]

	// Confirm deletion
	if !forceDelete {
		output.Warning.Printf("This will permanently delete agent '%s' and all its deployments.\n", idOrName)
		fmt.Print("Type the agent name to confirm: ")
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != idOrName {
			output.PrintInfo("Deletion cancelled.")
			return nil
		}
	}

	sp := output.NewSpinner(fmt.Sprintf("Deleting agent '%s'...", idOrName))
	sp.Start()

	client := api.NewClient()
	err := client.DeleteAgent(idOrName)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to delete agent: %v", err)
		return err
	}

	output.PrintSuccess("Agent '%s' deleted successfully.", idOrName)
	return nil
}

// --- STATUS ---
var agentsStatusCmd = &cobra.Command{
	Use:   "status <name>",
	Short: "Show agent status",
	Long:  "Show detailed status information for an agent.",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentsStatus,
}

func runAgentsStatus(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Fetching status for '%s'...", name))
	sp.Start()

	client := api.NewClient()
	agent, err := client.GetAgent(name)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to get agent: %v", err)
		return err
	}

	// Check output format
	if config.Get().OutputFormat == "json" {
		return output.JSON(agent)
	}

	output.Header(fmt.Sprintf("Agent: %s", agent.Name))
	output.Println("")
	output.KeyValue("ID", agent.ID)
	output.KeyValue("Name", agent.Name)
	output.KeyValue("Type", agent.AgentType)
	output.KeyValue("Status", formatStatus(agent.Status))
	output.KeyValue("Region", agent.Region)
	output.KeyValue("Spawn Enabled", fmt.Sprintf("%v", agent.SpawnEnabled))
	output.KeyValue("Created", agent.CreatedAt.Format("2006-01-02 15:04:05"))
	output.KeyValue("Updated", agent.UpdatedAt.Format("2006-01-02 15:04:05"))

	if agent.Description != "" {
		output.Println("")
		output.KeyValue("Description", agent.Description)
	}

	return nil
}

// --- RENAME ---
var agentsRenameCmd = &cobra.Command{
	Use:   "rename <current-name> <new-name>",
	Short: "Rename an agent",
	Long: `Rename an agent to a new name.

The agent URL will remain unchanged to preserve existing integrations.
Only the name will be updated.`,
	Args: cobra.ExactArgs(2),
	RunE: runAgentsRename,
}

var forceRename bool

func init() {
	agentsRenameCmd.Flags().BoolVarP(&forceRename, "force", "f", false, "Skip confirmation prompt")
	agentsCmd.AddCommand(agentsListCmd)
	agentsCmd.AddCommand(agentsCreateCmd)
	agentsCmd.AddCommand(agentsDeleteCmd)
	agentsCmd.AddCommand(agentsStatusCmd)
	agentsCmd.AddCommand(agentsRenameCmd)
}

func runAgentsRename(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	currentName := args[0]
	newName := args[1]

	// Validate new name is different
	if currentName == newName {
		output.PrintError("New name must be different from current name")
		return fmt.Errorf("new name must be different from current name")
	}

	// Get current agent to verify it exists
	client := api.NewClient()
	_, err := client.GetAgent(currentName)
	if err != nil {
		output.PrintError("Failed to find agent '%s': %v", currentName, err)
		return err
	}

	// Confirm rename
	if !forceRename {
		output.Warning.Printf("Renaming agent '%s' to '%s'\n", currentName, newName)
		fmt.Print("Continue? (y/N): ")
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" {
			output.PrintInfo("Rename cancelled.")
			return nil
		}
	}

	sp := output.NewSpinner(fmt.Sprintf("Renaming agent '%s' to '%s'...", currentName, newName))
	sp.Start()

	// Update agent with new name
	updatedAgent, err := client.UpdateAgent(currentName, &api.AgentUpdateRequest{
		Name: &newName,
	})
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to rename agent: %v", err)
		return err
	}

	output.PrintSuccess("Agent renamed successfully!")
	output.Println("")
	output.KeyValue("Old Name", currentName)
	output.KeyValue("New Name", updatedAgent.Name)
	output.KeyValue("ID", updatedAgent.ID)

	// Show URL preservation message
	output.Println("")
	output.Info.Println("ℹ Note: The agent URL remains unchanged to preserve existing integrations.")
	output.Dim.Printf("  You can access the agent using either the new name or ID.\n")

	return nil
}

// formatStatus adds color to status strings
func formatStatus(status string) string {
	switch strings.ToLower(status) {
	case "running", "active", "healthy":
		return output.Green.Sprint(status)
	case "pending", "deploying", "building":
		return output.Yellow.Sprint(status)
	case "stopped", "inactive":
		return output.Gray.Sprint(status)
	case "error", "failed", "unhealthy":
		return output.Red.Sprint(status)
	default:
		return status
	}
}
