package cmd

// afy access: which workspaces an agent's key reaches on the vector
// API -- its own, plus the ones you grant it -- and the change. The rules are
// the control plane's (GET/POST/DELETE /agents/{agent}/access[...]); an agent's
// own key is refused them, so this needs your account key.
//
// A GRANT WIDENS WHAT THE AGENT'S CODE CAN READ AND WRITE, so adding one is a
// two-step change, as `afy collections regions` is: the command shows what the
// change would make reachable and asks "Proceed? [y/N]". --yes skips the
// question; without a terminal, or with --json, --yes is required (exit 2): a
// script must never hang on a question nobody can answer. A removal narrows
// access, so it asks nothing. A change that changes nothing says so and asks
// nothing.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// accessChangeWindow is how soon a change reaches the agent's key: vectordb
// re-reads the allowed set with the key, every KEY_LIVENESS_INTERVAL_MS
// (vectordb backend/services/keyLiveness.js). aetherfy-e2e-tests
// tests/pyunit/test_agent_access_window_pair.py holds it to that constant.
const accessChangeWindow = "60 seconds"

var agentsAccessCmd = &cobra.Command{
	Use:   "access <agent>",
	Short: "Show or change the workspaces an agent's key can use",
	Long: `Show or change the workspaces an agent's key can use on the vector API.

An agent reaches the collections of its own workspace, and of the workspaces
you grant it; any other workspace is refused to it. --add grants a workspace,
--remove revokes one; each takes a name, repeats, or takes a comma-separated
list. Granting gives the agent access; it does not move it (moving is
'afy update' with --workspace). An empty value, or one starting with
"-", is refused, exit 2. Your own key is unaffected.

Adding access lets the agent's code read, write, create and delete every
collection there, including ones added later, so the command shows what would
become reachable and asks before granting. --yes skips the question; it is
required when stdin is not a terminal and with --json. Removing asks nothing.
A change takes effect within ` + accessChangeWindow + `.

A grant is data access only: it never lets the agent's key use the granted
workspace's connections (Slack, Google and the like), because a connection token
acts at the provider. Each grant has a connections switch, off when the grant is
made. --connections-on turns it on for a granted workspace (and asks first, like
--add), --connections-off turns it off. They take names, repeat, and cannot be
combined with --add or --remove: grant first, then switch.`,
	Example: `  # What can the agent reach?
  afy access support-bot

  # Grant it a second workspace, then confirm
  afy access support-bot --add research

  # From a script: grant one workspace, revoke another
  afy access support-bot --add research --remove archive --yes --json

  # Let it use the connections of a workspace it is granted
  afy access support-bot --connections-on research`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		if len(agentsAccessConnectionsOn)+len(agentsAccessConnectionsOff) > 0 {
			return runVec(cmd, func(r *vecRun) int {
				return agentsAccessConnections(r, args[0], agentsAccessAdd, agentsAccessRemove,
					agentsAccessConnectionsOn, agentsAccessConnectionsOff, agentsAccessYes, os.Stdin, interactive)
			})
		}
		return runVec(cmd, func(r *vecRun) int {
			return agentsAccess(r, args[0], agentsAccessAdd, agentsAccessRemove, agentsAccessYes, os.Stdin, interactive)
		})
	},
}

var (
	agentsAccessAdd    []string
	agentsAccessRemove []string
	agentsAccessYes    bool

	agentsAccessConnectionsOn  []string
	agentsAccessConnectionsOff []string
)

