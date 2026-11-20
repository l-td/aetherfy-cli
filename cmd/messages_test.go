package cmd

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// `afy messages list <run-id>` reads a run's mailbox, every page of it, in order;
// `afy messages send <run-id> <body>` sends one, the body JSON when it reads as
// JSON and the text otherwise, each send with an id of its own. A run waiting
// for a message (awaiting_message) is shown and followed as a run in progress.

func messageRow(seq int, sender, body, acked string) string {
	senderRun := "null"
	if sender == "run" {
		senderRun = `"run-2"`
	}
	ack := "null"
	if acked != "" {
		ack = `"` + acked + `"`
	}
	return `{"id":"m` + string(rune('0'+seq)) + `","seq":` + string(rune('0'+seq)) + `,"sender":"` + sender +
		`","sender_run_id":` + senderRun + `,"body":` + body + `,"created_at":"2026-11-19T10:00:0` +
		string(rune('0'+seq)) + `Z","acked_at":` + ack + `}`
}

// sentAnswer is the control plane's answer to a send (its MessageSentResponse): the stored message's id, seq, sender
// and time, and the run it went to. Not a history row: a send's answer has no body and no read mark.
func sentAnswer(seq int) string {
	return `{"message":{"id":"m` + string(rune('0'+seq)) + `","seq":` + string(rune('0'+seq)) +
		`,"sender":"person","created_at":"2026-11-19T10:00:0` + string(rune('0'+seq)) + `Z"},` +
		`"recipient":{"id":"run-1","agent_id":"a-1","agent":"reviewer","state":"awaiting_message"}}`
}

func TestASendsJSONOutputIsTheControlPlanesAnswerAndNothingMore(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/deployments/run-1/messages", 201, sentAnswer(2))
	stdout, stderr, err := runAfy(t, cp, "messages", "send", "run-1", "hi", "--json")
	if err != nil {
		t.Fatalf("send --json: %v\n%s", err, stderr)
	}
	var got struct {
		Message map[string]json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("send --json printed no JSON object: %v\n%s", err, stdout)
	}
	keys := make([]string, 0, len(got.Message))
	for k := range got.Message {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "created_at,id,sender,seq" {
		t.Errorf("the sent message carries fields the control plane never sent: %v", keys)
	}
}

func TestMessagesReadsEveryPageInOrder(t *testing.T) {
	cp := newFakeCP(t)
	cp.onSequence("GET", "/deployments/run-1/messages",
		cpAnswer{status: 200, body: `{"messages":[` + messageRow(1, "person", `"hello"`, "2026-11-19T10:01:00Z") +
			`],"next_after":1}`},
		cpAnswer{status: 200, body: `{"messages":[` + messageRow(2, "run", `{"progress":0.5}`, "") +
			`],"next_after":null}`},
	)
	stdout, stderr, err := runAfy(t, cp, "messages", "list", "run-1")
	if err != nil {
		t.Fatalf("messages: %v\n%s", err, stderr)
	}
	first, second := strings.Index(stdout, "#1 "), strings.Index(stdout, "#2 ")
	if first < 0 || second < first {
		t.Fatalf("both messages, in order, were not printed:\n%s", stdout)
	}
	if !strings.Contains(stdout, `from the account`) || !strings.Contains(stdout, `"hello"`) ||
		!strings.Contains(stdout, `from run run-2`) || !strings.Contains(stdout, `{"progress":0.5}`) {
		t.Errorf("a message's sender or body is missing:\n%s", stdout)
	}
	calls := cp.called("GET", "/deployments/run-1/messages")
	if len(calls) != 2 || calls[0].Query != "after=0" || calls[1].Query != "after=1" {
		t.Errorf("the pages were not read in order from after=0: %+v", calls)
	}
}

func TestMessagesSendSendsJSONAsJSONAndTextAsText(t *testing.T) {
	for arg, want := range map[string]interface{}{
		`{"approved":true}`: map[string]interface{}{"approved": true},
		`42`:                float64(42),
		`yes please`:        "yes please",
	} {
		cp := newFakeCP(t)
		cp.on("POST", "/deployments/run-1/messages", 201, sentAnswer(3))
		stdout, stderr, err := runAfy(t, cp, "messages", "send", "run-1", arg)
		if err != nil {
			t.Fatalf("send %q: %v\n%s", arg, err, stderr)
		}
		if !strings.Contains(stdout, "Message 3 sent to run run-1.") {
			t.Errorf("send %q: the send is not said:\n%s", arg, stdout)
		}
		calls := cp.called("POST", "/deployments/run-1/messages")
		if len(calls) != 1 {
			t.Fatalf("send %q: %d sends, want 1", arg, len(calls))
		}
		got := calls[0].Body["body"]
		if gotMap, ok := got.(map[string]interface{}); ok {
			if wantMap, ok := want.(map[string]interface{}); !ok || gotMap["approved"] != wantMap["approved"] {
				t.Errorf("send %q: body %v, want %v", arg, got, want)
			}
		} else if got != want {
			t.Errorf("send %q: body %#v, want %#v", arg, got, want)
		}
	}
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestEachSendCarriesAnIdOfItsOwn(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/deployments/run-1/messages", 201, sentAnswer(1))
	for i := 0; i < 2; i++ {
		if _, stderr, err := runAfy(t, cp, "messages", "send", "run-1", "a"); err != nil {
			t.Fatalf("send: %v\n%s", err, stderr)
		}
	}
	calls := cp.called("POST", "/deployments/run-1/messages")
	if len(calls) != 2 {
		t.Fatalf("%d sends, want 2", len(calls))
	}
	a, _ := calls[0].Body["id"].(string)
	b, _ := calls[1].Body["id"].(string)
	if !uuidV4.MatchString(a) || !uuidV4.MatchString(b) || a == b {
		t.Errorf("each send must carry its own UUID: %q, %q", a, b)
	}
}

func TestARunAwaitingAMessageIsFollowedAsARunInProgress(t *testing.T) {
	if !inFlightRunStates["awaiting_message"] {
		t.Error("`afy run --wait` would stop following a run that waits for a message")
	}
	if got := plainRunState("awaiting_message", nil); got != "awaiting_message" {
		t.Errorf("the state is shown as %q, want awaiting_message", got)
	}
}

func TestAMessageSentWithTheAccountsKeyIsShownAsTheAccountsNeverAsARuns(t *testing.T) {
	// The sender is the server's `sender` field: a person's message is the
	// account's even if a run id were ever carried beside it.
	cp := newFakeCP(t)
	row := strings.Replace(messageRow(1, "person", `"approved"`, ""), `"sender_run_id":null`, `"sender_run_id":"run-9"`, 1)
	cp.on("GET", "/deployments/run-1/messages", 200, `{"messages":[`+row+`],"next_after":null}`)
	stdout, stderr, err := runAfy(t, cp, "messages", "list", "run-1")
	if err != nil {
		t.Fatalf("messages list: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "from the account") || strings.Contains(stdout, "from run") {
		t.Errorf("a message sent with the account's key must read as the account's:\n%s", stdout)
	}
}
