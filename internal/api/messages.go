package api

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
)

// A RUN'S MAILBOX (agent messaging). Every run has one: any run of its run tree
// can send it a message, and so can a person on the account (the CLI, with the
// account's key). The run reads its own mailbox in order with the helpers'
// receive(); here it is read as history, read or not.

// RunMessage is one message of a run's mailbox, as the history answers it.
type RunMessage struct {
	ID             string          `json:"id"`
	Seq            int64           `json:"seq"`
	Sender         string          `json:"sender"` // "run" or "person"
	SenderRunID    *string         `json:"sender_run_id"`
	Body           json.RawMessage `json:"body"`
	CreatedAt      string          `json:"created_at"`
	AcknowledgedAt *string         `json:"acked_at"`
}

// RunMessagesPage is one page of a mailbox's history.
type RunMessagesPage struct {
	Messages  []RunMessage `json:"messages"`
	NextAfter *int64       `json:"next_after"`
}

// SentMessage is the message a send stored, as the control plane answers it (its SentMessage): its id, its place,
// who sent it and when. Not a history row: a send's answer carries no body and no read mark.
type SentMessage struct {
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
	Sender    string `json:"sender"` // "run" or "person"
	CreatedAt string `json:"created_at"`
}

// SentRunMessage is the answer to a send: the message as stored, and its run.
type SentRunMessage struct {
	Message   SentMessage     `json:"message"`
	Recipient json.RawMessage `json:"recipient"`
}

// ListRunMessages reads one page of a run's mailbox after `after` (a seq): GET
// /deployments/{run}/messages.
func (c *Client) ListRunMessages(runID string, after int64) (*RunMessagesPage, error) {
	var page RunMessagesPage
	path := fmt.Sprintf("/deployments/%s/messages?after=%d", url.PathEscape(runID), after)
	if err := c.Get(path, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// SendRunMessage sends `body` (any JSON value) to a run's mailbox: POST
// /deployments/{run}/messages. The message carries an id of its own, made here,
// so a send repeated after a lost answer is the same message, stored once.
func (c *Client) SendRunMessage(runID string, body json.RawMessage) (*SentRunMessage, error) {
	id, err := newMessageID()
	if err != nil {
		return nil, err
	}
	var resp SentRunMessage
	payload := map[string]any{"id": id, "body": body}
	if err := c.Post("/deployments/"+url.PathEscape(runID)+"/messages", payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// newMessageID is a random (version 4) UUID.
func newMessageID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("a message id could not be made: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
