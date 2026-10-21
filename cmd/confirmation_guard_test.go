package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// EVERY QUESTION A COMMAND ASKS goes through the one helper (cmd/confirm.go), so
// the one exit rule holds: no terminal and no skip flag is exit 2 before asking, a
// "no" is exit 1, never 0. The shape that broke the rule twice (a deploy that
// printed "cancelled" and exited 0, an init that printed "Aborted." and exited 0)
// was a command reading its own answer with fmt.Scanln. A command that does that
// again is not asking through the helper, so it is refused here, by name.

var answerReaders = []string{"fmt.Scanln(", "fmt.Scanf(", "fmt.Scan(", "promptYesNo"}

// answerReadersIn lists the banned calls in one file's source, comments aside.
func answerReadersIn(src string) []string {
	var found []string
	for i, line := range strings.Split(src, "\n") {
		code := line
		if c := strings.Index(code, "//"); c >= 0 {
			code = code[:c]
		}
		for _, banned := range answerReaders {
			if strings.Contains(code, banned) {
				found = append(found, banned+" at line "+strconv.Itoa(i+1))
			}
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
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, v := range answerReadersIn(string(raw)) {
			t.Errorf("%s: reads an answer itself (%s): ask through confirmDestructive / "+
				"confirmDestructiveOn / askToConfirm (cmd/confirm.go)", f, v)
		}
	}
	if scanned < 20 {
		t.Fatalf("only %d source files scanned", scanned)
	}
}

// Positive control: the scan reports a violation when there is one, and not a
// comment that names the old call.
func TestTheConfirmationGuardSeesAnAnswerReader(t *testing.T) {
	if got := answerReadersIn("package cmd\nfunc f() { var a string; fmt.Scanln(&a) }\n"); len(got) != 1 {
		t.Errorf("a Scanln was not seen: %v", got)
	}
	if got := answerReadersIn("// promptYesNo was here\nvar x = 1\n"); len(got) != 0 {
		t.Errorf("a comment was reported: %v", got)
	}
}
