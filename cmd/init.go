package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/detect"
	"github.com/l-td/aetherfy-cli/internal/link"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// yamlSchemaModeline is aetherfy.yaml's first line: an editor that speaks the
// yaml-language-server protocol validates and completes the file against the
// published schema. The control plane's export starts with the same line.
const yamlSchemaModeline = "# yaml-language-server: $schema=https://docs.aetherfy.com/aetherfy-yaml.schema.json"

var initCmd = &cobra.Command{
	Use:   "init [path]",
	Short: "Create an agent for this folder, or link it to an existing one",
	Long: `Set up a folder to deploy to an agent.

With no --agent, creates a NEW agent: scans the folder, asks which workspace
the agent lives in and how it runs, creates the agent, writes aetherfy.yaml
and links the folder to the agent (.aetherfy/agent.json).

With --agent <name>, links the folder to an EXISTING agent. If the folder has
no aetherfy.yaml yet, the agent's current configuration is written as one
(this also works in an empty folder); an existing aetherfy.yaml is kept unless
--force.

--target names the link (default: "default"); other targets in the file are
left alone. That is how one codebase deploys to several agents:
'afy init --agent invoice-parser-staging --target staging', then
'afy deploy --target staging'.

Commit .aetherfy/ so every clone and CI job deploys to the same agent.

The regions and memory sizes offered are the ones your plan allows, read from
the server. All prompts can be skipped by providing flags, or pass -y/--yes to
accept every default (useful for scripting/CI).`,
	Example: `  # Create an agent for this folder (interactive)
  afy init

  # Accept all defaults (non-interactive)
  afy init -y --workspace default

  # Link this folder to an existing agent
  afy init --agent invoice-parser

  # Add a staging target pointing at another agent
  afy init --agent invoice-parser-staging --target staging`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

var (
	initName       string
	initAgent      string
	initTarget     string
	initWorkspace  string
	initRuntime    string
	initEntrypoint string
	initType       string
	initRegion     string
	initMemory     int
	initKeepAlive  bool
	initForce      bool
	initYes        bool
	initSchedule   string
)

func init() {
	initCmd.Flags().StringVar(&initName, "name", "", "Name of the new agent (skips prompt)")
	initCmd.Flags().StringVar(&initAgent, "agent", "", "Link this folder to this existing agent instead of creating one")
	initCmd.Flags().StringVar(&initTarget, "target", link.DefaultTarget, "Name of the link written (other targets are kept)")
	initCmd.Flags().StringVar(&initWorkspace, "workspace", "", "Workspace the new agent lives in (asked when the account has several)")
	initCmd.Flags().StringVar(&initRuntime, "runtime", "", "Runtime: python3.11, python3.12, python3.13, node20, node22, node20-ts, node22-ts, bun, dockerfile (skips prompt)")
	initCmd.Flags().StringVar(&initEntrypoint, "entrypoint", "", "Entrypoint file, e.g. main.py or index.js (skips prompt)")
	initCmd.Flags().StringVar(&initType, "type", "", "Agent type: service or job (skips prompt)")
	initCmd.Flags().StringVar(&initRegion, "region", "", "Region, one your plan allows (skips prompt)")
	initCmd.Flags().IntVar(&initMemory, "memory", 0, "Memory in MB, a size your plan allows (skips prompt)")
	initCmd.Flags().BoolVar(&initKeepAlive, "keep-alive", false, "Enable always-on billing (skips billing prompt)")
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "Overwrite an existing aetherfy.yaml without asking")
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "Accept every prompt's default (non-interactive)")
	initCmd.Flags().StringVar(&initSchedule, "schedule", "", "Schedule — runs the agent as a scheduled task on a 5-field cron expression in UTC, min every 5 minutes, e.g. '0 3 * * *'")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

var (
	reSlugUnsafe = regexp.MustCompile(`[^a-z0-9-]`)
	reSlugDashes = regexp.MustCompile(`-+`)
)

