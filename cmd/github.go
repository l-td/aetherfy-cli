package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Root github command
// ---------------------------------------------------------------------------

var githubCmd = &cobra.Command{
	Use:   "github",
	Short: "Manage GitHub integration",
	Long: `Connect your GitHub account and link agents to repositories.

Once connected, you can link agents to GitHub repos so that every push
to the configured branch automatically triggers a new deployment.

Subcommands:
  connect              Install the Aetherfy GitHub App in your browser
  disconnect           Remove the GitHub App installation
  status               Show GitHub connection status
  link <agent> <repo>  Link an agent to a GitHub repo for auto-deploy
  unlink <agent>       Remove the GitHub link from an agent`,
	Example: `  afy github connect
  afy github status
  afy github link my-bot myorg/my-agent
  afy github link my-bot myorg/my-agent@develop
  afy github unlink my-bot
  afy github disconnect`,
}

// ---------------------------------------------------------------------------
// github connect
// ---------------------------------------------------------------------------

var githubConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect your GitHub account via the Aetherfy GitHub App",
	Long: `Install the Aetherfy GitHub App to connect your account.

The CLI will attempt to open the installation URL in your default browser.
If that fails, copy and paste the URL manually.

After installing the App on GitHub, you are redirected back to Aetherfy.
One App installation covers all repos you grant access to.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}

		client := api.NewClient()
		url := client.GitHubConnectURL()

		output.Println("Open this URL in your browser to connect GitHub:")
		output.Println("")
		output.Bold.Println("  " + url)
		output.Println("")

		// Best-effort browser open
		if err := openBrowser(url); err == nil {
			output.PrintInfo("Opening browser...")
		} else {
			output.PrintInfo("Copy and paste the URL above into your browser.")
		}

		return nil
	},
}

// ---------------------------------------------------------------------------
// github disconnect
// ---------------------------------------------------------------------------

var githubDisconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Disconnect the Aetherfy GitHub App",
	Long: `Remove the stored GitHub App installation from your account.

This does not delete existing webhook links on agents — use
'afy github unlink <agent>' first if you want to clean those up.

To fully revoke access, also uninstall the App from your GitHub settings.
This operation is idempotent: it succeeds even if you are not connected.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}

		client := api.NewClient()
		if err := client.GitHubDisconnect(); err != nil {
			output.PrintError("Failed to disconnect GitHub: %v", err)
			os.Exit(1)
		}

		output.PrintSuccess("GitHub disconnected.")
		return nil
	},
}

// ---------------------------------------------------------------------------
// github status
// ---------------------------------------------------------------------------

var githubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show GitHub connection status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}

		client := api.NewClient()
		status, err := client.GitHubStatus()
		if err != nil {
			output.PrintError("Failed to get GitHub status: %v", err)
			os.Exit(1)
		}

		if !status.Connected {
			output.PrintWarning("GitHub not connected.")
			output.Println("")
			output.Println("Run 'afy github connect' to link your GitHub account.")
			return nil
		}

		output.PrintSuccess("GitHub connected")
		if status.InstallationID != nil {
			output.KeyValue("Installation ID", fmt.Sprintf("%d", *status.InstallationID))
		}
		if status.ConnectedAt != nil {
			output.KeyValue("Connected at", status.ConnectedAt.Local().Format(time.RFC1123))
		}
		return nil
	},
}

// ---------------------------------------------------------------------------
// github link
// ---------------------------------------------------------------------------

var (
	githubLinkBranch  string
	githubLinkRootDir string
)

