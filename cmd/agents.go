package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/config"
	"github.com/aetherfy/cli/internal/output"
	"github.com/aetherfy/cli/internal/yamldiff"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
		// Surface a degraded (partial multi-region) deploy inline on the list
		// view — health belongs on the primary surface, not a detail click
		// (control-plane REVIEW_FAQ §63). Separate trailing marker, not a
		// status value. Same DEGRADED term as the dashboard.
		if tag := formatDegradedTag(a.IsDegraded, a.RegionsReady, a.RegionsTotal); tag != "" {
			status += " " + tag
		}
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
	agentsCreateCmd.Flags().StringVarP(&agentRuntime, "runtime", "r", "python3.11", "Runtime: python3.11, python3.12, python3.13, node20, node22, node20-ts, node22-ts, bun, dockerfile")
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

// --- STOP / START (user-invoked pause) ---
var agentsStopCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Pause an agent",
	Long: `Pause an agent: stop every machine and prevent the Fly.io proxy from
re-waking it on incoming traffic. Reversible via 'afy agents start <name>'.

Distinct from a billing-driven STOPPED state.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsStop,
}

var agentsStartCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Resume a paused agent",
	Long:  "Resume an agent that was paused with 'afy agents stop'.",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentsStart,
}

// --- ARCHIVE / RESTORE (destroy Fly app, preserve config; re-provision) ---
var agentsArchiveCmd = &cobra.Command{
	Use:   "archive <name>",
	Short: "Archive an agent, freeing its plan slot",
	Long: `Archive an agent: destroy its Fly.io app to free the plan quota slot,
while preserving all of its configuration and the stored code bundle.

Distinct from 'afy agents stop' (pause/resume), which keeps the Fly app
provisioned. Archiving releases the slot so you can create or restore
another agent; the agent shows up as 'archived' in 'afy agents list'.

Reversible via 'afy agents restore <name>', which re-provisions it from the
preserved bundle (subject to a plan-quota re-check).`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsArchive,
}

var agentsRestoreCmd = &cobra.Command{
	Use:   "restore <name>",
	Short: "Restore an archived agent",
	Long: `Restore an agent that was archived with 'afy agents archive': re-provision
its Fly.io app from the preserved code bundle and redeploy it.

Restoring consumes a plan quota slot, so it is re-checked at restore time —
if you are at your plan limit the restore is rejected until you free a slot
(delete or archive another agent) or upgrade your plan. The deploy then runs
asynchronously; track progress with 'afy agents list'.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsRestore,
}

func runAgentsStop(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	idOrName := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Pausing agent '%s'...", idOrName))
	sp.Start()

	client := api.NewClient()
	err := client.StopAgent(idOrName)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to pause agent: %v", err)
		return err
	}

	output.PrintSuccess("Agent '%s' paused. Resume with 'afy agents start %s'.", idOrName, idOrName)
	output.PrintInfo("Stopped agents keep billing at the base rate; archive to stop billing.")
	return nil
}

func runAgentsStart(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	idOrName := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Starting agent '%s'...", idOrName))
	sp.Start()

	client := api.NewClient()
	err := client.StartAgent(idOrName)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to start agent: %v", err)
		return err
	}

	output.PrintSuccess("Agent '%s' is starting. Use 'afy agents status %s' to monitor.", idOrName, idOrName)
	return nil
}

func runAgentsArchive(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	idOrName := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Archiving agent '%s'...", idOrName))
	sp.Start()

	client := api.NewClient()
	err := client.ArchiveAgent(idOrName)
	sp.Stop()

	if err != nil {
		// AGENT_ALREADY_ARCHIVED (and the other archive 409s) carry a clear
		// server message — pass it straight through.
		output.PrintError("Failed to archive agent: %v", err)
		return err
	}

	output.PrintSuccess("Agent '%s' archived. Its config is preserved; run 'afy agents restore %s' to redeploy it.", idOrName, idOrName)
	return nil
}

