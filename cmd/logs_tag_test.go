package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// A PLATFORM LINE PRINTS ITS TAG ONCE. The control plane's lines carry their
// tag in the record -- stream "system", level SYSTEM (or ERROR) -- and not in
// their text; `afy logs` printed "[<level>] <message>", and the message used to
// start with "[SYSTEM] " as well, so every platform line read
// "[SYSTEM] [SYSTEM] ...". A platform ERROR must still not read like the
// customer's own ERROR: it prints as "[SYSTEM ERROR]".
func TestAPlatformLinePrintsItsTagOnceAndAPlatformErrorStaysDistinct(t *testing.T) {
	output.DisableColors()
	defer output.EnableColors()
	ts := time.Date(2026, 9, 27, 9, 8, 31, 0, time.UTC)
	cases := []struct {
		entry api.LogEntry
		want  string
	}{
		{api.LogEntry{Timestamp: ts, Stream: "system", Level: "SYSTEM",
			Message: "Aetherfy log forwarder active"},
			"2026-09-27 09:08:31 [SYSTEM] Aetherfy log forwarder active"},
		{api.LogEntry{Timestamp: ts, Stream: "system", Level: "ERROR",
			Message: "Aetherfy run r1 could not hold its machine awake"},
			"2026-09-27 09:08:31 [SYSTEM ERROR] Aetherfy run r1 could not hold its machine awake"},
		// The customer's own lines are printed exactly as before.
		{api.LogEntry{Timestamp: ts, Stream: "stderr", Level: "ERROR", Message: "boom"},
			"2026-09-27 09:08:31 [ERROR] boom"},
		{api.LogEntry{Timestamp: ts, Stream: "stdout", Level: "INFO", Message: "hello"},
			"2026-09-27 09:08:31 [INFO] hello"},
	}
	for _, tc := range cases {
		got := strings.TrimRight(captureStdout(t, func() {
			printLogLine(tc.entry, "2006-01-02 15:04:05")
		}), "\n")
		if got != tc.want {
			t.Fatalf("printed %q, want %q", got, tc.want)
		}
		if strings.Count(got, "SYSTEM") > 1 {
			t.Fatalf("the platform tag printed more than once: %q", got)
		}
	}
}