// scopes splits each flag value on commas (as --regions takes a list),
// dropping duplicates.
func scopes(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// refuseEmptyScope refuses an empty workspace name: every agent and collection
// lives in a workspace, so there is no unnamed one to grant.
func refuseEmptyScope(flag string, values []string) error {
	for _, s := range scopes(values) {
		if s == "" {
			return refuse("--%s got an empty workspace name: every collection lives in a named workspace. "+
				"Name the workspace. Nothing was sent.", flag)
		}
	}
	return nil
}

func scopeLabel(scope string) string {
	return fmt.Sprintf("workspace '%s'", scope)
}

func reaches(a *api.AgentAccess, scope string) bool {
	for _, w := range a.AllowedWorkspaces {
		if w == scope {
			return true
		}
	}
	return false
}

func granted(a *api.AgentAccess, scope string) bool {
	for _, w := range a.GrantedWorkspaces {
		if w == scope {
			return true
		}
	}
	return false
}

func agentsAccess(r *vecRun, agent string, add, remove []string, yes bool, stdin io.Reader, interactive bool) int {
	if err := refuseFlagLikeValue("add", add...); err != nil {
		return r.fail(err)
	}
	if err := refuseFlagLikeValue("remove", remove...); err != nil {
		return r.fail(err)
	}
	if err := refuseEmptyScope("add", add); err != nil {
		return r.fail(err)
	}
	if err := refuseEmptyScope("remove", remove); err != nil {
		return r.fail(err)
	}
	return changeAgentAccess(r, agent, add, remove, yes, stdin, interactive)
}

// changeAgentAccess is agentsAccess once its flag values were checked.
func changeAgentAccess(r *vecRun, agent string, add, remove []string, yes bool, stdin io.Reader, interactive bool) int {
	adds, removes := scopes(add), scopes(remove)
	for _, s := range adds {
		for _, t := range removes {
			if s == t {
				return r.fail(refuse("%s is both added and removed: pass it to one of --add or --remove", scopeLabel(s)))
			}
		}
	}
	changing := len(adds) > 0 || len(removes) > 0
	if len(adds) > 0 && !yes && r.json {
		return r.fail(refuse("not changing agent '%s': --json cannot answer the confirmation. "+
			"Pass --yes to grant access without the question.", agent))
	}
	if len(adds) > 0 && !yes && !interactive {
		return r.fail(refuse("not changing agent '%s': stdin is not a terminal, so nobody can "+
			"confirm. Pass --yes to grant access without the question.", agent))
	}

	cp := r.controlPlane()
	current, err := cp.GetAgentAccess(agent)
	if err != nil {
		return r.fail(err)
	}
	if !changing {
		if r.json {
			return r.printJSON(accessJSON(current, nil, nil))
		}
		printAccess(r.stdout, current)
		return 0
	}

	// The agent's own scope is not a grant: it always reaches it, and the
	// control plane refuses granting or revoking it (AGENT_WORKSPACE_GRANT_REDUNDANT).
	// Refused here, before any change is shown or asked about.
	for _, s := range append(append([]string(nil), adds...), removes...) {
		if isOwn(current, s) {
			return r.fail(refuse("%s is agent '%s''s own: its key always reaches it, so there is "+
				"nothing to grant or revoke", scopeLabel(s), agent))
		}
	}

	// What would change: only the scopes that move.
	var toAdd, toRemove []string
	for _, s := range adds {
		if !reaches(current, s) {
			toAdd = append(toAdd, s)
		}
	}
	for _, s := range removes {
		if granted(current, s) {
			toRemove = append(toRemove, s)
		}
	}
	if !r.json {
		printAccessPreview(r.stdout, current, toAdd, toRemove, adds, removes)
	}
	if len(toAdd) == 0 && len(toRemove) == 0 {
		if r.json {
			return r.printJSON(accessJSON(current, []string{}, []string{}))
		}
		return 0
	}
	if len(toAdd) > 0 && !yes {
		fmt.Fprint(r.stderr, "Proceed? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return r.fail(refuse("agent '%s' not changed: the change was not confirmed", agent))
		}
	}

	final := current
	added, removed := []string{}, []string{}
	// Narrow first, then widen: an interrupted run leaves less reachable, never more.
	for _, s := range toRemove {
		next, err := cp.RevokeWorkspace(agent, s)
		if err != nil {
			return r.fail(partialAccessError(err, added, removed))
		}
		final, removed = next, append(removed, s)
	}
	for _, s := range toAdd {
		next, err := cp.GrantWorkspace(agent, s)
		if err != nil {
			return r.fail(partialAccessError(err, added, removed))
		}
		final, added = next, append(added, s)
	}
	if r.json {
		return r.printJSON(accessJSON(final, added, removed))
	}
	fmt.Fprintf(r.stdout, "Access of agent '%s' changed; it takes effect within %s.\n", agent, accessChangeWindow)
	printAccess(r.stdout, final)
	return 0
}

// agentsAccessConnections turns the connections switch of granted workspaces on
// or off. A connection token acts at the provider, so turning it ON is
// confirmed like a grant is (--yes skips it; required without a terminal and
// with --json); turning it off narrows and asks nothing.
func agentsAccessConnections(r *vecRun, agent string, add, remove, on, off []string, yes bool, stdin io.Reader, interactive bool) int {
	if len(add)+len(remove) > 0 {
		return r.fail(refuse("--connections-on/--connections-off cannot be combined with --add or --remove: " +
			"grant the workspace first, then switch its connections. Nothing was sent."))
	}
	for flag, values := range map[string][]string{"connections-on": on, "connections-off": off} {
		if err := refuseFlagLikeValue(flag, values...); err != nil {
			return r.fail(err)
		}
		if err := refuseEmptyScope(flag, values); err != nil {
			return r.fail(err)
		}
	}
	ons, offs := scopes(on), scopes(off)
	for _, s := range ons {
		if contains(offs, s) {
			return r.fail(refuse("%s is both switched on and off: pass it to one of --connections-on or --connections-off", scopeLabel(s)))
		}
	}
	if len(ons) > 0 && !yes && r.json {
		return r.fail(refuse("not changing agent '%s': --json cannot answer the confirmation. "+
			"Pass --yes to allow connections without the question.", agent))
	}
	if len(ons) > 0 && !yes && !interactive {
		return r.fail(refuse("not changing agent '%s': stdin is not a terminal, so nobody can "+
			"confirm. Pass --yes to allow connections without the question.", agent))
	}

	cp := r.controlPlane()
	current, err := cp.GetAgentAccess(agent)
	if err != nil {
		return r.fail(err)
	}
	for _, s := range append(append([]string(nil), ons...), offs...) {
		if isOwn(current, s) {
			return r.fail(refuse("%s is agent '%s''s own: its key always uses its connections, so there is "+
				"no switch to change", scopeLabel(s), agent))
		}
		if !granted(current, s) {
			return r.fail(refuse("%s is not granted to agent '%s': grant it first (afy access %s --add %s). Nothing was sent.",
				scopeLabel(s), agent, agent, s))
		}
	}
	var toOn, toOff []string
	for _, s := range ons {
		if !contains(current.ConnectionsWorkspaces, s) {
			toOn = append(toOn, s)
		}
	}
	for _, s := range offs {
		if contains(current.ConnectionsWorkspaces, s) {
			toOff = append(toOff, s)
		}
	}
	if len(toOn) == 0 && len(toOff) == 0 {
		if r.json {
			return r.printJSON(accessJSON(current, []string{}, []string{}))
		}
		fmt.Fprintf(r.stdout, "No change: agent '%s' already has the connections access asked for.\n", agent)
		return 0
	}
	if !r.json {
		for _, s := range toOn {
			fmt.Fprintf(r.stdout, "Agent '%s' will be able to ask for tokens for every connection in %s, including ones added later, and act with them at the provider.\n",
				agent, scopeLabel(s))
		}
		for _, s := range toOff {
			fmt.Fprintf(r.stdout, "Agent '%s' will no longer be able to ask for tokens for the connections of %s.\n", agent, scopeLabel(s))
		}
	}
	if len(toOn) > 0 && !yes {
		fmt.Fprint(r.stderr, "Proceed? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return r.fail(refuse("agent '%s' not changed: the change was not confirmed", agent))
		}
	}
	final := current
	// Narrow first, then widen: an interrupted run leaves less reachable, never more.
	for _, s := range toOff {
		next, err := cp.SetConnectionsSwitch(agent, s, false)
		if err != nil {
			return r.fail(err)
		}
		final = next
	}
	for _, s := range toOn {
		next, err := cp.SetConnectionsSwitch(agent, s, true)
		if err != nil {
			return r.fail(err)
		}
		final = next
	}
	if r.json {
		return r.printJSON(accessJSON(final, toOn, toOff))
	}
	fmt.Fprintf(r.stdout, "Connections access of agent '%s' changed; it takes effect within %s.\n", agent, accessChangeWindow)
	printAccess(r.stdout, final)
	return 0
}

func isOwn(a *api.AgentAccess, scope string) bool {
	return a.OwnWorkspace == scope
}

// partialAccessError keeps what was already applied visible: the changes are
// one request each.
func partialAccessError(err error, added, removed []string) error {
	if len(added) == 0 && len(removed) == 0 {
		return err
	}
	return fmt.Errorf("%w (already applied: added %s; removed %s)", err, labels(added), labels(removed))
}

func labels(scopes []string) string {
	if len(scopes) == 0 {
		return "none"
	}
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = scopeLabel(s)
	}
	return strings.Join(out, ", ")
}

