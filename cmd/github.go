package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
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
  connect              Authorize Aetherfy on GitHub in your browser
  disconnect [account] Disconnect one GitHub account, or all of them
  status               Show which GitHub accounts are connected
  link <agent> <repo>  Link an agent to a GitHub repo for auto-deploy
  unlink <agent>       Remove the GitHub link from an agent

Several agents can share one repository — give each its own folder with
'link --root-dir'.`,
	Example: `  afy github connect
  afy github status
  afy github link my-bot myorg/my-agent
  afy github link my-bot myorg/my-agent@develop
  afy github link my-bot myorg/monorepo --root-dir agents/my-bot
  afy github unlink my-bot
  afy github disconnect acme-corp
  afy github disconnect`,
}

// ---------------------------------------------------------------------------
// github connect
// ---------------------------------------------------------------------------

var githubConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect your GitHub account via the Aetherfy GitHub App",
	Long: `Authorize Aetherfy on GitHub to connect your account.

The CLI will attempt to open the authorization URL in your default browser.
If that fails, copy and paste the URL manually.

Authorizing attaches EVERY account the Aetherfy App is installed on that you
can reach: your own, and any organization you are an admin of. If it is
installed nowhere yet, GitHub will ask where to install it.

Running this again when you are already connected is how you add another
account — it never replaces the ones you have.

After finishing on GitHub, the command waits for the connection and reports
it. The link is valid for a limited time; if it expires before anything is
recorded, run the command again for a fresh one.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		if code := runGitHubConnect(api.NewClient(), githubConnectPollInterval); code != 0 {
			os.Exit(code)
		}
		return nil
	},
}

// How often the connect flow asks whether the installation has landed. The
// wait is bounded by the install link's own expiry, never by a tick count.
const githubConnectPollInterval = 5 * time.Second

// Exit codes runGitHubConnect reports back to RunE. Kept as values rather than
// os.Exit calls inside the flow so the whole thing is drivable from a test.
const (
	githubConnectOK        = 0
	githubConnectFailed    = 1
	githubConnectInterrupt = 130
)

// Indirection so a test can run the flow without a browser window opening.
var githubOpenBrowser = openBrowser

// runGitHubConnect drives the whole connect flow and returns a process exit
// code.
//
// Reading status BEFORE starting is not an optimisation. A poll that begins
// from an already-connected baseline would report success on its first tick
// without GitHub having been touched at all, so "connected" would stop meaning
// anything. The baseline COUNT is what the wait compares against, so the
// eventual answer is evidence that this attempt attached something — which it
// has to be, because an account may already hold installations and connecting
// again is how another is added.
func runGitHubConnect(client *api.Client, tick time.Duration) int {
	baseline, err := client.GitHubStatus()
	if err != nil {
		output.PrintError("Failed to get GitHub status: %v", err)
		return githubConnectFailed
	}

	// A CONNECTED ACCOUNT IS NO LONGER A REASON TO STOP. Connecting again is how
	// a SECOND GitHub account is added — an organization beside a personal one —
	// so this reports what is already there and carries on. The baseline still
	// matters for the poll below: starting from a connected one would make the
	// first tick report success without GitHub having been touched, so the wait
	// watches the installation COUNT rather than the bare flag.
	baselineCount := len(baseline.Installations)
	if baseline.Connected {
		printGitHubConnection(baseline, "GitHub already connected")
		output.Println("")
		output.Println("Continuing — authorizing again adds any other accounts you administer.")
		output.Println("")
	}

	url, expiresAt, err := client.GitHubConnectURL()
	if err != nil {
		output.PrintError("Failed to begin the GitHub connection: %v", err)
		return githubConnectFailed
	}

	// A response with no expires_at decodes to the zero time, which as a
	// deadline is already long past — the wait would end instantly and report
	// that the link had expired, on a link that is perfectly good. That is a
	// lie in the one direction this command must never lie, so say what is
	// actually wrong instead and do not poll. Refusing here is NOT tolerance
	// for an older server: it is a malformed answer, and the browser flow it
	// describes is unaffected either way.
	if expiresAt.IsZero() {
		output.PrintError("The server did not say when this installation link expires, so there is nothing to wait for.")
		output.Println("")
		output.Println("The link above still works — finish on GitHub, then check with 'afy github status'.")
		return githubConnectFailed
	}

	output.Println("Authorize Aetherfy on GitHub; if it is installed nowhere yet, GitHub will ask where.")
	output.Println("")
	output.Println("Open this URL in your browser to connect GitHub:")
	output.Println("")
	output.Bold.Println("  " + url)
	output.Println("")

	// Best-effort browser open
	if err := githubOpenBrowser(url); err == nil {
		output.PrintInfo("Opening browser...")
	} else {
		output.PrintInfo("Copy and paste the URL above into your browser.")
	}

	output.PrintInfo("Waiting for you to finish on GitHub. This link is valid until %s.",
		expiresAt.Local().Format(time.RFC1123))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return waitForGitHubConnection(ctx, client, expiresAt, tick, baselineCount)
}

