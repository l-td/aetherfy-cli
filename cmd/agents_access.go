package cmd

// afy access: which workspaces an agent's key reaches on the vector API -- its
// own, plus the ones you grant it -- the level it holds in each, and whether
// its own key may write its own secrets; and the change. The rules are the
// control plane's (GET/POST/DELETE/PUT /agents/{agent}/access[...]; the design
// is aetherfy-control-plane docs/AGENT_ACCESS_LEVELS.md); an agent's own key is
// refused every change, so this needs your account key.
//
// A CHANGE THAT WIDENS WHAT THE AGENT'S CODE CAN DO -- a grant, a higher level,
// the secrets switch on -- is a two-step change, as `afy collections regions`
// is: the command shows what the change would make possible and asks
// "Proceed? [y/N]" through the shared confirmation (cmd/confirm.go): --yes skips
// the question; without a terminal --yes is required (exit 2, before anything is
// asked or sent); a "no" is exit 1. The current access is read FIRST, so only a
// change that would really widen needs the question: an --add of a workspace
// already granted, or a state already set, changes nothing and asks nothing,
// even under --json. A change that narrows asks nothing.
//
// The words are the canonical ones (cmd/access_words.go): an agent "Lives in"
// one workspace and "Can access" others, each at a level said in plain words.

import (
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
// re-reads the allowed set and its levels with the key, every
// KEY_LIVENESS_INTERVAL_MS (vectordb backend/services/keyLiveness.js).
// aetherfy-e2e-tests tests/pyunit/test_agent_access_window_pair.py holds it to
// that constant.
const accessChangeWindow = "60 seconds"

// followDefault is the --own-level value that returns the agent to its
// workspace's default level.
const followDefault = "default"

var agentsAccessCmd = &cobra.Command{
	Use:   "access <agent>",
	Short: "Show or change the workspaces an agent's key can use, and its level in each",
	Long: `Show or change the workspaces an agent's key can use on the vector API, and
what it may do in each.

An agent lives in one workspace and can access the collections of the others
you grant it; any other workspace is refused to it. In each it holds a level:

  read        search, query, retrieve and count points; read a collection's
              information and schema
  read_write  read, plus add, change and delete points, and create collections
              and indexes
  full        read and write, plus delete collections, delete indexes and change
              their structure (schema, aliases)

--add grants a workspace, --remove revokes one; each takes a name, repeats, or
takes a comma-separated list. A new grant is read; --level sets the level of
every workspace --add names, and --add of a workspace already granted with
--level changes its level. --level is only taken with --add. --own-level sets
its level where it lives (read, read_write, full), or "default" to follow
the level the workspace sets for its agents ('afy workspaces update
--agent-access-level'; a new workspace's default is read_write). A workspace's
default applies to the agents that live there, never to one that can only access
it. Granting gives the agent access; it does not move it (moving is 'afy update'
with --workspace). An empty value, or one starting with "-", is refused, exit 2.
Your own key is unaffected.

A change that lets the agent's code do more -- a grant, a higher level -- shows
what becomes possible and asks first. --yes skips the question; it is required
when stdin is not a terminal (exit 2, before anything is asked or sent). Answering
no exits 1 and changes nothing. A change that changes nothing asks nothing, and a
change that narrows asks nothing. A change takes effect within ` + accessChangeWindow + `.

--secrets on|off turns on or off whether the agent's own API key may create,
update and delete its OWN secrets. It never reaches its workspace's secrets (those
are shared by every agent living there and are written by people) or another
agent's. Off by default, and a move turns it off. A secret it writes is stored at
once and read when the agent starts: a service sees it after its next deployment,
a task on its next run, which is why it suits tasks that rotate a credential for
their next run. Turning it on asks first (like a grant); turning it off asks
nothing and takes effect on the agent's next request. It is a command of its own:
do not combine it with --add, --remove or --own-level.`,
	Example: `  # What can the agent reach, and at which level?
  afy access support-bot

  # Grant it a second workspace to read, then confirm
  afy access support-bot --add research

  # Let it write points there
  afy access support-bot --add research --level read_write

  # Only read in its own workspace; then back to the workspace's default
  afy access support-bot --own-level read
  afy access support-bot --own-level default

  # From a script: grant one workspace, revoke another
  afy access support-bot --add research --remove archive --yes --json

  # Let it write its own secrets, then take it back
  afy access support-bot --secrets on
  afy access support-bot --secrets off`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		if cmd.Flags().Changed("secrets") {
			if len(agentsAccessAdd) > 0 || len(agentsAccessRemove) > 0 || cmd.Flags().Changed("level") || cmd.Flags().Changed("own-level") {
				return refuse("--secrets is a command of its own: do not combine it with --add, --remove, --level or --own-level. Nothing was sent.")
			}
			return runVec(cmd, func(r *vecRun) int {
				return agentsAccessSecrets(r, args[0], agentsAccessSecretsState, agentsAccessYes, os.Stdin, interactive)
			})
		}
		change := accessChange{
			add:         agentsAccessAdd,
			remove:      agentsAccessRemove,
			level:       agentsAccessLevel,
			levelSet:    cmd.Flags().Changed("level"),
			ownLevel:    agentsAccessOwnLevel,
			ownLevelSet: cmd.Flags().Changed("own-level"),
			yes:         agentsAccessYes,
		}
		return runVec(cmd, func(r *vecRun) int {
			return agentsAccess(r, args[0], change, os.Stdin, interactive)
		})
	},
}

