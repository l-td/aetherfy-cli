package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/config"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

var spawnCmd = &cobra.Command{
	Use:   "spawn <parent-agent> <child-agent>",
	Short: "Spawn a JOB agent from a parent agent",
	Long: `Spawn a JOB agent from a parent SERVICE agent.

This triggers an ephemeral execution of the child JOB agent. The JOB agent
will run once and terminate when complete. Payload data is passed via
AETHERFY_SPAWN_PAYLOAD environment variable.

The parent agent must have spawn_enabled=true.
The child agent must be of type JOB.`,
	Example: `  # Spawn a JOB agent with JSON payload
  afy spawn my-service my-job --payload '{"task": "process", "id": 123}'

  # Spawn with payload from file
  afy spawn my-service my-job --payload-file payload.json

  # Read payload from stdin
  cat payload.json | afy spawn my-service my-job --stdin`,
	Args: cobra.ExactArgs(2),
	RunE: runSpawn,
}

var (
	spawnPayload     string
	spawnPayloadFile string
	spawnStdin       bool
)

func init() {
	spawnCmd.Flags().StringVarP(&spawnPayload, "payload", "p", "", "JSON payload to pass to the spawned agent")
	spawnCmd.Flags().StringVarP(&spawnPayloadFile, "payload-file", "f", "", "Read payload from JSON file")
	spawnCmd.Flags().BoolVar(&spawnStdin, "stdin", false, "Read payload from stdin")
}

func runSpawn(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	parentAgentID := args[0]
	childAgentID := args[1]

	// Build payload
	var payload map[string]interface{}

	// Check payload sources (only one allowed)
	sources := 0
	if spawnPayload != "" {
		sources++
	}
	if spawnPayloadFile != "" {
		sources++
	}
	if spawnStdin {
		sources++
	}

	if sources > 1 {
		output.PrintError("Specify only one of --payload, --payload-file, or --stdin")
		return nil
	}

	if spawnPayload != "" {
		// Parse JSON payload from flag
		if err := json.Unmarshal([]byte(spawnPayload), &payload); err != nil {
			output.PrintError("Invalid JSON payload: %v", err)
			return nil
		}
	} else if spawnPayloadFile != "" {
		// Read payload from file
		data, err := os.ReadFile(spawnPayloadFile)
		if err != nil {
			output.PrintError("Failed to read payload file: %v", err)
			return nil
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			output.PrintError("Invalid JSON in payload file: %v", err)
			return nil
		}
	} else if spawnStdin {
		// Read payload from stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			output.PrintError("Failed to read from stdin: %v", err)
			return nil
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &payload); err != nil {
				output.PrintError("Invalid JSON from stdin: %v", err)
				return nil
			}
		}
	}

	// Build request
	req := &api.SpawnRequest{
		ChildAgentID: childAgentID,
		Payload:      payload,
	}

	sp := output.NewSpinner(fmt.Sprintf("Spawning agent '%s' from '%s'...", childAgentID, parentAgentID))
	sp.Start()

	client := api.NewClient()
	resp, err := client.SpawnAgent(parentAgentID, req)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to spawn agent: %v", err)
		return nil
	}

	// Check output format
	if config.Get().OutputFormat == "json" {
		return output.JSON(resp)
	}

	output.PrintSuccess("Agent spawned successfully!")
	output.Println("")
	output.KeyValue("Spawn ID", resp.SpawnID)
	output.KeyValue("Deployment ID", resp.DeploymentID)
	if resp.MachineID != "" {
		output.KeyValue("Machine ID", resp.MachineID)
	}
	output.KeyValue("Status", formatStatus(resp.Status))

	if resp.Message != "" {
		output.Println("")
		output.Dim.Println(resp.Message)
	}

	output.Println("")
	output.Println("The spawned agent will run once and terminate when complete.")
	output.Println("Use 'afy logs " + childAgentID + "' to view logs.")

	return nil
}
