package cmd

import (
	"fmt"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
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

// --- CREATE ---

var workspacesCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workspace",
	Long: `Create a new workspace for multi-agent coordination.

Workspace names must be 3-63 characters, lowercase alphanumeric and hyphens,
and cannot start or end with a hyphen.`,
	Example: `  # Create a workspace
  afy workspaces create invoice-pipeline

  # Create with a description
  afy workspaces create invoice-pipeline --description "Invoice processing agents"`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesCreate,
}

var workspaceDescription string

func init() {
	workspacesCreateCmd.Flags().StringVarP(&workspaceDescription, "description", "d", "", "Workspace description")
}

func runWorkspacesCreate(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Creating workspace '%s'...", name))
	sp.Start()

	client := api.NewClient()
	workspace, err := client.CreateWorkspace(&api.WorkspaceCreateRequest{
		Name:        name,
		Description: workspaceDescription,
	})
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to create workspace: %v", err)
		return err
	}

	output.PrintSuccess("Workspace '%s' created!", workspace.Name)
	output.Println("")
	output.KeyValue("ID", workspace.ID)
	output.KeyValue("Name", workspace.Name)
	if workspace.Description != "" {
		output.KeyValue("Description", workspace.Description)
	}

	output.Println("")
	output.Println("Next steps:")
	// `agents create` has no --workspace flag; suggesting one sent users to a
	// flag that does not exist. Assignment is a separate step (or the
	// `workspace:` key in aetherfy.yaml).
	output.Println("  • Put an agent in this workspace:")
	output.Println("    afy update my-agent --workspace " + workspace.Name)
	output.Println("  • Add shared secrets:")
	output.Println("    afy secrets set --workspace " + workspace.Name + " MY_SECRET=value")

	return nil
}

// --- LIST ---

var workspacesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	Long:  "List all workspaces in your account with their agent counts.",
	Example: `  # List workspaces
  afy workspaces list

  # List in JSON format
  afy workspaces list --output json`,
	RunE: runWorkspacesList,
}

func runWorkspacesList(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	sp := output.NewSpinner("Fetching workspaces...")
	sp.Start()

	client := api.NewClient()
	workspaces, err := client.ListWorkspaces()
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to list workspaces: %v", err)
		return err
	}

	if len(workspaces) == 0 {
		output.PrintInfo("No workspaces found.")
		output.Println("")
		output.Println("Create one with:")
		output.Println("  afy workspaces create <name>")
		return nil
	}

	if config.Get().OutputFormat == "json" {
		return output.JSON(workspaces)
	}

	table := output.Table([]string{"Name", "Agents", "Description", "Created"})
	for _, ws := range workspaces {
		desc := ws.Description
		if desc == "" {
			desc = "-"
		}
		table.Append([]string{
			ws.Name,
			fmt.Sprintf("%d", ws.AgentCount),
			desc,
			ws.CreatedAt.Format("2006-01-02"),
		})
	}
	table.Render()

	output.Println("")
	output.Dim.Printf("Total: %d workspace(s)\n", len(workspaces))

	return nil
}

// --- INFO ---

var workspacesInfoCmd = &cobra.Command{
	Use:   "info <name>",
	Short: "Show workspace details",
	Long:  "Show detailed information about a workspace.",
	Example: `  # Show workspace info
  afy workspaces info my-workspace

  # Show in JSON format
  afy workspaces info my-workspace --output json`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesInfo,
}

func runWorkspacesInfo(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Fetching workspace '%s'...", name))
	sp.Start()

	client := api.NewClient()
	workspace, err := client.GetWorkspace(name)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to get workspace: %v", err)
		return err
	}

	if config.Get().OutputFormat == "json" {
		return output.JSON(workspace)
	}

	output.Header(workspace.Name)
	output.KeyValue("ID", workspace.ID)
	output.KeyValue("Name", workspace.Name)
	if workspace.Description != "" {
		output.KeyValue("Description", workspace.Description)
	}
	output.KeyValue("Agents", fmt.Sprintf("%d", workspace.AgentCount))
	output.KeyValue("Created", workspace.CreatedAt.Format("2006-01-02 15:04"))
	output.KeyValue("Updated", workspace.UpdatedAt.Format("2006-01-02 15:04"))

	output.Println("")
	output.Println("Commands:")
	output.Println("  afy workspaces agents " + name)
	output.Println("  afy secrets list --workspace " + name)

	return nil
}