var (
	agentsAccessAdd          []string
	agentsAccessRemove       []string
	agentsAccessLevel        string
	agentsAccessOwnLevel     string
	agentsAccessSecretsState string
	agentsAccessYes          bool
)

// accessChange is what one `afy access` asks for.
type accessChange struct {
	add, remove []string
	// level is the level of every --add workspace; levelSet says --level was given.
	level    string
	levelSet bool
	// ownLevel is --own-level: a level, or followDefault.
	ownLevel    string
	ownLevelSet bool
	yes         bool
}

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

func isLevel(v string) bool {
	for _, l := range api.AccessLevels {
		if v == l {
			return true
		}
	}
	return false
}

// levelRank orders the levels, lowest first; -1 for none.
func levelRank(v string) int {
	for i, l := range api.AccessLevels {
		if v == l {
			return i
		}
	}
	return -1
}

func scopeLabel(scope string) string {
	return fmt.Sprintf("workspace '%s'", scope)
}

// grantedLevel is the level a granted workspace holds, "" when not granted.
func grantedLevel(a *api.AgentAccess, scope string) string {
	for _, w := range a.GrantedWorkspaceRefs {
		if w.Name == scope {
			return w.Level
		}
	}
	for _, w := range a.GrantedWorkspaces {
		if w == scope {
			return "read"
		}
	}
	return ""
}

func granted(a *api.AgentAccess, scope string) bool {
	for _, w := range a.GrantedWorkspaces {
		if w == scope {
			return true
		}
	}
	return false
}

func agentsAccess(r *vecRun, agent string, c accessChange, stdin io.Reader, interactive bool) int {
	if err := refuseFlagLikeValue("add", c.add...); err != nil {
		return r.fail(err)
	}
	if err := refuseFlagLikeValue("remove", c.remove...); err != nil {
		return r.fail(err)
	}
	if err := refuseEmptyScope("add", c.add); err != nil {
		return r.fail(err)
	}
	if err := refuseEmptyScope("remove", c.remove); err != nil {
		return r.fail(err)
	}
	if c.levelSet && len(c.add) == 0 {
		return r.fail(refuse("--level sets the level of the workspaces --add names, and no --add was given. " +
			"For its own workspace, use --own-level. Nothing was sent."))
	}
	if c.levelSet && !isLevel(c.level) {
		return r.fail(refuse("--level must be one of %s; got '%s'. Nothing was sent.",
			strings.Join(api.AccessLevels, ", "), c.level))
	}
	if c.ownLevelSet && c.ownLevel != followDefault && !isLevel(c.ownLevel) {
		return r.fail(refuse("--own-level must be one of %s, or %s; got '%s'. Nothing was sent.",
			strings.Join(api.AccessLevels, ", "), followDefault, c.ownLevel))
	}
	return changeAgentAccess(r, agent, c, stdin, interactive)
}

