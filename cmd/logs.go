package cmd

import (
	"errors"
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
	// So is the size cap: an account over its stored-log size has its oldest lines
	// removed first, and a read that reaches before them says where the logs now start.
	if page.CapRolledOutThrough != nil {
		output.PrintNote("older lines were removed because your account is over its stored-log size%s; nothing before %s is left.",
			capPhrase(page.MaxBytesStored), page.CapRolledOutThrough.UTC().Format(time.RFC3339))
	}

	// --json: the logs, empty or not, the plan's retention in days (null =
	// negotiated) and whether --since was cut, so a script reads the same shape
	// every time.
	if output.IsJSON() {
		logs := page.Entries
		if logs == nil {
			logs = []api.LogEntry{}
		}
		return output.JSON(logsDocument{Logs: logs, RetentionDays: page.RetentionDays, SinceClamped: page.SinceClamped})
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
	Logs []api.LogEntry `json:"logs"`
	// RetentionDays is the plan's log retention; null = negotiated per contract.
	RetentionDays *int `json:"retention_days"`
	// SinceClamped is true when --since reached past the retention and was cut to it.
	SinceClamped bool `json:"since_clamped"`
}

// retentionPhrase is the retention as a sentence part: "3 days", "1 day".
func retentionPhrase(days *int) string {
	switch {
	case days == nil:
		return "negotiated"
	case *days == 1:
		return "1 day"
	default:
		return fmt.Sprintf("%d days", *days)
	}
}

// capPhrase names the plan's stored-log size in a sentence, or nothing where it is negotiated.
func capPhrase(bytes *int64) string {
	if bytes == nil {
		return ""
	}
	return fmt.Sprintf(" (%s on your plan)", output.FormatBytes(*bytes))
}

// followInterval is the poll period of `afy logs --follow` (a variable so a test can
// shorten it).
var followInterval = 2 * time.Second

func streamLogs(client *api.Client, agentID string) error {
	f := &follower{client: client, agentID: agentID}
	if err := f.seed(); err != nil {
		return err
	}

	output.PrintInfo("Streaming logs for '%s' (Ctrl+C to stop)...", agentID)
	output.Println("")

	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := f.tick(); err != nil {
			return err
		}
	}
	return nil
}

// follower is the state of one `afy logs --follow`: the cursor, and whether the
// current outage has been said yet.
type follower struct {
	client  *api.Client
	agentID string
	afterID int64
	warned  bool
	// cancel, when set, ends a seed that is waiting between attempts (a test stops its
	// goroutine with it; the command itself ends with Ctrl+C, so it leaves this nil).
	cancel <-chan struct{}
}

// seed sets the cursor from the most recent line, so following does not replay history.
// It is the same read as every poll, so it fails the same way: a refusal (a 4xx other
// than a rate limit, an answer the CLI cannot use: an agent that does not exist, a key
// that may not read it) ends the command through the one error path, and a network
// blip or a 5xx is said once and tried again, as the polls after it are. A follow
// started in the middle of a deploy or an outage waits for the control plane instead
// of dying on its first read.
func (f *follower) seed() error {
	for {
		seed, err := f.client.GetAgentLogs(f.agentID, api.LogQuery{Tail: 1, Level: logsLevel, Stream: logsStream, DeploymentID: logsRun})
		if err == nil {
			f.warned = false
			if len(seed.Entries) > 0 {
				f.afterID = seed.Entries[0].ID
			}
			return nil
		}
		if followRefused(err) {
			return printRefusal("Failed to start following logs", err)
		}
		f.warnOnce("Failed to start following logs", err)
		select {
		case <-time.After(followInterval):
		case <-f.cancel:
			return err
		}
	}
}

// warnOnce says a transient failure once per outage.
func (f *follower) warnOnce(what string, err error) {
	if !f.warned {
		output.PrintWarning("%s: %v (still trying)", what, err)
		f.warned = true
	}
}

// tick reads what arrived since the cursor and prints it. A failure the control plane
// refuses (a 4xx other than a rate limit, an answer the CLI cannot use) ends the
// command through the one error path; a network blip or a 5xx is said ONCE per outage
// and followed on past.
func (f *follower) tick() error {
	// When AfterID > 0, the server returns ASC — iterating in order prints
	// lines as they were emitted and lets us advance the cursor exactly.
	page, err := f.client.GetAgentLogs(f.agentID, api.LogQuery{AfterID: f.afterID, Tail: 500, Level: logsLevel, Stream: logsStream, DeploymentID: logsRun})
	if err != nil {
		if followRefused(err) {
			return printRefusal("Failed to fetch logs", err)
		}
		f.warnOnce("Failed to fetch logs", err)
		return nil
	}
	f.warned = false
	for _, log := range page.Entries {
		printLogLine(log, "15:04:05")
		if log.ID > f.afterID {
			f.afterID = log.ID
		}
	}
	return nil
}

// followRefused reports whether a failed read while following is the control
// plane's refusal (a 4xx other than a rate limit) or an answer the CLI cannot use:
// retrying those would repeat the same answer forever.
func followRefused(err error) bool {
	var invalid *api.ResponseInvalidError
	if errors.As(err, &invalid) {
		return true
	}
	status, _, _, ok := serverRefusal(err)
	return ok && status >= 400 && status < 500 && status != 429
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