func runAgentsRestore(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	idOrName := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Restoring agent '%s'...", idOrName))
	sp.Start()

	client := api.NewClient()
	err := client.RestoreAgent(idOrName)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to restore agent: %v", err)
		// Quota is re-checked on restore. When the plan is full the server
		// returns PLAN_LIMIT_EXCEEDED — add an actionable hint on top of the
		// server message. AGENT_NOT_ARCHIVED and other 409s pass through
		// unchanged (their server message is already self-explanatory).
		if apiErr, ok := err.(*api.APIError); ok && apiErr.Code == "PLAN_LIMIT_EXCEEDED" {
			output.Println("")
			output.PrintInfo("Delete or archive another agent to free a slot before restoring, or upgrade your plan.")
		}
		return err
	}

	output.PrintSuccess("Agent '%s' restore initiated. This may take a few minutes while the deploy runs. Track status via 'afy agents list'.", idOrName)
	return nil
}

// --- CANCEL (abandon an in-flight deployment) ---
var agentsCancelCmd = &cobra.Command{
	Use:   "cancel <name>",
	Short: "Cancel a pending deployment",
	Long: `Cancel an agent's pending deployment.

Useful when you notice a build will fail (bad Dockerfile, wrong deps) and
want to abandon it without deleting the agent itself — fix the issue and
redeploy.

Phase 1: only QUEUED deployments are cancellable. In-flight builds
(BUILDING / DEPLOYING) return a clear 409 until cooperative-cancellation
support lands in the backend workers.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsCancel,
}

func runAgentsCancel(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	idOrName := args[0]

	sp := output.NewSpinner(fmt.Sprintf("Finding pending deployment for '%s'...", idOrName))
	sp.Start()

	client := api.NewClient()

	// list_deployments returns DESC by created_at. We scan for a pending
	// user-initiated deploy — explicitly skipping ephemeral spawn rows
	// (one Deployment per spawn() call from a parent SERVICE), which are
	// system-managed and not user-cancellable. The backend cancel route
	// would 404 on those anyway; filtering here gives a cleaner UX.
	deployments, err := client.ListDeployments(idOrName)
	if err != nil {
		sp.Stop()
		output.PrintError("Failed to list deployments: %v", err)
		return err
	}

	var pending *api.Deployment
	for i := range deployments {
		if deployments[i].IsEphemeral {
			continue
		}
		s := deployments[i].Status
		if s == "queued" || s == "building" || s == "deploying" {
			pending = &deployments[i]
			break
		}
	}
	if pending == nil {
		sp.Stop()
		output.PrintInfo("No pending deployment to cancel for '%s'.", idOrName)
		return nil
	}

	sp.UpdateMessage(fmt.Sprintf("Cancelling deployment v%d (state: %s)...", pending.Version, pending.Status))

	result, err := client.CancelDeployment(idOrName, pending.Version)
	sp.Stop()
	if err != nil {
		output.PrintError("Failed to cancel deployment: %v", err)
		return err
	}

	// Two response shapes from the route:
	//   - QUEUED path: state="failed" already (route handled synchronously).
	//   - In-flight path (BUILDING/DEPLOYING): state unchanged, but
	//     CancellationRequested=true; the worker will transition to FAILED
	//     at its next checkpoint (within seconds for build start, up to
	//     a few minutes if mid-Docker-build subprocess).
	// Distinguish in the user-facing message so the user knows whether
	// to expect immediate vs eventual completion.
	if result.Status == "failed" {
		output.PrintSuccess("Deployment v%d cancelled.", result.Version)
	} else {
		output.PrintInfo(
			"Cancellation requested for deployment v%d (state: %s). "+
				"Worker will clean up at its next checkpoint. "+
				"Run 'afy agents status %s' to confirm completion.",
			result.Version, result.Status, idOrName,
		)
	}
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
	// Degraded = a partial multi-region deploy still converging (derived from
	// the current deployment, control-plane REVIEW_FAQ §63). Shown only when
	// degraded — a status command that hides this is a contract violation by
	// name. Same DEGRADED term as the dashboard + `afy agents list`.
	if agent.IsDegraded {
		output.KeyValue("Health", output.Warning.Sprintf("DEGRADED (%d/%d regions ready)", agent.RegionsReady, agent.RegionsTotal))
		if agent.DegradedReason != "" {
			output.KeyValue("Degraded reason", agent.DegradedReason)
		}
	}
	output.KeyValue("Region", agent.Region)
	output.KeyValue("Spawn Enabled", fmt.Sprintf("%v", agent.SpawnEnabled))
	if agent.WorkspaceName != "" {
		output.KeyValue("Workspace", agent.WorkspaceName)
	}
	output.KeyValue("Created", agent.CreatedAt.Format("2006-01-02 15:04:05"))
	output.KeyValue("Updated", agent.UpdatedAt.Format("2006-01-02 15:04:05"))

	if agent.Description != "" {
		output.Println("")
		output.KeyValue("Description", agent.Description)
	}

	// Spawn relationships. SERVICE: which JOBs it may spawn. JOB: which
	// SERVICEs could spawn it (reverse view, computed client-side) and,
	// if this instance was spawned, the SERVICE that spawned it.
	printSpawnRelationships(client, agent)

	return nil
}

// printSpawnRelationships renders the field-level spawn relationships for an
// agent. For JOBs it fetches the full agent list ONCE to compute the reverse
// "spawnable by" view and resolve the parent's name — no new endpoint.
func printSpawnRelationships(client *api.Client, agent *api.Agent) {
	output.Println("")
	switch strings.ToLower(agent.AgentType) {
	case "service":
		if len(agent.AllowedWorkers) > 0 {
			output.KeyValue("Allowed workers", "["+strings.Join(agent.AllowedWorkers, ", ")+"]")
		} else {
			output.KeyValue("Allowed workers", "(none)")
		}
	case "job":
		all, err := client.ListAgents()
		if err != nil {
			// Non-fatal: the core status output already rendered. Surface
			// the relationships as unknown rather than failing the command.
			output.KeyValue("Spawnable by", "(unavailable)")
			return
		}
		spawnableBy := api.SpawnableBy(agent.Name, all)
		if len(spawnableBy) > 0 {
			output.KeyValue("Spawnable by", "["+strings.Join(spawnableBy, ", ")+"]")
		} else {
			output.KeyValue("Spawnable by", "(none)")
		}
		if agent.ParentAgentID != nil {
			name := api.AgentNameByID(*agent.ParentAgentID, all)
			if name == "" {
				name = *agent.ParentAgentID
			}
			output.KeyValue("Spawned by", name)
		}
	}
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
	agentsCmd.AddCommand(agentsStopCmd)
	agentsCmd.AddCommand(agentsStartCmd)
	agentsCmd.AddCommand(agentsArchiveCmd)
	agentsCmd.AddCommand(agentsRestoreCmd)
	agentsCmd.AddCommand(agentsCancelCmd)
	agentsCmd.AddCommand(agentsStatusCmd)
	agentsCmd.AddCommand(agentsRenameCmd)
	agentsCmd.AddCommand(agentsUpdateCmd)
	agentsCmd.AddCommand(agentsPullCmd)
	agentsCmd.AddCommand(agentsDiffCmd)
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

// --- UPDATE ---
var agentsUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update an agent's workspace assignment or description",
	Long: `Update mutable fields on an existing agent.

Use --workspace to move the agent into a workspace, or --no-workspace to
make it workspaceless (the two are mutually exclusive). Use --description
to set the agent's description. At least one flag is required; flags that
aren't given are left unchanged.`,
	Example: `  # Assign the agent to a workspace
  afy agents update my-agent --workspace invoice-pipeline

  # Make the agent workspaceless (clear its workspace)
  afy agents update my-agent --no-workspace

  # Set the description (leaves the workspace untouched)
  afy agents update my-agent --description "Parses inbound invoices"`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsUpdate,
}

