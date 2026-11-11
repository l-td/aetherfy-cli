package cmd

import (
	"errors"
	"os"
	"sync"

	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/pkg/version"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	apiURL  string
	jsonOut bool
	verbose bool
	noColor bool
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
  afy init                     # Create an agent for this folder (or --agent <name> to link one)
  afy deploy                   # Deploy this folder to the agent it is linked to
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
	// inherit). Trade-off: unknown-flag/arg errors also stop dumping usage,
	// but the error text still names the offending flag and `--help` remains.
	//
	// SilenceErrors: Execute prints the error instead, in cobra's own
	// "Error: ..." form, EXCEPT one a command already printed in full
	// (printRefusal: the server's sentence, as written). Cobra printing it too
	// showed every refusal twice, the second time wrapped in "[409] ... (CODE)".
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// cobra validates required flags and flag groups AFTER this hook; do it
		// first, so a missing required flag is a usage error like the rest.
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return err
		}
		if err := cmd.ValidateFlagGroups(); err != nil {
			return err
		}
		// cobra has parsed the line and validated it: from here a failure is
		// not a usage error (see Execute).
		preRunReached = true

		// The one JSON switch, set before anything can print (and before the
		// version/help early return below: `afy version --json` is JSON).
		output.SetJSON(jsonOut)

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

		// Load credentials
		_, err = config.LoadCredentials()
		if err != nil {
			// On stderr: stdout may be the one JSON object a script parses.
			output.Warning.Fprintf(os.Stderr, "Warning: Failed to load credentials: %v\n", err)
		}

		_ = cfg // Silence unused warning
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags
// appropriately, and prints the error a command returns unless the command
// already printed it (see SilenceErrors above).
func Execute() error {
	groupsRejectUnknown.Do(func() { rejectUnknownSubcommands(rootCmd) })
	preRunReached = false
	err := rootCmd.Execute()
	// An error before PersistentPreRunE ran is cobra refusing the command line
	// itself: an unknown flag or command, a flag missing its value, too few or
	// too many arguments, a required flag not set. Like every other refusal
	// before anything is sent, it is exit 2. Told apart by WHERE it happened,
	// not by the wording of cobra's message.
	var in *inputError
	if err != nil && !preRunReached && !alreadyPrinted(err) && !errors.As(err, &in) {
		err = &inputError{msg: err.Error()}
	}
	if err != nil && !alreadyPrinted(err) {
		printUnprinted(err)
	}
	return err
}

var groupsRejectUnknown sync.Once

// rejectUnknownSubcommands makes a group (a command that only holds
// subcommands: secrets, workspaces, github, collections ...) refuse a word
// that is none of its subcommands. cobra answers it with the group's help and
// exit 0, because a group with no Run never validates its arguments; as a
// command that shows its help and takes no arguments it fails like an unknown
// command at the root does.
func rejectUnknownSubcommands(c *cobra.Command) {
	for _, sub := range c.Commands() {
		rejectUnknownSubcommands(sub)
	}
	if c != rootCmd && c.HasSubCommands() && !c.Runnable() {
		c.Args = cobra.NoArgs
		c.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
	}
}

// preRunReached is set once cobra has handed the command to PersistentPreRunE.
var preRunReached bool

func init() {
	// Global flags
	// Computed, not hardcoded — see the note on loginCmd. "~/.aetherfy" is only
	// the FALLBACK branch of config.ConfigDir(); on Windows the real directory
	// is %APPDATA%\aetherfy, and $AETHERFY_CONFIG_DIR overrides both.
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is "+config.ConfigPath()+")")
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "API base URL (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "print the result as JSON on stdout (hints and progress go to stderr; errors are JSON on stderr)")
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
	// group beside secrets/workspaces/github/collections/index/points. Without
	// that rule the structure looks arbitrary and the next person adds a
	// top-level `afy list-collections`.
	for _, c := range []*cobra.Command{deployCmd, redeployCmd, rollbackCmd} {
		c.GroupID = groupAgentLifecycle
	}
	for _, c := range []*cobra.Command{logsCmd, deploymentsCmd, spawnCmd} {
		c.GroupID = groupAgentOps
	}
	for _, c := range []*cobra.Command{secretsCmd, workspacesCmd, githubCmd, collectionsCmd, indexCmd, pointsCmd, gatewayCmd} {
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
	rootCmd.AddCommand(collectionsCmd)
	rootCmd.AddCommand(indexCmd)
	rootCmd.AddCommand(pointsCmd)
	rootCmd.AddCommand(gatewayCmd)
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
