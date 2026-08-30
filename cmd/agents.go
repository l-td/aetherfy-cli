package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/yamldiff"
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

	// The schedule columns only appear when at least one agent has a schedule —
	// accounts with no scheduled agents keep the original five-column layout.
	anyScheduled := false
	for i := range agents {
		if agents[i].CronSchedule != "" {
			anyScheduled = true
			break
		}
	}

	// The URL column appears only when at least one agent HAS a URL — an account
	// of drafts, or of scheduled tasks, keeps the narrower layout rather than
	// growing a column of dashes. The server decides which agents have one; this
	// only asks whether any do.
	anyURL := false
	for i := range agents {
		if agents[i].URL != "" {
			anyURL = true
			break
		}
	}

	// Table output. URL sits next to Status because "is it up" and "where do I
	// send the request" are the two questions this command exists to answer,
	// and the second had no answer anywhere in the CLI.
	headers := []string{"Name", "Type", "Status", "Regions", "ID"}
	if anyScheduled {
		headers = []string{"Name", "Type", "Status", "Regions", "Schedule", "Next Run", "Last Run", "ID"}
	}
	if anyURL {
		headers = append([]string{"Name", "Type", "Status", "URL"}, headers[3:]...)
	}
	table := output.Table(headers)
	for i := range agents {
		a := agents[i]
		status := formatStatus(a.Status)
		// Surface a degraded (partial multi-region) deploy inline on the list
		// view — health belongs on the primary surface, not a detail click
		// (control-plane REVIEW_FAQ §63). Separate trailing marker, not a
		// status value. Same DEGRADED term as the dashboard.
		if tag := formatDegradedTag(a.IsDegraded, a.RegionsReady, a.RegionsTotal); tag != "" {
			status += " " + tag
		}
		row := []string{a.Name, a.AgentType, status}
		if anyURL {
			row = append(row, formatAgentURL(a.URL))
		}
		row = append(row, formatRegions(a.Regions))
		if anyScheduled {
			schedCell, nextCell, lastCell := "", "", ""
			if a.CronSchedule != "" {
				schedCell = a.CronSchedule
				if a.CronPaused {
					nextCell = "(paused)"
				} else {
					nextCell = formatUTCTime(a.CronNextRunAt)
				}
				lastCell = formatLastRun(a)
			}
			row = append(row, schedCell, nextCell, lastCell)
		}
		row = append(row, a.ID)
		table.Append(row)
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

// normalizeAgentType validates a user-supplied agent type against the
// SERVICE|JOB enum and returns the lowercase value the backend expects.
//
// Single source for BOTH creation surfaces — the `-t` flag on `agents create`
// and the `type:` a consented `afy deploy --create` reads from aetherfy.yaml —
// so the deploy path cannot accept a type the create command would reject.
func normalizeAgentType(raw string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "SERVICE":
		return "service", nil
	case "JOB":
		return "job", nil
	}
	return "", fmt.Errorf("invalid agent type: must be SERVICE or JOB")
}

// createAgentRecord issues the POST /agents that creates an agent. Both
// `afy agents create` and the consented create inside `afy deploy` go through
// here rather than building their own request body, so the two can't drift.
// agentType must already be normalized (lowercase, backend form).
func createAgentRecord(client *api.Client, name, description, agentType, runtime string, spawnEnabled bool) (*api.Agent, error) {
	return client.CreateAgent(&api.AgentCreateRequest{
		Name:         name,
		Description:  description,
		AgentType:    agentType,
		Runtime:      runtime,
		SpawnEnabled: spawnEnabled,
	})
}

