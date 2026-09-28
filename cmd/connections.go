package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// afy connections — OAuth grants (Google, Slack, Notion) Aetherfy holds for an
// agent or a workspace. The platform runs the OAuth sign-in and keeps the
// tokens; agent code fetches a fresh access token by name at runtime. The CLI
// only starts a connection (the user grants it in a browser), lists and
// removes them. It never sees a token.
// ---------------------------------------------------------------------------

var connectionsCmd = &cobra.Command{
	Use:     "connections",
	Aliases: []string{"connection"},
	Short:   "Manage OAuth connections (Google, Slack, Notion)",
	Long: `Give an agent, or every agent in a workspace, access to Google, Slack or
Notion. Aetherfy runs the sign-in in your browser and keeps the grant; your
agent code asks for a fresh access token by the connection's name at runtime.

Subcommands:
  providers                       List the services you can connect
  list [--agent A | --workspace W] List connections
  connect <provider> (--agent A | --workspace W)
                                  Grant access in your browser
  disconnect <name> (--agent A | --workspace W)
                                  Remove a connection`,
	Example: `  afy connections providers
  afy connections connect google --agent reporter --scope https://www.googleapis.com/auth/drive.file
  afy connections connect slack --workspace team --name team-slack
  afy connections list --agent reporter
  afy connections disconnect google --agent reporter`,
}

var (
	connAgent     string
	connWorkspace string
	connName      string
	connScopes    []string
	connNoBrowser bool
)

// connectionTarget turns the --agent/--workspace pair into a target. Cobra
// already refuses both at once; required says whether neither is an error.
func connectionTarget(required bool) (api.ConnectionTarget, bool) {
	if connAgent == "" && connWorkspace == "" {
		if required {
			output.PrintError("Say where the connection lives: --agent <name> or --workspace <name>")
		}
		return api.ConnectionTarget{}, false
	}
	return api.ConnectionTarget{Agent: connAgent, Workspace: connWorkspace}, true
}

// connectionTargetLabel names a target the way a person would say it.
func connectionTargetLabel(t api.ConnectionTarget) string {
	if t.Workspace != "" {
		return "workspace " + t.Workspace
	}
	return "agent " + t.Agent
}

// connectionWhere names where a listed connection lives.
func connectionWhere(c api.Connection) string {
	if c.Scope == "agent" && c.Agent != nil {
		return c.Agent.Name
	}
	if c.Workspace != nil {
		return *c.Workspace
	}
	return ""
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

// ---------------------------------------------------------------------------
// providers
// ---------------------------------------------------------------------------

var connectionsProvidersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List the services you can connect",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		providers, err := api.NewClient().ConnectionProviders()
		if err != nil {
			output.PrintError("Failed to list providers: %v", err)
			os.Exit(connectFailed)
		}
		if config.Get().OutputFormat == "json" {
			return output.JSON(providers)
		}
		table := output.Table([]string{"Provider", "Available", "Always requested", "Optional scopes"})
		for _, p := range providers {
			available := "yes"
			if !p.Configured {
				available = "not yet"
			}
			table.Append([]string{
				p.Provider, available,
				strings.Join(p.DefaultScopes, " "),
				strings.Join(optionalScopes(p), " "),
			})
		}
		table.Render()
		return nil
	},
}

// optionalScopes is the allow-list minus what is always requested: what
// --scope can add.
func optionalScopes(p api.ConnectionProvider) []string {
	always := map[string]bool{}
	for _, s := range p.DefaultScopes {
		always[s] = true
	}
	var extra []string
	for _, s := range p.AllowedScopes {
		if !always[s] {
			extra = append(extra, s)
		}
	}
	return extra
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

var connectionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List connections",
	Long: `List connections on the account, or those an agent or workspace holds.

With --agent, the list is what that agent can use: its own connections, then
its workspace's. When both have the same name, the agent's own is the one its
code gets a token for.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		client := api.NewClient()
		var conns []api.Connection
		var err error
		if target, ok := connectionTarget(false); ok {
			conns, err = client.ListTargetConnections(target)
		} else {
			conns, err = client.ListConnections()
		}
		if err != nil {
			output.PrintError("Failed to list connections: %v", err)
			os.Exit(connectFailed)
		}
		if config.Get().OutputFormat == "json" {
			return output.JSON(conns)
		}
		if len(conns) == 0 {
			output.PrintInfo("No connections yet. Start one with: afy connections connect <provider> --agent <name>")
			return nil
		}
		table := output.Table([]string{"Name", "Provider", "Scope", "Where", "Account", "Status", "Last used"})
		for _, c := range conns {
			lastUsed := "never"
			if c.LastUsedAt != nil {
				lastUsed = c.LastUsedAt.Local().Format("2006-01-02 15:04")
			}
			table.Append([]string{
				c.Name, c.Provider, c.Scope, connectionWhere(c), orDash(c.AccountLabel), c.Status, lastUsed,
			})
		}
		table.Render()
		return nil
	},
}

// ---------------------------------------------------------------------------
// connect
// ---------------------------------------------------------------------------

var connectionsConnectCmd = &cobra.Command{
	Use:   "connect <provider>",
	Short: "Connect Google, Slack or Notion to an agent or workspace",
	Long: `Start a connection and grant access in your browser.

The CLI prints the provider's consent URL and opens it. Approve every
permission shown; the provider then sends your browser back to the Aetherfy
dashboard. The command waits until the connection is recorded, or until the
link expires (ten minutes).

Connecting again under a name that already exists replaces that grant, which
is how a connection marked needs_reauth is fixed.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		target, ok := connectionTarget(true)
		if !ok {
			os.Exit(connectFailed)
		}
		req := api.ConnectionCreateRequest{Provider: args[0], Name: connName, Scopes: connScopes}
		if code := runConnectionConnect(api.NewClient(), target, req, connNoBrowser, connectionsConnectPollInterval); code != 0 {
			os.Exit(code)
		}
		return nil
	},
}

