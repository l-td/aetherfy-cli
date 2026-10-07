package cmd

import (
	"fmt"
	"strings"

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
	output.KeyValue("Regions", strings.Join(workspace.Regions, ", "))
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
description is mutable. A workspace name is fixed once created, because
other resources reference it by name and nothing rewrites those
references. To "rename" a workspace, delete and recreate it.`,
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

// --- REGIONS ---

var workspacesRegionsCmd = &cobra.Command{
	Use:   "regions <name>",
	Short: "Change the regions a workspace allows",
	Long: `Change the regions a workspace allows.

A workspace's regions are a ceiling for what is in it, not a placement: adding
a region copies nothing, and removing one is refused while an agent or a
collection of the workspace still uses it (move or narrow those first). So the
change copies and deletes no data, and asks nothing.

The change runs in the background. --wait follows it to the end: exit 1 if it
fails, 4 if it is still running after 30 minutes.`,
	Example: `  # Allow a second region
  afy workspaces regions research --regions us-east-1,eu-central-1

  # Narrow it again, following the change to its end
  afy workspaces regions research --regions us-east-1 --wait`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesRegions,
}

var (
	workspaceRegions     []string
	workspaceRegionsWait bool
)

func init() {
	workspacesRegionsCmd.Flags().StringSliceVar(&workspaceRegions, "regions", nil, "Regions the workspace allows, comma-separated, required")
	workspacesRegionsCmd.Flags().BoolVar(&workspaceRegionsWait, "wait", false, "Follow the change to the end (exit 1 if it fails, 4 if still running after 30 minutes)")
	_ = workspacesRegionsCmd.MarkFlagRequired("regions")
}

func runWorkspacesRegions(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Changing the regions of workspace '%s'...", name))
	sp.Start()

	client := api.NewClient()
	change, err := client.UpdateWorkspaceRegions(name, workspaceRegions)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to change workspace regions: %v", err)
		return err
	}

	var op *api.Operation
	if workspaceRegionsWait && change.OperationID != "" {
		sp := output.NewSpinner(fmt.Sprintf("Waiting for operation %s...", change.OperationID))
		sp.Start()
		op, err = pollOperation(client, change.OperationID)
		sp.Stop()
		if err != nil {
			output.PrintError("%v", err)
			return err
		}
	}

	if config.Get().OutputFormat == "json" {
		return output.JSON(struct {
			Workspace string                      `json:"workspace"`
			Change    *api.WorkspaceRegionsChange `json:"change"`
			Operation *api.Operation              `json:"operation"`
		}{name, change, op})
	}

	if change.Status == "no_op" {
		output.PrintInfo("No change: workspace '%s' already allows %s.", name, strings.Join(change.CurrentRegions, ", "))
		return nil
	}
	if op != nil {
		output.PrintSuccess("Workspace '%s' now allows %s.", name, strings.Join(change.ToRegions, ", "))
		return nil
	}
	output.PrintSuccess("Regions change for workspace '%s' accepted.", name)
	output.KeyValue("Operation", change.OperationID)
	output.KeyValue("Regions", strings.Join(change.ToRegions, ", "))
	output.Println("")
	output.Println("It runs in the background. See the result with:")
	output.Println("  afy workspaces info " + name)
	return nil
}

func init() {
	workspacesCmd.AddCommand(workspacesCreateCmd)
	workspacesCmd.AddCommand(workspacesListCmd)
	workspacesCmd.AddCommand(workspacesInfoCmd)
	workspacesCmd.AddCommand(workspacesUpdateCmd)
	workspacesCmd.AddCommand(workspacesDeleteCmd)
	workspacesCmd.AddCommand(workspacesAgentsCmd)
	workspacesCmd.AddCommand(workspacesRegionsCmd)
}
