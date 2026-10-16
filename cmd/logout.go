package cmd

import (
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
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
		return output.Result(map[string]interface{}{"logged_out": false})
	}

	if err := config.DeleteCredentials(); err != nil {
		return printRefusal("Failed to remove credentials", err)
	}

	output.PrintSuccess("Logged out successfully.")
	return output.Result(map[string]interface{}{"logged_out": true})
}