var githubLinkCmd = &cobra.Command{
	Use:   "link <agent> <repo>",
	Short: "Link an agent to a GitHub repository for auto-deploy",
	Long: `Link an agent to a GitHub repository.

A push webhook is registered on the repository. Every push to the
configured branch (default: main) triggers a new deployment.

Requires your GitHub account to be connected ('afy github connect').

<repo> format: owner/repo  (branch defaults to main)
Use --branch or append @branch to override the branch.

If several agents live in one repository, point each at its own folder
with --root-dir. That folder holds the agent's aetherfy.yaml, and only
that folder is uploaded when you push — sibling folders never enter the
build. Omit it and the whole repository is the build context.

Re-running 'afy github link' on an already-linked agent is supported
and is the recovery path for two situations:
  * You need a fresh webhook secret (the response prints it once and
    there is no separate fetch endpoint — re-link to rotate it).
  * A previous link landed in an inconsistent state and you want to
    re-register the webhook from scratch.

Re-linking creates the new webhook on GitHub first, commits, then
removes the old one — your existing auto-deploy keeps working until
the new link is live.`,
	Example: `  afy github link my-bot myorg/my-agent
  afy github link my-bot myorg/my-agent --branch develop
  afy github link my-bot myorg/my-agent@develop
  afy github link my-bot myorg/monorepo --root-dir agents/my-bot`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}

		agentID := args[0]
		repoArg := args[1]

		// Parse optional @branch embedded in repo arg
		repo := repoArg
		branch := githubLinkBranch
		if idx := strings.Index(repoArg, "@"); idx != -1 {
			repo = repoArg[:idx]
			if branch == "" {
				branch = repoArg[idx+1:]
			}
		}
		if branch == "" {
			branch = "main"
		}

		// Validate repo format
		if !isValidGitHubRepo(repo) {
			output.PrintError("Invalid repo format. Expected: owner/repo")
			os.Exit(1)
		}

		client := api.NewClient()
		sp := output.NewSpinner(fmt.Sprintf("Linking %s to %s@%s...", agentID, repo, branch))
		sp.Start()
		resp, err := client.GitHubLinkAgent(agentID, repo, branch, githubLinkRootDir)
		sp.Stop()

		if err != nil {
			output.PrintError("Failed to link agent: %v", err)
			// 422 has two causes now: GitHub not connected, and a rejected
			// --root-dir. Gate the connect hint on the code, or a user who
			// typed a bad path gets told to reconnect an account that is
			// already fine.
			if apiErr, ok := err.(*api.APIError); ok && apiErr.StatusCode == 422 {
				if apiErr.Code == "GITHUB_NOT_CONNECTED" {
					output.Println("")
					output.Println("Make sure your GitHub account is connected first:")
					output.Println("  afy github connect")
				}
			}
			os.Exit(1)
		}

		output.PrintSuccess("Agent linked to GitHub")
		output.KeyValue("Repo", resp.Repo)
		output.KeyValue("Branch", resp.Branch)
		if resp.RootDir != "" {
			output.KeyValue("Directory", resp.RootDir)
		}
		output.KeyValue("Webhook ID", resp.WebhookID)
		// Shown ONCE — the server never returns it again, and re-linking is
		// the only way to get a new one.
		if resp.WebhookSecret != "" {
			output.KeyValue("Webhook secret", resp.WebhookSecret)
			output.Println("")
			output.Println("Copy the webhook secret now — it is not retrievable later.")
		}
		output.Println("")
		if resp.RootDir != "" {
			output.Println("Pushes to " + resp.Branch + " that touch " + resp.RootDir + " will now trigger automatic deployments.")
		} else {
			output.Println("Pushes to " + resp.Branch + " will now trigger automatic deployments.")
		}
		return nil
	},
}

// ---------------------------------------------------------------------------
// github unlink
// ---------------------------------------------------------------------------

var githubUnlinkCmd = &cobra.Command{
	Use:   "unlink <agent>",
	Short: "Remove the GitHub link from an agent",
	Long: `Remove the GitHub webhook link from an agent.

The webhook registered on GitHub is deleted (best-effort).
This operation is idempotent — it succeeds even if the agent
is not currently linked.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}

		agentID := args[0]
		client := api.NewClient()

		sp := output.NewSpinner(fmt.Sprintf("Unlinking %s...", agentID))
		sp.Start()
		err := client.GitHubUnlinkAgent(agentID)
		sp.Stop()

		if err != nil {
			output.PrintError("Failed to unlink agent: %v", err)
			os.Exit(1)
		}

		output.PrintSuccess("Agent unlinked from GitHub.")
		return nil
	},
}

// ---------------------------------------------------------------------------
// Registration and helpers
// ---------------------------------------------------------------------------

func init() {
	githubLinkCmd.Flags().StringVarP(&githubLinkBranch, "branch", "b", "", "Branch to watch (default: main, or embedded @branch in repo arg)")
	githubLinkCmd.Flags().StringVar(&githubLinkRootDir, "root-dir", "", "Repo-relative folder holding this agent's code and aetherfy.yaml (default: the repository root)")

	githubCmd.AddCommand(githubConnectCmd)
	githubCmd.AddCommand(githubDisconnectCmd)
	githubCmd.AddCommand(githubStatusCmd)
	githubCmd.AddCommand(githubLinkCmd)
	githubCmd.AddCommand(githubUnlinkCmd)
}

// openBrowser tries to open url in the user's default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// isValidGitHubRepo returns true if s has the form "owner/repo".
func isValidGitHubRepo(s string) bool {
	parts := strings.SplitN(s, "/", 2)
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}
