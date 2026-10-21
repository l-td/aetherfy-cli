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
	// action is what the command does, as a verb phrase, in the sentence the
	// refusal uses: "This <action>; pass --yes to confirm without a prompt."
	action string
	// flag skips the question ("--yes" when empty; afy init's overwrite is
	// skipped by its own --force).
	flag string
	// warn are extra lines shown before the question.
	warn []string
	// prompt is the question; accept says whether the typed line confirms.
	prompt string
	accept func(line string) bool
	// mismatch is the reason given when the line does not confirm ("" for none).
	mismatch string
}

// skip is the flag that skips the question.
func (a destructiveAsk) skip() string {
	if a.flag == "" {
		return "--yes"
	}
	return a.flag
}

// refuseUnconfirmable is the refusal of a command that asks before it acts and
// cannot be answered: no terminal and no skip flag. A usage error (exit 2,
// CLI_USAGE_ERROR), raised BEFORE anything is asked or sent, because nobody can
// answer and asking would read nothing, which a script would take for an answer.
func refuseUnconfirmable(yes, interactive bool, action, flag string) error {
	if yes || interactive {
		return nil
	}
	return refuse("This %s; pass %s to confirm without a prompt.", action, flag)
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
	if err := refuseUnconfirmable(yes, interactive, ask.action, ask.skip()); err != nil {
		return err
	}
	return askToConfirm(ask, p)
}

// confirmDestructive is the confirmation of every command that asks, run on the
// process's terminal (afy delete, afy secrets delete, afy workspaces delete,
// the deploy's overage question). The commands that run through a vecRun use
// confirmDestructiveOn with their own stdin; afy init asks through its one
// prompter. ONE RULE for all of them: no terminal and no skip flag is exit 2
// before asking, a "no" is exit 1 (cancelledError), never 0.
func confirmDestructive(yes bool, ask destructiveAsk) error {
	if yes {
		return nil
	}
	if err := refuseUnconfirmable(yes, stdinIsTerminal(), ask.action, ask.skip()); err != nil {
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
