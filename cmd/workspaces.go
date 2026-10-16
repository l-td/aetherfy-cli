package cmd

import (
	"fmt"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
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
and cannot start or end with a hyphen.

A workspace owns its regions: --regions names 1 up to your plan's limit of the
regions your plan allows, and agents and collections in the workspace can only
use those. Without --regions, a terminal is asked to pick; anything else
(a script, --json) is refused with the list of allowed regions, because there
is nothing to fall back on. Change them later with
'afy workspaces regions'.`,
	Example: `  # Create a workspace in one region
  afy workspaces create invoice-pipeline --regions us-east-1

  # Several regions (repeat the flag, or comma-separate), up to your plan's limit
  afy workspaces create invoice-pipeline --regions us-east-1,eu-central-1

  # With a description
  afy workspaces create invoice-pipeline --regions us-east-1 --description "Invoice processing agents"`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesCreate,
}

var (
	workspaceDescription   string
	workspaceCreateRegions []string
)

func init() {
	workspacesCreateCmd.Flags().StringVarP(&workspaceDescription, "description", "d", "", "Workspace description")
	workspacesCreateCmd.Flags().StringSliceVar(&workspaceCreateRegions, "regions", nil, "Regions the workspace owns, comma-separated or repeated, from the regions your plan allows (asked on a terminal when omitted)")
}

// chooseWorkspaceRegions is the regions a new workspace is created with: the
// ones typed (--regions), or, on a terminal, picked. Validated against the
// plan (GET /deploy-options) BEFORE anything is sent. Nothing is assumed:
// omitted without a terminal is refused naming the allowed ones.
func chooseWorkspaceRegions(cmd *cobra.Command, client *api.Client, p *prompter) ([]string, error) {
	opts, err := client.GetDeployOptions("")
	if err != nil {
		return nil, printRefusal("Failed to read the regions your plan allows", err)
	}
	allowed := opts.AllowedRegions()
	if len(allowed) == 0 {
		return nil, refuse("your plan allows no region for a workspace; check your plan on the dashboard")
	}
	max := opts.MaxRegions
	if max < 1 {
		max = 1
	}

	if cmd.Flags().Changed("regions") {
		var regions []string
		seen := map[string]bool{}
		for _, r := range workspaceCreateRegions {
			r = strings.TrimSpace(r)
			if r == "" {
				return nil, refuse("--regions got an empty region (allowed: %s)", strings.Join(allowed, ", "))
			}
			if seen[r] {
				continue
			}
			seen[r] = true
			regions = append(regions, r)
		}
		if len(regions) == 0 {
			return nil, refuse("--regions needs at least one region (allowed: %s)", strings.Join(allowed, ", "))
		}
		for _, r := range regions {
			if contains(allowed, r) {
				continue
			}
			known := false
			for _, o := range opts.Regions {
				if o.Code == r {
					known = true
				}
			}
			if known {
				return nil, refuse("region %s is not open on your plan (allowed: %s). Nothing was created.", r, strings.Join(allowed, ", "))
			}
			return nil, refuse("%s is not a region (allowed on your plan: %s). Nothing was created.", r, strings.Join(allowed, ", "))
		}
		if len(regions) > max {
			return nil, refuse("%d regions given, but your plan allows %d per workspace (allowed: %s). Nothing was created.", len(regions), max, strings.Join(allowed, ", "))
		}
		return regions, nil
	}

	if !isInteractive() || output.IsJSON() {
		return nil, refuse("a workspace owns its regions: pass --regions with up to %d of: %s. Nothing was created.", max, strings.Join(allowed, ", "))
	}
	if max == 1 || len(allowed) == 1 {
		idx, err := p.choose("Region", allowed, 0)
		if err != nil {
			return nil, err
		}
		return []string{allowed[idx]}, nil
	}
	picked, err := p.chooseMany("Regions", allowed, max)
	if err != nil {
		return nil, err
	}
	regions := make([]string, len(picked))
	for i, idx := range picked {
		regions[i] = allowed[idx]
	}
	return regions, nil
}

func runWorkspacesCreate(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	client := api.NewClient()
	regions, err := chooseWorkspaceRegions(cmd, client, newPrompter())
	if err != nil {
		return err
	}

	sp := output.NewSpinner(fmt.Sprintf("Creating workspace '%s'...", name))
	sp.Start()

	workspace, err := client.CreateWorkspace(&api.WorkspaceCreateRequest{
		Name:        name,
		Description: workspaceDescription,
		Regions:     regions,
	})
	sp.Stop()

	if err != nil {
		return printRefusal("Failed to create workspace", err)
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

	return output.Result(workspace)
}

// --- LIST ---

var workspacesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	Long:  "List all workspaces in your account with their agent counts.",
	Example: `  # List workspaces
  afy workspaces list

  # List in JSON format
  afy workspaces list --json`,
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
		return printRefusal("Failed to list workspaces", err)
	}

	// --json first: an empty list is [], not a sentence on stderr and nothing
	// on stdout.
	if output.IsJSON() {
		return output.JSON(workspaces)
	}

	if len(workspaces) == 0 {
		output.PrintInfo("No workspaces found.")
		output.Println("")
		output.Println("Create one with:")
		output.Println("  afy workspaces create <name> --regions <region>")
		return nil
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
  afy workspaces info my-workspace --json`,
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
		return printRefusal("Failed to get workspace", err)
	}

	if output.IsJSON() {
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
		fmt.Fprint(output.Out(), "Type the workspace name to confirm: ")
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != name {
			output.PrintInfo("Deletion cancelled.")
			return output.Result(map[string]interface{}{"workspace": name, "deleted": false})
		}
	}

	sp := output.NewSpinner(fmt.Sprintf("Deleting workspace '%s'...", name))
	sp.Start()

	client := api.NewClient()
	result, err := client.DeleteWorkspace(name)
	sp.Stop()

	if err != nil {
		// WORKSPACE_IS_LAST and the not-empty refusals say what to do: printed as written.
		return printRefusal("Failed to delete workspace", err)
	}

	output.PrintSuccess("Workspace '%s' deleted.", name)
	if result.SecretsDeleted > 0 {
		output.Dim.Printf("  %d secret(s) deleted.\n", result.SecretsDeleted)
	}

	return output.Result(map[string]interface{}{"workspace": name, "deleted": true, "secrets_deleted": result.SecretsDeleted})
}