// waitForGitHubConnection polls until the account is connected or the install
// link dies.
//
// The deadline is the link's own expiry, not a guess. A fixed timeout would
// have to say something about a user who is simply still reading GitHub's
// consent page, and there is nothing true to say. Past expiry there is: the
// callback refuses the state token, so the attempt genuinely cannot succeed
// any more, and the message can state that as fact.
func waitForGitHubConnection(ctx context.Context, client *api.Client, expiresAt time.Time, tick time.Duration, baselineCount int) int {
	ctx, cancel := context.WithDeadline(ctx, expiresAt)
	defer cancel()

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	lastWarning := ""
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				// Deliberately not the word "failed": nothing failed. The link
				// ran out, which is a thing that happens to people who take
				// their time, and the only action is a fresh one.
				output.PrintError("This link has expired without a connection being recorded. Run 'afy github connect' for a fresh link.")
				return githubConnectFailed
			}
			output.Printf("Stopped waiting. The link stays valid until %s; finishing on GitHub still connects. Check with 'afy github status'.\n",
				expiresAt.Local().Format(time.RFC1123))
			return githubConnectInterrupt

		case <-ticker.C:
			status, err := client.GitHubStatus()
			if err != nil {
				if apiErr, ok := err.(*api.APIError); ok && apiErr.IsRateLimited() {
					// The server said how long to wait. Ignoring it would
					// spend the rest of the link's life being refused.
					delay := time.Second
					if apiErr.RetryAfterSeconds != nil && *apiErr.RetryAfterSeconds > 1 {
						delay = time.Duration(*apiErr.RetryAfterSeconds) * time.Second
					}
					select {
					case <-ctx.Done():
					case <-time.After(delay):
					}
					continue
				}
				// Transient. Say it once per distinct message rather than once
				// per tick, and keep waiting — the deadline is the bound.
				if msg := err.Error(); msg != lastWarning {
					output.PrintWarning("Still waiting: %v", err)
					lastWarning = msg
				}
				continue
			}

			// MORE THAN WHEN WE STARTED, not merely "connected". An account
			// that already held one installation is connected on the first
			// tick, so a bare Connected check would report success without
			// GitHub having been touched — which is the property the baseline
			// read exists to preserve, now that connecting again is how a
			// second account is added.
			if len(status.Installations) > baselineCount {
				printGitHubConnection(status, "GitHub connected")
				return githubConnectOK
			}
		}
	}
}

// printGitHubConnection renders the connected accounts. Shared by `status` and
// by both of connect's success paths so the three cannot describe one account
// three different ways.
//
// ONE BLOCK PER INSTALLATION, because an Aetherfy account holds several GitHub
// accounts — a personal one and one per organization — and each agent deploys
// through the one its repository belongs to. A single block could only name one
// of them, and the one it named used to be whichever had most recently
// displaced the others.
func printGitHubConnection(status *api.GitHubStatus, headline string) {
	output.PrintSuccess(headline)
	for i, inst := range status.Installations {
		if i > 0 {
			output.Println("")
		}
		// WHICH account, printed first and above the id, because it is the
		// field a person can act on.
		output.KeyValue("Account", inst.AccountLogin+githubAccountKind(inst.AccountType))
		output.KeyValue("Installation ID", fmt.Sprintf("%d", inst.InstallationID))
		if inst.ConnectedAt != nil {
			output.KeyValue("Connected at", inst.ConnectedAt.Local().Format(time.RFC1123))
		}
		if inst.ManageURL != "" {
			output.Printf("To change which repositories Aetherfy can see on %s: %s\n",
				inst.AccountLogin, inst.ManageURL)
		}
	}
}

