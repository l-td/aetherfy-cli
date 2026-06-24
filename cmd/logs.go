package cmd

import (
	"fmt"
	"time"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/config"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs <agent>",
	Short: "View agent logs",
	Long: `View logs from an agent.

By default, shows the most recent logs. Use --tail to specify the number of lines.
Use --follow to stream logs in real-time.`,
	Example: `  # View recent logs
  afy logs my-agent

  # View last 100 lines
  afy logs my-agent --tail 100

  # Stream logs in real-time
  afy logs my-agent --follow`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

var (
	logsTail   int
	logsFollow bool
	logsSince  string
	logsLevel  string
	logsStream string
)

func init() {
	logsCmd.Flags().IntVarP(&logsTail, "tail", "n", 50, "Number of lines to show")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output (stream)")
	logsCmd.Flags().StringVar(&logsSince, "since", "", "Show logs since duration (e.g., 1h, 30m)")
	logsCmd.Flags().StringVar(&logsLevel, "level", "", "Filter by level(s), comma-separated (e.g., ERROR,WARN)")
	logsCmd.Flags().StringVar(&logsStream, "stream", "", "Filter by stream(s), comma-separated (stdout,stderr,system)")
}

func runLogs(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	agentID := args[0]

	client := api.NewClient()

	if logsFollow {
		return streamLogs(client, agentID)
	}

	// Fetch logs
	sp := output.NewSpinner("Fetching logs...")
	sp.Start()

	logs, err := client.GetAgentLogs(agentID, api.LogQuery{
		Tail:   logsTail,
		Since:  logsSince,
		Level:  logsLevel,
		Stream: logsStream,
	})
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to get logs: %v", err)
		return nil
	}

	if len(logs) == 0 {
		output.PrintInfo("No logs found for agent '%s'", agentID)
		return nil
	}

	// Check output format
	if config.Get().OutputFormat == "json" {
		return output.JSON(logs)
	}

	// Print logs
	for _, log := range logs {
		timestamp := log.Timestamp.Format("2006-01-02 15:04:05")
		levelColor := getLevelColor(log.Level)
		output.Dim.Printf("%s ", timestamp)
		levelColor.Printf("[%s] ", log.Level)
		fmt.Println(log.Message)
	}

	return nil
}

func streamLogs(client *api.Client, agentID string) error {
	output.PrintInfo("Streaming logs for '%s' (Ctrl+C to stop)...", agentID)
	output.Println("")

	// Seed the cursor from the most recent batch so we don't replay history.
	var afterID int64
	seed, err := client.GetAgentLogs(agentID, api.LogQuery{Tail: 1, Level: logsLevel, Stream: logsStream})
	if err == nil && len(seed) > 0 {
		afterID = seed[0].ID
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// When AfterID > 0, the server returns ASC — iterating in order prints
		// lines as they were emitted and lets us advance the cursor exactly.
		logs, err := client.GetAgentLogs(agentID, api.LogQuery{AfterID: afterID, Tail: 500, Level: logsLevel, Stream: logsStream})
		if err != nil {
			output.PrintWarning("Failed to fetch logs: %v", err)
			continue
		}
		for _, log := range logs {
			timestamp := log.Timestamp.Format("15:04:05")
			levelColor := getLevelColor(log.Level)
			output.Dim.Printf("%s ", timestamp)
			levelColor.Printf("[%s] ", log.Level)
			fmt.Println(log.Message)
			if log.ID > afterID {
				afterID = log.ID
			}
		}
	}
	return nil
}

func getLevelColor(level string) *output.ColorPrinter {
	switch level {
	case "ERROR", "FATAL", "CRITICAL":
		return (*output.ColorPrinter)(output.Red)
	case "WARN", "WARNING":
		return (*output.ColorPrinter)(output.Yellow)
	case "INFO":
		return (*output.ColorPrinter)(output.Cyan)
	case "DEBUG", "TRACE":
		return (*output.ColorPrinter)(output.Gray)
	default:
		return (*output.ColorPrinter)(output.White)
	}
}
