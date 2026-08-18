package cmd

import (
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show current authentication status",
	Long:  "Display information about the currently authenticated user.",
	RunE:  runWhoami,
}

func runWhoami(cmd *cobra.Command, args []string) error {
	creds := config.GetCredentials()

	if !config.IsLoggedIn() {
		output.PrintError("Not logged in. Run 'afy login' first.")
		return nil
	}

	cfg := config.Get()

	output.Header("Authentication Status")
	output.Println("")
	output.KeyValue("API Key", config.MaskAPIKey(creds.APIKey))
	output.KeyValue("Key Type", keyType(creds.APIKey))
	output.KeyValue("API URL", cfg.APIURL)

	if creds.Email != "" {
		output.KeyValue("Email", creds.Email)
	}
	if creds.Tier != "" {
		output.KeyValue("Tier", creds.Tier)
	}

	output.Println("")
	output.Dim.Println("Credentials stored in: " + config.CredentialsPath())

	return nil
}

func keyType(key string) string {
	if config.IsTestKey(key) {
		return "test"
	}
	return "live"
}
