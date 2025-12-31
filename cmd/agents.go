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
		return nil
	}

	if len(agents) == 0 {
		output.PrintInfo("No agents found. Create one with 'afy agents create <name>'")
		return nil
	}

	// Check output format
	if config.Get().OutputFormat == "json" {
		return output.JSON(agents)
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
	spawnEnabled     bool
)

func init() {
	agentsCreateCmd.Flags().StringVarP(&agentDescription, "description", "d", "", "Agent description")
	agentsCreateCmd.Flags().StringVarP(&agentType, "type", "t", "SERVICE", "Agent type: SERVICE or JOB")
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
		return nil
	}

	sp := output.NewSpinner(fmt.Sprintf("Creating agent '%s'...", name))
	sp.Start()

	client := api.NewClient()
	agent, err := client.CreateAgent(&api.AgentCreateRequest{
		Name:         name,
		Description:  agentDescription,
		AgentType:    agentType,
		SpawnEnabled: spawnEnabled,
	})
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to create agent: %v", err)
		return nil
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

	name := args[0]

	// Confirm deletion
	if !forceDelete {
		output.Warning.Printf("This will permanently delete agent '%s' and all its deployments.\n", name)
		fmt.Print("Type the agent name to confirm: ")
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != name {
			output.PrintInfo("Deletion cancelled.")
			return nil
		}
	}

	sp := output.NewSpinner(fmt.Sprintf("Deleting agent '%s'...", name))
	sp.Start()

	client := api.NewClient()
	err := client.DeleteAgent(name)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to delete agent: %v", err)
		return nil
	}

	output.PrintSuccess("Agent '%s' deleted successfully.", name)
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
		return nil
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

func init() {
	agentsCmd.AddCommand(agentsListCmd)
	agentsCmd.AddCommand(agentsCreateCmd)
	agentsCmd.AddCommand(agentsDeleteCmd)
	agentsCmd.AddCommand(agentsStatusCmd)
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
