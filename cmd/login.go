package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with your Aetherfy API key",
	Long: `Authenticate with your Aetherfy API key.

You can get your API key from the Aetherfy dashboard at https://app.aetherfy.com/dashboard/settings/api-keys

The API key will be stored securely in ~/.aetherfy/credentials.yaml

You can also set the AETHERFY_API_KEY environment variable to authenticate.`,
	Example: `  # Interactive login
  afy login

  # Login with API key directly
  afy login --api-key afy_live_xxxxx

  # Set environment variable (alternative)
  export AETHERFY_API_KEY=afy_live_xxxxx`,
	RunE: runLogin,
}

var loginAPIKey string

func init() {
	loginCmd.Flags().StringVar(&loginAPIKey, "api-key", "", "API key to use for authentication")
}

func runLogin(cmd *cobra.Command, args []string) error {
	apiKey := loginAPIKey

	// If no API key provided, prompt for it
	if apiKey == "" {
		fmt.Print("Enter your API key: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}
		apiKey = strings.TrimSpace(input)
	}

	// Validate format
	if err := config.ValidateAPIKey(apiKey); err != nil {
		output.PrintError("%v", err)
		return nil
	}

	// Validate with API
	sp := output.NewSpinner("Validating API key...")
	sp.Start()

	client := api.NewClientWithKey(apiKey)
	userInfo, err := client.ValidateAPIKey()
	sp.Stop()

	if err != nil {
		output.PrintError("Authentication failed: %v", err)
		if apiErr, ok := err.(*api.APIError); ok {
			if apiErr.StatusCode == 401 {
				output.Println("\nMake sure your API key is correct and not expired.")
				output.Println("Get a new key at: https://app.aetherfy.com/dashboard/settings/api-keys")
			}
		}
		return nil
	}

	// Save credentials
	creds := &config.Credentials{
		APIKey: apiKey,
		UserID: userInfo.UserID,
		Email:  userInfo.Email,
		Tier:   userInfo.Tier,
	}

	if err := creds.Save(); err != nil {
		output.PrintError("Failed to save credentials: %v", err)
		return nil
	}

	output.PrintSuccess("Logged in successfully!")

	// Show key type
	if config.IsTestKey(apiKey) {
		output.PrintWarning("Using test API key - requests will be in test mode")
	}

	output.Println("")
	output.Println("You can now use afy commands to manage your agents.")
	output.Println("Try 'afy agents list' to see your agents.")

	return nil
}
