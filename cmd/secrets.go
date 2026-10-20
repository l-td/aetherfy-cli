package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
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
			return refuse("Provide agent name or use --workspace flag")
		}
		agentIDOrName := args[0]
		target = agentIDOrName

		sp := output.NewSpinner("Fetching secrets...")
		sp.Start()
		secrets, err = client.ListSecrets(agentIDOrName)
		sp.Stop()
	}

	if err != nil {
		return printRefusal("Failed to list secrets", err)
	}

	// JSON mode: always emit valid JSON, even for empty results
	if output.IsJSON() {
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
			return refuse("Provide agent name or use --workspace flag")
		}
		target = args[0]
		pairs = args[1:]
	}

	// Handle stdin mode
	if secretsStdin {
		if len(pairs) != 1 {
			return refuse("With --stdin, provide exactly one key name")
		}
		key := pairs[0]
		// Remove any = and value part if present
		if idx := strings.Index(key, "="); idx != -1 {
			key = key[:idx]
		}

		if isReservedSecretKey(key) {
			return refuse("Key '%s' is reserved for platform use. Names starting with AETHERFY_ are not allowed.", key)
		}

		// Read value from stdin
		reader := bufio.NewReader(os.Stdin)
		value, err := reader.ReadString('\n')
		if err != nil && err.Error() != "EOF" {
			return printRefusal("Failed to read from stdin", err)
		}
		value = strings.TrimSpace(value)

		// Set secret based on mode
		if workspaceFlag != "" {
			err = client.SetWorkspaceSecret(workspaceFlag, key, value)
		} else {
			err = client.SetSecret(target, key, value)
		}

		if err != nil {
			return printRefusal("Failed to set secret", err)
		}

		output.PrintSuccess("Secret '%s' set successfully", key)
		printSecretEffectNote(target, workspaceFlag != "")
		return output.Result(secretsSetJSON(target, workspaceFlag != "", []secretSetOutcome{{Key: key, Set: true}}))
	}

	// Parse KEY=value pairs
	if len(pairs) == 0 {
		return refuse("Provide at least one KEY=value pair")
	}

	// Whether any pair actually landed, so the effect note is printed once and
	// only when something was stored.
	anySet := false
	var outcomes []secretSetOutcome

	for _, pair := range pairs {
		idx := strings.Index(pair, "=")
		if idx == -1 {
			msg := fmt.Sprintf("Invalid format '%s'. Use KEY=value", pair)
			output.PrintError("%s", msg)
			outcomes = append(outcomes, secretSetOutcome{Key: pair, Error: msg})
			continue
		}

		key := pair[:idx]
		value := pair[idx+1:]

		if key == "" {
			msg := fmt.Sprintf("Key cannot be empty in '%s'", pair)
			output.PrintError("%s", msg)
			outcomes = append(outcomes, secretSetOutcome{Key: pair, Error: msg})
			continue
		}

		if isReservedSecretKey(key) {
			msg := fmt.Sprintf("Key '%s' is reserved for platform use. Names starting with AETHERFY_ are not allowed.", key)
			output.PrintError("%s", msg)
			outcomes = append(outcomes, secretSetOutcome{Key: key, Error: msg})
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
			_ = printRefusal(fmt.Sprintf("Failed to set '%s'", key), err)
			_, code, msg, _ := serverRefusal(err)
			if msg == "" {
				msg = err.Error()
			}
			outcomes = append(outcomes, secretSetOutcome{Key: key, Code: code, Error: msg})
			continue
		}

		output.PrintSuccess("Secret '%s' set", key)
		outcomes = append(outcomes, secretSetOutcome{Key: key, Set: true})
		anySet = true
	}

	if anySet {
		printSecretEffectNote(target, workspaceFlag != "")
	}

	// A pair that was not stored is a failed command: the results are still
	// written (which keys landed), and the exit code says some did not.
	if err := output.Result(secretsSetJSON(target, workspaceFlag != "", outcomes)); err != nil {
		return err
	}
	for _, o := range outcomes {
		if !o.Set {
			return shown(fmt.Errorf("not every secret was set"))
		}
	}
	return nil
}

// secretSetOutcome is one KEY=value of `afy secrets set`: stored, or why not.
type secretSetOutcome struct {
	Key   string `json:"key"`
	Set   bool   `json:"set"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// secretsSetJSON is `afy secrets set --json`.
func secretsSetJSON(target string, workspace bool, outcomes []secretSetOutcome) map[string]interface{} {
	scope := "agent"
	if workspace {
		scope = "workspace"
	}
	if outcomes == nil {
		outcomes = []secretSetOutcome{}
	}
	return map[string]interface{}{"scope": scope, "target": target, "secrets": outcomes}
}

// printSecretEffectNote says WHEN the write the user just made reaches their
// code. Printed after a successful set or delete, because that is where they are
// standing and the answer is not what most people assume.
//
// A secret is bound when a machine is CREATED and that machine keeps it for
// life. Nothing pushes a value into a running process. Until 2026-09-22 the
// platform told people the opposite for task agents ("every run starts a fresh
// machine"), which stopped being true when a task's supervisor started
// surviving between runs; this command said nothing at all, which is why a
// rotated credential could sit unused with no sign anywhere that it had.
//
// ONE MESSAGE FOR BOTH AGENT TYPES, because the CLI does not know which this is
// without another round trip, and the advice does not differ: deploy. The task
// note explains why a deploy is still worth doing when the value technically
// arrives without one.
func printSecretEffectNote(target string, workspace bool) {
	output.Println("")
	if workspace {
		output.Dim.Printf("Takes effect on each agent in workspace '%s' the next time it is deployed.\n", target)
	} else {
		output.Dim.Printf("Takes effect the next time '%s' is deployed: 'afy deploy', 'afy redeploy %s', or a push to a linked repository.\n", target, target)
	}
	output.Dim.Println("A running machine keeps the values it was created with; 'afy rollback' re-deploys an already-built image and does not apply new secrets.")
	output.Dim.Println("A 'type: job' agent gets the value on its next run without a deploy, but pays a cold start on every run until you deploy it.")
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
			return refuse("With --workspace, provide the key name")
		}
		target = workspaceFlag
		key = args[0]
	} else {
		// Agent mode: two args (agent and key)
		if len(args) != 2 {
			return refuse("Provide agent name and key, or use --workspace flag")
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
		return output.Result(map[string]interface{}{"key": key, "deleted": false})
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
		return printRefusal("Failed to delete secret", err)
	}

	output.PrintSuccess("Secret '%s' deleted", key)
	printSecretEffectNote(target, workspaceFlag != "")
	return output.Result(map[string]interface{}{"key": key, "deleted": true})
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

// isReservedSecretKey returns true if the key name is reserved for platform use.
// The AETHERFY_ prefix is reserved; using it would risk shadowing platform-injected
// environment variables inside the agent container.
func isReservedSecretKey(key string) bool {
	return strings.HasPrefix(strings.ToUpper(key), "AETHERFY_")
}
