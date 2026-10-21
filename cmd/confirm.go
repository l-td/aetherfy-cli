package cmd

import (
	"bufio"
	"fmt"
	"io"
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

// refuseUnconfirmable is the refusal of a destructive command that cannot be
// answered: no terminal and no --yes. A usage error (exit 2, CLI_USAGE_ERROR),
// raised BEFORE anything is asked or sent, because nobody can answer and
// asking would read nothing, which a script would take for an answer.
func refuseUnconfirmable(yes, interactive bool, what string) error {
	if yes || interactive {
		return nil
	}
	return refuse("This deletes %s; pass --yes to confirm without a prompt.", what)
}

// askToConfirm asks the question and reads the answer from p.
//
//   - A person answers no: a cancelledError (exit 1).
//   - Input that ends before an answer: refused, exit 2, nothing done (the
//     prompter's rule for every question).
func askToConfirm(ask destructiveAsk, p *prompter) error {
	for _, line := range ask.warn {
		output.Warning.Fprintln(p.out, line)
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

// confirmDestructiveOn is the whole confirmation: --yes skips it, a run that
// cannot be answered is refused, anything else is asked.
func confirmDestructiveOn(yes bool, ask destructiveAsk, interactive bool, p *prompter) error {
	if yes {
		return nil
	}
	if err := refuseUnconfirmable(yes, interactive, ask.what); err != nil {
		return err
	}
	return askToConfirm(ask, p)
}

// confirmDestructive is the confirmation every destructive command makes
// (afy delete, afy secrets delete, afy workspaces delete), asking on the
// process's terminal.
func confirmDestructive(yes bool, ask destructiveAsk) error {
	if yes {
		return nil
	}
	if err := refuseUnconfirmable(yes, stdinIsTerminal(), ask.what); err != nil {
		return err
	}
	return askToConfirm(ask, newPrompter())
}

// vecPrompter is the prompter of a vector command: the question goes to
// stderr, so --json keeps stdout to one object, and the answer is read from
// the command's stdin.
func vecPrompter(r *vecRun, stdin io.Reader) *prompter {
	return &prompter{in: bufio.NewReader(stdin), out: r.stderr}
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
