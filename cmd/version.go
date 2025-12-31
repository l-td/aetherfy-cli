package cmd

import (
	"fmt"

	"github.com/aetherfy/cli/pkg/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  "Print detailed version information including build date and commit hash.",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(version.Full())
	},
}
