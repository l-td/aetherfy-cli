package cmd

// Text that tells a user what to run must name something they can actually run.
//
// WHAT SHIPPED WITHOUT THIS. The self-updater was renamed `update` -> `upgrade`
// because `afy update <name>` became agent config after the agents group was
// flattened. The replace left two survivors, failing in opposite directions:
//
//	cmd/update.go   `afy upgrade`'s own help said "use 'afy upgrade <name>'"
//	                for agent config. `upgrade` is cobra.NoArgs, so that line
//	                passes ONE argument too many.
//	replace.go      the not-writable error said "sudo afy update", the AGENT
//	                command, which needs <name>. That passes one too FEW.
//
// Nothing noticed either. TestUpdateIsAgentConfigAndUpgradeIsTheSelfUpdater
// checks the two commands are not swapped, TestSuggestionsUseTheRealBinaryName
// checks the binary is called afy, TestSuggestionsDoNotNamePhantomFlags checks
// the flags exist. All passed: the commands existed, the name was right, no
// flag was named. Only the arity was wrong, and nothing asked about arity.
//
// WHY THE TWO ARE CHECKED DIFFERENTLY, which is the interesting part.
//
// The too-many direction generalises: a line that passes <placeholder> tokens
// says exactly how many arguments it means, so any command's help can be
// checked against any command's Args. That is TestDocumentedInvocations below.
//
// The too-FEW direction does not generalise, and an earlier draft of this file
// that tried it was wrong. Checking that a bare `afy foo` is accepted with zero
// arguments reds seven legitimate lines, because prose names commands bare all
// the time and correctly:
//
//	"Resume an agent that was paused with 'afy stop'."   (cmd/agents.go:287)
//
// `afy stop` needs an agent name; that sentence is still perfectly good English
// and perfectly good help. A guard that reds it would be deleted within a week,
// and deserve it. So the too-few direction is checked where the claim is
// specific instead — see TestErrorMessagesNameRunnableCommands.

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/release"
	"github.com/spf13/cobra"
)

// An `afy …` invocation anywhere in prose — inside an Example block, mid-
// sentence in a Long description, or inside backticks in an error. The token
// classes stop the match at a quote, a period, or a value like 0.1.0, so a
// trailing `'.` never becomes an argument.
var documentedInvocation = regexp.MustCompile(
	`afy((?: (?:<[a-z][a-z-]*>|[a-z][a-z0-9-]*|--?[a-z][a-z0-9-]*))+)`)

// A documented positional, e.g. <name> or <agent>. ONLY these are counted.
//
// A bare word after a command is ambiguous — in `afy update my-agent
// --workspace research`, both `my-agent` and `research` are bare words and only
// the first is positional. Counting those would make this guard wrong in a way
// that gets it deleted. A placeholder is unambiguous, and it is what prose uses
// when it means "an argument goes here".
var placeholderArg = regexp.MustCompile(`^<[a-z][a-z-]*>$`)

// checkInvocations resolves every `afy …` line in text and returns how many
// carried placeholder arguments, and a problem for each the named command
// refuses. where names the source, so a problem says which text is wrong.
//
// A placeholder right after a flag that takes a value (`--agent <name>`) is
// that flag's VALUE, not a positional, and is not counted. After a boolean
// flag (`--yes <name>`) it is a positional, and is.
func checkInvocations(where, text string) (checked int, problems []string) {

	for _, m := range documentedInvocation.FindAllStringSubmatch(text, -1) {
		line := "afy" + m[1]

		// Cobra's own resolution, so `afy agents list` walks to the subcommand
		// exactly as a real invocation would, rather than this test
		// re-implementing the lookup and disagreeing with it.
		target, rest, err := rootCmd.Find(strings.Fields(m[1]))
		if err != nil || target == nil || target.Args == nil {
			// An unresolvable name is a different defect with its own guard,
			// and a group with no Args of its own accepts what cobra gives it.
			continue
		}

		args := 0
		for i := 0; i < len(rest); i++ {
			r := rest[i]
			if strings.HasPrefix(r, "-") {
				if !strings.Contains(r, "=") && takesValue(target, strings.TrimLeft(r, "-")) {
					i++ // the next token is this flag's value
				}
				continue
			}
			if placeholderArg.MatchString(r) {
				args++
			}
		}
		if args == 0 {
			continue
		}
		checked++

		// Non-empty stand-ins: cobra echoes the argument back in its refusal,
		// and `unknown command "" for ...` reads like a different defect than
		// the one being reported.
		standIns := make([]string, args)
		for i := range standIns {
			standIns[i] = "ARG"
		}

		if err := target.Args(target, standIns); err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s says %q, but `%s` does not accept the %d argument(s) that line passes it: %v. "+
					"Most likely the sentence is about a DIFFERENT command and a rename rewrote "+
					"the name inside it, leaving a reader who follows this with an error.",
				where, line, target.CommandPath(), args, err))
		}
	}
	return checked, problems
}