func runAgentsCreate(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	name := args[0]

	// Validate agent type
	normalizedType, typeErr := normalizeAgentType(agentType)
	if typeErr != nil {
		output.PrintError("Invalid agent type. Must be SERVICE or JOB")
		return typeErr
	}

	sp := output.NewSpinner(fmt.Sprintf("Creating agent '%s'...", name))
	sp.Start()

	client := api.NewClient()
	agent, err := createAgentRecord(client, name, agentDescription, normalizedType, agentRuntime, spawnEnabled)
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
	Long: `Pause an agent: stop every machine and prevent the platform from
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
	Long: `Archive an agent: tear down its running app to free the plan quota slot,
while preserving all of its configuration and the stored code bundle.

Distinct from 'afy agents stop' (pause/resume), which keeps the app
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
its app from the preserved code bundle and redeploy it.

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

	// Two facts, because two things are true on return. The pause IS applied —
	// the control plane commits status=paused and closes the billing interval
	// before answering — while the Fly machines take a few more seconds to wind
	// down, converged by a background job. Saying only "paused" would hide that;
	// saying the agent is "stopping" would contradict `afy agents status`, which
	// reports paused immediately.
	output.PrintSuccess("Agent '%s' paused; its machines are stopping. Resume with 'afy agents start %s'.", idOrName, idOrName)
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
	// Where to send a request, when the server says there is one.
	if agent.URL != "" {
		output.KeyValue("URL", agent.URL)
	}
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
	output.KeyValue("Regions", formatRegions(agent.Regions))
	output.KeyValue("Spawn Enabled", fmt.Sprintf("%v", agent.SpawnEnabled))
	if agent.WorkspaceName != "" {
		output.KeyValue("Workspace", agent.WorkspaceName)
	}
	// Cron schedule block — only for agents that actually have a schedule, so
	// schedule-less agents keep the same status layout as before.
	if agent.CronSchedule != "" {
		output.KeyValue("Schedule", agent.CronSchedule+" (UTC)")
		if agent.CronPaused {
			output.KeyValue("Next run", "(paused)")
		} else {
			output.KeyValue("Next run", formatUTCTime(agent.CronNextRunAt))
		}
		output.KeyValue("Last run", formatLastRun(*agent))
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

// --- RUN (manual "run now" of a JOB agent) ---
var agentsRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Run a job agent now",
	Long: `Trigger a one-off run of a deployed job agent immediately.

This is a manual run: the agent executes once and terminates, independent of any
cron schedule. Only deployed 'type: job' agents can be run.

Pass input with --payload (inline JSON) or --payload-file. Use --wait to block
until the run finishes — the command then exits 0 on success, 1 on failure.`,
	Example: `  # Run a job agent and return immediately
  afy agents run nightly-report

  # Run with a JSON payload and wait for the result
  afy agents run nightly-report --payload '{"date":"2026-07-17"}' --wait`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsRun,
}

var (
	runPayload     string
	runPayloadFile string
	runWait        bool
)

func init() {
	agentsRunCmd.Flags().StringVarP(&runPayload, "payload", "p", "", "JSON payload to pass to the run")
	agentsRunCmd.Flags().StringVarP(&runPayloadFile, "payload-file", "f", "", "Read the JSON payload from a file")
	agentsRunCmd.Flags().BoolVar(&runWait, "wait", false, "Wait for the run to finish (exit 1 if it fails)")
}

// parseRunPayload resolves the run payload from --payload / --payload-file
// (mutually exclusive). Returns nil when neither is set — the run body's
// payload field is then omitted.
func parseRunPayload() (map[string]interface{}, error) {
	if runPayload != "" && runPayloadFile != "" {
		return nil, fmt.Errorf("specify only one of --payload or --payload-file")
	}
	var raw []byte
	switch {
	case runPayload != "":
		raw = []byte(runPayload)
	case runPayloadFile != "":
		data, err := os.ReadFile(runPayloadFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read payload file: %w", err)
		}
		raw = data
	default:
		return nil, nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid JSON payload: %w", err)
	}
	return payload, nil
}

func runAgentsRun(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}
	name := args[0]

	payload, err := parseRunPayload()
	if err != nil {
		output.PrintError("%v", err)
		return nil
	}

	sp := output.NewSpinner(fmt.Sprintf("Starting run of '%s'...", name))
	sp.Start()
	client := api.NewClient()
	resp, err := client.RunAgent(name, payload)
	sp.Stop()
	if err != nil {
		printAgentActionError("Failed to run agent", err)
		return err
	}

	if config.Get().OutputFormat == "json" {
		return output.JSON(resp)
	}

	output.PrintSuccess("Run started.")
	output.KeyValue("Run ID", resp.DeploymentID)
	output.KeyValue("Version", fmt.Sprintf("v%d", resp.Version))
	output.Println("")

	if runWait {
		watchRun(client, name, resp.DeploymentID)
	} else {
		output.Printf("Follow it with: afy logs %s --run %s\n", name, resp.DeploymentID)
	}
	return nil
}

// watchRun polls a run's deployment to a terminal state, printing status
// transitions. A JOB run converges to COMPLETED (success) or FAILED (the
// machine exited non-zero / OOM). Mirrors watchDeployment's ticker but only
// terminates on the run-terminal states and sets the process exit code so
// `afy agents run --wait` is usable as a CI gate.
func watchRun(client *api.Client, agentName, deploymentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			output.PrintWarning("Run watch timed out; check it with 'afy logs %s --run %s'.", agentName, deploymentID)
			return
		case <-ticker.C:
			d, err := client.GetDeployment(deploymentID)
			if err != nil {
				output.PrintWarning("Failed to get run status: %v", err)
				continue
			}
			if d.Status != lastStatus {
				output.Printf("Status: %s\n", formatRunState(d.Status))
				lastStatus = d.Status
			}
			switch strings.ToLower(d.Status) {
			case "completed":
				output.PrintSuccess("Run completed.")
				return
			case "failed", "error":
				output.PrintError("Run failed.")
				if d.ErrorMessage != "" {
					output.Printf("Reason: %s\n", d.ErrorMessage)
				}
				os.Exit(1)
			}
		}
	}
}