// githubAccountKind renders GitHub's account kind in this product's words.
// The control plane passes "User" / "Organization" through verbatim so the
// wording is a client's choice; this is that choice. An unrecognised kind
// renders NOTHING rather than a guess — the login alone is already the useful
// half, and a wrong noun beside a correct name reads as a bug in the name.
func githubAccountKind(accountType string) string {
	switch accountType {
	case "Organization":
		return " (organization)"
	case "User":
		return " (personal account)"
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// github disconnect
// ---------------------------------------------------------------------------

var githubDisconnectCmd = &cobra.Command{
	Use:   "disconnect [account]",
	Short: "Disconnect a GitHub account, or all of them",
	Long: `Remove GitHub App installations from your Aetherfy account.

With an account name, only that GitHub account is disconnected: agents linked
to its repositories stop deploying on push, and agents deploying through your
other GitHub accounts are untouched.

With no argument, EVERY connected GitHub account is removed.

Either way the agents keep their links, so reconnecting an account resumes
auto-deploy with nothing to set up again — use 'afy github unlink <agent>'
first if you want an agent to forget its repository entirely.

To fully revoke access, also uninstall the App from your GitHub settings.
The no-argument form is idempotent: it succeeds even if you are not connected.`,
	Example: `  afy github disconnect
  afy github disconnect acme-corp`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}

		client := api.NewClient()

		if len(args) == 0 {
			if err := client.GitHubDisconnect(); err != nil {
				output.PrintError("Failed to disconnect GitHub: %v", err)
				os.Exit(1)
			}
			output.PrintSuccess("GitHub disconnected.")
			return nil
		}

		// AN ACCOUNT NAME, NOT AN INSTALLATION ID. The id is a number nobody
		// carries around; the account login is what 'afy github status' prints
		// and what the repositories are named after. Resolving it here is the
		// only place that mapping is needed.
		account := args[0]
		status, err := client.GitHubStatus()
		if err != nil {
			output.PrintError("Failed to get GitHub status: %v", err)
			os.Exit(1)
		}
		var target *api.GitHubInstallation
		for i := range status.Installations {
			if strings.EqualFold(status.Installations[i].AccountLogin, account) {
				target = &status.Installations[i]
				break
			}
		}
		if target == nil {
			output.PrintError("No connected GitHub account named %q.", account)
			if len(status.Installations) > 0 {
				output.Println("")
				output.Println("Connected accounts:")
				for _, inst := range status.Installations {
					output.Dim.Printf("  %s\n", inst.AccountLogin)
				}
			}
			os.Exit(1)
		}

		if err := client.GitHubDisconnectInstallation(target.InstallationID); err != nil {
			output.PrintError("Failed to disconnect %s: %v", target.AccountLogin, err)
			os.Exit(1)
		}
		output.PrintSuccess("Disconnected %s. Your other GitHub accounts are unaffected.", target.AccountLogin)
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

		// The manage URL is per account and prints inside each block — an
		// account holds several, and one trailing line could only ever offer
		// the way in to one of them.
		printGitHubConnection(status, "GitHub connected")
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
			// A 404 HERE MEANS THREE DIFFERENT THINGS and says which of them
			// it is: a mistyped repo, a mistyped OWNER, or a repository the
			// App was never granted. The owner is the one a person cannot
			// check -- it is the account the App is installed on, not their
			// username -- so the answer is to show what they can actually
			// link rather than to word the failure better.
			//
			// Fail-soft: this runs AFTER the link already failed and its
			// error is on screen. A second failure here must not replace the
			// first one's message with its own.
			if apiErr, ok := err.(*api.APIError); ok && apiErr.StatusCode == 404 {
				printLinkableRepos(client, repo)
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
			// WHAT IT IS FOR, not just that it is precious. Telling someone to
			// copy a secret and never saying why reads as "auto-deploy needs
			// this", which is the one thing it does not mean: Aetherfy
			// registered it on the hook and GitHub signs each delivery with
			// it. The dashboard's banner says the same, for the same reason.
			output.Println("You do not need the webhook secret for auto-deploy — it is already")
			output.Println("registered on the hook, and GitHub signs every push delivery with it.")
			output.Println("Keep it only to verify deliveries yourself or to sign a test push;")
			output.Println("it is shown once, and re-linking mints a new one.")
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
// github repos
// ---------------------------------------------------------------------------

var githubReposCmd = &cobra.Command{
	Use:   "repos",
	Short: "List the repositories Aetherfy can link",
	Long: `List the repositories your GitHub App installation can reach.

These are the only repositories 'afy github link' accepts. Linking registers a
webhook ON the repository, so a repository the App cannot reach cannot be
linked, whoever owns it.

The account shown is the one the App is installed on. It is an organization as
often as a person, and it is NOT necessarily your GitHub username -- which is
the half of owner/repo there is otherwise no way to look up.

Change which repositories the App can see from the URL in 'afy github status'.`,
	Example: `  # Everything you can link
  afy github repos

  # Then link one of them
  afy github link my-agent acme/my-repo`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}

		client := api.NewClient()
		list, err := client.GitHubRepositories()
		if err != nil {
			output.PrintError("Failed to list repositories: %v", err)
			if apiErr, ok := err.(*api.APIError); ok && apiErr.Code == "GITHUB_NOT_CONNECTED" {
				output.Println("")
				output.Println("Connect your GitHub account first:")
				output.Println("  afy github connect")
			}
			os.Exit(1)
		}

		if config.Get().OutputFormat == "json" {
			return output.JSON(list)
		}

		if len(list.Repositories) == 0 {
			// NOT AN ERROR, and not an empty table either. The App is
			// connected and has been granted nothing, which is a state with a
			// specific fix that neither of those would name.
			output.PrintWarning("The Aetherfy GitHub App can reach no repositories.")
			output.Println("Grant it access from the URL in 'afy github status', then try again.")
			return nil
		}

		// GROUPED BY ACCOUNT. The list is a union across every installation, so
		// one heading could not name all of them — and which account a
		// repository belongs to is the half of owner/repo there is otherwise no
		// way to look up.
		currentAccount := ""
		for _, r := range list.Repositories {
			if r.AccountLogin != currentAccount {
				if currentAccount != "" {
					output.Println("")
				}
				currentAccount = r.AccountLogin
				output.Printf("Repositories on %s%s that Aetherfy can link:\n\n",
					r.AccountLogin, githubAccountKind(r.AccountType))
			}
			if r.Private {
				output.Printf("  %s", r.FullName)
				output.Dim.Printf("  private, default branch %s\n", r.DefaultBranch)
				continue
			}
			output.Printf("  %s", r.FullName)
			output.Dim.Printf("  default branch %s\n", r.DefaultBranch)
		}
		output.Println("")
		output.Dim.Printf("%d repository(ies).\n", len(list.Repositories))
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
	githubCmd.AddCommand(githubReposCmd)
	githubCmd.AddCommand(githubLinkCmd)
	githubCmd.AddCommand(githubUnlinkCmd)
}

// printLinkableRepos prints what the account CAN link, after a link 404.
//
// SILENT ON ITS OWN FAILURE, deliberately. It runs after the link has already
// failed and printed why; a second error here would bury the first. The user
// is no worse off than before this existed.
func printLinkableRepos(client *api.Client, attempted string) {
	list, err := client.GitHubRepositories()
	if err != nil || list == nil || len(list.Repositories) == 0 {
		return
	}
	output.Println("")
	output.Println("Aetherfy can link these repositories:")
	accounts := []string{}
	seen := map[string]bool{}
	for _, r := range list.Repositories {
		output.Dim.Printf("  %s\n", r.FullName)
		if !seen[r.AccountLogin] {
			seen[r.AccountLogin] = true
			accounts = append(accounts, r.AccountLogin)
		}
	}
	// The owner is the half nobody can look up, so name the accounts when the
	// one they asked for is not among them. Comparing owners rather than whole
	// names: a right owner with a wrong repo is a typo the list above already
	// answers.
	askedOwner := attempted
	if idx := strings.Index(attempted, "/"); idx != -1 {
		askedOwner = attempted[:idx]
	}
	if !seen[askedOwner] && len(accounts) > 0 {
		output.Println("")
		output.Printf("You asked for %s. The App is installed on %s, not on the owner you named.\n",
			attempted, strings.Join(accounts, ", "))
	}
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