// How often connect asks whether the grant has landed. The wait itself is
// bounded by the link's own expiry, never by a tick count.
const connectionsConnectPollInterval = 3 * time.Second

// Exit codes, returned rather than os.Exit'd so the flow is drivable from a test.
const (
	connectOK        = 0
	connectFailed    = 1
	connectInterrupt = 130
)

// Indirection so a test can run the flow without a browser window opening.
// The same opener as `afy github connect`.
var connectionsOpenBrowser = openBrowser

// findConnection is the target's OWN connection named `name` — for an agent,
// the list also carries its workspace's, which a connect to the agent does not
// create.
func findConnection(conns []api.Connection, target api.ConnectionTarget, name string) *api.Connection {
	scope := "agent"
	if target.Workspace != "" {
		scope = "workspace"
	}
	for i := range conns {
		if conns[i].Name == name && conns[i].Scope == scope {
			return &conns[i]
		}
	}
	return nil
}

// runConnectionConnect drives the whole flow and returns an exit code.
//
// WHAT COUNTS AS DONE. The connection with this name, in this scope, whose
// connected_at is after the one it had before this attempt (or that did not
// exist before). A bare "exists" would report success on the first tick for a
// reconnect, without the provider having been touched. The baseline is the
// SERVER's own connected_at rather than this machine's clock, so a skewed
// local clock cannot make a finished grant look old or a stale one look new.
func runConnectionConnect(client *api.Client, target api.ConnectionTarget, req api.ConnectionCreateRequest, noBrowser bool, tick time.Duration) int {
	begun, err := client.BeginConnection(target, req)
	if err != nil {
		output.PrintError("Failed to start the connection: %v", err)
		return connectFailed
	}
	if begun.ConnectURL == "" || begun.ExpiresAt.IsZero() {
		output.PrintError("The server did not return a link to open and its expiry, so there is nothing to connect with.")
		return connectFailed
	}

	// Read AFTER begin (which is where the name is decided) and BEFORE the
	// browser opens, so nothing the user does can land before it.
	before, err := client.ListTargetConnections(target)
	if err != nil {
		output.PrintError("Failed to read the current connections: %v", err)
		return connectFailed
	}
	var baseline *time.Time
	if existing := findConnection(before, target, begun.Name); existing != nil {
		at := existing.ConnectedAt
		baseline = &at
	}

	output.Printf("Connecting %s to %s as %q.\n", req.Provider, connectionTargetLabel(target), begun.Name)
	output.Println("")
	output.Println("Open this URL in your browser and approve every permission shown:")
	output.Println("")
	output.Bold.Println("  " + begun.ConnectURL)
	output.Println("")
	if !noBrowser {
		if err := connectionsOpenBrowser(begun.ConnectURL); err == nil {
			output.PrintInfo("Opening browser...")
		} else {
			output.PrintInfo("Copy and paste the URL above into your browser.")
		}
	}
	output.PrintInfo("Waiting for the grant. This link is valid until %s.", begun.ExpiresAt.Local().Format(time.RFC1123))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return waitForConnection(ctx, client, target, begun, baseline, tick)
}

