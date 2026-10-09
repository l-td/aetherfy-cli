package cmd

// afy collections regions / move: the two control-plane changes to one
// collection. The collection is found by name through the vectors API, in the
// workspace the request is scoped to (--workspace, AETHERFY_WORKSPACE, or
// none) like every collections command, which answers its id; the change goes
// to the control plane by that id.
//
// TWO STEPS FOR A CHANGE THAT COPIES OR DELETES DATA. A regions change shows
// the server's preview -- built by the same planner the change runs, so it
// cannot promise what the change will not do -- and asks "Proceed? [y/N]".
// --yes skips the question. Without a terminal, or with --json, --yes is
// required: a script must never hang on a question nobody can answer. A
// refused preview sends nothing. A move copies nothing (it changes which
// workspace reaches the collection), so it asks nothing.
//
// --wait follows the operation to its end, as `afy start --wait` follows a
// start: exit 0 when it succeeds, 1 when it fails, 4 when it is still running
// at the bound.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// --- REGIONS ---

var collectionsRegionsCmd = &cobra.Command{
	Use:   "regions <name>",
	Short: "Change the regions a collection's data is placed in",
	Long: `Change the regions a collection's data is placed in.

A region you add gets a copy of the collection's data; a region you remove has
its copy deleted. The command first shows what the change would do -- the data
it copies and deletes, which of your plan's region changes per 30 days it would
be, how much copying is left -- and asks before making it. --yes skips the
question; it is required when stdin is not a terminal and with --json.

The change runs in the background. --wait follows it to the end: exit 1 if it
fails, 4 if it is still running after 30 minutes.`,
	Example: `  # See what moving to two regions would do, then confirm
  afy collections regions articles --regions us-east-1,eu-central-1

  # From a script, following the change to its end
  afy collections regions articles --regions us-east-1 --workspace research --yes --wait --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		return runVec(cmd, func(r *vecRun) int {
			return collectionsRegions(r, args[0], collectionRegionsSet, collectionRegionsYes, collectionRegionsWait, os.Stdin, interactive)
		})
	},
}

var (
	collectionRegionsSet  []string
	collectionRegionsYes  bool
	collectionRegionsWait bool
)

func collectionsRegions(r *vecRun, name string, regions []string, yes, wait bool, stdin io.Reader, interactive bool) int {
	if len(regions) == 0 {
		return r.fail(refuse("--regions is required: the regions to place collection '%s' in, comma-separated", name))
	}
	if !yes && r.json {
		return r.fail(refuse("not changing collection '%s': --json cannot answer the confirmation. "+
			"Pass --yes to change it without the question.", name))
	}
	if !yes && !interactive {
		return r.fail(refuse("not changing collection '%s': stdin is not a terminal, so nobody can "+
			"confirm. Pass --yes to change it without the question.", name))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	col, err := client.GetCollection(name)
	if err != nil {
		return r.fail(err)
	}
	cp := r.controlPlane()
	preview, err := cp.PreviewCollectionRegions(col.ID, regions)
	if err != nil {
		return r.fail(err)
	}
	if !r.json {
		printRegionsPreview(r.stdout, name, workspaceLabel(client.Workspace()), preview)
	}
	if refusal := preview.RefusalError(); refusal != nil {
		// What the change would answer: sent nowhere.
		return r.fail(refusal)
	}
	if preview.NoOp {
		if r.json {
			return r.printJSON(regionsJSON(r, name, preview, nil, nil))
		}
		return 0
	}
	if !yes {
		fmt.Fprint(r.stderr, "Proceed? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return r.fail(refuse("collection '%s' not changed: the change was not confirmed", name))
		}
	}
	change, err := cp.ChangeCollectionRegions(col.ID, regions)
	if err != nil {
		return r.fail(err)
	}
	var op *api.Operation
	if wait && change.OperationID != "" {
		if !r.json {
			fmt.Fprintf(r.stdout, "Changing the regions of collection '%s' (operation %s)...\n", name, change.OperationID)
		}
		var code int
		op, code = followOperation(r, cp, change.OperationID)
		if code != 0 {
			return code
		}
	}
	if r.json {
		return r.printJSON(regionsJSON(r, name, preview, change, op))
	}
	switch {
	case change.NoOp:
		fmt.Fprintf(r.stdout, "No change: collection '%s' is already in those regions.\n", name)
	case op != nil:
		fmt.Fprintf(r.stdout, "Collection '%s' is now in %s.\n", name, strings.Join(preview.ToRegions, ", "))
	default:
		fmt.Fprintf(r.stdout, "Regions change for collection '%s' accepted (operation %s). It runs in the background: "+
			"'afy collections get %s' shows the new regions once it finishes, or pass --wait to follow it.\n",
			name, change.OperationID, name)
	}
	return 0
}

// printRegionsPreview says what the change would do, in the dashboard's words.
func printRegionsPreview(w io.Writer, name, where string, p *api.RegionsChangePreview) {
	if p.NoOp {
		fmt.Fprintf(w, "No change: collection '%s' in %s is already in %s.\n", name, where, strings.Join(p.FromRegions, ", "))
		return
	}
	fmt.Fprintf(w, "Collection '%s' in %s: %s -> %s\n", name, where,
		strings.Join(p.FromRegions, ", "), strings.Join(p.ToRegions, ", "))
	if len(p.RegionsToAdd) > 0 {
		it := "them"
		if len(p.RegionsToAdd) == 1 {
			it = "it"
		}
		fmt.Fprintf(w, "  Adds %s: copies %s of data into %s.\n", strings.Join(p.RegionsToAdd, ", "), humanBytes(p.CopyBytes), it)
	}
	if len(p.DataDeletedFrom) > 0 {
		fmt.Fprintf(w, "  Removes %s: this collection's data there is deleted.\n", strings.Join(p.DataDeletedFrom, ", "))
	}
	// A refused change: the error that follows names the numbers, and the
	// window's lines would describe a change that will not be made.
	if l := p.Limits; l != nil && p.Refusal == nil {
		if !l.Counted {
			fmt.Fprintln(w, "  Copies no data, so it does not count toward your plan's region-change limits.")
		} else {
			if l.Count.Limit == nil {
				fmt.Fprintln(w, "  A region change that copies data. Your plan has no limit on these.")
			} else if l.Count.ThisChange != nil {
				fmt.Fprintf(w, "  Region change %d of %d that copy data in the last %d days.\n", *l.Count.ThisChange, *l.Count.Limit, l.WindowDays)
			}
			if l.Size.LimitBytes != nil && l.Size.LeftAfterBytes != nil {
				left := *l.Size.LeftAfterBytes
				if left < 0 {
					left = 0
				}
				fmt.Fprintf(w, "  %s of %s copied between regions in the last %d days; %s left after this change.\n",
					humanBytes(l.Size.UsedBytes), humanBytes(*l.Size.LimitBytes), l.WindowDays, humanBytes(left))
			}
		}
	}
}

// humanBytes is a byte count in 1024-steps, one decimal under 100 (the
// dashboard's formatBytes).
func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	u := 0
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if v >= 100 || u == 0 {
		return fmt.Sprintf("%.0f %s", v, units[u])
	}
	s := fmt.Sprintf("%.1f", v)
	s = strings.TrimSuffix(s, ".0")
	return s + " " + units[u]
}

func regionsJSON(r *vecRun, name string, p *api.RegionsChangePreview, change *api.CollectionChange, op *api.Operation) interface{} {
	out := struct {
		vecEnvelope
		Collection  string                    `json:"collection"`
		Preview     *api.RegionsChangePreview `json:"preview"`
		OperationID *string                   `json:"operation_id"`
		Operation   *api.Operation            `json:"operation"`
	}{vecEnvelope: r.envelope(), Collection: name, Preview: p, Operation: op}
	if change != nil && change.OperationID != "" {
		id := change.OperationID
		out.OperationID = &id
	}
	return out
}

// --- MOVE ---

var collectionsMoveCmd = &cobra.Command{
	Use:   "move <name>",
	Short: "Move a collection to another workspace, or out of any",
	Long: `Move a collection to another workspace, or out of any (--to ""; on
Windows PowerShell 5.1, which drops an empty argument, write --to= instead).

A move changes only which workspace reaches the collection: its regions and
its data stay where they are, so the target must allow every region it is
placed in. Its aliases are deleted; recreate the ones you need. The collection
is found in the workspace --workspace names, like every collections command,
and --to names where it goes. It copies nothing, so it asks nothing.

--wait follows the move to the end: exit 1 if it fails, 4 if it is still
running after 30 minutes.`,
	Example: `  # Move a collection from no workspace into "research"
  afy collections move articles --to research

  # Move it back out of any workspace, and wait for it
  afy collections move articles --workspace research --to "" --wait`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		toSet := cmd.Flags().Changed("to")
		return runVec(cmd, func(r *vecRun) int {
			return collectionsMove(r, args[0], collectionMoveTo, toSet, collectionMoveWait)
		})
	},
}

var (
	collectionMoveTo   string
	collectionMoveWait bool
)

func collectionsMove(r *vecRun, name, to string, toSet, wait bool) int {
	if !toSet {
		return r.fail(refuse("--to is required: the workspace to move collection '%s' to, or \"\" for none", name))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	col, err := client.GetCollection(name)
	if err != nil {
		return r.fail(err)
	}
	cp := r.controlPlane()
	var target *string
	if to != "" {
		ws, err := cp.GetWorkspace(to)
		if err != nil {
			return r.fail(err)
		}
		target = &ws.ID
	}
	change, err := cp.MoveCollection(col.ID, target)
	if err != nil {
		return r.fail(err)
	}
	var op *api.Operation
	if wait && change.OperationID != "" {
		if !r.json {
			fmt.Fprintf(r.stdout, "Moving collection '%s' to %s (operation %s)...\n", name, workspaceLabel(to), change.OperationID)
		}
		var code int
		op, code = followOperation(r, cp, change.OperationID)
		if code != 0 {
			return code
		}
	}
	if r.json {
		out := struct {
			vecEnvelope
			Collection  string         `json:"collection"`
			To          *string        `json:"to"`
			NoOp        bool           `json:"no_op"`
			Regions     []string       `json:"regions"`
			OperationID *string        `json:"operation_id"`
			Operation   *api.Operation `json:"operation"`
		}{vecEnvelope: r.envelope(), Collection: name, NoOp: change.NoOp, Regions: change.Regions, Operation: op}
		if to != "" {
			out.To = &to
		}
		if change.OperationID != "" {
			id := change.OperationID
			out.OperationID = &id
		}
		return r.printJSON(out)
	}
	switch {
	case change.NoOp:
		fmt.Fprintf(r.stdout, "No change: collection '%s' is already in %s.\n", name, workspaceLabel(to))
	case op != nil:
		fmt.Fprintf(r.stdout, "Collection '%s' moved to %s. Its data and regions are unchanged; its aliases were deleted.\n",
			name, workspaceLabel(to))
	default:
		fmt.Fprintf(r.stdout, "Collection '%s' is moving to %s (operation %s). Its data and regions stay where they are; "+
			"its aliases are deleted. Pass --wait to follow the move.\n", name, workspaceLabel(to), change.OperationID)
	}
	return 0
}

// --- following an operation ---

// The bound --wait follows an operation for (afy run --wait's), and how
// often it asks. Vars so tests can shorten them.
var (
	operationWaitBound    = 30 * time.Minute
	operationPollInterval = 3 * time.Second
	operationSleep        = time.Sleep
)

// pollOperation polls an operation until it ends: the final operation and
// nil when it succeeded; an error saying why when it failed; a
// *stillPendingError (exit 4, as `afy start --wait`) when the bound passes
// first. A poll that cannot be answered returns that error.
func pollOperation(cp *api.Client, id string) (*api.Operation, error) {
	var waited time.Duration
	for {
		op, err := cp.GetOperation(id)
		if err != nil {
			return nil, err
		}
		if op.Done() {
			if op.Status == "failed" {
				msg := fmt.Sprintf("operation %s failed", id)
				for _, f := range op.Failures {
					msg += ": " + f.Message
				}
				return op, fmt.Errorf("%s", msg)
			}
			return op, nil
		}
		if waited >= operationWaitBound {
			return op, &stillPendingError{msg: fmt.Sprintf(
				"operation %s is still %s after %s. It keeps running; its result shows once it finishes.",
				id, op.Status, operationWaitBound)}
		}
		operationSleep(operationPollInterval)
		waited += operationPollInterval
	}
}

// followOperation is pollOperation for the vector-family commands: the
// failure reported through r, and its exit code.
func followOperation(r *vecRun, cp *api.Client, id string) (*api.Operation, int) {
	op, err := pollOperation(cp, id)
	if err == nil {
		return op, 0
	}
	code := r.fail(err)
	if _, pending := err.(*stillPendingError); pending {
		return op, exitStillPending
	}
	return op, code
}

func init() {
	collectionsRegionsCmd.Flags().StringSliceVar(&collectionRegionsSet, "regions", nil, "Regions to place the collection in, comma-separated, required")
	collectionsRegionsCmd.Flags().BoolVarP(&collectionRegionsYes, "yes", "y", false, "Make the change without asking")
	collectionsRegionsCmd.Flags().BoolVar(&collectionRegionsWait, "wait", false, "Follow the change to the end (exit 1 if it fails, 4 if still running after 30 minutes)")

	collectionsMoveCmd.Flags().StringVar(&collectionMoveTo, "to", "", "Workspace to move the collection to (\"\" for none, written --to= on Windows PowerShell 5.1), required")
	collectionsMoveCmd.Flags().BoolVar(&collectionMoveWait, "wait", false, "Follow the move to the end (exit 1 if it fails, 4 if still running after 30 minutes)")

	collectionsCmd.AddCommand(collectionsRegionsCmd)
	collectionsCmd.AddCommand(collectionsMoveCmd)
}