// levelChange is a granted workspace whose level moves.
type levelChange struct {
	Workspace string `json:"workspace"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// changeAgentAccess is agentsAccess once its flag values were checked.
func changeAgentAccess(r *vecRun, agent string, c accessChange, stdin io.Reader, interactive bool) int {
	adds, removes := scopes(c.add), scopes(c.remove)
	for _, s := range adds {
		for _, t := range removes {
			if s == t {
				return r.fail(refuse("%s is both added and removed: pass it to one of --add or --remove", scopeLabel(s)))
			}
		}
	}
	changing := len(adds) > 0 || len(removes) > 0 || c.ownLevelSet

	// The current access is read FIRST: whether the change widens anything is
	// known only from it. An --add of a workspace already granted changes
	// nothing and must not need a --yes under --json.
	cp := r.controlPlane()
	current, err := cp.GetAgentAccess(agent)
	if err != nil {
		return r.fail(err)
	}
	if !changing {
		if r.json {
			return r.printJSON(accessJSON(current, nil, nil, nil, false))
		}
		printAccess(r.stdout, current)
		return 0
	}

	// The agent's own scope is not a grant: it always reaches it, and the
	// control plane refuses granting or revoking it (AGENT_WORKSPACE_GRANT_REDUNDANT).
	// Its level there is --own-level. Refused here, before any change is shown
	// or asked about.
	for _, s := range append(append([]string(nil), adds...), removes...) {
		if current.OwnWorkspace == s {
			return r.fail(refuse("%s is agent '%s''s own: its key always reaches it, so there is "+
				"nothing to grant or revoke. Its level there is --own-level.", scopeLabel(s), agent))
		}
	}

	// What would change: only what moves.
	var toAdd, toRemove []string
	var levels []levelChange
	addLevel := "read"
	if c.levelSet {
		addLevel = c.level
	}
	for _, s := range adds {
		held := grantedLevel(current, s)
		switch {
		case held == "":
			toAdd = append(toAdd, s)
		case c.levelSet && held != c.level:
			levels = append(levels, levelChange{Workspace: s, From: held, To: c.level})
		}
	}
	for _, s := range removes {
		if granted(current, s) {
			toRemove = append(toRemove, s)
		}
	}
	ownChange := false
	var ownSetting *string
	ownTo := current.OwnWorkspaceLevel
	if c.ownLevelSet {
		if c.ownLevel != followDefault {
			v := c.ownLevel
			ownSetting = &v
			ownTo = v
		} else {
			ownTo = current.WorkspaceDefaultLevel
		}
		was := ""
		if current.OwnWorkspaceLevelSetting != nil {
			was = *current.OwnWorkspaceLevelSetting
		}
		now := ""
		if ownSetting != nil {
			now = *ownSetting
		}
		ownChange = was != now
	}

	widening := len(toAdd) > 0 || (ownChange && levelRank(ownTo) > levelRank(current.OwnWorkspaceLevel))
	for _, l := range levels {
		if levelRank(l.To) > levelRank(l.From) {
			widening = true
		}
	}
	// One confirmation for every widening: exit 2 before anything is asked when
	// nobody can answer, exit 1 on a "no" (cmd/confirm.go).
	ask := destructiveAsk{
		action:   fmt.Sprintf("grants access to agent '%s'", agent),
		prompt:   "Proceed? [y/N] ",
		accept:   yesAnswer,
		mismatch: fmt.Sprintf("agent '%s' was not changed", agent),
	}
	if widening {
		if err := refuseUnconfirmable(c.yes, interactive, ask.action, ask.skip()); err != nil {
			return r.fail(err)
		}
	}
	preview := accessPreview{
		toAdd: toAdd, addLevel: addLevel, toRemove: toRemove, levels: levels,
		ownChange: ownChange, ownTo: ownTo, ownFollows: c.ownLevelSet && ownSetting == nil,
		adds: adds, removes: removes,
	}
	if !r.json {
		printAccessPreview(r.stdout, current, preview)
	} else if widening && !c.yes {
		// --json keeps stdout to one object; the person at the terminal still
		// sees what they are asked to confirm.
		printAccessPreview(r.stderr, current, preview)
	}
	if len(toAdd) == 0 && len(toRemove) == 0 && len(levels) == 0 && !ownChange {
		if r.json {
			return r.printJSON(accessJSON(current, []string{}, []string{}, []levelChange{}, false))
		}
		return 0
	}
	if widening && !c.yes {
		if err := askToConfirm(ask, vecPrompter(r, stdin)); err != nil {
			return r.fail(err)
		}
	}

	final := current
	added, removed, leveled := []string{}, []string{}, []levelChange{}
	ownDone := false
	fail := func(err error) int {
		return r.fail(partialAccessError(err, added, removed, leveled, ownDone))
	}
	// Narrow first, then widen: an interrupted run leaves less reachable, never more.
	for _, s := range toRemove {
		next, err := cp.RevokeWorkspace(agent, s)
		if err != nil {
			return fail(err)
		}
		final, removed = next, append(removed, s)
	}
	sort.SliceStable(levels, func(i, j int) bool {
		return levelRank(levels[i].To)-levelRank(levels[i].From) < levelRank(levels[j].To)-levelRank(levels[j].From)
	})
	ownLowers := ownChange && levelRank(ownTo) <= levelRank(current.OwnWorkspaceLevel)
	if ownLowers {
		next, err := cp.SetOwnWorkspaceLevel(agent, ownSetting)
		if err != nil {
			return fail(err)
		}
		final, ownDone = next, true
	}
	for _, l := range levels {
		next, err := cp.GrantWorkspace(agent, l.Workspace, l.To)
		if err != nil {
			return fail(err)
		}
		final, leveled = next, append(leveled, l)
	}
	if ownChange && !ownLowers {
		next, err := cp.SetOwnWorkspaceLevel(agent, ownSetting)
		if err != nil {
			return fail(err)
		}
		final, ownDone = next, true
	}
	for _, s := range toAdd {
		level := ""
		if c.levelSet {
			level = c.level
		}
		next, err := cp.GrantWorkspace(agent, s, level)
		if err != nil {
			return fail(err)
		}
		final, added = next, append(added, s)
	}
	if r.json {
		return r.printJSON(accessJSON(final, added, removed, leveled, ownDone))
	}
	fmt.Fprintf(r.stdout, "Access of agent '%s' changed; it takes effect within %s.\n", agent, accessChangeWindow)
	printAccess(r.stdout, final)
	return 0
}

// partialAccessError keeps what was already applied visible: the changes are
// one request each.
func partialAccessError(err error, added, removed []string, leveled []levelChange, ownDone bool) error {
	if len(added) == 0 && len(removed) == 0 && len(leveled) == 0 && !ownDone {
		return err
	}
	var levels []string
	for _, l := range leveled {
		levels = append(levels, fmt.Sprintf("%s to %s", scopeLabel(l.Workspace), l.To))
	}
	if ownDone {
		levels = append(levels, "its own workspace's level")
	}
	return fmt.Errorf("%w (already applied: added %s; removed %s; levels %s)",
		err, labels(added), labels(removed), orNone(levels))
}

func orNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

func labels(scopes []string) string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = scopeLabel(s)
	}
	return orNone(out)
}

// ownLevelNote says where the agent's level where it lives comes from.
func ownLevelNote(a *api.AgentAccess) string {
	if a.OwnWorkspaceLevelSetting == nil {
		return fmt.Sprintf("%s, the workspace's default", wordsOf(a.OwnWorkspaceLevel))
	}
	return fmt.Sprintf("%s, set for this agent", wordsOf(a.OwnWorkspaceLevel))
}

// printAccess says where the agent lives and which workspaces it can access,
// each at its level in plain words, then whether it may write its own secrets.
func printAccess(w io.Writer, a *api.AgentAccess) {
	fmt.Fprintf(w, "Agent '%s':\n", a.Agent)
	fmt.Fprintf(w, "  Lives in: %s  (%s)\n", a.OwnWorkspace, ownLevelNote(a))
	names := append([]string(nil), a.GrantedWorkspaces...)
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Fprintln(w, "  Can access: no other workspace")
	} else {
		fmt.Fprintln(w, "  Can access:")
		for _, n := range names {
			fmt.Fprintf(w, "    %s  (%s)\n", n, wordsOf(grantedLevel(a, n)))
		}
	}
	if a.SecretsAccess {
		fmt.Fprintln(w, "  Secrets: on (its own API key may write its own secrets)")
	} else {
		fmt.Fprintln(w, "  Secrets: off")
	}
}

type accessPreview struct {
	toAdd      []string
	addLevel   string
	toRemove   []string
	levels     []levelChange
	ownChange  bool
	ownTo      string
	ownFollows bool
	adds       []string
	removes    []string
}

// printAccessPreview says what the change would do, in the dashboard's words.
func printAccessPreview(w io.Writer, a *api.AgentAccess, p accessPreview) {
	if len(p.toAdd) == 0 && len(p.toRemove) == 0 && len(p.levels) == 0 && !p.ownChange {
		fmt.Fprintf(w, "No change: agent '%s' already has the access asked for.\n", a.Agent)
		return
	}
	fmt.Fprintf(w, "Agent '%s':\n", a.Agent)
	narrows := false
	for _, s := range p.toAdd {
		fmt.Fprintf(w, "  Adds %s at %s, every collection in it, including ones added later.\n    %s\n",
			scopeLabel(s), wordsOf(p.addLevel), raisedSentence(s, p.addLevel))
	}
	for _, l := range p.levels {
		fmt.Fprintf(w, "  Changes its level in %s from %s to %s.\n", scopeLabel(l.Workspace), wordsOf(l.From), wordsOf(l.To))
		if levelRank(l.To) > levelRank(l.From) {
			fmt.Fprintf(w, "    %s\n", raisedSentence(l.Workspace, l.To))
		} else {
			narrows = true
		}
	}
	if p.ownChange {
		how := "set for this agent"
		if p.ownFollows {
			how = "the workspace's default"
		}
		fmt.Fprintf(w, "  Sets its level where it lives, workspace '%s', to %s (%s).\n", a.OwnWorkspace, wordsOf(p.ownTo), how)
		if levelRank(p.ownTo) > levelRank(a.OwnWorkspaceLevel) {
			fmt.Fprintf(w, "    %s\n", raisedSentence(a.OwnWorkspace, p.ownTo))
		} else if p.ownTo != a.OwnWorkspaceLevel {
			narrows = true
		}
	}
	for _, s := range p.toRemove {
		fmt.Fprintf(w, "  Removes %s: the agent can no longer use it.\n", scopeLabel(s))
		narrows = true
	}
	if narrows {
		fmt.Fprintf(w, "  %s\n", narrowedNote)
	}
	for _, s := range p.adds {
		if !contains(p.toAdd, s) && !hasLevelChange(p.levels, s) {
			fmt.Fprintf(w, "  Already reaches %s.\n", scopeLabel(s))
		}
	}
	for _, s := range p.removes {
		if !contains(p.toRemove, s) {
			fmt.Fprintf(w, "  Already does not have %s granted.\n", scopeLabel(s))
		}
	}
}

func hasLevelChange(levels []levelChange, s string) bool {
	for _, l := range levels {
		if l.Workspace == s {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func accessJSON(a *api.AgentAccess, added, removed []string, leveled []levelChange, ownChanged bool) interface{} {
	return struct {
		Access          *api.AgentAccess `json:"access"`
		Added           []string         `json:"added"`
		Removed         []string         `json:"removed"`
		LevelsChanged   []levelChange    `json:"levels_changed"`
		OwnLevelChanged bool             `json:"own_level_changed"`
	}{Access: a, Added: added, Removed: removed, LevelsChanged: leveled, OwnLevelChanged: ownChanged}
}

// --- SECRETS ---

func agentsAccessSecrets(r *vecRun, agent, state string, yes bool, stdin io.Reader, interactive bool) int {
	var on bool
	switch state {
	case "on":
		on = true
	case "off":
		on = false
	default:
		return r.fail(refuse("--secrets is on or off; got '%s'. Nothing was sent.", state))
	}
	// Read FIRST: a state already set changes nothing and asks nothing, even
	// under --json.
	cp := r.controlPlane()
	current, err := cp.GetAgentAccess(agent)
	if err != nil {
		return r.fail(err)
	}
	if current.SecretsAccess == on {
		if r.json {
			return r.printJSON(secretsJSON(current, false))
		}
		fmt.Fprintf(r.stdout, "No change: secrets access of agent '%s' is already %s.\n", agent, state)
		return 0
	}
	if on && !yes {
		ask := destructiveAsk{
			action: fmt.Sprintf("turns on secrets access for agent '%s'", agent),
			warn: []string{fmt.Sprintf("Agent '%s' will be able to create, update and delete its own secrets with the API "+
				"key Aetherfy gives it. It never reaches its workspace's secrets or another agent's. A new value takes "+
				"effect at its next run or deploy.", agent)},
			prompt:   "Proceed? [y/N] ",
			accept:   yesAnswer,
			mismatch: fmt.Sprintf("agent '%s' was not changed", agent),
		}
		if err := confirmDestructiveOn(yes, ask, interactive, vecPrompter(r, stdin)); err != nil {
			return r.fail(err)
		}
	}
	final, err := cp.SetSecretsAccess(agent, on)
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(secretsJSON(final, true))
	}
	fmt.Fprintf(r.stdout, "Secrets access of agent '%s' is %s. A secret it writes is read when the agent starts: "+
		"a service sees it after its next deployment, a task on its next run.\n", agent, state)
	return 0
}

func secretsJSON(a *api.AgentAccess, changed bool) interface{} {
	return struct {
		Agent         string `json:"agent"`
		SecretsAccess bool   `json:"secrets_access"`
		Changed       bool   `json:"changed"`
	}{Agent: a.Agent, SecretsAccess: a.SecretsAccess, Changed: changed}
}

func init() {
	agentsAccessCmd.Flags().StringArrayVar(&agentsAccessAdd, "add", nil, "Grant a workspace (repeat, or comma-separated)")
	agentsAccessCmd.Flags().StringArrayVar(&agentsAccessRemove, "remove", nil, "Revoke a granted workspace (repeat, or comma-separated)")
	agentsAccessCmd.Flags().StringVar(&agentsAccessLevel, "level", "", "The level of the --add workspaces: read, read_write or full (a new grant is read)")
	agentsAccessCmd.Flags().StringVar(&agentsAccessOwnLevel, "own-level", "", "Its level where it lives: read, read_write, full, or default (the workspace's default)")
	agentsAccessCmd.Flags().StringVar(&agentsAccessSecretsState, "secrets", "", "Let the agent's own key write its own secrets: on or off")
	agentsAccessCmd.Flags().BoolVarP(&agentsAccessYes, "yes", "y", false, "Change access without asking")
}
