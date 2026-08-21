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

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "afy",
	Short: "Aetherfy CLI - Deploy and manage AI agents",
	Long: `afy is the command-line interface for the Aetherfy platform.

Deploy, manage, and monitor your AI agents with ease.

Quick start:
  afy login                    # Authenticate with your API key
  afy init                     # Generate aetherfy.yaml for your project
  afy deploy                   # Deploy current directory
  afy logs <agent>             # View agent logs
  afy deployments <agent>      # View deployment history
  afy rollback <agent> <ver>   # Roll back to a previous version

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

	// Add subcommands
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
	rootCmd.AddCommand(agentsCmd)
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(secretsCmd)
	rootCmd.AddCommand(workspacesCmd)
	rootCmd.AddCommand(spawnCmd)
	rootCmd.AddCommand(githubCmd)
	rootCmd.AddCommand(rollbackCmd)
	rootCmd.AddCommand(redeployCmd)
	rootCmd.AddCommand(deploymentsCmd)

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