// --- DELETE ---

var workspacesDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a workspace",
	Long: `Delete a workspace and all its secrets.

Vector collections in the workspace are NOT deleted automatically.
Clean them up separately via the vectordb API.

The workspace must have no active agents. Delete all agents first.`,
	Example: `  # Delete a workspace (prompts for confirmation)
  afy workspaces delete my-workspace

  # Delete without confirmation
  afy workspaces delete my-workspace --force`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesDelete,
}

var workspaceForceDelete bool

func init() {
	workspacesDeleteCmd.Flags().BoolVarP(&workspaceForceDelete, "force", "f", false, "Skip confirmation prompt")
}

func runWorkspacesDelete(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	if !workspaceForceDelete {
		output.Warning.Printf("This will permanently delete workspace '%s' and all its secrets.\n", name)
		output.Warning.Println("Vector collections will NOT be deleted.")
		fmt.Print("Type the workspace name to confirm: ")
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != name {
			output.PrintInfo("Deletion cancelled.")
			return nil
		}
	}

	sp := output.NewSpinner(fmt.Sprintf("Deleting workspace '%s'...", name))
	sp.Start()

	client := api.NewClient()
	result, err := client.DeleteWorkspace(name)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to delete workspace: %v", err)
		return err
	}

	output.PrintSuccess("Workspace '%s' deleted.", name)
	if result.SecretsDeleted > 0 {
		output.Dim.Printf("  %d secret(s) deleted.\n", result.SecretsDeleted)
	}

	return nil
}

// --- UPDATE ---

var workspacesUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update a workspace's description",
	Long: `Update mutable fields on an existing workspace. Currently only the
description is mutable — workspace names are immutable post-creation
because three String columns reference them without FK cascade (see
docs/REVIEW_FAQ.md §53 in the control-plane repo). To "rename" a
workspace, delete and recreate it.`,
	Example: `  # Update the description
  afy workspaces update invoice-pipeline --description "Updated invoice processing agents"

  # Clear the description (set to empty string)
  afy workspaces update invoice-pipeline --description ""`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesUpdate,
}

// Separate variable from create's flag so the two commands don't share
// state across invocations. cobra binds these per-flag-definition.
var workspaceUpdateDescription string

func init() {
	workspacesUpdateCmd.Flags().StringVarP(&workspaceUpdateDescription, "description", "d", "", "New workspace description")
	// Cobra has no built-in way to detect "user explicitly passed empty
	// string" vs "user didn't pass flag"; we use Changed() at runtime
	// below. Make the flag required to enforce that the user passed
	// SOMETHING to mutate.
	_ = workspacesUpdateCmd.MarkFlagRequired("description")
}

func runWorkspacesUpdate(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Updating workspace '%s'...", name))
	sp.Start()

	client := api.NewClient()
	workspace, err := client.UpdateWorkspace(name, &api.WorkspaceUpdateRequest{
		Description: workspaceUpdateDescription,
	})
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to update workspace: %v", err)
		return err
	}

	output.PrintSuccess("Workspace '%s' updated.", workspace.Name)
	output.Println("")
	output.KeyValue("Name", workspace.Name)
	if workspace.Description != "" {
		output.KeyValue("Description", workspace.Description)
	} else {
		output.KeyValue("Description", "(none)")
	}

	return nil
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
		// `deploy` has no --workspace flag either. An agent joins a workspace
		// through its own config or `agents update`, then deploys normally.
		output.Println("Add an agent to this workspace with:")
		output.Println("  afy update <agent> --workspace " + workspaceName)
		output.Println("or set 'workspace: " + workspaceName + "' in its aetherfy.yaml, then deploy.")
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
	workspacesCmd.AddCommand(workspacesCreateCmd)
	workspacesCmd.AddCommand(workspacesListCmd)
	workspacesCmd.AddCommand(workspacesInfoCmd)
	workspacesCmd.AddCommand(workspacesUpdateCmd)
	workspacesCmd.AddCommand(workspacesDeleteCmd)
	workspacesCmd.AddCommand(workspacesAgentsCmd)
}
