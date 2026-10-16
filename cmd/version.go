package cmd

import (
	"fmt"
	"github.com/l-td/aetherfy-cli/internal/output"

	"github.com/l-td/aetherfy-cli/pkg/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  "Print detailed version information including build date and commit hash.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if output.IsJSON() {
			return output.JSON(map[string]string{"version": version.Short(), "details": version.Full()})
		}
		fmt.Print(version.Full())
		return nil
	},
}
