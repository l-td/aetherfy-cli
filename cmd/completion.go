package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for afy.

To load completions:

Bash:
  $ source <(afy completion bash)
  # To load completions for each session, add to your ~/.bashrc:
  # echo 'source <(afy completion bash)' >> ~/.bashrc

Zsh:
  $ source <(afy completion zsh)
  # To load completions for each session, add to your ~/.zshrc:
  # echo 'source <(afy completion zsh)' >> ~/.zshrc

Fish:
  $ afy completion fish | source
  # To load completions for each session:
  # afy completion fish > ~/.config/fish/completions/afy.fish

PowerShell:
  PS> afy completion powershell | Out-String | Invoke-Expression
  # To load completions for each session, add to your profile:
  # afy completion powershell >> $PROFILE
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
		default:
			return cmd.Help()
		}
	},
}

func init() {
	completionCmd.GroupID = groupAccount
	rootCmd.AddCommand(completionCmd)
}