// isInteractive returns true when stdin is a real terminal.
// When false (CI, piped input, e2e tests), prompts are skipped and
// defaults are used for any missing flags.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// slugify turns a string into a safe agent name
func slugify(s string) string {
	s = strings.ToLower(s)
	s = reSlugUnsafe.ReplaceAllString(s, "-")
	s = reSlugDashes.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func runInit(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	if strings.TrimSpace(initTarget) == "" {
		return refuse("--target needs a name")
	}
	client := api.NewClient()
	if cmd.Flags().Changed("agent") {
		return initLinkExisting(cmd, client, absDir, initAgent, initTarget)
	}
	return initNewAgent(cmd, client, absDir)
}

// initLinkExisting links dir's target to an existing agent, by its id, and
// writes the agent's configuration as aetherfy.yaml when the folder has none.
// The file is the control plane's export, written unchanged: the server owns
// its shape, its modeline included.
func initLinkExisting(cmd *cobra.Command, client *api.Client, dir, name, target string) error {
	if strings.TrimSpace(name) == "" {
		return refuse("--agent needs the name of the agent to link")
	}
	agent, err := client.GetAgent(name)
	if err != nil {
		return printRefusal("Failed to find agent '"+name+"'", err)
	}

	// ONE RULE: export when the agent has a release, generate when it does
	// not. An existing aetherfy.yaml is kept unless --force.
	configPath := filepath.Join(dir, "aetherfy.yaml")
	wroteYAML, generated := false, false
	if !fileExists(configPath) || initForce {
		data, err := client.GetAgentYAML(agent.ID)
		switch {
		case isNotDeployed(err):
			// A draft (made with `afy create`): nothing to export, so the
			// file is generated from this folder, in the draft's workspace.
			ws, err := workspaceOf(client, agent)
			if err != nil {
				return printRefusal("Failed to find agent '"+agent.Name+"''s workspace", err)
			}
			gen, err := generateConfig(cmd, client, dir, ws, isInteractive() && !initYes)
			if err != nil {
				return err
			}
			data, generated = []byte(gen.content), true
		case err != nil:
			// RELEASE_CONFIG_NOT_ON_RECORD and the rest: as the server wrote them.
			return printRefusal("Failed to read agent '"+agent.Name+"''s configuration", err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(configPath, data, 0o644); err != nil {
			return fmt.Errorf("failed to write aetherfy.yaml: %w", err)
		}
		wroteYAML = true
	}

	previous := ""
	if f, err := link.Read(dir); err == nil {
		previous = f.Targets[target]
	}
	if err := link.Write(dir, target, agent.ID); err != nil {
		return fmt.Errorf("failed to write %s: %w", link.RelPath(), err)
	}

	output.PrintSuccess("Linked this folder to agent '%s' (target '%s').", agent.Name, target)
	if previous != "" && previous != agent.ID {
		output.Dim.Printf("  Target '%s' pointed at agent %s before.\n", target, previous)
	}
	switch {
	case generated:
		output.PrintInfo("%s has no release yet: wrote a new aetherfy.yaml from this folder; `afy deploy` ships the first release.", agent.Name)
	case wroteYAML:
		output.PrintInfo("Wrote aetherfy.yaml from the agent's live release.")
	default:
		output.PrintInfo("Kept your aetherfy.yaml (--force replaces it with the agent's current configuration).")
	}
	printLinkNextSteps(target)
	return nil
}

func printLinkNextSteps(target string) {
	output.Println("")
	output.Println("Commit .aetherfy/ so every clone and CI job deploys to the same agent.")
	if target == link.DefaultTarget {
		output.Println("Run 'afy deploy' when you are ready to go live.")
	} else {
		output.Println("Run 'afy deploy --target " + target + "' when you are ready to go live.")
	}
}

// initNewAgent creates an agent for dir: it asks what it needs, creates the
// agent (in a workspace, by id), then writes aetherfy.yaml and the link. A
// refused create writes nothing.
func initNewAgent(cmd *cobra.Command, client *api.Client, absDir string) error {
	if f, err := link.Read(absDir); err == nil {
		if id, ok := f.Targets[initTarget]; ok {
			return refuse("this folder's target '%s' is already linked to agent %s. To link it to another "+
				"existing agent, use 'afy init --agent <name>'; to add a new agent beside it, pass --target <name>", initTarget, id)
		}
	} else if !errors.Is(err, link.ErrNotLinked) {
		return err
	}

	configPath := filepath.Join(absDir, "aetherfy.yaml")
	if proceed, err := confirmOverwrite(configPath); err != nil || !proceed {
		return err
	}

	// -y / --yes disables the interactive path entirely, reusing the
	// non-interactive default fallbacks for every prompt. Does NOT imply
	// --force: overwriting an existing aetherfy.yaml still requires an
	// explicit opt-in.
	interactive := isInteractive() && !initYes

	// --- Agent name ---
	agentName := initName
	if agentName == "" {
		defaultName := slugify(filepath.Base(absDir))
		if interactive {
			namePrompt := promptui.Prompt{
				Label:   "Agent name",
				Default: defaultName,
				Validate: func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("name cannot be empty")
					}
					return nil
				},
			}
			var err error
			agentName, err = namePrompt.Run()
			if err != nil {
				return fmt.Errorf("prompt cancelled")
			}
		} else {
			agentName = defaultName
		}
	}
	agentName = slugify(strings.TrimSpace(agentName))

	// --- Workspace: where the agent lives. Not in aetherfy.yaml. ---
	ws, err := pickWorkspace(client, initWorkspace, cmd.Flags().Changed("workspace"), interactive, os.Stdin, os.Stdout)
	if err != nil {
		return printRefusal("Failed to find the workspace", err)
	}

	gen, err := generateConfig(cmd, client, absDir, ws, interactive)
	if err != nil {
		return err
	}

	output.Println("")
	sp := output.NewSpinner(fmt.Sprintf("Creating agent '%s' in workspace '%s'...", agentName, ws.Name))
	sp.Start()
	agent, err := createAgentRecord(client, agentName, "", ws.ID)
	sp.Stop()
	if err != nil {
		return printRefusal("Failed to create agent", err)
	}
	output.PrintSuccess("Agent '%s' created in workspace '%s'.", agent.Name, ws.Name)

	if err := os.WriteFile(configPath, []byte(gen.content), 0o644); err != nil {
		output.PrintError("The agent was created, but aetherfy.yaml could not be written (%v). "+
			"Link the folder with 'afy init --agent %s'.", err, agent.Name)
		return err
	}
	if err := link.Write(absDir, initTarget, agent.ID); err != nil {
		output.PrintError("The agent was created, but %s could not be written (%v). "+
			"Link the folder with 'afy init --agent %s'.", link.RelPath(), err, agent.Name)
		return err
	}
	output.PrintSuccess("aetherfy.yaml and %s written.", link.RelPath())
	output.Println("")
	printConfigSummary(agent.Name, ws.Name, gen.runtime, gen.agentType, gen.region, gen.memoryMB, gen.keepAlive, gen.schedule)
	printLinkNextSteps(initTarget)
	return nil
}

// confirmOverwrite applies init's rule for an existing aetherfy.yaml: --force
// overwrites; otherwise a terminal is asked, and without one it is refused.
// It reports whether to go on (false with no error: the person declined).
func confirmOverwrite(configPath string) (bool, error) {
	if fileExists(configPath) && !initForce {
		if !isInteractive() {
			output.PrintError("aetherfy.yaml already exists. Use --force to overwrite.")
			return false, fmt.Errorf("aetherfy.yaml already exists")
		}
		prompt := promptui.Prompt{
			Label:     "aetherfy.yaml already exists. Overwrite",
			IsConfirm: true,
		}
		if _, err := prompt.Run(); err != nil {
			output.Println("Aborted.")
			return false, nil
		}
	}

	return true, nil
}

// generatedConfig is the aetherfy.yaml init writes for an agent that has no
// release, and what the summary prints of it.
type generatedConfig struct {
	content, runtime, agentType, region, schedule string
	memoryMB                                      int
	keepAlive                                     bool
}

// generateConfig scans dir and asks (or takes from flags, or defaults) how the
// agent runs, offering only the regions the plan allows inside workspace ws and
// the memory sizes the plan allows (GET /deploy-options). The content is the
// file `afy init` writes: the schema modeline first, nothing the platform owns.
// Used for a new agent and for a draft (an agent with no release to export).
func generateConfig(cmd *cobra.Command, client *api.Client, absDir string, ws *api.Workspace, interactive bool) (*generatedConfig, error) {
	output.Println("")
	output.PrintInfo("Scanning your project...")

	hints := detect.Project(absDir)
	if hints.Runtime != "" {
		output.PrintSuccess("Detected runtime: %s", hints.Runtime)
	}
	if hints.Entrypoint != "" {
		output.PrintSuccess("Detected entrypoint: %s", hints.Entrypoint)
	}
	if hints.Runtime == "" && initRuntime == "" {
		if isInteractive() {
			output.PrintWarning("Could not detect runtime — you will be asked to choose")
		} else {
			output.PrintWarning("Could not detect runtime — use --runtime to specify one")
		}
	}
	selectedRuntime := initRuntime
	if selectedRuntime == "" {
		selectedRuntime = hints.Runtime
	}
	if hints.Entrypoint == "" && initEntrypoint == "" && selectedRuntime != "dockerfile" {
		if isInteractive() {
			output.PrintWarning("Could not detect entrypoint — you will be asked to specify one")
		} else {
			output.PrintWarning("Could not detect entrypoint — add 'entrypoint:' to aetherfy.yaml before deploying")
		}
	}
	if hints.HasMastra {
		output.PrintSuccess("Detected 'mastra' in dependencies")
	}
	if hints.HasUvLock {
		output.PrintSuccess("Detected uv.lock — will use 'uv sync --frozen' for reproducible installs")
	} else if hints.HasPyprojectToml {
		output.PrintWarning("pyproject.toml found but uv.lock is missing — run 'uv lock' and commit uv.lock before deploying")
	}
	output.Println("")

	// The regions and sizes this plan allows, from the server.
	opts, err := client.GetDeployOptions()
	if err != nil {
		return nil, printRefusal("Failed to read the regions and memory sizes your plan allows", err)
	}

	// --- Runtime ---
	runtime := initRuntime
	if runtime == "" {
		if interactive {
			runtimeOptions := []string{"python3.13", "python3.12", "python3.11", "node22", "node20", "node22-ts", "node20-ts", "bun", "dockerfile"}
			runtimeDefault := 2 // python3.11
			if hints.Runtime != "" {
				for i, r := range runtimeOptions {
					if r == hints.Runtime {
						runtimeDefault = i
						break
					}
				}
			}
			runtimeSelect := promptui.Select{
				Label:     "Runtime",
				Items:     runtimeOptions,
				CursorPos: runtimeDefault,
			}
			var err error
			_, runtime, err = runtimeSelect.Run()
			if err != nil {
				return nil, fmt.Errorf("prompt cancelled")
			}
		} else if hints.Runtime != "" {
			runtime = hints.Runtime
		} else {
			return nil, fmt.Errorf("could not detect runtime. Use --runtime to specify one (python3.11, python3.12, python3.13, node20, node22, node20-ts, node22-ts, bun, dockerfile)")
		}
	}

	// --- Agent type ---
	agentType := initType
	if agentType == "" {
		if interactive {
			typeSelect := promptui.Select{
				Label: "Agent type",
				Items: []string{
					"service  (always-on listener, e.g. API or webhook handler)",
					"job      (ephemeral, wakes up, runs, then stops)",
				},
			}
			typeIdx, _, err := typeSelect.Run()
			if err != nil {
				return nil, fmt.Errorf("prompt cancelled")
			}
			agentType = "service"
			if typeIdx == 1 {
				agentType = "job"
			}
		} else {
			agentType = "service"
		}
	}

	// --- Schedule ---
	// A schedule is a trigger on either agent type. The server is the
	// authoritative cron validator; the expression is only carried into
	// aetherfy.yaml. The --schedule flag skips the prompt.
	schedule := initSchedule
	if schedule == "" && interactive {
		schedulePrompt := promptui.Prompt{
			Label:   "Schedule — UTC, 5-field cron (min every 5 minutes), e.g. '0 3 * * *' — leave blank for none",
			Default: "",
		}
		s, sErr := schedulePrompt.Run()
		if sErr == nil {
			schedule = strings.TrimSpace(s)
		}
	}

	// --- Region: one the plan allows, inside the workspace's ---
	// A deploy refuses regions outside the agent's workspace's
	// (DEPLOY_REGIONS_NOT_IN_SCOPE), so only those are offered. A workspace
	// with no regions yet takes them from the agent's first deploy (the home
	// region on a single-region plan), so then every region the plan allows is.
	allowedRegions := regionsInScope(opts.AllowedRegions(), ws.Regions)
	if len(allowedRegions) == 0 {
		return nil, fmt.Errorf("none of workspace '%s''s regions (%s) is one your plan allows; change the workspace's "+
			"regions (afy workspaces regions %s --regions ...) or your plan", ws.Name, strings.Join(ws.Regions, ", "), ws.Name)
	}
	defaultRegion := allowedRegions[0]
	if opts.HomeRegion != nil && contains(allowedRegions, *opts.HomeRegion) {
		defaultRegion = *opts.HomeRegion
	}
	region := initRegion
	switch {
	case region != "":
		if !contains(allowedRegions, region) {
			return nil, refuse("--region %s is not one your plan allows in workspace '%s' (allowed: %s)",
				region, ws.Name, strings.Join(allowedRegions, ", "))
		}
	case interactive:
		regionSelect := promptui.Select{
			Label:     "Region",
			Items:     allowedRegions,
			CursorPos: indexOf(allowedRegions, defaultRegion),
		}
		idx, _, err := regionSelect.Run()
		if err != nil {
			return nil, fmt.Errorf("prompt cancelled")
		}
		region = allowedRegions[idx]
	default:
		region = defaultRegion
	}

	// --- Memory: a size the plan allows ---
	allowedMemory := opts.AllowedMemory()
	if len(allowedMemory) == 0 {
		return nil, fmt.Errorf("your plan allows no memory size; check your plan on the dashboard")
	}
	memoryMB := initMemory
	switch {
	case memoryMB != 0:
		if !containsInt(allowedMemory, memoryMB) {
			return nil, refuse("--memory %d is not a size your plan allows (allowed: %s MB)", memoryMB, joinInts(allowedMemory))
		}
	case interactive:
		items := make([]string, len(allowedMemory))
		for i, m := range allowedMemory {
			items[i] = fmt.Sprintf("%d MB", m)
		}
		memorySelect := promptui.Select{Label: "Memory", Items: items}
		idx, _, err := memorySelect.Run()
		if err != nil {
			return nil, fmt.Errorf("prompt cancelled")
		}
		memoryMB = allowedMemory[idx]
	default:
		memoryMB = allowedMemory[0]
	}

	// --- Billing mode ---
	keepAlive := initKeepAlive
	if !cmd.Flags().Changed("keep-alive") && interactive {
		billingSelect := promptui.Select{
			Label: "Billing mode",
			Items: []string{
				"auto-sleep  ($0 when idle, ~500ms cold start)",
				"always-on   (+$10/mo, 0ms cold start)",
			},
		}
		billingIdx, _, err := billingSelect.Run()
		if err != nil {
			return nil, fmt.Errorf("prompt cancelled")
		}
		keepAlive = billingIdx == 1
	}

	// --- Entrypoint ---
	// dockerfile runtime: user owns the container, entrypoint is irrelevant
	entrypoint := ""
	if runtime != "dockerfile" {
		entrypoint = initEntrypoint
		if entrypoint == "" {
			entrypoint = hints.Entrypoint
		}
		if entrypoint == "" && interactive {
			epPrompt := promptui.Prompt{
				Label:   "Entrypoint file (e.g. main.py, index.js) — leave blank to set later",
				Default: "",
			}
			ep, epErr := epPrompt.Run()
			if epErr == nil {
				entrypoint = strings.TrimSpace(ep)
			}
		}
	}

	return &generatedConfig{
		content:   buildAetherfyYAML(runtime, agentType, region, memoryMB, keepAlive, entrypoint, schedule),
		runtime:   runtime,
		agentType: agentType,
		region:    region,
		memoryMB:  memoryMB,
		keepAlive: keepAlive,
		schedule:  schedule,
	}, nil

}

// workspaceOf is the workspace agent lives in, found by its id among the
// account's (the agent's read names it by id and name; its regions come from
// the workspace's own record).
func workspaceOf(client *api.Client, agent *api.Agent) (*api.Workspace, error) {
	all, err := client.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == agent.WorkspaceID {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("workspace %s of agent '%s' is not among the account's workspaces", agent.WorkspaceID, agent.Name)
}

// regionsInScope is allowed (the plan's, in the platform's order) narrowed to
// the workspace's regions; all of allowed when the workspace has none yet.
func regionsInScope(allowed, workspace []string) []string {
	if len(workspace) == 0 {
		return allowed
	}
	var out []string
	for _, r := range allowed {
		if contains(workspace, r) {
			out = append(out, r)
		}
	}
	return out
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return 0
}

func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

func joinInts(list []int) string {
	out := make([]string, len(list))
	for i, n := range list {
		out[i] = strconv.Itoa(n)
	}
	return strings.Join(out, ", ")
}

// printConfigSummary prints the chosen config values as an aligned table.
// Column widths are computed from the actual label set so adding or renaming
// a row never requires re-padding by hand.
func printConfigSummary(name, workspace, runtime, agentType, region string, memoryMB int, keepAlive bool, schedule string) {
	billing := "auto-sleep"
	if keepAlive {
		billing = "always-on"
	}

	rows := [][2]string{
		{"Agent", name},
		{"Workspace", workspace},
		{"Runtime", runtime},
		{"Type", agentType},
		{"Region", region},
		{"Memory", fmt.Sprintf("%d MB", memoryMB)},
		{"Billing", billing},
	}
	// Only surface the schedule row when one was set — schedule-less agents
	// keep the summary unchanged.
	if schedule != "" {
		rows = append(rows, [2]string{"Schedule", schedule + " (UTC)"})
	}

	maxLabel := 0
	for _, r := range rows {
		if len(r[0]) > maxLabel {
			maxLabel = len(r[0])
		}
	}

	output.Println("Configuration:")
	for _, r := range rows {
		// "%-*s" right-pads the label+colon to a fixed column width.
		output.Println(fmt.Sprintf("  %-*s %s", maxLabel+1, r[0]+":", r[1]))
	}
}

// buildAetherfyYAML is the aetherfy.yaml `afy init` writes for a new agent:
// how it runs, and nothing the platform owns (its name, description and
// workspace are set on the agent, not in the file). The schema modeline is
// the first line.
func buildAetherfyYAML(runtime, agentType, region string, memoryMB int, keepAlive bool, entrypoint, schedule string) string {
	var sb strings.Builder

	sb.WriteString(yamlSchemaModeline + "\n")
	sb.WriteString("# Aetherfy Configuration File\n")
	sb.WriteString(fmt.Sprintf("runtime: %s\n", runtime))
	sb.WriteString(fmt.Sprintf("type: %s\n", agentType))
	sb.WriteString("regions:\n")
	sb.WriteString(fmt.Sprintf("  - %s\n", region))
	sb.WriteString(fmt.Sprintf("memory_mb: %d\n", memoryMB))

	if keepAlive {
		sb.WriteString("keep_alive: true\n")
	} else {
		sb.WriteString("keep_alive: false\n")
	}

	if runtime != "dockerfile" {
		if entrypoint != "" {
			sb.WriteString(fmt.Sprintf("entrypoint: %q\n", entrypoint))
		} else {
			hint := "main.py"
			switch {
			case runtime == "bun":
				hint = "index.ts"
			case strings.HasSuffix(runtime, "-ts"):
				hint = "index.ts"
			case strings.HasPrefix(runtime, "node"):
				hint = "index.js"
			}
			sb.WriteString(fmt.Sprintf("# entrypoint: %s  # TODO: set your entrypoint file\n", hint))
		}
	}

	// Cron schedule. Quoted so cron expressions like "0 3 * * *"
	// are always parsed as a string. Validated server-side on deploy.
	if schedule != "" {
		sb.WriteString(fmt.Sprintf("schedule: %q  # UTC, 5-field cron (min every 5 minutes)\n", schedule))
	}

	sb.WriteString("\n# --- ADVANCED SETTINGS ---\n")
	sb.WriteString("# Uncomment below to enable multi-agent spawning. workers is required:\n")
	sb.WriteString("# the agents this one may spawn, or [\"*\"] for any of your agents.\n")
	sb.WriteString("# spawn:\n")
	sb.WriteString("#   enabled: true\n")
	sb.WriteString("#   workers:\n")
	sb.WriteString("#     - sub-agent-1\n")

	return sb.String()
}
