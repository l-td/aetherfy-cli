package cmd

// afy collections deleted / restore: recovering a deleted collection from its
// backup (docs: vectordb backend/docs/COLLECTION_RECOVERY.md).
//
// A delete hides the collection at once and keeps a backup for the plan's
// window (7 days on Free, 30 on Starter, 90 on Performance, no limit on
// Enterprise). `deleted` lists what can still be restored in the workspace, with
// the window and what is left of the plan's restores this month; `restore`
// creates a new collection from one, under its old name or --as another. The
// restore runs in the background, region by region; --wait follows it as
// `afy collections regions --wait` follows a region change (exit 1 if it fails,
// 4 if still running after 30 minutes).
//
// A deleted collection is addressed by the id `deleted` shows. A name works as
// a shorthand when exactly one deleted collection in the workspace had it; two
// of the same name are told apart by id (this refuses and lists them), and a
// name nothing matches is refused too: both are input the command refused
// before sending anything (exit 2, as a usage error).
// Only a person's key may do this: an agent's key is refused 403.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

// --- DELETED ---

var collectionsDeletedCmd = &cobra.Command{
	Use:   "deleted",
	Short: "List the deleted collections that can still be restored",
	Long: `List the deleted collections of a workspace that can still be restored.

Each shows the id to restore it by, when it was deleted and until when it can be
restored (your plan's window, counted from when its backup was ready), its size,
who deleted it (a person, or an agent by its id), and its status: ready, backing_up
(its backup is still being taken), unavailable (it could not be completed; its data
is kept and support can retry it) or restoring. The last line says what is left of
your plan's restores this month.`,
	Example: `  # What can I get back from the research workspace?
  afy collections deleted --workspace research

  # The same, for a script
  afy collections deleted --workspace research --json`,
	Args: refuseArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return collectionsDeleted(r) })
	},
}

type deletedCollectionJSON struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	DeletedAt string                   `json:"deleted_at"`
	DeletedBy vectors.DeletedBy        `json:"deleted_by"`
	ExpiresAt *string                  `json:"expires_at"`
	Points    int64                    `json:"points"`
	Bytes     int64                    `json:"bytes"`
	Regions   []string                 `json:"regions"`
	Status    string                   `json:"status"`
	Restore   *vectors.RestoreProgress `json:"restore"`
}

// inWorkspace keeps the deleted collections that were in the workspace.
func inWorkspace(all []vectors.DeletedCollection, workspace string) []vectors.DeletedCollection {
	var out []vectors.DeletedCollection
	for _, d := range all {
		if d.Workspace != nil && *d.Workspace == workspace {
			out = append(out, d)
		}
	}
	return out
}

func collectionsDeleted(r *vecRun) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	list, err := client.ListDeletedCollections()
	if err != nil {
		return r.fail(err)
	}
	rows := inWorkspace(list.DeletedCollections, client.Workspace())
	if r.json {
		out := struct {
			vecEnvelope
			DeletedCollections []deletedCollectionJSON   `json:"deleted_collections"`
			Allowance          vectors.RecoveryAllowance `json:"allowance"`
		}{vecEnvelope: r.envelope(), DeletedCollections: []deletedCollectionJSON{}, Allowance: list.Allowance}
		for _, d := range rows {
			out.DeletedCollections = append(out.DeletedCollections, deletedCollectionJSON{
				ID: d.ID, Name: d.Name, DeletedAt: d.DeletedAt, DeletedBy: d.DeletedBy, ExpiresAt: d.ExpiresAt, Points: d.Points,
				Bytes: d.Bytes, Regions: d.Regions, Status: d.Status, Restore: d.Restore,
			})
		}
		return r.printJSON(out)
	}
	if len(rows) == 0 {
		fmt.Fprintf(r.stdout, "No deleted collections to restore in %s.\n", workspaceLabel(client.Workspace()))
	} else {
		table := make([][]string, 0, len(rows))
		for _, d := range rows {
			status := d.Status
			if d.Restore != nil {
				status = fmt.Sprintf("restoring %d/%d", d.Restore.RegionsReady, d.Restore.RegionsTotal)
			}
			table = append(table, []string{
				d.Name, d.ID, shortDate(d.DeletedAt), deletedBy(d.DeletedBy), restorableUntil(d.ExpiresAt), humanBytes(d.Bytes), status,
			})
		}
		output.RenderTable(r.stdout, []string{"Name", "ID", "Deleted", "Deleted by", "Restorable until", "Size", "Status"}, table)
		fmt.Fprintf(r.stdout, "\n%d deleted collection(s) in %s\n", len(rows), workspaceLabel(client.Workspace()))
	}
	if line := allowanceLine(list.Allowance); line != "" {
		fmt.Fprintln(r.stdout, line)
	}
	return 0
}

