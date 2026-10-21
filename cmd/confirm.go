package cmd

import (
	"fmt"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/output"
)

// cancelledError is a destructive command the person declined at its prompt.
// Nothing was done, and the command did not do what it was asked: exit 1, with
// the error document under --json. It is never a success, because a script that
// sees exit 0 believes the delete happened.
type cancelledError struct{ reason string }

func (e *cancelledError) Error() string {
	if e.reason == "" {
		return "Cancelled"
	}
	return "Cancelled: " + e.reason
}

// destructiveAsk is what a destructive command asks before it acts.
type destructiveAsk struct {
	// what is the thing being destroyed, in the sentence the refusal uses:
	// "This deletes <what>; pass --yes to confirm without a prompt."
	what string
	// warn are extra lines shown before the question.
	warn []string
	// prompt is the question; accept says whether the typed line confirms.
	prompt string
	accept func(line string) bool
	// mismatch is the reason given when the line does not confirm ("" for none).
	mismatch string
}

// confirmDestructive is the ONE confirmation every destructive command makes
// (afy delete, afy secrets delete, afy workspaces delete). yes skips it.
//
//   - No terminal and no --yes: refused BEFORE anything is asked, as a usage
//     error (exit 2, CLI_USAGE_ERROR). Nobody can answer, so asking would read
//     nothing and a script would take the silence for an answer.
//   - A person answers no: a cancelledError (exit 1).
//   - Input that ends before an answer: refused, exit 2, nothing done (the
//     prompter's rule for every question).
func confirmDestructive(yes bool, ask destructiveAsk) error {
	if yes {
		return nil
	}
	if !stdinIsTerminal() {
		return refuse("This deletes %s; pass --yes to confirm without a prompt.", ask.what)
	}
	p := newPrompter()
	for _, line := range ask.warn {
		output.Warning.Println(line)
	}
	fmt.Fprint(p.out, ask.prompt)
	line, err := p.readLine()
	if err != nil {
		return err
	}
	if !ask.accept(line) {
		return &cancelledError{reason: ask.mismatch}
	}
	return nil
}

// typedName accepts only the exact name.
func typedName(name string) func(string) bool {
	return func(line string) bool { return line == name }
}

// yesAnswer accepts "y" and "yes", in any case.
func yesAnswer(line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