var (
	agentUpdateWorkspace   string
	agentUpdateNoWorkspace bool
	agentUpdateDescription string
)

func init() {
	agentsUpdateCmd.Flags().StringVar(&agentUpdateWorkspace, "workspace", "", "Assign the agent to this workspace")
	agentsUpdateCmd.Flags().BoolVar(&agentUpdateNoWorkspace, "no-workspace", false, "Clear the agent's workspace (make it workspaceless)")
	agentsUpdateCmd.Flags().StringVarP(&agentUpdateDescription, "description", "d", "", "Set the agent's description")
}

func runAgentsUpdate(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	wsChanged := cmd.Flags().Changed("workspace")
	descChanged := cmd.Flags().Changed("description")

	// Exactly one of --workspace / --no-workspace must be supplied — they
	// are distinct intents (set vs clear) and must not be combined.
	if wsChanged && agentUpdateNoWorkspace {
		output.PrintError("--workspace and --no-workspace are mutually exclusive")
		return fmt.Errorf("--workspace and --no-workspace are mutually exclusive")
	}
	if !wsChanged && !agentUpdateNoWorkspace && !descChanged {
		output.PrintError("Specify at least one of --workspace <name>, --no-workspace, or --description <text>")
		return fmt.Errorf("no update specified")
	}

	req := &api.AgentUpdateRequest{}

	// Only touch workspace_name when a workspace flag was actually given —
	// otherwise a description-only update would send a workspace_name and
	// clobber the existing assignment.
	if wsChanged {
		// Reject the empty string: clearing is --no-workspace's job, so we
		// don't want two ways to express "workspaceless".
		if agentUpdateWorkspace == "" {
			output.PrintError("--workspace requires a non-empty name; use --no-workspace to clear")
			return fmt.Errorf("empty workspace name")
		}
		encoded, err := json.Marshal(agentUpdateWorkspace)
		if err != nil {
			return err
		}
		req.WorkspaceName = json.RawMessage(encoded)
	} else if agentUpdateNoWorkspace {
		// --no-workspace → explicit JSON null clears the assignment.
		req.WorkspaceName = json.RawMessage("null")
	}

	if descChanged {
		req.Description = &agentUpdateDescription
	}

	sp := output.NewSpinner(fmt.Sprintf("Updating agent '%s'...", name))
	sp.Start()

	client := api.NewClient()
	agent, err := client.UpdateAgent(name, req)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to update agent: %v", err)
		return err
	}

	output.PrintSuccess("Agent '%s' updated.", agent.Name)
	output.Println("")
	output.KeyValue("Name", agent.Name)
	if agent.WorkspaceName != "" {
		output.KeyValue("Workspace", agent.WorkspaceName)
	} else {
		output.KeyValue("Workspace", "(none)")
	}
	if agent.Description != "" {
		output.KeyValue("Description", agent.Description)
	}

	return nil
}

