package cmd

import (
	"github.com/l-td/aetherfy-cli/internal/output"
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// ONE INPUT SOURCE FOR AN INTERACTIVE FLOW. Every question a command asks
// reads from the same buffered reader, created once per run. Two readers over
// one stdin lose input: each buffers past the line it was asked for, and the
// next question never sees what the first one swallowed. promptui does exactly
// that inside every prompt (its readline reads ahead; measured: two prompts
// over "a\nb\n" answered "a" and EOF), so `afy init` asks through this
// line-based prompter instead: a numbered list for a choice, a line for text.

// prompter asks questions on out and reads the answers, a line each, from in.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// newPrompter is the run's prompter over the process's stdin and stdout. A
// var so a test can script the whole flow through the real command.
var newPrompter = func() *prompter {
	return &prompter{in: bufio.NewReader(os.Stdin), out: output.Out()}
}

// stdinIsTerminal says whether questions can be asked at all. A var so a test
// can run the interactive flow without a terminal.
var stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// errNoAnswer is input that ended before a question was answered: refused,
// exit 2, nothing done.
var errNoAnswer = errors.New("no answer: the input ended before the question was answered. Nothing was done")

// readLine reads one answer. Input that ends with nothing typed is
// errNoAnswer; a last line without a newline still counts.
func (p *prompter) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", &inputError{msg: errNoAnswer.Error()}
	}
	return strings.TrimSpace(line), nil
}

// text asks for a line; an empty answer is def.
func (p *prompter) text(label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(p.out, "%s: ", label)
	}
	answer, err := p.readLine()
	if err != nil {
		return "", err
	}
	if answer == "" {
		return def, nil
	}
	return answer, nil
}

// choose asks for one of items, by number or by its text; an empty answer is
// item def. Anything else is refused (exit 2): a script must not land on a
// guess.
func (p *prompter) choose(label string, items []string, def int) (int, error) {
	fmt.Fprintf(p.out, "%s:\n", label)
	for i, it := range items {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, it)
	}
	fmt.Fprintf(p.out, "%s [%d]: ", label, def+1)
	answer, err := p.readLine()
	if err != nil {
		return 0, err
	}
	if answer == "" {
		return def, nil
	}
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(items) {
		return n - 1, nil
	}
	for i, it := range items {
		if answer == it || answer == strings.Fields(it)[0] {
			return i, nil
		}
	}
	return 0, refuse("%q is not one of 1-%d for %s. Nothing was done.", answer, len(items), strings.ToLower(label))
}

// confirm asks a yes/no question, defaulting to no.
func (p *prompter) confirm(label string) (bool, error) {
	fmt.Fprintf(p.out, "%s [y/N]: ", label)
	answer, err := p.readLine()
	if err != nil {
		return false, err
	}
	a := strings.ToLower(answer)
	return a == "y" || a == "yes", nil
}