// --- RUNS (scheduled + manual run history) ---
var agentsRunsCmd = &cobra.Command{
	Use:   "runs <name>",
	Short: "Show run history for a job agent",
	Long: `List recent scheduled and manual runs for a job agent, newest first.

Only cron and manual runs are shown; spawned runs belong to the parent agent's
history and are excluded.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsRuns,
}

var runsLimit int

func init() {
	agentsRunsCmd.Flags().IntVar(&runsLimit, "limit", 20, "Maximum number of runs to show (max 100)")
}

func runAgentsRuns(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}
	name := args[0]

	sp := output.NewSpinner("Fetching run history...")
	sp.Start()
	client := api.NewClient()
	runs, err := client.ListAgentRuns(name, api.RunsQuery{Limit: runsLimit})
	sp.Stop()
	if err != nil {
		printAgentActionError("Failed to list runs", err)
		return nil
	}

	if config.Get().OutputFormat == "json" {
		return output.JSON(runs)
	}
	if len(runs) == 0 {
		output.PrintInfo("No runs found for '%s'.", name)
		return nil
	}

	table := output.Table([]string{"When", "Trigger", "State", "Duration", "Run ID"})
	for i := range runs {
		r := runs[i]
		table.Append([]string{
			r.CreatedAt.Local().Format("2006-01-02 15:04"),
			r.TriggerSource,
			formatRunState(r.State),
			formatRunDuration(r.DurationSeconds),
			r.ID,
		})
	}
	table.Render()

	output.Println("")
	output.Dim.Printf("Total: %d run(s)\n", len(runs))
	return nil
}

// --- SCHEDULE (pause / resume a cron schedule) ---
var agentsScheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage a job agent's cron schedule",
	Long:  "Pause or resume the cron schedule of a job agent.",
}

var agentsSchedulePauseCmd = &cobra.Command{
	Use:   "pause <name>",
	Short: "Pause a job agent's cron schedule",
	Long: `Pause a job agent's cron schedule: no scheduled runs fire until you resume.
Manual runs ('afy agents run') are unaffected. Idempotent.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsSchedulePause,
}