// --- PULL (export current state as aetherfy.yaml) ---
var agentsPullCmd = &cobra.Command{
	Use:   "pull <agent-name>",
	Short: "Export an agent's current configuration as aetherfy.yaml",
	Long: `Export an agent's current configuration as aetherfy.yaml.

Use this to re-sync a local aetherfy.yaml after editing the agent via the
dashboard or API. Prints to stdout by default (so you can redirect it):

  afy agents pull my-agent > aetherfy.yaml

Or write to a file directly with -o. The YAML is the declarative subset only
(server-derived fields like id/status are excluded) and re-deploying it is a
no-op — it's the inverse of 'afy deploy' under merge-patch semantics.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsPull,
}

var pullOutputFile string

func init() {
	agentsPullCmd.Flags().StringVarP(&pullOutputFile, "output", "o", "", "Write YAML to this file instead of stdout")
}

func runAgentsPull(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]
	client := api.NewClient()
	data, err := client.GetAgentYAML(name)
	if err != nil {
		output.PrintError("Failed to pull agent '%s': %v", name, err)
		return err
	}

	if pullOutputFile != "" {
		if err := os.WriteFile(pullOutputFile, data, 0o644); err != nil {
			output.PrintError("Failed to write %s: %v", pullOutputFile, err)
			return err
		}
		output.PrintSuccess("Wrote %s", pullOutputFile)
		return nil
	}

	// Raw to stdout — no spinner/color/decoration, so `afy agents pull foo >
	// foo.yaml` produces a clean file.
	fmt.Print(string(data))
	return nil
}

// --- DIFF (preview what `afy deploy` would change vs preserve) ---
var agentsDiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Preview what `afy deploy` would change vs preserve",
	Long: `Compare the local aetherfy.yaml against the agent's current state and
print what 'afy deploy' would do, under merge-patch semantics:

  ~ field: old → new   would change
  + field: value       would set (currently unset)
  - field              would clear (declared null)
  = field: value       no-op (declared, already matches)
    field: value       preserved (omitted locally — deploy keeps it)

Auto-detects aetherfy.yaml in the current directory (like 'afy deploy').
Exits non-zero when there are changes, so it's usable as a CI gate.`,
	Args: cobra.NoArgs,
	RunE: runAgentsDiff,
}

var diffPath string

func init() {
	agentsDiffCmd.Flags().StringVarP(&diffPath, "path", "p", ".", "Project directory containing aetherfy.yaml")
}

