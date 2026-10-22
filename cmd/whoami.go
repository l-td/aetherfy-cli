package cmd

import (
	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show current authentication status",
	Long: `Display information about the currently authenticated user.

The account's email and plan are read from the API on every run, so they are
right whichever way the key was supplied (afy login or AETHERFY_API_KEY) and
right after a plan change. Nothing about the plan is stored on this machine.`,
	RunE: runWhoami,
}

func runWhoami(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	creds := config.GetCredentials()
	cfg := config.Get()

	user, err := api.NewClient().ValidateAPIKey()
	if err != nil {
		return printRefusal("Failed to read the account", err)
	}

	output.Header("Authentication Status")
	output.Println("")
	output.KeyValue("API Key", config.MaskAPIKey(creds.APIKey))
	output.KeyValue("Key Type", keyType(creds.APIKey))
	output.KeyValue("API URL", cfg.APIURL)

	if user.Email != "" {
		output.KeyValue("Email", user.Email)
	}
	if user.Tier != "" {
		output.KeyValue("Tier", user.Tier)
	}

	output.Println("")
	output.Dim.Println("Credentials stored in: " + config.CredentialsPath())

	return output.Result(map[string]interface{}{
		"api_key": config.MaskAPIKey(creds.APIKey), "key_type": keyType(creds.APIKey),
		"api_url": cfg.APIURL, "email": user.Email, "tier": user.Tier,
		"credentials_path": config.CredentialsPath(),
	})
}

func keyType(key string) string {
	if config.IsTestKey(key) {
		return "test"
	}
	return "live"
}
