package cmd

import (
	"fmt"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs <agent>",
	Short: "View agent logs",
	Long: `View logs from an agent.

By default, shows the most recent logs. Use --tail to specify the number of lines.
Use --follow to stream logs in real-time. Narrow the output with --level and/or
--stream (comma-separated, applied server-side).`,
	Example: `  # View recent logs
  afy logs my-agent

  # View last 100 lines
  afy logs my-agent --tail 100

  # Stream logs in real-time
  afy logs my-agent --follow

  # Only errors and warnings
  afy logs my-agent --level ERROR,WARN

  # Only stderr output, since the last hour
  afy logs my-agent --stream stderr --since 1h`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

var (
	logsTail   int
	logsFollow bool
	logsSince  string
	logsLevel  string
	logsStream string
	logsRun    string
)

func init() {
	logsCmd.Flags().IntVarP(&logsTail, "tail", "n", 50, "Number of lines to show")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output (stream)")
	logsCmd.Flags().StringVar(&logsSince, "since", "", "Show logs since duration (e.g., 1h, 30m)")
	logsCmd.Flags().StringVar(&logsLevel, "level", "", "Filter by level(s), comma-separated (e.g., ERROR,WARN)")
	logsCmd.Flags().StringVar(&logsStream, "stream", "", "Filter by stream(s), comma-separated (stdout,stderr,system)")
	logsCmd.Flags().StringVar(&logsRun, "run", "", "Scope logs to a single run ID (from 'afy runs')")
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

	page, err := client.GetAgentLogs(agentID, api.LogQuery{
		Tail:         logsTail,
		Since:        logsSince,
		Level:        logsLevel,
		Stream:       logsStream,
		DeploymentID: logsRun,
	})
	sp.Stop()

	if err != nil {
		return printRefusal("Failed to get logs", err)
	}

	// A --since past the plan's retention is not an error: what is kept is
	// shown, and this says why it starts where it does.
	if page.SinceClamped {
		output.PrintNote("--since %s reaches past your plan's log retention (%s); showing what is kept.",
			logsSince, retentionPhrase(page.RetentionDays))
	}

	// --json: the logs, empty or not, and the plan's retention in days (null =
	// unlimited), so a script reads the same shape every time.
	if output.IsJSON() {
		logs := page.Entries
		if logs == nil {
			logs = []api.LogEntry{}
		}
		return output.JSON(logsDocument{RetentionDays: page.RetentionDays, SinceClamped: page.SinceClamped, Logs: logs})
	}

	logs := page.Entries
	if len(logs) == 0 {
		output.PrintInfo("No logs found for agent '%s'", agentID)
		return nil
	}

	// Print logs
	for _, log := range logs {
		printLogLine(log, "2006-01-02 15:04:05")
	}

	return nil
}

// logsDocument is `afy logs --json`: the logs and the plan's window on them.
type logsDocument struct {
	// RetentionDays is the plan's log retention; null = the plan keeps logs
	// without limit.
	RetentionDays *int `json:"retention_days"`
	// SinceClamped is true when --since reached past the retention and was cut to it.
	SinceClamped bool           `json:"since_clamped"`
	Logs         []api.LogEntry `json:"logs"`
}

// retentionPhrase is the retention as a sentence part: "7 days", "1 day".
func retentionPhrase(days *int) string {
	switch {
	case days == nil:
		return "unlimited"
	case *days == 1:
		return "1 day"
	default:
		return fmt.Sprintf("%d days", *days)
	}
}

func streamLogs(client *api.Client, agentID string) error {
	output.PrintInfo("Streaming logs for '%s' (Ctrl+C to stop)...", agentID)
	output.Println("")

	// Seed the cursor from the most recent batch so we don't replay history.
	var afterID int64
	seed, err := client.GetAgentLogs(agentID, api.LogQuery{Tail: 1, Level: logsLevel, Stream: logsStream, DeploymentID: logsRun})
	if err == nil && len(seed.Entries) > 0 {
		afterID = seed.Entries[0].ID
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// When AfterID > 0, the server returns ASC — iterating in order prints
		// lines as they were emitted and lets us advance the cursor exactly.
		page, err := client.GetAgentLogs(agentID, api.LogQuery{AfterID: afterID, Tail: 500, Level: logsLevel, Stream: logsStream, DeploymentID: logsRun})
		if err != nil {
			output.PrintWarning("Failed to fetch logs: %v", err)
			continue
		}
		for _, log := range page.Entries {
			printLogLine(log, "15:04:05")
			if log.ID > afterID {
				afterID = log.ID
			}
		}
	}
	return nil
}

// systemStream is the control plane's platform stream: the one thing that tells
// a platform line from the customer's own output. Pinned to the control plane's
// models/agent_log.SYSTEM_LOG_STREAM by TestTheSystemStreamIsTheControlPlanes.
const systemStream = "system"

// printLogLine prints one stored line as `<time> [<tag>] <message>`.
func printLogLine(log api.LogEntry, timeLayout string) {
	// --json with --follow is a stream, one compact JSON object per line.
	if output.IsJSON() {
		_ = output.JSONLine(log)
		return
	}
	output.Dim.Printf("%s ", log.Timestamp.Format(timeLayout))
	getLevelColor(log.Level).Printf("[%s] ", logTag(log))
	fmt.Fprintln(output.Out(), log.Message)
}

// logTag is what goes in the brackets: the line's level, and for a line the
// PLATFORM wrote at a level of its own (stream "system", level other than
// SYSTEM -- today its ERROR lines) "SYSTEM <level>", so a platform error never
// reads like the customer's own. The tag is built from the record's fields and
// never from its text: platform lines carry none in their message, which is
// what used to print "[SYSTEM] [SYSTEM] ...".
func logTag(log api.LogEntry) string {
	if log.Stream == systemStream && log.Level != "" && log.Level != "SYSTEM" {
		return "SYSTEM " + log.Level
	}
	return log.Level
}

func getLevelColor(level string) *output.Printer {
	switch level {
	case "ERROR", "FATAL", "CRITICAL":
		return output.Red
	case "WARN", "WARNING":
		return output.Yellow
	case "INFO":
		return output.Cyan
	case "DEBUG", "TRACE":
		return output.Gray
	default:
		return output.White
	}
}