// takesValue reports whether c has a flag of that name (its own or inherited,
// long or shorthand) that takes a value rather than standing alone.
func takesValue(c *cobra.Command, name string) bool {
	f := c.Flags().Lookup(name)
	if f == nil {
		f = c.InheritedFlags().Lookup(name)
	}
	if f == nil && len(name) == 1 {
		f = c.Flags().ShorthandLookup(name)
	}
	return f != nil && f.NoOptDefVal == ""
}

// The flag-value rule, both ways, through the same function the guard runs.
func TestAPlaceholderAfterAValuedFlagIsItsValueNotAPositional(t *testing.T) {
	if n, p := checkInvocations("fixture", "afy init --agent <name> --target <t>"); n != 0 || len(p) != 0 {
		t.Errorf("flag values were counted as positionals: checked %d, %v", n, p)
	}
	// CONTROL: after a boolean flag a placeholder is positional, and two of
	// them are more than `afy init [path]` accepts.
	if _, p := checkInvocations("fixture", "afy init --yes <a> <b>"); len(p) != 1 {
		t.Errorf("two positionals after a bool flag must be refused, got %v", p)
	}
	if _, p := checkInvocations("fixture", "afy update <agent> <new>"); len(p) != 1 {
		t.Errorf("too many positionals must still be refused, got %v", p)
	}
}

func TestDocumentedInvocationsSatisfyTheirCommandArgs(t *testing.T) {
	checked := 0

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, src := range []struct{ where, text string }{
			{"`" + c.CommandPath() + "` help", c.Long},
			{"`" + c.CommandPath() + "` examples", c.Example},
		} {
			n, problems := checkInvocations(src.where, src.text)
			checked += n
			for _, p := range problems {
				t.Error(p)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)

	// ANTI-NO-OP. Every assertion above is inside a loop that a regex feeds. If
	// the help-text fields change, or the token classes stop matching the house
	// style, this passes while checking nothing at all. The floor is what makes
	// that failure loud instead of green. Eight at the time of writing; five
	// leaves room for the help text to change without the floor being what fails.
	if checked < 5 {
		t.Fatalf("only %d documented invocation(s) with placeholder arguments were found "+
			"across the whole command tree — the scan is no longer reaching the help text, "+
			"so this guard is asserting nothing", checked)
	}
	t.Logf("checked %d documented invocations that pass positional arguments", checked)
}

func TestErrorMessagesNameRunnableCommands(t *testing.T) {
	// THE TOO-FEW DIRECTION, where the claim is specific enough to check.
	//
	// NotWritableError is what a user sees when an upgrade cannot write to the
	// install directory, and on POSIX it hands them a command to re-run with
	// elevated rights. The rename left that naming `afy update` — the AGENT
	// command, which needs an agent name — so anyone who hit a permission
	// failure mid-upgrade was told to run something that fails differently.
	//
	// It survived the sweep that fixed the help text because it is assembled
	// with a format verb: the source reads `sudo %s upgrade`, and no grep for
	// "afy update" can see that. Building the message and reading it back is
	// the only way to ask what the user is actually told.
	//
	// release_test.go already covers this message, and covers PRESENCE rather
	// than correctness: it asserts the words "sudo" or "administrator" appear.
	// Both were true the whole time it was wrong.
	//
	// THE ASSERTION IS AGAINST THE COMMAND OBJECT, not a literal. An error
	// raised BY the upgrade path must send the user back to the upgrade
	// command, whatever it is currently called, so renaming updateCmd moves the
	// expectation with it instead of leaving a second copy to drift.
	//
	// PLATFORM-DEPENDENT, deliberately. Windows has no sudo, so that branch
	// suggests no command at all. Asserting the POSIX shape everywhere would
	// fail on Windows for being right; asserting nothing on Windows would let a
	// command appear there later unchecked. Each branch asserts its own.
	msg := (&release.NotWritableError{
		Dir: "/usr/local/bin",
		Err: os.ErrPermission,
	}).Error()

	if !strings.Contains(msg, "afy ") {
		if runtime.GOOS != "windows" {
			t.Errorf("NotWritableError names no command to re-run on %s: %q. "+
				"Every other platform tells the user how to elevate. If that was removed "+
				"deliberately, this branch is what needs updating.", runtime.GOOS, msg)
		}
		if !strings.Contains(strings.ToLower(msg), "administrator") {
			t.Errorf("NotWritableError on %s suggests neither a command nor elevation: %q",
				runtime.GOOS, msg)
		}
		return
	}

	want := "afy " + updateCmd.Name()
	if !strings.Contains(msg, want) {
		t.Errorf("NotWritableError is raised by the upgrade path but does not send the user "+
			"back to %q: %q.\n\nWhatever command it names instead is a different one, and "+
			"following it will fail for a different reason than the permission error that "+
			"produced this message.", want, msg)
	}

	// And the command it names must accept what the line passes it.
	_, problems := checkInvocations("NotWritableError", msg)
	for _, p := range problems {
		t.Error(p)
	}
}
