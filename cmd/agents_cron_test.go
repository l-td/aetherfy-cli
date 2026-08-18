package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

func init() {
	// Deterministic assertions: strip ANSI color so the colorized helpers
	// return their plain text under `go test`.
	output.DisableColors()
}

// buildAetherfyYAML emits `schedule:` (quoted) when set, and omits it entirely
// when empty — schedule-less agents keep the original file shape.
func TestBuildAetherfyYAMLSchedule(t *testing.T) {
	with := buildAetherfyYAML("nightly", "python3.11", "job", "us-east-1", 256, false, false, "main.py", "0 3 * * *")
	if !strings.Contains(with, `schedule: "0 3 * * *"`) {
		t.Errorf("expected quoted schedule line, got:\n%s", with)
	}

	without := buildAetherfyYAML("web", "python3.11", "service", "us-east-1", 256, false, false, "main.py", "")
	if strings.Contains(without, "schedule:") {
		t.Errorf("no schedule expected when empty, got:\n%s", without)
	}
}

func TestFormatRunState(t *testing.T) {
	cases := map[string]string{
		"completed": "completed",
		"failed":    "failed",
		"error":     "failed",
		"queued":    "queued",
		"active":    "active",
		"weird":     "weird",
	}
	for in, want := range cases {
		if got := formatRunState(in); got != want {
			t.Errorf("formatRunState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatUTCTime(t *testing.T) {
	if got := formatUTCTime(nil); got != "-" {
		t.Errorf("nil time: got %q, want -", got)
	}
	ts := time.Date(2026, 7, 17, 3, 0, 0, 0, time.UTC)
	if got := formatUTCTime(&ts); got != "2026-07-17 03:00 UTC" {
		t.Errorf("got %q, want 2026-07-17 03:00 UTC", got)
	}
	// A non-UTC input is normalized to UTC before formatting.
	loc := time.FixedZone("GMT+2", 2*3600)
	shifted := ts.In(loc)
	if got := formatUTCTime(&shifted); got != "2026-07-17 03:00 UTC" {
		t.Errorf("non-UTC input: got %q, want 2026-07-17 03:00 UTC", got)
	}
}

func TestFormatLastRun(t *testing.T) {
	// No fire yet.
	if got := formatLastRun(api.Agent{}); got != "never" {
		t.Errorf("empty: got %q, want never", got)
	}
	// Fired with a recent timestamp -> badge + relative time.
	last := time.Now().Add(-5 * time.Minute)
	got := formatLastRun(api.Agent{CronLastStatus: "fired", CronLastRunAt: &last})
	if !strings.Contains(got, "fired") || !strings.Contains(got, "ago") {
		t.Errorf("fired: got %q, want it to contain 'fired' and 'ago'", got)
	}
}

func TestFormatCronStatusBadge(t *testing.T) {
	cases := map[string]string{
		"fired":   "fired",
		"skipped": "skipped",
		"missed":  "missed",
		"":        "-",
	}
	for in, want := range cases {
		if got := formatCronStatusBadge(in); got != want {
			t.Errorf("formatCronStatusBadge(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatRunDuration(t *testing.T) {
	if got := formatRunDuration(nil); got != "-" {
		t.Errorf("nil duration: got %q, want -", got)
	}
	secs := 65.0
	if got := formatRunDuration(&secs); got != "1m 5s" {
		t.Errorf("65s: got %q, want 1m 5s", got)
	}
}

// parseRunPayload resolves --payload / --payload-file (mutually exclusive) into
// a map, erroring on both-set or invalid JSON.
func TestParseRunPayload(t *testing.T) {
	orig, origFile := runPayload, runPayloadFile
	defer func() { runPayload, runPayloadFile = orig, origFile }()

	// none set -> nil, no error
	runPayload, runPayloadFile = "", ""
	if p, err := parseRunPayload(); err != nil || p != nil {
		t.Errorf("none: p=%v err=%v, want nil,nil", p, err)
	}

	// valid inline JSON
	runPayload, runPayloadFile = `{"k":"v"}`, ""
	p, err := parseRunPayload()
	if err != nil {
		t.Fatalf("valid: unexpected err %v", err)
	}
	if p["k"] != "v" {
		t.Errorf("valid: got %v", p)
	}

	// both set -> error
	runPayload, runPayloadFile = `{"k":"v"}`, "payload.json"
	if _, err := parseRunPayload(); err == nil {
		t.Error("both set: expected error")
	}

	// invalid JSON -> error
	runPayload, runPayloadFile = `{not json`, ""
	if _, err := parseRunPayload(); err == nil {
		t.Error("invalid JSON: expected error")
	}
}

// Flag plumbing: the new flags are registered on their commands.
func TestNewFlagsRegistered(t *testing.T) {
	if logsCmd.Flags().Lookup("run") == nil {
		t.Error("logs --run flag not registered")
	}
	if agentsRunCmd.Flags().Lookup("payload") == nil || agentsRunCmd.Flags().Lookup("wait") == nil {
		t.Error("agents run --payload/--wait flags not registered")
	}
	if agentsRunsCmd.Flags().Lookup("limit") == nil {
		t.Error("agents runs --limit flag not registered")
	}
	if initCmd.Flags().Lookup("schedule") == nil {
		t.Error("init --schedule flag not registered")
	}
}
