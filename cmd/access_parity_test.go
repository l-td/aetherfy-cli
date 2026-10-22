package cmd

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// EVERY DASHBOARD ACCESS SETTING HAS ITS CLI COMMAND, AND THE REVERSE (grants
// round 2, owner decision 2026-10-21). The list is written in the control
// plane's design doc (docs/AGENT_ACCESS_LEVELS.md, "The dashboard setting and
// its CLI command"); this is its CLI half. A setting people can change in the
// dashboard but not here, or a command here with no place in the dashboard,
// would be a rule that holds on one surface only.
//
// The dashboard half of the same list is held by the dashboard's own unit test
// (dashboard/tests/unit/access-setting-pairs-test.js), which reads these same
// pairs from the design doc when the sibling is checked out.

type accessPair struct {
	setting string   // where the dashboard has it
	command []string // the CLI command path
	flags   []string // the flags that carry the setting
}

var accessPairs = []accessPair{
	{"Agent, ACCESS tab: workspaces it can access, each with a level (grant, change a level)", []string{"access"}, []string{"add", "level"}},
	{"Agent, ACCESS tab: remove a workspace it can access", []string{"access"}, []string{"remove"}},
	{"Agent, ACCESS tab: the level where it lives (a level, or the workspace default)", []string{"access"}, []string{"own-level"}},
	{"Agent, ACCESS tab: the secrets switch", []string{"access"}, []string{"secrets"}},
	{"Agent, ACCESS tab: read the access", []string{"access"}, nil},
	{"Workspace, Settings: the default level for its own agents", []string{"workspaces", "update"}, []string{"agent-access-level"}},
	{"Workspace, Agents & access: who lives here, who can access it, each with a level", []string{"workspaces", "agents"}, nil},
	{"Collection settings: protect", []string{"collections", "protect"}, nil},
	{"Collection settings: unprotect", []string{"collections", "unprotect"}, []string{"yes"}},
	{"Collections list: the PROTECTED badge", []string{"collections", "list"}, nil},
	{"Collection page, USED BY list: the agents that used it recently", []string{"collections", "delete"}, []string{"yes"}},
}

func findCommand(t *testing.T, path []string) *cobra.Command {
	t.Helper()
	c, _, err := rootCmd.Find(path)
	if err != nil || c == nil || c == rootCmd || c.Name() != path[len(path)-1] {
		t.Fatalf("no CLI command %q (err %v)", strings.Join(path, " "), err)
	}
	return c
}

func TestEveryDashboardAccessSettingHasItsCLICommand(t *testing.T) {
	for _, p := range accessPairs {
		c := findCommand(t, p.command)
		for _, f := range p.flags {
			if c.Flags().Lookup(f) == nil {
				t.Errorf("%q: afy %s has no --%s", p.setting, strings.Join(p.command, " "), f)
			}
		}
	}
}

// The reverse: every CLI command that sets or shows access is a dashboard
// setting in the list. The commands are found in the tree, never typed here: a
// command with an access flag, or named protect, unprotect or access.
func TestEveryCLIAccessCommandIsADashboardSetting(t *testing.T) {
	listed := map[string]bool{}
	for _, p := range accessPairs {
		listed[strings.Join(p.command, " ")] = true
	}
	accessFlags := []string{"own-level", "secrets", "agent-access-level"}
	found := map[string]bool{}
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		for _, sub := range c.Commands() {
			p := append(append([]string(nil), path...), sub.Name())
			isAccess := sub.Name() == "protect" || sub.Name() == "unprotect" || (len(p) == 1 && sub.Name() == "access")
			for _, f := range accessFlags {
				if sub.Flags().Lookup(f) != nil {
					isAccess = true
				}
			}
			if isAccess {
				found[strings.Join(p, " ")] = true
			}
			walk(sub, p)
		}
	}
	walk(rootCmd, nil)
	// A floor, so an empty walk cannot pass: the four commands this round is about.
	for _, want := range []string{"access", "workspaces update", "collections protect", "collections unprotect"} {
		if !found[want] {
			t.Fatalf("the walk did not find afy %s: the scan is not reaching the command tree (%v)", want, keys(found))
		}
	}
	for cmd := range found {
		if !listed[cmd] {
			t.Errorf("afy %s sets or shows access but is not a dashboard setting in accessPairs "+
				"(and in the design doc's list): add the pair, or the command has no place here", cmd)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
