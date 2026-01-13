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
	Use:   "list <agent>",
	Short: "List secrets for an agent",
	Long:  "List all secrets (keys only) for an agent.",
	Args:  cobra.ExactArgs(1),
	RunE:  runSecretsList,
}

func runSecretsList(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	agentIDOrName := args[0]

	sp := output.NewSpinner("Fetching secrets...")
	sp.Start()

	client := api.NewClient()
	secrets, err := client.ListSecrets(agentIDOrName)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to list secrets: %v", err)
		return nil
	}

	if len(secrets) == 0 {
		output.PrintInfo("No secrets found for agent '%s'", agentIDOrName)
		output.Println("")
		output.Println("Set a secret with: afy secrets set " + agentIDOrName + " KEY=value")
		return nil
	}

	// Check output format
	if config.Get().OutputFormat == "json" {
		return output.JSON(secrets)
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
	Use:   "set <agent> <KEY=value>...",
	Short: "Set a secret",
	Long: `Set one or more secrets for an agent.

Secrets can be provided as KEY=value pairs or read from stdin.
Values are encrypted at rest and injected as environment variables.`,
	Example: `  # Set a single secret
  afy secrets set my-agent API_KEY=sk-xxxxx

  # Set multiple secrets
  afy secrets set my-agent API_KEY=sk-xxxxx DB_URL=postgres://...

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

	agentIDOrName := args[0]
	pairs := args[1:]

	client := api.NewClient()

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

		if err := client.SetSecret(agentIDOrName, key, value); err != nil {
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

		if err := client.SetSecret(agentIDOrName, key, value); err != nil {
			output.PrintError("Failed to set '%s': %v", key, err)
			continue
		}

		output.PrintSuccess("Secret '%s' set", key)
	}

	return nil
}

// --- DELETE ---
var secretsDeleteCmd = &cobra.Command{
	Use:   "delete <agent> <key>",
	Short: "Delete a secret",
	Long:  "Delete a secret from an agent.",
	Args:  cobra.ExactArgs(2),
	RunE:  runSecretsDelete,
}

func runSecretsDelete(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	agentIDOrName := args[0]
	key := args[1]

	// Confirm
	output.Warning.Printf("Delete secret '%s' from agent '%s'? [y/N] ", key, agentIDOrName)
	var confirm string
	_, _ = fmt.Scanln(&confirm)
	if strings.ToLower(confirm) != "y" {
		output.PrintInfo("Cancelled")
		return nil
	}

	sp := output.NewSpinner("Deleting secret...")
	sp.Start()

	client := api.NewClient()
	err := client.DeleteSecret(agentIDOrName, key)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to delete secret: %v", err)
		return nil
	}

	output.PrintSuccess("Secret '%s' deleted", key)
	return nil
}

func init() {
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
}
