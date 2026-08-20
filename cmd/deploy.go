package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/archive"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

// githubRepoRefPattern validates owner/repo or owner/repo@ref
var githubRepoRefPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(@\S+)?$`)

var deployCmd = &cobra.Command{
	Use:   "deploy [path]",
	Short: "Deploy an agent",
	Long: `Deploy code to an Aetherfy agent.

The path should contain an aetherfy.yaml configuration file.
If no path is specified, the current directory is used.

The deployment process:
  1. Validates aetherfy.yaml exists
  2. Creates a ZIP archive of your code
  3. Uploads to Aetherfy
  4. Builds a Docker image
  5. Deploys to Fly.io

By default the command waits for the deployment to complete and streams
status updates. Use --detach to return immediately after upload.

Files matching patterns in .afyignore will be excluded.

Use --from-github to deploy directly from a public GitHub repository
without a local clone. The agent name is read from the repo's aetherfy.yaml
unless --agent is specified.

If the agent does not exist yet, an interactive deploy offers to create it
from the type and runtime declared in aetherfy.yaml. Creation is never
automatic: on a terminal it asks and defaults to no, and anywhere else it
happens only when --create is passed.`,
	Example: `  # Deploy current directory (waits for completion)
  afy deploy

  # Deploy specific directory
  afy deploy ./my-agent

  # Deploy and return immediately (fire and forget)
  afy deploy --detach

  # Deploy and specify agent by name
  afy deploy --agent my-bot

  # Create the agent from aetherfy.yaml if it does not exist yet (works in CI)
  afy deploy --create

  # Deploy from a public GitHub repo (Level 1)
  afy deploy --from-github owner/repo
  afy deploy --from-github owner/repo@v1.2.3 --agent my-bot`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDeploy,
}

var (
	deployDetach     bool
	deployAgent      string
	deployFromGitHub string
	deployYes        bool
	deployCreate     bool
)

func init() {
	deployCmd.Flags().BoolVarP(&deployDetach, "detach", "d", false, "Return immediately after upload without waiting for completion")
	deployCmd.Flags().StringVarP(&deployAgent, "agent", "a", "", "Agent ID or name (reads from aetherfy.yaml if not specified)")
	deployCmd.Flags().StringVar(&deployFromGitHub, "from-github", "", "Deploy from a public GitHub repo: owner/repo[@ref]")
	deployCmd.Flags().BoolVarP(&deployYes, "yes", "y", false, "Skip the overage confirmation prompt and proceed (non-interactive)")
	deployCmd.Flags().BoolVar(&deployCreate, "create", false, "Create the agent from aetherfy.yaml's type and runtime if it does not exist. Required to create without a terminal; --yes never implies it")
}

// promptYesNo asks a yes/no question on stdin and reports whether the answer
// was yes. Default NO: anything other than "y" — including a blank line, EOF,
// or a closed stdin — declines. It is a var so tests can drive the confirm
// paths without a terminal.
var promptYesNo = func(question string) bool {
	output.Warning.Printf("%s [y/N] ", question)
	var answer string
	_, _ = fmt.Scanln(&answer)
	return strings.EqualFold(strings.TrimSpace(answer), "y")
}

// uuidPattern matches the canonical UUID form the control-plane resolves agents
// by (api/routes/agents.py _resolve_agent tries UUID() first, then name).
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// createOnDeployInput is everything the AGENT_NOT_FOUND branch needs to decide
// whether a deploy may create the missing agent. Every input is a parameter —
// no globals, no terminal — so the ratified automation rule is testable.
type createOnDeployInput struct {
	AgentName   string                  // the agent this deploy targeted
	Config      *archive.AetherfyConfig // parsed aetherfy.yaml; nil disables the offer
	CreateFlag  bool                    // --create
	YesFlag     bool                    // --yes (the COST prompt's flag)
	Interactive bool                    // stdin is a terminal
	Confirm     func(question string) bool
}

// createOnDeployOutcome is the decision. Create=false means "print today's
// AGENT_NOT_FOUND error and stop" — verbatim when Warn is empty.
type createOnDeployOutcome struct {
	Create    bool
	AgentType string // normalized, backend form — the value that will be SENT
	Runtime   string // from aetherfy.yaml — the value that will be SENT
	Announce  string // printed immediately before creating (non-interactive consent)
	Question  string // the question that was asked (interactive consent)
	Warn      string // printed before today's error when creation cannot be offered
}

// decideCreateOnDeploy resolves the ratified consent rule for a deploy that hit
// AGENT_NOT_FOUND:
//
//   - INTERACTIVE (a terminal, no --yes, no --create): ask, defaulting to No.
//   - NON-INTERACTIVE (no terminal, or --yes present): never auto-confirm.
//     --yes covers the overage cost confirmation ONLY. Creation requires an
//     explicit --create; without it the deploy fails with today's error,
//     verbatim. A typo in CI costs a failed run, never a duplicate agent.
//   - --create is itself the consent, so it does not ask.
//
// The type/runtime it reports are the exact values the create will send —
// whatever this returns is what gets printed AND what gets sent.
func decideCreateOnDeploy(in createOnDeployInput) createOnDeployOutcome {
	// No manifest to describe the agent with → never offer. This is also the
	// terminator for the post-create retry, which passes a nil Config so a
	// second AGENT_NOT_FOUND cannot loop back into creating.
	if in.Config == nil {
		return createOnDeployOutcome{}
	}

	consented := in.CreateFlag
	mayAsk := in.Interactive && !in.YesFlag && !in.CreateFlag
	if !consented && !mayAsk {
		// Today's error, verbatim — no hint, no prompt, nothing created.
		return createOnDeployOutcome{}
	}

	// A UUID target names an agent RECORD, not a name to mint. The control-plane
	// name rule (lowercase letters, digits, hyphens) happens to accept a UUID,
	// so creating here would silently succeed and leave an agent named after a
	// typo'd ID.
	if uuidPattern.MatchString(in.AgentName) {
		return createOnDeployOutcome{
			Warn: fmt.Sprintf("'%s' is an agent ID, not a name — refusing to create an agent named after it. "+
				"Target the agent by name (--agent <name>) or create it with 'afy agents create <name>'.", in.AgentName),
		}
	}

	// aetherfy.yaml must SAY what the agent is. Nothing here is defaulted:
	// runtime is fixed at creation server-side (a guessed one would fail every
	// later deploy with RUNTIME_IMMUTABLE), and a guessed type is a guess the
	// prompt would have to hide.
	if strings.TrimSpace(in.Config.Type) == "" {
		return createOnDeployOutcome{
			Warn: fmt.Sprintf("Agent '%s' does not exist and aetherfy.yaml declares no 'type:' — "+
				"creating it would have to guess. Add 'type: service' or 'type: job' and re-run.", in.AgentName),
		}
	}
	agentType, err := normalizeAgentType(in.Config.Type)
	if err != nil {
		return createOnDeployOutcome{
			Warn: fmt.Sprintf("Agent '%s' does not exist and aetherfy.yaml declares type: %q — "+
				"it must be 'service' or 'job'.", in.AgentName, in.Config.Type),
		}
	}
	runtime := strings.TrimSpace(in.Config.Runtime)
	if runtime == "" {
		return createOnDeployOutcome{
			Warn: fmt.Sprintf("Agent '%s' does not exist and aetherfy.yaml declares no 'runtime:' — "+
				"creating it would have to guess. Add a 'runtime:' and re-run.", in.AgentName),
		}
	}

	if consented {
		return createOnDeployOutcome{
			Create:    true,
			AgentType: agentType,
			Runtime:   runtime,
			Announce: fmt.Sprintf("Agent '%s' doesn't exist. Creating it as %s/%s (from aetherfy.yaml).",
				in.AgentName, agentType, runtime),
		}
	}

	question := fmt.Sprintf("Agent '%s' doesn't exist. Create it as %s/%s (from aetherfy.yaml)?",
		in.AgentName, agentType, runtime)
	if !in.Confirm(question) {
		return createOnDeployOutcome{Question: question}
	}
	return createOnDeployOutcome{
		Create:    true,
		AgentType: agentType,
		Runtime:   runtime,
		Question:  question,
	}
}

// handleDeployResult post-processes the first deploy attempt for the D2 Part 6
// overage cost gate, the freeze/pause 403s, and the consented create-on-deploy
// (AGENT_NOT_FOUND). The caller does the first Deploy(confirm=false) with its
// spinner and stops it BEFORE calling this (so a prompt isn't drawn over the
// spinner). On OVERAGE_CONFIRM_REQUIRED it shows "this adds ~$X/mo — continue?"
// and, on confirm (or --yes), re-sends with confirm_overage=true (its own brief
// spinner). Non-special errors pass through for the caller to print generically.
//
// cfg is the parsed aetherfy.yaml, and is what the AGENT_NOT_FOUND branch
// describes a would-be agent with; pass nil to disable creation entirely.
func handleDeployResult(client *api.Client, agentID string, cfg *archive.AetherfyConfig, tarballData []byte, resp *api.DeployResponse, err error) (*api.DeployResponse, error) {
	if err == nil {
		return resp, nil
	}

	apiErr, ok := err.(*api.APIError)
	if !ok {
		return nil, err // transport error — caller prints it
	}

	switch apiErr.Code {
	case "AGENT_NOT_FOUND":
		outcome := decideCreateOnDeploy(createOnDeployInput{
			AgentName:   agentID,
			Config:      cfg,
			CreateFlag:  deployCreate,
			YesFlag:     deployYes,
			Interactive: isInteractive(),
			Confirm:     promptYesNo,
		})
		if !outcome.Create {
			if outcome.Warn != "" {
				output.PrintWarning("%s", outcome.Warn)
			}
			// Declined, or never offered → today's actionable error, unchanged.
			return nil, err
		}
		if outcome.Announce != "" {
			output.PrintInfo("%s", outcome.Announce)
		}

		// The same POST /agents `afy agents create` issues, with the type and
		// runtime that were just printed — no other create path exists.
		sp := output.NewSpinner(fmt.Sprintf("Creating agent '%s'...", agentID))
		sp.Start()
		agent, createErr := createAgentRecord(client, agentID, "", outcome.AgentType, outcome.Runtime, false)
		sp.Stop()
		if createErr != nil {
			output.PrintError("Failed to create agent '%s': %v", agentID, createErr)
			return nil, createErr
		}
		output.PrintSuccess("Agent '%s' created (%s/%s).", agent.Name, outcome.AgentType, outcome.Runtime)

		// Continue the deploy with the archive already in hand.
		sp = output.NewSpinner("Uploading and deploying...")
		sp.Start()
		r, e := client.Deploy(agentID, tarballData, false)
		sp.Stop()
		// cfg=nil: the retry keeps the overage/freeze handling but can never
		// offer to create again, so a repeat AGENT_NOT_FOUND fails instead of
		// looping.
		return handleDeployResult(client, agentID, nil, tarballData, r, e)

	case "OVERAGE_CONFIRM_REQUIRED":
		// The additional monthly $ is usually present; when the control-plane
		// omits it, fall back to a grammatical phrase rather than "adds more of
		// usage".
		impact := "would add usage beyond your plan's included allowance"
		if apiErr.AdditionalMonthlyUSD != nil {
			impact = fmt.Sprintf("adds ~$%.2f/mo of usage", *apiErr.AdditionalMonthlyUSD)
		}
		if !deployYes {
			if !isInteractive() {
				output.PrintError("This deployment %s. Re-run with --yes to proceed.", impact)
				os.Exit(1)
			}
			if !promptYesNo(fmt.Sprintf("This deployment %s — continue?", impact)) {
				output.PrintInfo("Deployment cancelled.")
				os.Exit(0)
			}
		}
		// Confirmed (or --yes) → resend accepting the overage.
		sp := output.NewSpinner("Deploying...")
		sp.Start()
		r, e := client.Deploy(agentID, tarballData, true)
		sp.Stop()
		return r, e

	case "SOFT_CAP_EXCEEDED", "DUNNING_FROZEN":
		// Freeze/pause: show the control-plane's actionable message cleanly,
		// without the "[403] … (CODE)" wrapper.
		output.PrintError("%s", apiErr.Message)
		os.Exit(1)
	}

	return nil, err
}

func runDeploy(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	// Handle --from-github flag (Level 1: public repo deploy)
	if deployFromGitHub != "" {
		return runDeployFromGitHub(deployFromGitHub)
	}

	// Determine path
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		output.PrintError("Invalid path: %v", err)
		os.Exit(1)
	}

	// Check path exists
	info, err := os.Stat(absPath)
	if err != nil {
		output.PrintError("Path not found: %s", absPath)
		os.Exit(1)
	}
	if !info.IsDir() {
		output.PrintError("Path must be a directory: %s", absPath)
		os.Exit(1)
	}

	output.PrintInfo("Deploying from: %s", absPath)
	output.Println("")

	// Validate aetherfy.yaml exists
	if err := archive.ValidateAetherfyConfig(absPath); err != nil {
		output.PrintError("%v", err)
		output.Println("")
		output.Println("Create an aetherfy.yaml file with your agent configuration.")
		output.Println("Example:")
		output.Println("")
		output.Dim.Println("  name: my-agent")
		output.Dim.Println("  runtime: python3.11")
		output.Dim.Println("  entrypoint: main.py")
		os.Exit(1)
	}

	// Parsed once: the agent target falls back to 'name', and a deploy that hits
	// AGENT_NOT_FOUND needs 'type'/'runtime' to describe what creating it would
	// make. ValidateAetherfyConfig above already proved this parses.
	cfg, cfgErr := archive.ParseAetherfyConfig(absPath)

	// Use --agent flag or fall back to 'name' in aetherfy.yaml
	agentID := deployAgent
	if agentID == "" {
		if cfgErr != nil || cfg.Name == "" {
			output.PrintError("Agent name not found. Use --agent flag or set 'name' in aetherfy.yaml")
			os.Exit(1)
		}
		agentID = cfg.Name
	}

	// Load ignore patterns
	ignorePatterns, err := archive.LoadIgnorePatterns(absPath)
	if err != nil {
		output.PrintWarning("Failed to load .afyignore: %v", err)
	}

	// Create tarball archive (.tar.gz)
	sp := output.NewSpinner("Creating archive...")
	sp.Start()

	tarballData, err := archive.CreateTarball(absPath, ignorePatterns)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to create archive: %v", err)
		os.Exit(1)
	}

	sizeMB := float64(len(tarballData)) / 1024 / 1024
	output.PrintSuccess("Archive created (%.2f MB)", sizeMB)

	// Upload and deploy
	sp = output.NewSpinner("Uploading and deploying...")
	sp.Start()

	client := api.NewClient()
	resp, err := client.Deploy(agentID, tarballData, false)
	sp.Stop()
	// Handle the D2 Part 6 overage confirm + freeze/pause 403s + the consented
	// create-on-deploy (may prompt, which is why the spinner is stopped first).
	resp, err = handleDeployResult(client, agentID, cfg, tarballData, resp, err)

	if err != nil {
		output.PrintError("Deployment failed: %v", err)
		os.Exit(1)
	}

	output.KeyValue("Deployment ID", resp.DeploymentID)
	output.Println("")

	if deployDetach {
		output.PrintSuccess("Deployment queued.")
		output.Println("Run 'afy logs " + agentID + "' to follow progress.")
	} else {
		watchDeployment(client, agentID, resp.DeploymentID)
	}

	return nil
}

// runDeployFromGitHub clones a public GitHub repo and feeds it into the standard deploy pipeline.
// repoRef has the form "owner/repo" or "owner/repo@ref".
func runDeployFromGitHub(repoRef string) error {
	// Validate format
	if !githubRepoRefPattern.MatchString(repoRef) {
		output.PrintError("Invalid --from-github format. Expected: owner/repo or owner/repo@ref")
		output.Println("Examples:")
		output.Println("  afy deploy --from-github psf/requests@v2.31.0")
		output.Println("  afy deploy --from-github myorg/my-agent")
		os.Exit(1)
	}

	// Split repo and ref
	repo := repoRef
	ref := "main"
	if idx := strings.Index(repoRef, "@"); idx != -1 {
		repo = repoRef[:idx]
		ref = repoRef[idx+1:]
	}

	cloneURL := "https://github.com/" + repo + ".git"

	// Create a temp dir for the clone
	tmpDir, err := os.MkdirTemp("", "afy-github-*")
	if err != nil {
		output.PrintError("Failed to create temp directory: %v", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// Clone the repo
	sp := output.NewSpinner(fmt.Sprintf("Cloning %s@%s...", repo, ref))
	sp.Start()

	// Determine whether ref looks like a full SHA (40 hex chars)
	isSHA := len(ref) == 40 && isHexString(ref)

	var cloneErr error
	if isSHA {
		// Full clone then checkout for exact SHA
		cloneErr = runGitCommand("clone", cloneURL, tmpDir)
		if cloneErr == nil {
			cloneErr = runGitCommandIn(tmpDir, "checkout", ref)
		}
	} else {
		// Shallow clone for branch/tag
		cloneErr = runGitCommand("clone", "--depth=1", "--branch="+ref, cloneURL, tmpDir)
	}

	sp.Stop()

	if cloneErr != nil {
		output.PrintError("Failed to clone repository: %v", cloneErr)
		output.Println("")
		output.Println("Make sure the repository is public and the ref exists.")
		os.Exit(1)
	}

	output.PrintSuccess("Repository cloned")

	// Validate aetherfy.yaml exists in the clone
	if err := archive.ValidateAetherfyConfig(tmpDir); err != nil {
		output.PrintError("%v", err)
		output.Println("")
		output.Println("The repository must contain an aetherfy.yaml file.")
		os.Exit(1)
	}

	// Parsed once — same two readers as the local deploy: the agent target, and
	// the type/runtime a consented create would use.
	cfg, cfgErr := archive.ParseAetherfyConfig(tmpDir)

	// Determine agent ID
	agentID := deployAgent
	if agentID == "" {
		if cfgErr != nil || cfg.Name == "" {
			output.PrintError("Agent name not found. Use --agent flag or set 'name' in aetherfy.yaml")
			os.Exit(1)
		}
		agentID = cfg.Name
	}

	// Load ignore patterns and create tarball
	ignorePatterns, _ := archive.LoadIgnorePatterns(tmpDir)

	sp = output.NewSpinner("Creating archive...")
	sp.Start()
	tarballData, err := archive.CreateTarball(tmpDir, ignorePatterns)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to create archive: %v", err)
		os.Exit(1)
	}

	sizeMB := float64(len(tarballData)) / 1024 / 1024
	output.PrintSuccess("Archive created (%.2f MB)", sizeMB)

	// Upload and deploy
	sp = output.NewSpinner("Uploading and deploying...")
	sp.Start()

	client := api.NewClient()
	resp, err := client.Deploy(agentID, tarballData, false)
	sp.Stop()
	// Handle the D2 Part 6 overage confirm + freeze/pause 403s + the consented
	// create-on-deploy (may prompt, which is why the spinner is stopped first).
	resp, err = handleDeployResult(client, agentID, cfg, tarballData, resp, err)

	if err != nil {
		output.PrintError("Deployment failed: %v", err)
		os.Exit(1)
	}

	output.KeyValue("Deployment ID", resp.DeploymentID)
	output.Println("")

	if deployDetach {
		output.PrintSuccess("Deployment queued.")
		output.Println("Run 'afy logs " + agentID + "' to follow progress.")
	} else {
		watchDeployment(client, agentID, resp.DeploymentID)
	}

	return nil
}

// runGitCommand runs a git subcommand with the given args.
func runGitCommand(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGitCommandIn runs a git subcommand inside the given directory.
func runGitCommandIn(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// isHexString returns true if every character in s is a hex digit.
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func watchDeployment(client *api.Client, agentID, deploymentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastStatus := ""

	for {
		select {
		case <-ctx.Done():
			output.PrintWarning("Deployment watch timed out")
			return
		case <-ticker.C:
			deployment, err := client.GetDeployment(deploymentID)
			if err != nil {
				output.PrintWarning("Failed to get deployment status: %v", err)
				continue
			}

			if deployment.Status != lastStatus {
				output.Printf("Status: %s\n", formatStatus(deployment.Status))
				lastStatus = deployment.Status
			}

			switch deployment.Status {
			case "completed", "running", "active":
				// A partial multi-region deploy is ACTIVE + serving but not yet
				// in every region (control-plane REVIEW_FAQ §63). End the watch
				// honestly rather than claiming a clean success.
				if deployment.IsDegraded {
					output.PrintWarning("Deployment is serving but DEGRADED — %d/%d regions ready.",
						deployment.RegionsReady, deployment.RegionsTotal)
					output.Println("Remaining regions converge in the background; check 'afy agents status'.")
				} else {
					output.PrintSuccess("Deployment completed!")
				}
				return
			case "failed", "error":
				output.PrintError("Deployment failed")
				if deployment.ErrorMessage != "" {
					output.Printf("Reason: %s\n", deployment.ErrorMessage)
				}
				// Try to get logs
				logs, err := client.GetDeploymentLogs(agentID, 20)
				if err == nil && len(logs) > 0 {
					output.Println("")
					output.Println("Recent logs:")
					for _, log := range logs {
						fmt.Printf("  %s\n", log.Message)
					}
				}
				output.Println("")
				output.Println("Run 'afy deploy' to try again.")
				return
			}
		}
	}
}
