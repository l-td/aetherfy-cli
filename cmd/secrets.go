package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/config"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:     "secrets",
	Aliases: []string{"secret", "s"},
	Short:   "Manage agent secrets",
	Long:    "Manage environment variables and secrets for your agents.",
}

// --- LIST ---
var secretsListCmd = &cobra.Command{
	Use:   "list [agent]",
	Short: "List secrets for an agent or workspace",
	Long: `List all secrets (keys only) for an agent or workspace.

Use --workspace flag to list workspace-scoped secrets.
Agent-scoped secrets are specific to one agent.
Workspace-scoped secrets are shared across all agents in a workspace.`,
	Example: `  # List secrets for an agent
  afy secrets list my-agent

  # List secrets for a workspace
  afy secrets list --workspace my-workspace`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSecretsList,
}

var workspaceFlag string

func runSecretsList(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	client := api.NewClient()
	var secrets []api.Secret
	var err error
	var target string

	// Workspace mode
	if workspaceFlag != "" {
		target = workspaceFlag
		sp := output.NewSpinner("Fetching workspace secrets...")
		sp.Start()
		secrets, err = client.ListWorkspaceSecrets(workspaceFlag)
		sp.Stop()
	} else {
		// Agent mode (require agent arg)
		if len(args) == 0 {
			output.PrintError("Provide agent name or use --workspace flag")
			return nil
		}
		agentIDOrName := args[0]
		target = agentIDOrName

		sp := output.NewSpinner("Fetching secrets...")
		sp.Start()
		secrets, err = client.ListSecrets(agentIDOrName)
		sp.Stop()
	}

	if err != nil {
		output.PrintError("Failed to list secrets: %v", err)
		return nil
	}

	// JSON mode: always emit valid JSON, even for empty results
	if config.Get().OutputFormat == "json" {
		return output.JSON(secrets)
	}

	if len(secrets) == 0 {
		if workspaceFlag != "" {
			output.PrintInfo("No secrets found for workspace '%s'", workspaceFlag)
			output.Println("")
			output.Println("Set a secret with: afy secrets set --workspace " + workspaceFlag + " KEY=value")
		} else {
			output.PrintInfo("No secrets found for agent '%s'", target)
			output.Println("")
			output.Println("Set a secret with: afy secrets set " + target + " KEY=value")
		}
		return nil
	}

	// Table output
	table := output.Table([]string{"Key", "Created", "Updated"})
	for _, s := range secrets {
		table.Append([]string{
			s.Key,
			s.CreatedAt.Format("2006-01-02 15:04"),
			s.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}
	table.Render()

	output.Println("")
	output.Dim.Printf("Total: %d secret(s)\n", len(secrets))

	return nil
}

// --- SET ---
var secretsSetCmd = &cobra.Command{
	Use:   "set [agent] <KEY=value>...",
	Short: "Set a secret for an agent or workspace",
	Long: `Set one or more secrets for an agent or workspace.

Use --workspace flag to set workspace-scoped secrets.
Agent secrets override workspace secrets with the same key.

Secrets can be provided as KEY=value pairs or read from stdin.
Values are encrypted at rest and injected as environment variables.`,
	Example: `  # Set a secret for an agent
  afy secrets set my-agent API_KEY=sk-xxxxx

  # Set multiple secrets for an agent
  afy secrets set my-agent API_KEY=sk-xxxxx DB_URL=postgres://...

  # Set a secret for a workspace
  afy secrets set --workspace my-workspace SHARED_API_KEY=sk-xxxxx

  # Read value from stdin (for sensitive data)
  echo "sk-xxxxx" | afy secrets set my-agent API_KEY --stdin`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSecretsSet,
}

var secretsStdin bool

func init() {
	secretsSetCmd.Flags().BoolVar(&secretsStdin, "stdin", false, "Read secret value from stdin")
}

func runSecretsSet(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	client := api.NewClient()

	var target string
	var pairs []string

	// Determine target and pairs based on workspace flag
	if workspaceFlag != "" {
		// Workspace mode: all args are KEY=value pairs
		target = workspaceFlag
		pairs = args
	} else {
		// Agent mode: first arg is agent, rest are pairs
		if len(args) == 0 {
			output.PrintError("Provide agent name or use --workspace flag")
			return nil
		}
		target = args[0]
		pairs = args[1:]
	}

	// Handle stdin mode
	if secretsStdin {
		if len(pairs) != 1 {
			output.PrintError("With --stdin, provide exactly one key name")
			return nil
		}
		key := pairs[0]
		// Remove any = and value part if present
		if idx := strings.Index(key, "="); idx != -1 {
			key = key[:idx]
		}

		// Read value from stdin
		reader := bufio.NewReader(os.Stdin)
		value, err := reader.ReadString('\n')
		if err != nil && err.Error() != "EOF" {
			output.PrintError("Failed to read from stdin: %v", err)
			return nil
		}
		value = strings.TrimSpace(value)

		// Set secret based on mode
		if workspaceFlag != "" {
			err = client.SetWorkspaceSecret(workspaceFlag, key, value)
		} else {
			err = client.SetSecret(target, key, value)
		}

		if err != nil {
			output.PrintError("Failed to set secret: %v", err)
			return nil
		}

		output.PrintSuccess("Secret '%s' set successfully", key)
		return nil
	}

	// Parse KEY=value pairs
	if len(pairs) == 0 {
		output.PrintError("Provide at least one KEY=value pair")
		return nil
	}

	for _, pair := range pairs {
		idx := strings.Index(pair, "=")
		if idx == -1 {
			output.PrintError("Invalid format '%s'. Use KEY=value", pair)
			continue
		}

		key := pair[:idx]
		value := pair[idx+1:]

		if key == "" {
			output.PrintError("Key cannot be empty in '%s'", pair)
			continue
		}

		// Set secret based on mode
		var err error
		if workspaceFlag != "" {
			err = client.SetWorkspaceSecret(workspaceFlag, key, value)
		} else {
			err = client.SetSecret(target, key, value)
		}

		if err != nil {
			output.PrintError("Failed to set '%s': %v", key, err)
			continue
		}

		output.PrintSuccess("Secret '%s' set", key)
	}

	return nil
}

// --- DELETE ---
var secretsDeleteCmd = &cobra.Command{
	Use:   "delete [agent] <key>",
	Short: "Delete a secret from an agent or workspace",
	Long: `Delete a secret from an agent or workspace.

Use --workspace flag to delete workspace-scoped secrets.`,
	Example: `  # Delete an agent secret
  afy secrets delete my-agent API_KEY

  # Delete a workspace secret
  afy secrets delete --workspace my-workspace SHARED_API_KEY`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSecretsDelete,
}

func runSecretsDelete(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	var target string
	var key string

	// Determine target and key based on workspace flag
	if workspaceFlag != "" {
		// Workspace mode: only one arg (the key)
		if len(args) != 1 {
			output.PrintError("With --workspace, provide the key name")
			return nil
		}
		target = workspaceFlag
		key = args[0]
	} else {
		// Agent mode: two args (agent and key)
		if len(args) != 2 {
			output.PrintError("Provide agent name and key, or use --workspace flag")
			return nil
		}
		target = args[0]
		key = args[1]
	}

	// Confirm
	if workspaceFlag != "" {
		output.Warning.Printf("Delete secret '%s' from workspace '%s'? [y/N] ", key, target)
	} else {
		output.Warning.Printf("Delete secret '%s' from agent '%s'? [y/N] ", key, target)
	}

	var confirm string
	_, _ = fmt.Scanln(&confirm)
	if strings.ToLower(confirm) != "y" {
		output.PrintInfo("Cancelled")
		return nil
	}

	sp := output.NewSpinner("Deleting secret...")
	sp.Start()

	client := api.NewClient()
	var err error
	if workspaceFlag != "" {
		err = client.DeleteWorkspaceSecret(workspaceFlag, key)
	} else {
		err = client.DeleteSecret(target, key)
	}
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to delete secret: %v", err)
		return nil
	}

	output.PrintSuccess("Secret '%s' deleted", key)
	return nil
}

func init() {
	// Add workspace flag to all commands
	secretsListCmd.Flags().StringVarP(&workspaceFlag, "workspace", "w", "", "Workspace name (for workspace-scoped secrets)")
	secretsSetCmd.Flags().StringVarP(&workspaceFlag, "workspace", "w", "", "Workspace name (for workspace-scoped secrets)")
	secretsDeleteCmd.Flags().StringVarP(&workspaceFlag, "workspace", "w", "", "Workspace name (for workspace-scoped secrets)")

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
}
