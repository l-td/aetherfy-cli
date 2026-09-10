package cmd

// Documented `afy …` command lines must be runnable as written.
//
// WHAT SHIPPED WITHOUT THIS, and why every existing guard passed. The
// self-updater was renamed `update` -> `upgrade` because `afy update <name>`
// became agent config. The replace also rewrote sentences that were ABOUT the
// agent command, so `afy upgrade`'s own help ended up telling users:
//
//	To change an AGENT's configuration, use 'afy upgrade <name>'.
//
// `upgrade` is declared cobra.NoArgs. That instruction cannot work. And nothing
// noticed: TestUpdateIsAgentConfigAndUpgradeIsTheSelfUpdater checks the two
// commands are not swapped, TestSuggestionsUseTheRealBinaryName checks the
// binary is called afy, TestSuggestionsDoNotNamePhantomFlags checks the flags
// exist. The command existed, the binary name was right, no flag was named.
// Only the ARITY was wrong, and no guard asked about arity.
//
// So this asks the one question the others do not: does the command a piece of
// help text names actually accept the arguments that help text passes it?

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// An `afy …` invocation anywhere in help text — inside an Example block, or
// mid-sentence in a Long description, which is where the shipped defect was.
// The token classes stop the match at a quote, a period or a value like 0.1.0,
// so a trailing `'.` never becomes an argument.
var documentedInvocation = regexp.MustCompile(
	`afy((?: (?:<[a-z][a-z-]*>|[a-z][a-z0-9-]*|--?[a-z][a-z0-9-]*))+)`)

// A documented positional, e.g. <name> or <agent>. ONLY these are counted.
//
// A bare word after a command is ambiguous — in `afy update my-agent
// --workspace research`, both `my-agent` and `research` are bare words and only
// the first is positional. Counting those would make this guard wrong in a way
// that gets it deleted. A placeholder is unambiguous, and it is what help text
// uses when it means "an argument goes here".
var placeholderArg = regexp.MustCompile(`^<[a-z][a-z-]*>$`)

func TestDocumentedInvocationsSatisfyTheirCommandArgs(t *testing.T) {
	checked := 0

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, text := range []string{c.Long, c.Example} {
			for _, m := range documentedInvocation.FindAllStringSubmatch(text, -1) {
				line := "afy" + m[1]
				tokens := strings.Fields(m[1])

				// Cobra's own resolution, so `afy agents list` walks to the
				// subcommand exactly as a real invocation would rather than
				// this test re-implementing the lookup and disagreeing with it.
				target, rest, err := rootCmd.Find(tokens)
				if err != nil || target == nil {
					// An unresolvable name is a different defect, and
					// TestEveryDocumentedCommandIsRegistered owns it.
					continue
				}

				args := 0
				for _, r := range rest {
					if placeholderArg.MatchString(r) {
						args++
					}
				}
				if args == 0 || target.Args == nil {
					continue
				}
				checked++

				// Non-empty stand-ins: cobra echoes the argument back in its
				// refusal, and `unknown command "" for ...` reads like a
				// different defect than the one being reported.
				standIns := make([]string, args)
				for i := range standIns {
					standIns[i] = "ARG"
				}
				if err := target.Args(target, standIns); err != nil {
					t.Errorf(
						"`%s` help documents %q, but `%s` does not accept %d argument(s): %v\n\n"+
							"The command named there takes a different number of arguments than "+
							"the line passes it, so a reader following this help gets an error. "+
							"Most likely the sentence is about a DIFFERENT command and a rename "+
							"rewrote the name inside it.",
						c.CommandPath(), line, target.CommandPath(), args, err)
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)

	// ANTI-NO-OP. Every assertion above is inside a loop that a regex feeds. If
	// the help-text field names change, or the token classes stop matching the
	// house style, this file passes while checking nothing at all. The floor is
	// what makes that failure loud instead of green.
	// Eight at the time of writing; five leaves room for the help text to change
	// without making this floor the thing that fails.
	if checked < 5 {
		t.Fatalf("only %d documented invocation(s) with placeholder arguments were found "+
			"across the whole command tree — the scan is no longer reaching the help text, "+
			"so this guard is asserting nothing", checked)
	}
	t.Logf("checked %d documented invocations that pass positional arguments", checked)
}
