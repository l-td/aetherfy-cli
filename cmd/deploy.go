package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/archive"
	"github.com/l-td/aetherfy-cli/internal/link"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

// githubRepoRefPattern validates owner/repo or owner/repo@ref
var githubRepoRefPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(@\S+)?$`)

var deployCmd = &cobra.Command{
	Use:   "deploy [path]",
	Short: "Deploy an agent",
	Long: `Deploy code to the agent this folder is linked to.

A folder is linked to its agent by .aetherfy/agent.json, which 'afy init'
writes (for a new agent) or 'afy init --agent <name>' writes (for an existing
one). Commit .aetherfy/ so every clone and CI job deploys to the same agent.
A deploy NEVER creates an agent: a folder with no link, no such target, or a
link to an agent this account does not have is refused, naming the command
that links it.

One codebase can deploy to several agents through targets: --target picks
one (default: "default"); 'afy init --agent <name> --target <t>' adds one.

The path should contain an aetherfy.yaml; if no path is given, the current
directory is used. The deployment process:
  1. Validates aetherfy.yaml exists
  2. Creates an archive of your code (files matching .afyignore are left out)
  3. Uploads to Aetherfy
  4. Builds an image
  5. Deploys it and prints the agent's URL

By default the command waits for the deployment to complete and streams
status updates. Use --detach to return immediately after upload.

