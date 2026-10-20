package output

import (
	"encoding/json"
	"io"
	"os"
	"reflect"

	"github.com/fatih/color"
)

// THE JSON CONTRACT, in one place. With --json:
//
//   - stdout carries ONLY JSON: the command's result, written by JSON/Result
//     and by nothing else;
//   - everything meant for a person -- progress, hints, warnings, prompts, the
//     human text of a success -- goes to stderr (Out), so a script that pipes
//     stdout never has to filter it;
//   - an error is one JSON object on stderr, {"error":{"status","code",
//     "message"}}, the code the server's when it sent one (PrintAPIError).
//
// Without --json nothing changes: text on stdout, text errors on stderr.
// The mode is set once per run (SetJSON, from the root command's flag).

var (
	jsonMode bool
	emitted  bool
	// midLine is whether the last byte this package wrote to stderr in JSON
	// mode was not a newline (a prompt waiting for an answer): an error object
	// must start on a line of its own, or a script reading stderr line by line
	// sees the prompt glued to it.
	midLine bool
)

// stderrText is stderr as the human text of a JSON-mode run uses it.
type stderrText struct{}

func (stderrText) Write(p []byte) (int, error) {
	n, err := os.Stderr.Write(p)
	if n > 0 {
		midLine = p[n-1] != '\n'
	}
	return n, err
}

// SetJSON switches the run into (or out of) JSON mode, and forgets any result
// written by a previous run in the same process.
func SetJSON(on bool) {
	jsonMode = on
	emitted = false
	midLine = false
}

// IsJSON reports whether the run is in JSON mode.
func IsJSON() bool { return jsonMode }

// Emitted reports whether a JSON result has been written this run.
func Emitted() bool { return emitted }

// Out is where text meant for a person goes: stdout, or stderr in JSON mode.
// Evaluated at each write, so a test that swaps os.Stdout/os.Stderr is seen.
func Out() io.Writer {
	if jsonMode {
		return stderrText{}
	}
	return os.Stdout
}

// colorOut is Out for coloured text: the colour library's own writers (which
// translate ANSI on legacy Windows consoles) in text mode, stderr in JSON mode.
func colorOut() io.Writer {
	if jsonMode {
		return stderrText{}
	}
	return color.Output
}

// JSON writes data to stdout as indented JSON, the one place stdout gets
// anything in JSON mode. A nil slice is written as [], never null: a script
// reads the same shape for an empty list as for a full one.
func JSON(data interface{}) error {
	emitted = true
	if rv := reflect.ValueOf(data); rv.IsValid() && rv.Kind() == reflect.Slice && rv.IsNil() {
		data = []interface{}{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// JSONLine writes data to stdout as one compact JSON line: the form of a
// stream (afy logs --follow), where each line is a result of its own.
func JSONLine(data interface{}) error {
	emitted = true
	return json.NewEncoder(os.Stdout).Encode(data)
}

// Result is JSON for a command that has a human-text success of its own: in
// JSON mode it writes the result, in text mode it does nothing (the command
// has already said what happened in words).
func Result(data interface{}) error {
	if !jsonMode {
		return nil
	}
	return JSON(data)
}

// ErrorBody is the error a command reports with --json.
type ErrorBody struct {
	Status  int    `json:"status,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// PrintAPIError reports a refusal: the message alone as text ("Error: ..."),
// or in JSON mode the whole {"error":{status,code,message}} on one stderr
// line. status and code are the server's, 0 and "" when it sent none.
func PrintAPIError(status int, code, message string) {
	if jsonMode {
		if midLine {
			_, _ = os.Stderr.Write([]byte("\n"))
			midLine = false
		}
		_ = json.NewEncoder(os.Stderr).Encode(struct {
			Error ErrorBody `json:"error"`
		}{ErrorBody{Status: status, Code: code, Message: message}})
		return
	}
	Error.Fprintf(os.Stderr, "Error: %s\n", message)
}
