package cmd

import (
	"github.com/aetherfy/cli/internal/config"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored credentials",
	Long:  "Remove stored API key and credentials from your local machine.",
	RunE:  runLogout,
}

func runLogout(cmd *cobra.Command, args []string) error {
	if !config.IsLoggedIn() {
		output.PrintInfo("Not currently logged in.")
		return nil
	}

	if err := config.DeleteCredentials(); err != nil {
		output.PrintError("Failed to remove credentials: %v", err)
		return nil
	}

	output.PrintSuccess("Logged out successfully.")
	return nil
}
