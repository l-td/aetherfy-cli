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
  connect              Open the GitHub OAuth URL in your browser
  disconnect           Revoke the stored GitHub token
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
	Short: "Connect your GitHub account via OAuth",
	Long: `Open the GitHub OAuth authorization page to connect your account.

The CLI will attempt to open the URL in your default browser.
If that fails, copy and paste the URL manually.

After authorizing, GitHub redirects back to Aetherfy and stores
an encrypted token — one token per account, covering all repos.`,
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
	Short: "Revoke stored GitHub token",
	Long: `Remove the stored GitHub OAuth token from your account.

This does not delete existing webhook links on agents — use
'afy github unlink <agent>' first if you want to clean those up.

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
		if status.Scope != "" {
			output.KeyValue("Scopes", status.Scope)
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
	githubLinkBranch string
)

var githubLinkCmd = &cobra.Command{
	Use:   "link <agent> <repo>",
	Short: "Link an agent to a GitHub repository for auto-deploy",
	Long: `Link an agent to a GitHub repository.

A push webhook is registered on the repository. Every push to the
configured branch (default: main) triggers a new deployment.

Requires your GitHub account to be connected ('afy github connect').

<repo> format: owner/repo  (branch defaults to main)
Use --branch or append @branch to override the branch.`,
	Example: `  afy github link my-bot myorg/my-agent
  afy github link my-bot myorg/my-agent --branch develop
  afy github link my-bot myorg/my-agent@develop`,
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
		resp, err := client.GitHubLinkAgent(agentID, repo, branch)
		sp.Stop()

		if err != nil {
			output.PrintError("Failed to link agent: %v", err)
			if apiErr, ok := err.(*api.APIError); ok && apiErr.StatusCode == 422 {
				output.Println("")
				output.Println("Make sure your GitHub account is connected first:")
				output.Println("  afy github connect")
			}
			os.Exit(1)
		}

		output.PrintSuccess("Agent linked to GitHub")
		output.KeyValue("Repo", resp.Repo)
		output.KeyValue("Branch", resp.Branch)
		output.KeyValue("Webhook ID", resp.WebhookID)
		output.Println("")
		output.Println("Pushes to " + resp.Branch + " will now trigger automatic deployments.")
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
