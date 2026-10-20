package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// THE PLAN'S LOG WINDOW, END TO END (the shipped command against a stand-in
// control plane). The control plane never returns a log older than the plan's
// retention. A `--since` that reaches past it is NOT an error: the kept logs are
// shown, a sentence on stderr names the plan's retention, and `--json` carries
// the retention.

const oneLog = `[{"id":7,"timestamp":"2026-10-20T10:00:00Z","stream":"stdout","level":"INFO","message":"kept"}]`

func TestASinceBeyondTheRetentionShowsWhatIsKeptAndNamesTheRetentionOnStderr(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog,
		map[string]string{"X-Log-Retention-Days": "7", "X-Log-Since-Clamped": "true"})

	stdout, stderr, err := runAfy(t, cp, "logs", "bot", "--since", "30d")
	if err != nil {
		t.Fatalf("a --since past the retention is not an error: %v", err)
	}
	if !strings.Contains(stdout, "kept") {
		t.Errorf("the kept logs are shown on stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "--since 30d reaches past your plan's log retention (7 days)") {
		t.Errorf("stderr must name the plan's retention, got %q", stderr)
	}
	if strings.Contains(stdout, "retention") {
		t.Errorf("the note is not part of the result on stdout: %q", stdout)
	}
}

func TestASinceInsideTheRetentionSaysNothing(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog, map[string]string{"X-Log-Retention-Days": "7"})

	_, stderr, err := runAfy(t, cp, "logs", "bot", "--since", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "retention") {
		t.Errorf("no note when the window was not cut, got %q", stderr)
	}
}

func TestJSONCarriesThePlansRetentionAndKeepsStdoutOnlyJSON(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog,
		map[string]string{"X-Log-Retention-Days": "30", "X-Log-Since-Clamped": "true"})

	stdout, stderr, err := runAfy(t, cp, "logs", "bot", "--since", "90d", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RetentionDays *int              `json:"retention_days"`
		SinceClamped  bool              `json:"since_clamped"`
		Logs          []json.RawMessage `json:"logs"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if doc.RetentionDays == nil || *doc.RetentionDays != 30 || !doc.SinceClamped || len(doc.Logs) != 1 {
		t.Errorf("document = %+v", doc)
	}
	if !strings.Contains(stderr, "plan's log retention (30 days)") {
		t.Errorf("the note is on stderr in JSON mode too, got %q", stderr)
	}
}

func TestJSONRetentionIsNullOnAPlanThatKeepsLogsWithoutLimit(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, `[]`, map[string]string{"X-Log-Retention-Days": "unlimited"})

	stdout, _, err := runAfy(t, cp, "logs", "bot", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"retention_days": null`) || !strings.Contains(stdout, `"logs": []`) {
		t.Errorf("unlimited is null and an empty read is [], got %s", stdout)
	}
}

func TestARetentionTheControlPlaneDidNotStateFailsTheRead(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, `[]`, map[string]string{"X-Log-Retention-Days": "soon"})

	_, stderr, err := runAfy(t, cp, "logs", "bot")
	if err == nil {
		t.Fatal("a read whose window is unknown must not pass as a success")
	}
	if !strings.Contains(stderr+err.Error(), "X-Log-Retention-Days") {
		t.Errorf("the failure names the header, got %q / %v", stderr, err)
	}
}