--from-github deploys the code of a public GitHub repository, without a local
clone, to the agent THIS folder is linked to.
`,
	Example: `  # Deploy this folder to its linked agent (waits for completion)
  afy deploy

  # Deploy to the folder's "staging" target
  afy deploy --target staging

  # Deploy a specific directory, and return immediately
  afy deploy ./my-agent --detach

  # Deploy a public GitHub repo's code to this folder's agent
  afy deploy --from-github owner/repo@v1.2.3`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDeploy,
}

var (
	deployDetach     bool
	deployTarget     string
	deployFromGitHub string
	deployYes        bool
)

func init() {
	deployCmd.Flags().BoolVarP(&deployDetach, "detach", "d", false, "Return immediately after upload without waiting for completion")
	deployCmd.Flags().StringVar(&deployTarget, "target", link.DefaultTarget, "Which of the folder's linked agents to deploy to")
	deployCmd.Flags().StringVar(&deployFromGitHub, "from-github", "", "Deploy from a public GitHub repo: owner/repo[@ref]")
	deployCmd.Flags().BoolVarP(&deployYes, "yes", "y", false, "Skip the overage confirmation prompt and proceed (non-interactive)")
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

// isUnknownFields reports a local aetherfy.yaml refused for naming a key the
// server does not accept -- a file that exists, so the "create one" help that
// follows other validation failures does not apply.
func isUnknownFields(err error) bool {
	var uf *archive.UnknownFieldsError
	return errors.As(err, &uf)
}

// handleDeployResult post-processes the first deploy attempt for the D2 Part 6
// overage cost gate and the freeze/pause 403s. A deploy never creates an agent. The caller does the first Deploy(confirm=false) with its
// spinner and stops it BEFORE calling this (so a prompt isn't drawn over the
// spinner). On OVERAGE_CONFIRM_REQUIRED it shows "this adds ~$X/mo — continue?"
// and, on confirm (or --yes), re-sends with confirm_overage=true (its own brief
// spinner). Non-special errors pass through for the caller to print generically.
func handleDeployResult(client *api.Client, agentID string, tarballData []byte, resp *api.DeployResponse, err error) (*api.DeployResponse, error) {
	if err == nil {
		return resp, nil
	}

	apiErr, ok := err.(*api.APIError)
	if !ok {
		return nil, err // transport error — caller prints it
	}

	switch apiErr.Code {
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

	// The link lives in the folder being deployed; --from-github takes only
	// the CODE from GitHub, and deploys it to the agent this folder links.
	path := "."
	if len(args) > 0 {
		path = args[0]
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		output.PrintError("Invalid path: %v", err)
		os.Exit(1)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		output.PrintError("Path not found: %s", absPath)
		os.Exit(1)
	}
	if !info.IsDir() {
		output.PrintError("Path must be a directory: %s", absPath)
		os.Exit(1)
	}

	client := api.NewClient()
	agent, err := resolveLinkedAgent(client, absPath, deployTarget)
	if err != nil {
		output.PrintError("%v", err)
		os.Exit(1)
	}
	warnUnlessLinkCommitted(absPath)

	if deployFromGitHub != "" {
		return runDeployFromGitHub(client, agent, deployFromGitHub)
	}

	output.PrintInfo("Deploying from: %s", absPath)
	output.Println("")
	if err := archive.ValidateAetherfyConfig(absPath); err != nil {
		output.PrintError("%v", err)
		if isUnknownFields(err) {
			// The file exists and names a key the server would refuse: the
			// sentence above says which.
			os.Exit(1)
		}
		output.Println("")
		output.Println("Add an aetherfy.yaml beside your code, with at least a runtime:")
		output.Println("")
		output.Dim.Println("  runtime: python3.11")
		output.Dim.Println("  entrypoint: main.py")
		output.Println("")
		output.Println("'afy init' scans the folder and writes one.")
		os.Exit(1)
	}
	return deployDir(client, agent, absPath)
}

// resolveLinkedAgent is the agent target names in dir's link file, read back
// from the control plane by its id. Every refusal names the command that
// fixes it; none creates anything.
func resolveLinkedAgent(client *api.Client, dir, target string) (*api.Agent, error) {
	fix := "afy init --agent <name>"
	if target != link.DefaultTarget {
		fix += " --target " + target
	}
	f, err := link.Read(dir)
	if errors.Is(err, link.ErrNotLinked) {
		return nil, fmt.Errorf("this folder is not linked to an agent (no %s). Link it to an existing agent "+
			"with '%s', or create one with 'afy init'. A deploy never creates an agent", link.RelPath(), fix)
	}
	if err != nil {
		return nil, err
	}
	id, ok := f.Targets[target]
	if !ok {
		return nil, fmt.Errorf("this folder has no target '%s' (its targets: %s). Link one with '%s'",
			target, strings.Join(f.Names(), ", "), fix)
	}
	agent, err := client.GetAgent(id)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			return nil, fmt.Errorf("target '%s' links agent %s, which this account does not have (deleted, or "+
				"another account's). Relink with '%s'", target, id, fix)
		}
		return nil, err
	}
	return agent, nil
}

// warnUnlessLinkCommitted warns when git will not carry the link to the other
// clones and CI jobs that deploy this folder. Outside git it says nothing.
func warnUnlessLinkCommitted(dir string) {
	switch link.Git(dir) {
	case link.Untracked:
		output.PrintWarning("%s is not committed. Commit it so every clone and CI job deploys to the same agent.", link.RelPath())
	case link.Ignored:
		output.PrintWarning("%s is ignored by git. Unignore and commit it so every clone and CI job deploys to the same agent.", link.RelPath())
	}
}

// deployDir archives dir and deploys it to agent.
func deployDir(client *api.Client, agent *api.Agent, dir string) error {
	ignorePatterns, err := archive.LoadIgnorePatterns(dir)
	if err != nil {
		output.PrintWarning("Failed to load .afyignore: %v", err)
	}

	sp := output.NewSpinner("Creating archive...")
	sp.Start()
	tarballData, err := archive.CreateTarball(dir, ignorePatterns)
	sp.Stop()
	if err != nil {
		output.PrintError("Failed to create archive: %v", err)
		os.Exit(1)
	}
	output.PrintSuccess("Archive created (%.2f MB)", float64(len(tarballData))/1024/1024)

	output.PrintInfo("Deploying to %s...", agent.Name)
	sp = output.NewSpinner("Uploading and deploying...")
	sp.Start()
	resp, err := client.Deploy(agent.ID, tarballData, false)
	sp.Stop()
	// The D2 Part 6 overage confirm and the freeze/pause 403s (may prompt,
	// which is why the spinner is stopped first).
	resp, err = handleDeployResult(client, agent.ID, tarballData, resp, err)
	if err != nil {
		_ = printRefusal("Deployment failed", err)
		os.Exit(1)
	}

	output.KeyValue("Deployment ID", resp.DeploymentID)
	output.Println("")
	if deployDetach {
		output.PrintSuccess("Deployment queued.")
		output.Println("Run 'afy logs " + agent.Name + "' to follow progress.")
		return nil
	}
	if err := watchDeployment(client, agent.ID, resp.DeploymentID, deploymentPollInterval, deploymentWatchTimeout); err != nil {
		os.Exit(1)
	}
	return nil
}

// printAgentURL prints where to send requests, after a deploy that succeeded.
//
// THE COMMAND USED TO END WITHOUT SAYING THIS. It printed a status and a
// "Run 'afy logs …'" line and stopped, leaving the one question a successful
// deploy creates — where do I send the request? — answered nowhere in the CLI.
//
// Re-fetches the agent rather than threading one down: the URL is minted
// SERVER-SIDE on the first successful deploy, so on the very deploy that
// creates it, any copy this process is already holding predates it.
//
// Prints only when the SERVER says there is an address. No agent_type check
// here any more: the server already returns an empty URL for a scheduled task
// (its image is not a web server) and for every agent until the edge is live,
// so re-deciding it here would be a second rule that can disagree with the
// first.
//
// Fail-soft and silent. A deploy that worked must not print a warning because a
// follow-up read did not; its result is already on screen above.
func printAgentURL(client *api.Client, agentID string) {
	agent, err := client.GetAgent(agentID)
	if err != nil || agent == nil || agent.URL == "" {
		return
	}
	output.Println("")
	// NOT output.KeyValue: its 15-wide label column exists to align a BLOCK of
	// keys, and there is no block here. This runs at the tail of the deploy
	// watch, whose neighbours are "Status: x" lines and a ✓ — none of them in
	// that column. Padded to 15 the address landed eight columns right of the
	// curl line directly beneath it, which reads as a typo. The 6 is the width
	// that puts both addresses in the same column as the one after "  curl ".
	output.Bold.Printf("%-6s", "URL:")
	output.Printf(" %s\n", agent.URL)
	output.Dim.Printf("  curl %s\n", agent.URL)
}

// printBuildFailureDetail prints the build output a failed deployment failed on.
//
// SILENT WHEN THERE IS NOTHING, and that is not the same as printing an empty
// section. A deployment that failed AFTER the build — a region that never came
// up, a cancellation — carries no build output, and a "Build output:" heading
// over nothing would read as "the build printed nothing", which is a different
// and untrue claim.
//
// Indented, not boxed. The server already capped and redacted it; the CLI's job
// is to make it obvious where the quoted output starts and stops.
func printBuildFailureDetail(detail string) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return
	}
	output.Println("")
	output.Println("Build output:")
	for _, line := range strings.Split(detail, "\n") {
		output.Dim.Printf("  %s\n", line)
	}
}

// runDeployFromGitHub clones a public GitHub repo and feeds it into the standard deploy pipeline.
// repoRef has the form "owner/repo" or "owner/repo@ref".
func runDeployFromGitHub(client *api.Client, agent *api.Agent, repoRef string) error {
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
		if !isUnknownFields(err) {
			output.Println("")
			output.Println("The repository must contain an aetherfy.yaml file.")
		}
		os.Exit(1)
	}
	return deployDir(client, agent, tmpDir)
}

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

// THE CONTROL PLANE'S STAGE BUDGETS, mirrored here and pinned to the constants
// they mirror by TestTheDeployWatchCoversTheControlPlanesStageBudgets (live
// against the sibling checkout; test/cpbudgets). A release stays `deploying`
// until its health gate has seen it serve, so a wait that ends sooner reports
// "timed out" for a deploy that then succeeds.
const (
	// aetherfy-control-plane orchestrator/fly_builder.py BUILD_TIMEOUT_SECONDS
	cpBuildTimeout = 300 * time.Second
	// aetherfy-control-plane workers/deploy_worker.py DEPLOY_HEALTH_GATE_TIMEOUT_SECONDS
	cpHealthGateTimeout = 300 * time.Second
	// What the control plane does not bound with one number: the wait in the
	// build and deploy queues, and creating and booting the machines.
	deploymentWatchSlack = 5 * time.Minute
)

// How often a command waiting on a deployment re-reads it, and how long it
// waits in all before giving up: every stage the control plane may spend, and
// the part it does not bound.
const (
	deploymentPollInterval = 5 * time.Second
	deploymentWatchTimeout = cpBuildTimeout + cpHealthGateTimeout + deploymentWatchSlack
)

var (
	errDeploymentFailed        = errors.New("deployment failed")
	errDeploymentWatchTimedOut = errors.New("deployment did not finish before the wait ran out")
	errDeploymentReplaced      = errors.New("a newer deployment went live instead")
)

// watchDeployment follows a deployment until it settles, and returns nil only
// when it went live.
//
// A COMMAND THAT WAITS IS A COMMAND A SCRIPT TRUSTS WITH ITS EXIT STATUS. On
// 2026-09-14 a failed `afy deploy`, `afy redeploy` and `afy rollback` each
// printed "Deployment failed" and exited 0, so a pipeline read them as green and
// the fallback a script had written for exactly that case never ran. So a
// failure is an error, and its reason goes to stderr beside the "Error:" line.
// A wait that runs out is an error too: nothing was confirmed.
func watchDeployment(client *api.Client, agentID, deploymentID string, pollInterval, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	lastStatus := ""

	for {
		select {
		case <-ctx.Done():
			output.PrintError("Deployment watch timed out; the deployment has not finished. Check it with 'afy deployments %s'.", agentID)
			return errDeploymentWatchTimedOut
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
					output.Println("Remaining regions converge in the background; check 'afy status'.")
				} else {
					output.PrintSuccess("Deployment completed!")
				}
				printAgentURL(client, agentID)
				return nil
			case "superseded", "rolled_back":
				// TERMINAL, NOT IN PROGRESS. A newer release of this agent went
				// live first -- a second push while this one was still being
				// checked, or a rollback -- so this version is not the one
				// serving. Before this the watch kept polling a state that never
				// changes until it timed out. Non-zero: a script that deployed
				// this version must not read it as live.
				output.PrintError("Deployment v%d was replaced before it went live: a newer deployment of this agent is serving instead.", deployment.Version)
				output.Println(fmt.Sprintf("Check which version is live with 'afy deployments %s'.", agentID))
				return errDeploymentReplaced
			case "failed", "error":
				output.PrintError("Deployment failed")
				if deployment.ErrorMessage != "" {
					fmt.Fprintf(os.Stderr, "Reason: %s\n", deployment.ErrorMessage)
				}
				// THE STEP THAT FAILED, when the server sent it. "Reason" above
				// is a mapped sentence — the same one for a pip resolution
				// failure and for a step that ran out of memory — and this
				// command is where someone is standing when they first need to
				// know which they had. Printed BEFORE the runtime logs below,
				// which belong to the previous version that is still running and
				// have nothing to do with why this build failed.
				printBuildFailureDetail(deployment.BuildFailureDetail)
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
				return errDeploymentFailed
			}
		}
	}
}