// --- UPDATE ---

var workspacesUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Rename a workspace or change its description",
	Long: `Rename a workspace (--name), change its description (--description), or both.

Everything inside refers to a workspace by id, so a rename moves nothing: its
agents keep running and are not redeployed, and its collections, secrets and
grants stay where they are. What changes is the name people and code use: code
that addresses the workspace by its old name (an SDK's workspace="old-name",
'--workspace old-name') stops finding it. Agents using workspace="auto" are
unaffected.

A name already used by another of your workspaces is refused.`,
	Example: `  # Rename a workspace
  afy workspaces update invoice-pipeline --name invoices

  # Update the description
  afy workspaces update invoice-pipeline --description "Updated invoice processing agents"

  # Clear the description (set to empty string)
  afy workspaces update invoice-pipeline --description ""`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesUpdate,
}

// Separate variables from create's flags so the two commands don't share
// state across invocations. cobra binds these per-flag-definition.
var (
	workspaceUpdateName        string
	workspaceUpdateDescription string
)

func init() {
	workspacesUpdateCmd.Flags().StringVar(&workspaceUpdateName, "name", "", "New workspace name")
	workspacesUpdateCmd.Flags().StringVarP(&workspaceUpdateDescription, "description", "d", "", "New workspace description")
}

func runWorkspacesUpdate(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]
	req := &api.WorkspaceUpdateRequest{}
	if cmd.Flags().Changed("name") {
		if workspaceUpdateName == "" {
			output.PrintError("--name needs the workspace's new name")
			return shown(fmt.Errorf("empty name"))
		}
		req.Name = &workspaceUpdateName
	}
	// Changed, not the value: --description "" is how a description is cleared.
	if cmd.Flags().Changed("description") {
		req.Description = &workspaceUpdateDescription
	}
	if req.Name == nil && req.Description == nil {
		output.PrintError("Specify --name <new> and/or --description <text>")
		return shown(fmt.Errorf("no update specified"))
	}

	sp := output.NewSpinner(fmt.Sprintf("Updating workspace '%s'...", name))
	sp.Start()
	client := api.NewClient()
	workspace, err := client.UpdateWorkspace(name, req)
	sp.Stop()
	if err != nil {
		return printRefusal("Failed to update workspace", err)
	}

	if req.Name != nil && workspace.Name != name {
		output.PrintSuccess("Workspace '%s' renamed to '%s'.", name, workspace.Name)
		output.Dim.Printf("  Its agents keep running. Code that names it '%s' must use '%s' now.\n", name, workspace.Name)
	} else {
		output.PrintSuccess("Workspace '%s' updated.", workspace.Name)
	}
	output.Println("")
	output.KeyValue("Name", workspace.Name)
	if workspace.Description != "" {
		output.KeyValue("Description", workspace.Description)
	} else {
		output.KeyValue("Description", "(none)")
	}
	return output.Result(workspace)
}

