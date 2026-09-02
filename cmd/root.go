package cmd

import (
	"os"

	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/pkg/version"
	"github.com/spf13/cobra"
)

var (
	cfgFile      string
	apiURL       string
	outputFormat string
	verbose      bool
	noColor      bool
)

// Help groups. PRESENTATION ONLY — nothing branches on a GroupID; the flatten
// leaves ~twenty top-level verbs and an ungrouped list of twenty is a wall.
const (
	groupAgentLifecycle = "agent-lifecycle"
	groupAgentOps       = "agent-ops"
	groupResources      = "resources"
	groupAccount        = "account"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "afy",
	Short: "Aetherfy CLI - Deploy and manage AI agents",
	Long: `afy is the command-line interface for the Aetherfy platform.

Deploy, manage, and monitor your AI agents with ease.

A bare verb is an AGENT verb: 'afy list' lists your agents, 'afy logs <name>'
reads one agent's logs. Only the other nouns are grouped.

Quick start:
  afy login                    # Authenticate with your API key
  afy init                     # Generate aetherfy.yaml for your project
  afy deploy                   # Deploy current directory
  afy list                     # List your agents
  afy logs <agent>             # View agent logs
  afy deployments <agent>      # View deployment history
  afy rollback <agent> <ver>   # Roll back to a previous version
  afy upgrade                  # Replace this binary with the newest release

For more information, visit: https://docs.aetherfy.com`,
	Version: version.Short(),
	// Runtime errors (a failed API call, a rejected deploy) already print a
	// clean "Error: ..." line — dumping the full usage/help text after them is
	// noise. SilenceUsage suppresses that dump for every command (children
	// inherit). SilenceErrors is deliberately left off so cobra still prints the
	// error itself. Trade-off: unknown-flag/arg errors also stop dumping usage,
	// but the error text still names the offending flag and `--help` remains.
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for certain commands
		if cmd.Name() == "version" || cmd.Name() == "help" {
			return nil
		}

		// Load configuration
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Apply command-line overrides
		if apiURL != "" {
			config.SetAPIURL(apiURL)
		}
		if verbose {
			config.SetVerbose(true)
		}
		if noColor {
			config.SetNoColor(true)
			output.DisableColors()
		}
		if outputFormat != "" {
			config.SetOutputFormat(outputFormat)
			output.SetFormat(outputFormat)
		}

		// Load credentials
		_, err = config.LoadCredentials()
		if err != nil {
			output.PrintWarning("Failed to load credentials: %v", err)
		}

		_ = cfg // Silence unused warning
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags
	// Computed, not hardcoded — see the note on loginCmd. "~/.aetherfy" is only
	// the FALLBACK branch of config.ConfigDir(); on Windows the real directory
	// is %APPDATA%\aetherfy, and $AETHERFY_CONFIG_DIR overrides both.
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is "+config.ConfigPath()+")")
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "API base URL (overrides config)")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "", "output format: text, json, table")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colored output")

	rootCmd.AddGroup(
		&cobra.Group{ID: groupAgentLifecycle, Title: "Agent lifecycle:"},
		&cobra.Group{ID: groupAgentOps, Title: "Agent operations:"},
		&cobra.Group{ID: groupResources, Title: "Other resources:"},
		&cobra.Group{ID: groupAccount, Title: "Account and CLI:"},
	)
	rootCmd.SetHelpCommandGroupID(groupAccount)
	rootCmd.SetCompletionCommandGroupID(groupAccount)

	// AGENTS ARE THE DEFAULT NOUN. A bare verb is an agent verb — `afy list`,
	// `afy logs`, `afy deploy` — and ONLY the non-default nouns keep a group:
	// secrets, workspaces, github. Docker works exactly this way, and it is the
	// reason `docker ps` is not `docker containers ps`.
	//
	// The `agents` group used to exist and was removed, with no alias left
	// behind: one spelling per command. The agent verbs it held now register at
	// the root, from cmd/agents.go's own init().
	//
	// SO: DO NOT ADD A TOP-LEVEL COMMAND THAT TAKES A NOUN. A new verb here is
	// an agent verb by definition. Anything about some other kind of object goes
	// under that object's group, and a genuinely new kind of object gets a new
	// group beside secrets/workspaces/github. Without that rule the structure
	// looks arbitrary and the next person adds `afy collections list`.
	for _, c := range []*cobra.Command{deployCmd, redeployCmd, rollbackCmd} {
		c.GroupID = groupAgentLifecycle
	}
	for _, c := range []*cobra.Command{logsCmd, deploymentsCmd, spawnCmd} {
		c.GroupID = groupAgentOps
	}
	for _, c := range []*cobra.Command{secretsCmd, workspacesCmd, githubCmd} {
		c.GroupID = groupResources
	}
	for _, c := range []*cobra.Command{initCmd, versionCmd, loginCmd, logoutCmd, whoamiCmd} {
		c.GroupID = groupAccount
	}

	// ONE PER LINE — see the note in cmd/agents.go's init(). docs-site parses
	// these calls statically; a loop makes the command invisible to the guard
	// that stops the docs describing a command the CLI does not have.
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(redeployCmd)
	rootCmd.AddCommand(rollbackCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(deploymentsCmd)
	rootCmd.AddCommand(spawnCmd)
	rootCmd.AddCommand(secretsCmd)
	rootCmd.AddCommand(workspacesCmd)
	rootCmd.AddCommand(githubCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
	// Deliberately NOT behind checkAuth(): replacing the CLI binary needs no
	// Aetherfy account, and a logged-out user is exactly who might be stuck on
	// an old build. Spelled `upgrade` since the flatten — see cmd/update.go.
	updateCmd.GroupID = groupAccount
	rootCmd.AddCommand(updateCmd)

	// Custom version template
	rootCmd.SetVersionTemplate(version.Full() + "\n")
}

// checkAuth ensures the user is logged in before running a command
func checkAuth() error {
	if !config.IsLoggedIn() {
		output.PrintError("Not logged in. Run 'afy login' first.")
		os.Exit(3)
	}
	return nil
}