// shortDate is an ISO time as a date, in UTC ("2026-11-14").
func shortDate(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.UTC().Format("2006-01-02")
}

// deletedBy says who deleted the collection: a person, or an agent by its id.
func deletedBy(by vectors.DeletedBy) string {
	if by.Kind == "agent" {
		return "agent " + by.AgentID
	}
	return "person"
}

func restorableUntil(expires *string) string {
	if expires == nil {
		return "no time limit"
	}
	return shortDate(*expires)
}

// allowanceLine is what the plan allows, in one sentence; a part the plan does
// not limit is left out.
func allowanceLine(a vectors.RecoveryAllowance) string {
	var parts []string
	if a.RecoveryDays == nil {
		parts = append(parts, "Deleted collections are kept without a time limit")
	} else {
		parts = append(parts, fmt.Sprintf("Deleted collections are kept %d days", *a.RecoveryDays))
	}
	limited := false
	if a.RestoresPerMonth != nil && a.RestoresLeft != nil {
		parts = append(parts, fmt.Sprintf("%d of %d restores left this month", *a.RestoresLeft, *a.RestoresPerMonth))
		limited = true
	}
	if a.RestoreBytesPerMonth != nil && a.RestoreBytesLeft != nil {
		parts = append(parts, fmt.Sprintf("%s of %s left to restore", humanBytes(*a.RestoreBytesLeft), humanBytes(*a.RestoreBytesPerMonth)))
		limited = true
	}
	line := strings.Join(parts, " · ")
	if limited {
		line += fmt.Sprintf(" (resets %s)", shortDate(a.ResetsAt))
	}
	return line
}

// --- RESTORE ---

var collectionsRestoreCmd = &cobra.Command{
	Use:   "restore <id|name>",
	Short: "Restore a deleted collection, or a day of a live one, as a new collection",
	Long: `Restore a deleted collection from its backup, as a new collection.

With --day, restore a LIVE collection instead: the argument is its name, --day is
one of the days 'afy collections backups' lists, and --as names the new collection
(required: a restore never goes over the live collection, and the live collection
is not changed). The restore is of the daily backup, so it is as of a range of times,
not one moment. It needs a plan that includes it (403 PLAN_LIMIT_EXCEEDED
otherwise) and counts against the same restores and volume per month.

Name it by the id 'afy collections deleted' shows, or by its old name when
exactly one deleted collection in the workspace had it. The restored collection
has the same points, vectors, payloads, indexes, schema and aliases, in the same
regions, and a new id. It keeps its old name unless that is taken (409
COLLECTION_NAME_TAKEN_FOR_RESTORE); --as restores it under another.

A restore counts against your plan's restores per month and the volume restored
per month; a refusal (403 PLAN_LIMIT_EXCEEDED) names which, the plan's number and
when it resets. A name that matches no deleted collection, or more than one, is
refused before anything is sent (exit 2). It runs
in the background: --wait follows it to the end (exit 1 if it fails, 4 if it is
still running after 30 minutes).`,
	Example: `  # Restore by name
  afy collections restore articles --workspace research

  # Restore by id under another name, and wait for it
  afy collections restore 3f2b0c1e-0c61-4c0e-9b53-2f9f6a7a4d10 --as articles-v2 --wait --json

  # Restore a live collection as it was on a day, into a new collection
  afy collections restore articles --day 2026-10-30 --as articles-before --workspace research --wait`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int {
			if cmd.Flags().Changed("day") {
				return collectionsRestoreDay(r, args[0], collectionRestoreDay, collectionRestoreAs, collectionRestoreWait)
			}
			return collectionsRestore(r, args[0], collectionRestoreAs, collectionRestoreWait)
		})
	},
}

var (
	collectionRestoreAs   string
	collectionRestoreDay  string
	collectionRestoreWait bool
)

var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// pickDeleted turns the argument into one deleted collection's id: an id as
// given, or the one deleted collection of the workspace that had that name.
func pickDeleted(client *vectors.Client, arg string) (string, error) {
	if uuidShape.MatchString(arg) {
		return arg, nil
	}
	list, err := client.ListDeletedCollections()
	if err != nil {
		return "", err
	}
	var same []vectors.DeletedCollection
	for _, d := range inWorkspace(list.DeletedCollections, client.Workspace()) {
		if d.Name == arg {
			same = append(same, d)
		}
	}
	switch len(same) {
	case 0:
		return "", refuse("no deleted collection named '%s' in %s. 'afy collections deleted' lists what can be restored",
			arg, workspaceLabel(client.Workspace()))
	case 1:
		return same[0].ID, nil
	}
	ids := make([]string, 0, len(same))
	for _, d := range same {
		ids = append(ids, fmt.Sprintf("%s (deleted %s)", d.ID, shortDate(d.DeletedAt)))
	}
	return "", refuse("%d deleted collections in %s were named '%s'; restore one by its id: %s",
		len(same), workspaceLabel(client.Workspace()), arg, strings.Join(ids, ", "))
}