// --- AGENTS ---

var workspacesAgentsCmd = &cobra.Command{
	Use:   "agents <workspace>",
	Short: "List the agents that live in a workspace and the ones granted access to it",
	Long: `List a workspace's agents, in two sections.

"Lives here" is the agents that live in the workspace: names, types, status
and when they were created. "Granted access" is the agents of your OTHER
workspaces that were granted access to this one, and when. A grant gives an
agent access to this workspace's collections; it does not move it ('afy
update <agent> --workspace <name>' moves one; 'afy access <agent>' manages
its grants).

With --json: {"living": [...], "granted": [...]}.`,
	Example: `  # Both sections
  afy workspaces agents my-workspace

  # As JSON
  afy workspaces agents my-workspace --json`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspacesAgents,
}

// workspaceAgentsJSON is `afy workspaces agents --json`.
type workspaceAgentsJSON struct {
	Living  []api.Agent        `json:"living"`
	Granted []api.GrantedAgent `json:"granted"`
}

func runWorkspacesAgents(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	workspaceName := args[0]

	sp := output.NewSpinner("Fetching workspace agents...")
	sp.Start()

	client := api.NewClient()
	living, err := client.ListWorkspaceAgents(workspaceName)
	var granted []api.GrantedAgent
	if err == nil {
		granted, err = client.ListWorkspaceGrantedAgents(workspaceName)
	}
	sp.Stop()

	if err != nil {
		// WORKSPACE_NOT_FOUND and the rest: the server's sentence, as written,
		// and a failing exit code.
		return printRefusal("Failed to list workspace agents", err)
	}

	// Always arrays, never null: a script reads the same shape for an empty
	// workspace as for a full one.
	if living == nil {
		living = []api.Agent{}
	}
	if granted == nil {
		granted = []api.GrantedAgent{}
	}

	if output.IsJSON() {
		return output.JSON(workspaceAgentsJSON{Living: living, Granted: granted})
	}

	if len(living) == 0 {
		output.Printf("Lives here: no agents (move one in with: afy update <agent> --workspace %s)\n", workspaceName)
	} else {
		output.Printf("Lives here (%d)\n", len(living))
		table := output.Table([]string{"Name", "Type", "Status", "Created"})
		for _, a := range living {
			agentType := string(a.AgentType)
			if agentType == "" {
				agentType = "SERVICE" // Default
			}
			table.Append([]string{
				a.Name,
				agentType,
				string(a.Status),
				a.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		table.Render()
	}

	output.Println("")
	if len(granted) == 0 {
		output.Println("Granted access: no agents from other workspaces have access to this one")
	} else {
		output.Printf("Granted access (%d)\n", len(granted))
		table := output.Table([]string{"Name", "Granted"})
		for _, g := range granted {
			at := g.GrantedAt
			table.Append([]string{g.Name, formatUTCTime(&at)})
		}
		table.Render()
		output.Dim.Println("A grant gives an agent access to this workspace's collections; it does not move it.")
	}

	if len(living) > 0 {
		output.Println("")
		output.Dim.Println("Agents living in the same workspace can:")
		output.Dim.Println("  • Share secrets (workspace-scoped)")
		output.Dim.Println("  • Share vector collections")
		output.Dim.Println("  • Communicate via HTTP (AETHERFY_AGENT_<NAME>_URL)")
	}

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
		if sentence, ok := narrowBlockedSentence(err); ok {
			status, code, _, _ := serverRefusal(err)
			output.PrintAPIError(status, code, sentence)
			return shown(err)
		}
		return printRefusal("Failed to change workspace regions", err)
	}

	var op *api.Operation
	if workspaceRegionsWait && change.OperationID != "" {
		sp := output.NewSpinner(fmt.Sprintf("Waiting for operation %s...", change.OperationID))
		sp.Start()
		op, err = pollOperation(client, change.OperationID)
		sp.Stop()
		if err != nil {
			return printRefusal("", err)
		}
	}

	if output.IsJSON() {
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