func runAgentsDiff(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	// Parse the local aetherfy.yaml into a map (NOT the typed struct) so we
	// preserve the declared / omitted / explicit-null distinction merge-patch
	// depends on.
	localPath := filepath.Join(diffPath, "aetherfy.yaml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		output.PrintError("Could not read %s: %v", localPath, err)
		return err
	}
	var local map[string]interface{}
	if err := yaml.Unmarshal(data, &local); err != nil {
		output.PrintError("Invalid aetherfy.yaml: %v", err)
		return err
	}
	name, _ := local["name"].(string)
	if name == "" {
		output.PrintError("aetherfy.yaml has no 'name' — cannot resolve the agent to diff against")
		return fmt.Errorf("missing name in aetherfy.yaml")
	}

	client := api.NewClient()
	serverYAML, err := client.GetAgentYAML(name)
	if err != nil {
		output.PrintError("Failed to fetch current state for '%s': %v", name, err)
		return err
	}
	var server map[string]interface{}
	if err := yaml.Unmarshal(serverYAML, &server); err != nil {
		output.PrintError("Control plane returned invalid YAML: %v", err)
		return err
	}

	diffs := yamldiff.Diff(local, server)
	printAgentDiff(name, diffs)

	// CI-friendly: non-zero exit when a deploy would change something.
	if yamldiff.HasChanges(diffs) {
		os.Exit(1)
	}
	return nil
}

// printAgentDiff renders the merge-patch diff with the project's color scheme.
func printAgentDiff(name string, diffs []yamldiff.FieldDiff) {
	output.Header(fmt.Sprintf("Diff for agent: %s", name))
	output.Println("")
	for _, d := range diffs {
		switch d.Kind {
		case yamldiff.Change:
			line := fmt.Sprintf("~ %s: %s → %s", d.Key, d.Current, d.Declared)
			// runtime is immutable post-create (the deploy rejects a change
			// with RUNTIME_IMMUTABLE) — flag it so the preview isn't misleading.
			if d.Key == "runtime" {
				output.Red.Printf("%s  (immutable — deploy will reject)\n", line)
			} else {
				output.Yellow.Println(line)
			}
		case yamldiff.Add:
			output.Green.Printf("+ %s: %s\n", d.Key, d.Declared)
		case yamldiff.Clear:
			output.Red.Printf("- %s  (clear; currently %s)\n", d.Key, d.Current)
		case yamldiff.NoOp:
			output.Dim.Printf("= %s: %s\n", d.Key, d.Declared)
		case yamldiff.Preserve:
			output.Dim.Printf("  %s: %s (preserved by deploy)\n", d.Key, d.Current)
		}
	}
	output.Println("")
	if yamldiff.HasChanges(diffs) {
		output.PrintInfo("Run 'afy deploy' to apply these changes.")
	} else {
		output.PrintSuccess("No changes — local aetherfy.yaml matches the agent's current state.")
	}
}

// formatDegradedTag renders the inline DEGRADED health marker shared by the
// agents-list and deployments tables (REVIEW_FAQ §63). Empty string when not
// degraded. Health is a SEPARATE trailing marker — it never replaces the
// status/state value, which keeps its enum (running/active); DEGRADED is not a
// status. Single source so the dashboard and both CLI tables stay identical.
func formatDegradedTag(isDegraded bool, regionsReady, regionsTotal int) string {
	if !isDegraded {
		return ""
	}
	return output.Warning.Sprintf("⚠ DEGRADED %d/%d", regionsReady, regionsTotal)
}

// formatDeploymentState renders the State column value for the rollback
// deployment-history table. It appends "(current)" to the live-serving
// deployment (state == "active") so the user can see which version they'd be
// rolling back FROM — surface parity with the dashboard's "→ CURRENT" marker
// (lowercase to match CLI tone). Non-active states pass through unchanged.
func formatDeploymentState(state string) string {
	if state == "active" {
		return state + " (current)"
	}
	return state
}

// formatStatus adds color to status strings
func formatStatus(status string) string {
	switch strings.ToLower(status) {
	case "running", "active", "healthy":
		return output.Green.Sprint(status)
	case "pending", "deploying", "building":
		return output.Yellow.Sprint(status)
	case "stopped", "inactive", "archived":
		// Archived agents have no Fly app provisioned (slot freed) — render in
		// the same neutral/dim tone as other non-running lifecycle states so
		// the list clearly signals "not live" without an alarming color.
		return output.Gray.Sprint(status)
	case "usage_paused":
		// D2 spend-cap pause — a friendlier label than the raw enum value, in an
		// attention color (the account hit its spend limit).
		return output.Yellow.Sprint("paused (usage limit)")
	case "error", "failed", "unhealthy":
		return output.Red.Sprint(status)
	default:
		return status
	}
}