func waitForConnection(ctx context.Context, client *api.Client, target api.ConnectionTarget, begun *api.ConnectionConnectURL, baseline *time.Time, tick time.Duration) int {
	ctx, cancel := context.WithDeadline(ctx, begun.ExpiresAt)
	defer cancel()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	lastWarning := ""
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				output.PrintError("The link expired without a connection being recorded. Run 'afy connections connect' again for a fresh one.")
				return connectFailed
			}
			output.Printf("Stopped waiting. The link stays valid until %s; approving it still connects. Check with 'afy connections list'.\n",
				begun.ExpiresAt.Local().Format(time.RFC1123))
			return connectInterrupt

		case <-ticker.C:
			conns, err := client.ListTargetConnections(target)
			if err != nil {
				if apiErr, ok := err.(*api.APIError); ok && apiErr.IsRateLimited() {
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
				if msg := err.Error(); msg != lastWarning {
					output.PrintWarning("Still waiting: %v", err)
					lastWarning = msg
				}
				continue
			}
			c := findConnection(conns, target, begun.Name)
			if c != nil && (baseline == nil || c.ConnectedAt.After(*baseline)) {
				output.PrintSuccess("Connected %s", c.Name)
				output.KeyValue("Provider", c.Provider)
				output.KeyValue("Account", orDash(c.AccountLabel))
				output.KeyValue("Scopes", strings.Join(c.Scopes, " "))
				output.Println("")
				output.Printf("Agent code fetches a token with POST $AETHERFY_API_URL/connections/%s/token.\n", c.Name)
				return connectOK
			}
		}
	}
}

// ---------------------------------------------------------------------------
// disconnect
// ---------------------------------------------------------------------------

var connectionsDisconnectCmd = &cobra.Command{
	Use:   "disconnect <name>",
	Short: "Remove a connection",
	Long: `Remove a connection from an agent or a workspace.

Aetherfy asks the provider to revoke the grant, then removes the connection
either way, and says whether the provider confirmed. When it did not, remove
Aetherfy in your account settings at the provider.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		target, ok := connectionTarget(true)
		if !ok {
			os.Exit(connectFailed)
		}
		if code := runConnectionDisconnect(api.NewClient(), target, args[0]); code != 0 {
			os.Exit(code)
		}
		return nil
	},
}

func runConnectionDisconnect(client *api.Client, target api.ConnectionTarget, name string) int {
	deleted, err := client.DeleteConnection(target, name)
	if err != nil {
		output.PrintError("Failed to disconnect %s: %v", name, err)
		return connectFailed
	}
	output.PrintSuccess("Disconnected %s from %s", deleted.Name, connectionTargetLabel(target))
	if deleted.ProviderRevoked {
		output.Println("The provider confirmed the access is revoked.")
	} else {
		output.PrintWarning("The provider did not confirm a revoke, so Aetherfy may still be listed in your account settings there. Remove it there to be sure.")
	}
	return connectOK
}

func init() {
	// ONE LITERAL CALL PER FLAG, not a loop over the three commands: docs-site's
	// cli-surface extractor reads these registrations statically (as it does the
	// AddCommand calls in root.go) and cannot follow a loop variable, so a
	// looped flag is invisible to the guard that checks the docs' examples.
	connectionsListCmd.Flags().StringVar(&connAgent, "agent", "", "Agent name or UUID")
	connectionsListCmd.Flags().StringVar(&connWorkspace, "workspace", "", "Workspace name")
	connectionsListCmd.MarkFlagsMutuallyExclusive("agent", "workspace")
	connectionsConnectCmd.Flags().StringVar(&connAgent, "agent", "", "Agent name or UUID")
	connectionsConnectCmd.Flags().StringVar(&connWorkspace, "workspace", "", "Workspace name")
	connectionsConnectCmd.MarkFlagsMutuallyExclusive("agent", "workspace")
	connectionsDisconnectCmd.Flags().StringVar(&connAgent, "agent", "", "Agent name or UUID")
	connectionsDisconnectCmd.Flags().StringVar(&connWorkspace, "workspace", "", "Workspace name")
	connectionsDisconnectCmd.MarkFlagsMutuallyExclusive("agent", "workspace")
	connectionsConnectCmd.Flags().StringVar(&connName, "name", "", "Connection name agent code asks for (default: the provider)")
	connectionsConnectCmd.Flags().StringArrayVar(&connScopes, "scope", nil, "Extra scope to request on top of the provider's defaults (repeatable)")
	connectionsConnectCmd.Flags().BoolVar(&connNoBrowser, "no-browser", false, "Print the URL without opening a browser")

	connectionsCmd.AddCommand(connectionsProvidersCmd)
	connectionsCmd.AddCommand(connectionsListCmd)
	connectionsCmd.AddCommand(connectionsConnectCmd)
	connectionsCmd.AddCommand(connectionsDisconnectCmd)
}
