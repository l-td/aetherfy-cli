package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// EVERY QUESTION A COMMAND ASKS goes through the one helper (cmd/confirm.go), so
// the one exit rule holds: no terminal and no skip flag is exit 2 before asking, a
// "no" is exit 1 with CLI_CANCELLED, never 0. The shape that broke the rule twice (a
// deploy that printed "cancelled" and exited 0, an init that printed "Aborted." and
// exited 0) was a command reading its own answer from stdin.
//
// So any READ of stdin in cmd/ outside the helper is refused here, by file: a
// bufio reader or scanner, a fmt.Scan*, an io.ReadAll/io.Copy of stdin, an
// os.Stdin.Read. (os.Stdin.Fd(), the terminal check, reads nothing.) The files
// below are the ones that read stdin for a reason that is not a confirmation, each
// with its reason; the guard also fails when an allow-listed file no longer reads,
// so the list cannot outlive what it excuses.

var stdinReaders = regexp.MustCompile(
	`fmt\.Fscan\w*\(|fmt\.Scan\w*\(|bufio\.New(Reader|Scanner)\(|` +
		`io\.(ReadAll|Copy)\([^)]*[sS]tdin|os\.Stdin\.Read|promptYesNo`)

var stdinReadAllowed = map[string]string{
	"confirm.go": "the helper itself: vecPrompter wraps a vector command's stdin for the question askToConfirm asks",
	"prompt.go":  "the one prompter over stdin that the helper's callers, init's pickers and the workspace picker share",
	"login.go":   "the API key entry: a secret typed or piped, not a confirmation",
	"secrets.go": "a secret's value read from stdin (--stdin), not a confirmation",
	"spawn.go":   "the spawn payload read from stdin: data, not a question",
	"vectors.go": "the workspace picker of the vector commands: a choice among workspaces, not a confirmation",
}

// stdinReadersIn lists the stdin-reading constructs in one file's source, comments aside.
func stdinReadersIn(src string) []string {
	var found []string
	for i, line := range strings.Split(src, "\n") {
		code := line
		if c := strings.Index(code, "//"); c >= 0 {
			code = code[:c]
		}
		if m := stdinReaders.FindString(code); m != "" {
			found = append(found, m+" at line "+strconv.Itoa(i+1))
		}
	}
	return found
}

func TestNoCommandReadsItsOwnConfirmation(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source found (%v): the guard would pass over nothing", err)
	}
	scanned := 0
	reads := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		found := stdinReadersIn(string(raw))
		if len(found) > 0 {
			reads[f] = true
		}
		if _, ok := stdinReadAllowed[f]; ok {
			continue
		}
		for _, v := range found {
			t.Errorf("%s reads stdin itself (%s): ask through confirmDestructive / confirmDestructiveOn / "+
				"askToConfirm (cmd/confirm.go), or, if it is not a confirmation, add the file to "+
				"stdinReadAllowed with its reason", f, v)
		}
	}
	if scanned < 20 {
		t.Fatalf("only %d source files scanned", scanned)
	}
	for f, why := range stdinReadAllowed {
		if !reads[f] {
			t.Errorf("%s is allow-listed (%s) but reads no stdin any more: remove it from stdinReadAllowed", f, why)
		}
	}
}

// Positive control: every construct the guard names is seen, and a comment that
// names one, or the terminal check, is not.
func TestTheConfirmationGuardSeesEveryStdinReader(t *testing.T) {
	for _, src := range []string{
		"var a string; fmt.Scanln(&a)",
		"fmt.Scanf(\"%s\", &a)",
		"r := bufio.NewReader(os.Stdin)",
		"s := bufio.NewScanner(stdin)",
		"b, _ := io.ReadAll(os.Stdin)",
		"io.Copy(w, stdin)",
		"os.Stdin.Read(buf)",
		"if !promptYesNo(q) {",
	} {
		if got := stdinReadersIn("package cmd\nfunc f() { " + src + " }\n"); len(got) != 1 {
			t.Errorf("%q was not seen exactly once: %v", src, got)
		}
	}
	for _, src := range []string{
		"// promptYesNo was here",
		"interactive := term.IsTerminal(int(os.Stdin.Fd()))",
		"x := 1 // bufio.NewReader(os.Stdin)",
	} {
		if got := stdinReadersIn(src + "\n"); len(got) != 0 {
			t.Errorf("%q was reported: %v", src, got)
		}
	}
}
