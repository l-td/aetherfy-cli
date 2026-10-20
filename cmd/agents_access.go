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
// "Proceed? [y/N]". --yes skips the question; without a terminal, or with
// --json, --yes is required (exit 2): a script must never hang on a question
// nobody can answer. A change that narrows asks nothing. A change that changes
// nothing says so and asks nothing.

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

An agent reaches the collections of its own workspace, and of the workspaces
you grant it; any other workspace is refused to it. In each it holds a level:

  read        search, query and read; nothing it can change
  read_write  read, and add, change and delete points
  full        read and write, and create and delete collections, their
              schema, indexes and aliases

--add grants a workspace, --remove revokes one; each takes a name, repeats, or
takes a comma-separated list. A new grant is read; --level sets the level of
every workspace --add names, and --add of a workspace already granted with
--level changes its level. --level is only taken with --add. --own-level sets
its level in its own workspace (read, read_write, full), or "default" to follow
the level the workspace sets for its agents ('afy workspaces update
--agent-access-level'). Granting gives the agent access; it does not move it
(moving is 'afy update' with --workspace). An empty value, or one starting with
"-", is refused, exit 2. Your own key is unaffected.

A change that lets the agent's code do more -- a grant, a higher level -- shows
what becomes possible and asks first. --yes skips the question; it is required
when stdin is not a terminal and with --json. A change that narrows asks
nothing. A change takes effect within ` + accessChangeWindow + `.

Whether the agent's own key may write its own secrets is 'afy access secrets'.`,
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
  afy access support-bot --add research --remove archive --yes --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
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
	agentsAccessAdd      []string
	agentsAccessRemove   []string
	agentsAccessLevel    string
	agentsAccessOwnLevel string
	agentsAccessYes      bool
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
	// An --add widens (a new grant) or changes a level, so without a way to
	// confirm it is refused before anything is sent. Whether an --own-level
	// widens is known only from the current access, read first.
	mayWiden := len(adds) > 0
	if mayWiden && !c.yes && r.json {
		return r.fail(refuse("not changing agent '%s': --json cannot answer the confirmation. "+
			"Pass --yes to change its access without the question.", agent))
	}
	if mayWiden && !c.yes && !interactive {
		return r.fail(refuse("not changing agent '%s': stdin is not a terminal, so nobody can "+
			"confirm. Pass --yes to change its access without the question.", agent))
	}

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
	if widening && !c.yes && (r.json || !interactive) {
		why := "--json cannot answer the confirmation"
		if !r.json {
			why = "stdin is not a terminal, so nobody can confirm"
		}
		return r.fail(refuse("not changing agent '%s': the change raises its level, and %s. "+
			"Pass --yes to change its access without the question.", agent, why))
	}
	if !r.json {
		printAccessPreview(r.stdout, current, accessPreview{
			toAdd: toAdd, addLevel: addLevel, toRemove: toRemove, levels: levels,
			ownChange: ownChange, ownTo: ownTo, ownFollows: c.ownLevelSet && ownSetting == nil,
			adds: adds, removes: removes,
		})
	}
	if len(toAdd) == 0 && len(toRemove) == 0 && len(levels) == 0 && !ownChange {
		if r.json {
			return r.printJSON(accessJSON(current, []string{}, []string{}, []levelChange{}, false))
		}
		return 0
	}
	if widening && !c.yes {
		fmt.Fprint(r.stderr, "Proceed? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return r.fail(refuse("agent '%s' not changed: the change was not confirmed", agent))
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

// levelReach is what a level lets the agent do with a workspace's
// collections, as the preview says it.
var levelReach = map[string]string{
	"read":       "searchable and readable",
	"read_write": "readable, and its points writable",
	"full":       "readable and writable, and its collections creatable and deletable",
}

// ownLevelNote says where the agent's level at home comes from.
func ownLevelNote(a *api.AgentAccess) string {
	if a.OwnWorkspaceLevelSetting == nil {
		return fmt.Sprintf("%s, the workspace's default", a.OwnWorkspaceLevel)
	}
	return fmt.Sprintf("%s, set for this agent", a.OwnWorkspaceLevel)
}

// printAccess lists what the agent reaches and its level in each, its own
// scope first, then whether it may write secrets.
func printAccess(w io.Writer, a *api.AgentAccess) {
	fmt.Fprintf(w, "Agent '%s' can use:\n", a.Agent)
	fmt.Fprintf(w, "  %s  (its own workspace; %s)\n", a.OwnWorkspace, ownLevelNote(a))
	names := append([]string(nil), a.GrantedWorkspaces...)
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %s  (granted; %s)\n", n, grantedLevel(a, n))
	}
	if a.SecretsAccess {
		fmt.Fprintln(w, "Secrets: on (its own key may write its own and its workspace's secrets)")
	} else {
		fmt.Fprintln(w, "Secrets: off")
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
	for _, s := range p.toAdd {
		fmt.Fprintf(w, "  Adds %s at %s: every collection in workspace '%s', including ones added later, becomes %s by this agent's code.\n",
			scopeLabel(s), p.addLevel, s, levelReach[p.addLevel])
	}
	for _, l := range p.levels {
		fmt.Fprintf(w, "  Changes its level in %s from %s to %s: every collection there becomes %s by this agent's code.\n",
			scopeLabel(l.Workspace), l.From, l.To, levelReach[l.To])
	}
	if p.ownChange {
		how := "set for this agent"
		if p.ownFollows {
			how = "the workspace's default"
		}
		fmt.Fprintf(w, "  Sets its level in its own workspace '%s' to %s (%s): every collection there becomes %s by this agent's code.\n",
			a.OwnWorkspace, p.ownTo, how, levelReach[p.ownTo])
	}
	for _, s := range p.toRemove {
		fmt.Fprintf(w, "  Removes %s: the agent can no longer use it.\n", scopeLabel(s))
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

var agentsAccessSecretsCmd = &cobra.Command{
	Use:   "secrets <agent> on|off",
	Short: "Let an agent's own key write its own secrets, or not",
	Long: `Turn on or off whether an agent's own API key may create, update and delete the
secrets of that agent and of its own workspace -- never those of a workspace it
was granted. Off by default.

A secret the agent writes is stored at once and read once per deployment: a
service sees it after its next deployment, a task on its next run.

Turning it on asks first; --yes skips the question, and is required when stdin
is not a terminal and with --json. Turning it off asks nothing and takes effect
on the agent's next request. (An agent named "secrets" is addressed by its id
in 'afy access'.)`,
	Example: `  # Let the agent rotate its own keys
  afy access secrets support-bot on

  # And take it back
  afy access secrets support-bot off`,
	Args: refuseArgs(cobra.ExactArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		return runVec(cmd, func(r *vecRun) int {
			return agentsAccessSecrets(r, args[0], args[1], agentsAccessSecretsYes, os.Stdin, interactive)
		})
	},
}

var agentsAccessSecretsYes bool

func agentsAccessSecrets(r *vecRun, agent, state string, yes bool, stdin io.Reader, interactive bool) int {
	var on bool
	switch state {
	case "on":
		on = true
	case "off":
		on = false
	default:
		return r.fail(refuse("the second argument is on or off; got '%s'. Nothing was sent.", state))
	}
	if on && !yes && r.json {
		return r.fail(refuse("not changing agent '%s': --json cannot answer the confirmation. "+
			"Pass --yes to turn it on without the question.", agent))
	}
	if on && !yes && !interactive {
		return r.fail(refuse("not changing agent '%s': stdin is not a terminal, so nobody can "+
			"confirm. Pass --yes to turn it on without the question.", agent))
	}
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
		fmt.Fprintf(r.stdout, "Agent '%s' will be able to create, update and delete its own secrets and its own "+
			"workspace '%s''s secrets with the API key Aetherfy gives it, including keys other agents of that "+
			"workspace use.\n", agent, current.OwnWorkspace)
		fmt.Fprint(r.stderr, "Proceed? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return r.fail(refuse("agent '%s' not changed: the change was not confirmed", agent))
		}
	}
	final, err := cp.SetSecretsAccess(agent, on)
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(secretsJSON(final, true))
	}
	fmt.Fprintf(r.stdout, "Secrets access of agent '%s' is %s. A secret it writes is read once per deployment: "+
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
	agentsAccessCmd.Flags().StringVar(&agentsAccessOwnLevel, "own-level", "", "Its level in its own workspace: read, read_write, full, or default")
	agentsAccessCmd.Flags().BoolVarP(&agentsAccessYes, "yes", "y", false, "Change access without asking")
	agentsAccessSecretsCmd.Flags().BoolVarP(&agentsAccessSecretsYes, "yes", "y", false, "Turn it on without asking")
	agentsAccessCmd.AddCommand(agentsAccessSecretsCmd)
}