// printAccess lists what the agent reaches, its own scope first.
func printAccess(w io.Writer, a *api.AgentAccess) {
	fmt.Fprintf(w, "Agent '%s' can use:\n", a.Agent)
	fmt.Fprintf(w, "  %s  (its own workspace)\n", a.OwnWorkspace)
	names := append([]string(nil), a.GrantedWorkspaces...)
	sort.Strings(names)
	for _, n := range names {
		if contains(a.ConnectionsWorkspaces, n) {
			fmt.Fprintf(w, "  %s  (granted; connections on)\n", n)
		} else {
			fmt.Fprintf(w, "  %s  (granted; connections off)\n", n)
		}
	}
}

// printAccessPreview says what the change would do, in the dashboard's words.
func printAccessPreview(w io.Writer, a *api.AgentAccess, toAdd, toRemove, adds, removes []string) {
	if len(toAdd) == 0 && len(toRemove) == 0 {
		fmt.Fprintf(w, "No change: agent '%s' already has the access asked for.\n", a.Agent)
		return
	}
	fmt.Fprintf(w, "Agent '%s':\n", a.Agent)
	for _, s := range toAdd {
		what := fmt.Sprintf("every collection in workspace '%s'", s)
		fmt.Fprintf(w, "  Adds %s: %s, including ones added later, becomes readable and writable by this agent's code.\n",
			scopeLabel(s), what)
	}
	for _, s := range toRemove {
		fmt.Fprintf(w, "  Removes %s: the agent can no longer use it.\n", scopeLabel(s))
	}
	for _, s := range adds {
		if !contains(toAdd, s) {
			fmt.Fprintf(w, "  Already reaches %s.\n", scopeLabel(s))
		}
	}
	for _, s := range removes {
		if !contains(toRemove, s) {
			fmt.Fprintf(w, "  Already does not have %s granted.\n", scopeLabel(s))
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func accessJSON(a *api.AgentAccess, added, removed []string) interface{} {
	return struct {
		Access  *api.AgentAccess `json:"access"`
		Added   []string         `json:"added"`
		Removed []string         `json:"removed"`
	}{Access: a, Added: added, Removed: removed}
}

func init() {
	agentsAccessCmd.Flags().StringArrayVar(&agentsAccessAdd, "add", nil, "Grant a workspace (repeat, or comma-separated)")
	agentsAccessCmd.Flags().StringArrayVar(&agentsAccessRemove, "remove", nil, "Revoke a granted workspace (repeat, or comma-separated)")
	agentsAccessCmd.Flags().StringArrayVar(&agentsAccessConnectionsOn, "connections-on", nil, "Let the agent's key use a granted workspace's connections (repeat, or comma-separated)")
	agentsAccessCmd.Flags().StringArrayVar(&agentsAccessConnectionsOff, "connections-off", nil, "Stop the agent's key using a granted workspace's connections (repeat, or comma-separated)")
	agentsAccessCmd.Flags().BoolVarP(&agentsAccessYes, "yes", "y", false, "Grant access without asking")
}