var agentsScheduleResumeCmd = &cobra.Command{
	Use:   "resume <name>",
	Short: "Resume a paused cron schedule",
	Long: `Resume a paused cron schedule. The next run is recomputed from now —
occurrences that elapsed while paused are skipped, never backfilled. Idempotent.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentsScheduleResume,
}

func runAgentsSchedulePause(cmd *cobra.Command, args []string) error {
	return runScheduleToggle(args[0], true)
}

func runAgentsScheduleResume(cmd *cobra.Command, args []string) error {
	return runScheduleToggle(args[0], false)
}

// runScheduleToggle drives both schedule pause and resume — they share the same
// auth, spinner, response shape ({cron_paused, cron_next_run_at, changed}), and
// idempotent "already paused/live" messaging.
func runScheduleToggle(name string, pause bool) error {
	if err := checkAuth(); err != nil {
		return err
	}

	verb := "Resuming"
	if pause {
		verb = "Pausing"
	}
	sp := output.NewSpinner(fmt.Sprintf("%s schedule for '%s'...", verb, name))
	sp.Start()

	client := api.NewClient()
	var resp *api.ScheduleStateResponse
	var err error
	if pause {
		resp, err = client.PauseSchedule(name)
	} else {
		resp, err = client.ResumeSchedule(name)
	}
	sp.Stop()

	if err != nil {
		printAgentActionError("Failed to update schedule", err)
		return err
	}

	if config.Get().OutputFormat == "json" {
		return output.JSON(resp)
	}

	switch {
	case !resp.Changed:
		state := "live"
		if resp.CronPaused {
			state = "paused"
		}
		output.PrintInfo("Schedule for '%s' is already %s.", name, state)
	case resp.CronPaused:
		output.PrintSuccess("Schedule for '%s' paused.", name)
	default:
		output.PrintSuccess("Schedule for '%s' resumed.", name)
	}

	output.KeyValue("Paused", fmt.Sprintf("%v", resp.CronPaused))
	if resp.CronPaused {
		output.KeyValue("Next run", "(paused)")
	} else {
		output.KeyValue("Next run", formatUTCTime(resp.CronNextRunAt))
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
	agentsCmd.AddCommand(agentsRunCmd)
	agentsCmd.AddCommand(agentsRunsCmd)
	agentsScheduleCmd.AddCommand(agentsSchedulePauseCmd)
	agentsScheduleCmd.AddCommand(agentsScheduleResumeCmd)
	agentsCmd.AddCommand(agentsScheduleCmd)
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

// formatRegions renders an agent's region footprint for the list table and the
// status view. ONE formatter so the two surfaces cannot drift, the same reason
// formatDegradedTag is shared.
//
// An agent with no ACTIVE deployment has no footprint yet, and the server sends
// an empty list for it. That renders as "—" rather than "": a blank cell is
// what the old phantom `Region` field produced for EVERY agent, and it read as
// a broken column instead of as "nothing deployed".
func formatRegions(regions []string) string {
	if len(regions) == 0 {
		return "—"
	}
	return strings.Join(regions, ", ")
}

// formatAgentURL renders the URL column: the bare host, since every agent URL
// is https and the scheme costs eight characters of a table that is already
// wide. `afy agents status` prints the full URL — that is the one to copy.
//
// "—" means the server gave no address: a draft, a scheduled task (which serves
// nothing), or any agent before the edge is live. The rule lives there, not
// here.
func formatAgentURL(url string) string {
	if url == "" {
		return "—"
	}
	return strings.TrimPrefix(url, "https://")
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

// printAgentActionError prints the server error for a run/schedule action plus
// an actionable hint for the CP-4 cron/run-now error codes. Unknown codes fall
// through to the plain server message (already actionable). Never special-cases
// HTTP 502 — a bare 502 is edge infra, handled generically like the rest of the
// CLI.
func printAgentActionError(prefix string, err error) {
	output.PrintError("%s: %v", prefix, err)
	apiErr, ok := err.(*api.APIError)
	if !ok {
		return
	}
	switch apiErr.Code {
	case "AGENT_NOT_DEPLOYED":
		output.PrintInfo("Deploy it first: afy deploy")
	case "AGENT_SCHEDULE_NOT_SET":
		output.PrintInfo("Add `schedule:` to aetherfy.yaml and push (afy deploy).")
	case "AGENT_RUN_REQUIRES_JOB_TYPE":
		output.PrintInfo("Only `type: job` agents can be run on demand.")
	}
}

// formatRunState colorizes a run's deployment state for the runs table:
// COMPLETED green, FAILED red, in-flight (queued/building/deploying/active/
// running) yellow — matching the deployments colorizer's intent.
func formatRunState(state string) string {
	switch strings.ToLower(state) {
	case "completed":
		return output.Success.Sprint("completed")
	case "failed", "error":
		return output.Error.Sprint("failed")
	case "queued", "building", "deploying", "active", "running":
		return output.Warning.Sprint(state)
	default:
		return state
	}
}

// formatRunDuration renders a run's duration; nil (a run still in flight, or one
// with no machine timing yet) shows a dash.
func formatRunDuration(seconds *float64) string {
	if seconds == nil {
		return "-"
	}
	return output.FormatDuration(int(*seconds))
}

// formatUTCTime renders a timestamp in UTC with an explicit label — schedules
// are evaluated in UTC, so next/last-run times are shown in UTC to match. "-"
// for a nil time.
func formatUTCTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// formatLastRun renders a schedule's last fire outcome for the detail/list
// views: a colored fired/skipped/missed badge plus a relative timestamp. Reads
// as "never" when the schedule has not fired yet.
func formatLastRun(a api.Agent) string {
	if a.CronLastStatus == "" && a.CronLastRunAt == nil {
		return "never"
	}
	badge := formatCronStatusBadge(a.CronLastStatus)
	if a.CronLastRunAt != nil {
		return fmt.Sprintf("%s (%s)", badge, relativeTime(*a.CronLastRunAt))
	}
	return badge
}

// formatCronStatusBadge colorizes a cron_last_status value:
// fired green, skipped yellow, missed red.
func formatCronStatusBadge(status string) string {
	switch strings.ToLower(status) {
	case "fired":
		return output.Success.Sprint("fired")
	case "skipped":
		return output.Warning.Sprint("skipped")
	case "missed":
		return output.Error.Sprint("missed")
	case "":
		return "-"
	default:
		return status
	}
}

// relativeTime renders a coarse "Nx ago" for the last-run column.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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
