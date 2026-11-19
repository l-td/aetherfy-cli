package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// A RUN'S MAILBOX, from the terminal (agent messaging). A run reads its own
// mailbox with the helpers' receive(); a person sends it a message here, and
// reads its whole history, read or not, in order.

var messagesCmd = &cobra.Command{
	Use:   "messages",
	Short: "Read a run's messages, or send it one",
	Long: `A run's mailbox. Every run has one: the runs of its run tree can send it
messages, and so can you. The run reads them in order with receive(); a run
waiting for one (its state is awaiting_message) takes the next one it is sent.

The run's id is in 'afy runs <name>'.`,
}

var messagesListCmd = &cobra.Command{
	Use:   "list <run-id>",
	Short: "Every message a run was sent, in order",
	Long: `Read a run's mailbox: every message it was sent, in order, read or not,
with who sent it and whether the run has read it.`,
	Args: cobra.ExactArgs(1),
	RunE: runMessagesList,
}

var messagesSendCmd = &cobra.Command{
	Use:   "send <run-id> <body>",
	Short: "Send a message to a run",
	Long: `Send a message to a run's mailbox. The body is JSON when it reads as JSON
('{"approved": true}', '42', '"yes"'), and otherwise the text as given.

The message is stored once, and the run reads it with receive(); a run waiting
for one wakes. A run that has ended takes no messages.`,
	Args: cobra.ExactArgs(2),
	RunE: runMessagesSend,
}

func init() {
	messagesCmd.AddCommand(messagesListCmd)
	messagesCmd.AddCommand(messagesSendCmd)
}

// messageBody is the body as JSON: the argument itself when it parses as JSON,
// else the text as a JSON string.
func messageBody(arg string) json.RawMessage {
	trimmed := strings.TrimSpace(arg)
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	encoded, _ := json.Marshal(arg)
	return json.RawMessage(encoded)
}

func runMessagesSend(cmd *cobra.Command, args []string) error {
	runID, body := args[0], messageBody(args[1])
	client := api.NewClient()
	sp := output.NewSpinner(fmt.Sprintf("Sending a message to run %s...", runID))
	sp.Start()
	resp, err := client.SendRunMessage(runID, body)
	sp.Stop()
	if err != nil {
		return printRefusal("Failed to send the message", err)
	}
	output.PrintSuccess("Message %d sent to run %s.", resp.Message.Seq, runID)
	return output.Result(resp)
}

func runMessagesList(cmd *cobra.Command, args []string) error {
	runID := args[0]
	client := api.NewClient()
	var all []api.RunMessage
	var after int64
	for {
		page, err := client.ListRunMessages(runID, after)
		if err != nil {
			return printRefusal("Failed to read the run's messages", err)
		}
		all = append(all, page.Messages...)
		if page.NextAfter == nil {
			break
		}
		after = *page.NextAfter
	}
	if output.IsJSON() {
		return output.Result(map[string]any{"run_id": runID, "messages": all})
	}
	if len(all) == 0 {
		output.PrintInfo("Run %s has no messages.", runID)
		return nil
	}
	for _, m := range all {
		output.Println(formatRunMessage(m))
	}
	return nil
}

// formatRunMessage is one line: its place, who sent it, when, whether the run
// has read it, and the body as it was sent.
func formatRunMessage(m api.RunMessage) string {
	from := "a person"
	if m.Sender == "run" && m.SenderRunID != nil {
		from = "run " + *m.SenderRunID
	}
	read := output.Dim.Sprint("unread")
	if m.AcknowledgedAt != nil {
		read = "read"
	}
	return fmt.Sprintf("#%d  %s  from %s  %s  %s", m.Seq, m.CreatedAt, from, read, string(m.Body))
}
