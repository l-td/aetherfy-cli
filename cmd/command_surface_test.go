package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// readCmdSources concatenates this package's non-test Go files — the same set
// docs-site's extractor reads.
func readCmdSources(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading cmd/: %v", err)
	}
	var b strings.Builder
	var count int
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", n))
		if err != nil {
			t.Fatalf("reading %s: %v", n, err)
		}
		b.Write(data)
		b.WriteString("\n")
		count++
	}
	// Anti-no-op: an empty read would make every source assertion vacuously true.
	if count < 5 {
		t.Fatalf("only read %d source files from cmd/ — the scan is not reaching the package", count)
	}
	return stripComments(b.String())
}

// stripComments removes Go comments, because the rule below is about CODE.
//
// Written after this test failed on its own explanatory comment: the note in
// cmd/agents.go quotes `rootCmd.AddCommand(c)` to say why a loop is wrong, and
// a raw count read that as a thirty-third registration. A guard that a comment
// about the guard can break is a guard nobody will keep.
//
// Line-based and deliberately simple. A `//` inside a string literal would
// truncate that line, which cannot hide a registration: every one of them is on
// a line of its own.
func stripComments(src string) string {
	var out strings.Builder
	inBlock := false
	for _, line := range strings.Split(src, "\n") {
		for inBlock {
			if i := strings.Index(line, "*/"); i >= 0 {
				line, inBlock = line[i+2:], false
			} else {
				line = ""
				break
			}
		}
		if i := strings.Index(line, "/*"); i >= 0 {
			rest := line[i+2:]
			if j := strings.Index(rest, "*/"); j >= 0 {
				line = line[:i] + rest[j+2:]
			} else {
				line, inBlock = line[:i], true
			}
		}
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

func regexpAll(src, pattern string) []string {
	return regexp.MustCompile(pattern).FindAllString(src, -1)
}

// THE TOP-LEVEL COMMAND SURFACE. Agents are the default noun, so a bare verb is
// an agent verb, and only the non-default nouns keep a group.
//
// WHAT THIS PROTECTS. The `agents` group was removed with no alias left behind
// — one spelling per command — and that is the kind of decision a single
// well-meaning AddCommand quietly reverses. The rules below are the mechanical
// half of it. The judgement half (is this new verb about an AGENT, or about
// some other kind of object?) is written down in cmd/root.go where the
// registrations live, because no test can decide it.
//
// The `--help` grouping is checked too, for a reason that is not cosmetic: an
// ungrouped command lands in cobra's "Additional Commands" bucket at the bottom
// of a twenty-verb list, which is where a command goes to be undiscoverable.

func rootByName(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func TestTheAgentsGroupIsGoneWithNoAliasLeftBehind(t *testing.T) {
	// An alias would be worse than the unknown-command error, not better:
	// `afy agents update my-agent` under an alias silently keeps working, so
	// nobody learns the spelling changed and every doc stays half-right.
	for _, c := range rootCmd.Commands() {
		names := append([]string{c.Name()}, c.Aliases...)
		for _, n := range names {
			if n == "agents" || n == "agent" {
				t.Errorf("`afy %s` is back (as %q). Agents are the DEFAULT noun: "+
					"agent verbs live at the root, ungrouped.", n, c.Name())
			}
		}
	}
}

func TestTheAgentVerbsAreAllAtTheRoot(t *testing.T) {
	// The full list the flatten moved. A verb that quietly stops being
	// registered is a command that stops existing, and the only symptom is a
	// user typing it and being told it is unknown.
	for _, name := range []string{
		"list", "create", "delete", "stop", "start", "archive", "restore",
		"cancel", "status", "rename", "update", "pull", "diff", "run", "runs",
		"schedule",
		// already flat before the flatten; here so the list reads as the whole
		// agent surface rather than as "the ones that moved"
		"deploy", "logs", "rollback", "redeploy", "deployments", "spawn",
	} {
		if rootByName(t, name) == nil {
			t.Errorf("`afy %s` is not registered at the root", name)
		}
	}
}

func TestUpdateIsAgentConfigAndUpgradeIsTheSelfUpdater(t *testing.T) {
	// THE COLLISION THE RENAME EXISTS FOR. Both commands were called `update`
	// once the group dissolved. Getting these backwards is the worst outcome in
	// this file: `afy update my-agent` would try to install a release named
	// "my-agent", and `afy upgrade` would ask which agent to reconfigure.
	update := rootByName(t, "update")
	if update == nil {
		t.Fatal("`afy update` is not registered")
	}
	if update.Flags().Lookup("workspace") == nil {
		t.Error("`afy update` is not the AGENT-config command — it has no --workspace")
	}
	if update.Flags().Lookup("check") != nil {
		t.Error("`afy update` is the self-updater; it must be `afy upgrade`")
	}
	if !strings.Contains(update.Use, "<name>") {
		t.Errorf("`afy update` should take an agent name; Use = %q", update.Use)
	}

	upgrade := rootByName(t, "upgrade")
	if upgrade == nil {
		t.Fatal("`afy upgrade` is not registered")
	}
	for _, f := range []string{"check", "force", "version"} {
		if upgrade.Flags().Lookup(f) == nil {
			t.Errorf("`afy upgrade` is missing --%s; it may not be the self-updater", f)
		}
	}
}

func TestNoTwoTopLevelCommandsShareASpelling(t *testing.T) {
	// Cobra resolves the FIRST match and says nothing about the second, so a
	// collision is invisible until someone runs the loser.
	seen := map[string]string{}
	for _, c := range rootCmd.Commands() {
		for _, n := range append([]string{c.Name()}, c.Aliases...) {
			if prev, dup := seen[n]; dup {
				t.Errorf("%q is claimed by both %q and %q", n, prev, c.Name())
			}
			seen[n] = c.Name()
		}
	}
}

func TestEveryTopLevelCommandIsInAHelpGroup(t *testing.T) {
	// ~twenty verbs. An ungrouped one falls into cobra's "Additional Commands"
	// bucket under everything else, which is where a command goes to never be
	// found. Adding a command means choosing where it reads.
	var ungrouped []string
	for _, c := range rootCmd.Commands() {
		if c.IsAvailableCommand() && c.GroupID == "" {
			ungrouped = append(ungrouped, c.Name())
		}
	}
	sort.Strings(ungrouped)
	if len(ungrouped) > 0 {
		t.Errorf("these top-level commands have no help group: %v\n"+
			"Pick one of the group constants in cmd/root.go.", ungrouped)
	}
}

func TestScheduleKeepsItsSubcommands(t *testing.T) {
	// Flattening removed the group around the DEFAULT noun; it did not flatten
	// every group. `afy pause` would mean the agent, not its schedule.
	schedule := rootByName(t, "schedule")
	if schedule == nil {
		t.Fatal("`afy schedule` is not registered")
	}
	var subs []string
	for _, c := range schedule.Commands() {
		subs = append(subs, c.Name())
	}
	sort.Strings(subs)
	if len(subs) != 2 || subs[0] != "pause" || subs[1] != "resume" {
		t.Errorf("`afy schedule` subcommands = %v, want [pause resume]", subs)
	}
}

func TestEveryTopLevelCommandIsRegisteredWithALiteralAddCommand(t *testing.T) {
	// THE PAIR GATE FOR A CROSS-REPO PARSER, pinned on this side.
	//
	// aetherfy-dashboard/docs-site extracts the published CLI surface by
	// parsing this package STATICALLY: scripts/lib/cli-surface.mjs matches
	// `x.AddCommand(y)` and follows the variable y. Register from a loop and
	// the call reads `rootCmd.AddCommand(c)` — c is not a command variable it
	// can follow — so the command vanishes from the extracted surface with no
	// error anywhere. Measured during the flatten: 2 extracted command paths
	// instead of 48, and the only symptom was the docs guard rejecting
	// commands that plainly exist.
	//
	// Counting is enough and reimplementing their parser would be worse: this
	// reds the moment a registration stops being a literal call, which is the
	// only way the extractor can be starved.
	src := readCmdSources(t)
	literal := len(regexpAll(src, `rootCmd\.AddCommand\(\w+\)`))

	// cobra adds `help` itself and `completion` registers with its own literal
	// call, so the only commands NOT expected to have one are cobra's own.
	var registered int
	for _, c := range rootCmd.Commands() {
		if c.Name() == "help" {
			continue // synthesised by cobra, never registered in source
		}
		registered++
	}

	if literal != registered {
		t.Errorf("cobra has %d top-level commands but the source contains %d literal "+
			"rootCmd.AddCommand(x) calls.\n"+
			"Every registration must be a literal call on its own line — docs-site's "+
			"surface extractor parses this file and cannot follow a loop variable.",
			registered, literal)
	}
}

func TestTheNonDefaultNounsKeepTheirGroups(t *testing.T) {
	// The other half of the rule. If these ever flatten too, `afy list` becomes
	// ambiguous and the whole design falls over.
	for _, name := range []string{"secrets", "workspaces", "github"} {
		c := rootByName(t, name)
		if c == nil {
			t.Errorf("`afy %s` is not registered", name)
			continue
		}
		if len(c.Commands()) == 0 {
			t.Errorf("`afy %s` has no subcommands — it was flattened, and it must not be", name)
		}
	}
}