// followRestore polls a restore until it ends: nil when it is done, an error
// saying why when it failed, a *stillPendingError (exit 4) when the bound passes.
func followRestore(client *vectors.Client, id string) (*vectors.RestoreProgress, error) {
	deadline := operationNow().Add(operationWaitBound)
	for {
		p, err := client.GetRestore(id)
		if err != nil {
			return nil, err
		}
		switch p.State {
		case "done":
			return p, nil
		case "failed":
			msg := p.Error
			if msg == "" {
				msg = "the restore failed; nothing was changed"
			}
			return p, fmt.Errorf("restore %s failed: %s", id, msg)
		}
		if !operationNow().Before(deadline) {
			return p, &stillPendingError{msg: fmt.Sprintf(
				"restore %s is still running (%d of %d regions ready) after %s. It keeps running; "+
					"'afy collections get' shows the collection once it is ready.",
				id, p.RegionsReady, p.RegionsTotal, operationWaitBound)}
		}
		operationSleep(operationPollInterval)
	}
}

func collectionsRestore(r *vecRun, arg, as string, wait bool) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	id, err := pickDeleted(client, arg)
	if err != nil {
		return r.fail(err)
	}
	started, err := client.RestoreDeletedCollection(id, as)
	if err != nil {
		return r.fail(err)
	}
	return reportRestore(r, client, started, wait)
}

// reportRestore follows a started restore when asked and says how it stands: the same for a deleted
// collection's restore and for a day of a live collection's.
func reportRestore(r *vecRun, client *vectors.Client, started *vectors.RestoreStarted, wait bool) int {
	final := &vectors.RestoreProgress{
		ID: started.RestoreID, CollectionID: started.CollectionID, State: started.State,
		RegionsReady: started.RegionsReady, RegionsTotal: started.RegionsTotal,
	}
	exit := 0
	if wait {
		p, ferr := followRestore(client, started.RestoreID)
		if p != nil {
			final = p
		}
		if ferr != nil {
			exit = r.fail(ferr)
			if _, pending := ferr.(*stillPendingError); pending {
				exit = exitStillPending
			}
		}
	}
	if r.json {
		out := struct {
			vecEnvelope
			Restore struct {
				RestoreID    string   `json:"restore_id"`
				CollectionID string   `json:"collection_id"`
				Name         string   `json:"name"`
				Regions      []string `json:"regions"`
				State        string   `json:"state"`
				RegionsReady int      `json:"regions_ready"`
				RegionsTotal int      `json:"regions_total"`
			} `json:"restore"`
		}{vecEnvelope: r.envelope()}
		out.Restore.RestoreID, out.Restore.CollectionID, out.Restore.Name = started.RestoreID, started.CollectionID, started.Name
		out.Restore.Regions, out.Restore.State = started.Regions, final.State
		out.Restore.RegionsReady, out.Restore.RegionsTotal = final.RegionsReady, final.RegionsTotal
		if code := r.printJSON(out); code != 0 {
			return code
		}
		return exit
	}
	if exit != 0 {
		return exit
	}
	if final.State == "done" {
		fmt.Fprintf(r.stdout, "Collection '%s' restored in %s (%s).\n", started.Name, workspaceLabel(client.Workspace()), strings.Join(started.Regions, ", "))
		return 0
	}
	fmt.Fprintf(r.stdout, "Restoring '%s' in %s (restore %s, %d of %d regions ready). It runs in the background: "+
		"'afy collections get %s' shows it once it is ready, or pass --wait to follow it.\n",
		started.Name, workspaceLabel(client.Workspace()), started.RestoreID, final.RegionsReady, final.RegionsTotal, started.Name)
	return 0
}

func init() {
	collectionsRestoreCmd.Flags().StringVar(&collectionRestoreAs, "as", "", "Name for the restored collection (default: its old name; required with --day)")
	collectionsRestoreCmd.Flags().StringVar(&collectionRestoreDay, "day", "", "Restore this day (YYYY-MM-DD) of a live collection, from its daily backup, into a new collection")
	collectionsRestoreCmd.Flags().BoolVar(&collectionRestoreWait, "wait", false, "Follow the restore to the end (exit 1 if it fails, 4 if still running after 30 minutes)")

	collectionsCmd.AddCommand(collectionsDeletedCmd)
	collectionsCmd.AddCommand(collectionsRestoreCmd)
}
