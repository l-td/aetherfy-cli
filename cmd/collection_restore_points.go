package cmd

// afy collections restore-points / restore: getting a collection back from a
// RESTORE POINT, whether it still exists or was deleted (docs: vectordb
// backend/docs/COLLECTION_BACKUP_RESTORE.md).
//
// A collection's restore points are the copy taken when it was deleted (every
// plan has it, kept for the plan's window) and each of its daily copies within
// the plan's days (a plan with none, Free, lists only a deletion copy). A
// restore is of a POINT, by the id `restore-points` shows, into a NEW collection:
// the collection it is a copy of is never changed, so a live collection's day
// comes back as <name>-<date> unless --as names it. The restore runs in the
// background, region by region; --wait follows it as `afy collections regions
// --wait` follows a region change (exit 1 if it fails, 4 if still running after
// 30 minutes). It counts against ONE allowance of restores and volume per month,
// whichever kind of point it is.
//
// `restore-points` lists three ways, and exactly one is asked for: a live
// collection by its name (its daily copies), the deleted collections of the
// workspace (no argument: each as the copy taken at its deletion, with the
// collection's id), or one collection by its id (--collection-id: for a deleted
// collection, its deletion copy AND the daily copies from before it was damaged
// and deleted). An id is never guessed from the argument's shape.
//
// A daily copy is taken shard by shard, so it is as of a RANGE of times, not one
// moment: the list shows the range. Only a person's key may do this (an agent's
// key is refused 403).

import (
	"fmt"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

// sameInstant: shards taken within this of each other are shown as one time.
const sameInstant = time.Minute

// --- RESTORE-POINTS ---

var collectionsRestorePointsCmd = &cobra.Command{
	Use:   "restore-points [collection]",
	Short: "List the copies a collection can be restored from, or the deleted collections",
	Long: `List restore points: the copies a collection can be restored from.

  restore-points <collection>        a live collection's daily copies
  restore-points                     the workspace's deleted collections, each as the copy taken when it was deleted
  restore-points --collection-id ID  one collection by its id, live or deleted: for a deleted one, its deletion
                                     copy and the daily copies from before it was deleted

Each row shows the id to restore it by ('afy collections restore <id>'), what
kind of copy it is (daily, or deletion), when it was taken, its points and size,
and its status: ready, backing_up (a deletion copy still being taken),
unavailable (it could not be completed; its data is kept and support can retry
it) or restoring. A daily copy is as of a RANGE of times, not one moment: a
collection's shards are copied one after another, so the range is shown (a single
time where the shards were taken within a minute of each other). The last line
says how far back each kind of copy reaches on your plan and what is left of its
restores this month.`,
	Example: `  # What can I get back of articles?
  afy collections restore-points articles --workspace research

  # What was deleted from the research workspace?
  afy collections restore-points --workspace research

  # A deleted collection's other copies, from before it was deleted
  afy collections restore-points --collection-id 3f2b0c1e-0c61-4c0e-9b53-2f9f6a7a4d10

  # The same, for a script
  afy collections restore-points articles --workspace research --json`,
	Args: refuseArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return collectionsRestorePoints(r, args, collectionRestorePointsOf) })
	},
}

var (
	collectionRestorePointsOf string
	collectionRestoreAs       string
	collectionRestoreWait     bool
)

type restorePointJSON struct {
	ID         string                   `json:"id"`
	Kind       string                   `json:"kind"`
	Collection vectors.PointCollection  `json:"collection"`
	TakenAt    string                   `json:"taken_at"`
	Day        string                   `json:"day,omitempty"`
	TakenFrom  string                   `json:"taken_from,omitempty"`
	TakenTo    string                   `json:"taken_to,omitempty"`
	Points     int64                    `json:"points"`
	Bytes      int64                    `json:"bytes"`
	Regions    []string                 `json:"regions"`
	DeletedBy  *vectors.DeletedBy       `json:"deleted_by,omitempty"`
	ExpiresAt  *string                  `json:"expires_at,omitempty"`
	Status     string                   `json:"status"`
	Restore    *vectors.RestoreProgress `json:"restore"`
}

// inWorkspace keeps the restore points of collections that were in the workspace.
func inWorkspace(all []vectors.RestorePoint, workspace string) []vectors.RestorePoint {
	var out []vectors.RestorePoint
	for _, p := range all {
		if p.Collection.Workspace != nil && *p.Collection.Workspace == workspace {
			out = append(out, p)
		}
	}
	return out
}

func collectionsRestorePoints(r *vecRun, args []string, collectionID string) int {
	if len(args) == 1 && collectionID != "" {
		return r.fail(refuse("name a collection or give --collection-id, not both. Nothing was sent"))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	switch {
	case len(args) == 1:
		list, err := client.ListCollectionRestorePoints(args[0])
		if err != nil {
			return r.fail(err)
		}
		return printCollectionPoints(r, client, args[0], list)
	case collectionID != "":
		list, err := client.ListRestorePointsOf(collectionID)
		if err != nil {
			return r.fail(err)
		}
		return printCollectionPoints(r, client, collectionID, list)
	}
	list, err := client.ListDeletedRestorePoints()
	if err != nil {
		return r.fail(err)
	}
	return printDeletedPoints(r, client, inWorkspace(list.RestorePoints, client.Workspace()), list.Allowance)
}

func pointsJSON(points []vectors.RestorePoint) []restorePointJSON {
	out := []restorePointJSON{}
	for _, p := range points {
		out = append(out, restorePointJSON(p))
	}
	return out
}

// printCollectionPoints prints the restore points of ONE collection; asked is how the person named it.
func printCollectionPoints(r *vecRun, client *vectors.Client, asked string, list *vectors.RestorePoints) int {
	name := asked
	deleted := false
	if list.Collection != nil {
		name, deleted = list.Collection.Name, list.Collection.Deleted
	}
	if r.json {
		out := struct {
			vecEnvelope
			Collection    *vectors.PointCollection `json:"collection"`
			RestorePoints []restorePointJSON       `json:"restore_points"`
			Allowance     vectors.RestoreAllowance `json:"allowance"`
		}{vecEnvelope: r.envelope(), Collection: list.Collection, RestorePoints: pointsJSON(list.RestorePoints), Allowance: list.Allowance}
		return r.printJSON(out)
	}
	where := workspaceLabel(client.Workspace())
	if list.Collection != nil && list.Collection.Workspace != nil {
		where = workspaceLabel(*list.Collection.Workspace)
	}
	if len(list.RestorePoints) == 0 {
		if list.Allowance.DailyDays > 0 {
			fmt.Fprintf(r.stdout, "No restore points of '%s' in %s in the last %d days yet. The daily backup runs once a day; a collection created today appears after its first run.\n",
				name, where, list.Allowance.DailyDays)
		} else {
			fmt.Fprintf(r.stdout, "No restore points of '%s' in %s.\n", name, where)
		}
	} else {
		table := make([][]string, 0, len(list.RestorePoints))
		for _, p := range list.RestorePoints {
			table = append(table, []string{
				p.ID, p.Kind, pointTaken(p), pointUntil(p), fmt.Sprintf("%d", p.Points), humanBytes(p.Bytes), pointStatus(p),
			})
		}
		output.RenderTable(r.stdout, []string{"Restore point", "Kind", "Taken (UTC)", "Restorable until", "Points", "Size", "Status"}, table)
		fmt.Fprintf(r.stdout, "\n%d restore point(s) of '%s' in %s\n", len(list.RestorePoints), name, where)
	}
	fmt.Fprintln(r.stdout, allowanceLine(list.Allowance, true, deleted))
	return 0
}

// printDeletedPoints prints the workspace's deleted collections, each as its deletion copy.
func printDeletedPoints(r *vecRun, client *vectors.Client, rows []vectors.RestorePoint, allowance vectors.RestoreAllowance) int {
	if r.json {
		out := struct {
			vecEnvelope
			RestorePoints []restorePointJSON       `json:"restore_points"`
			Allowance     vectors.RestoreAllowance `json:"allowance"`
		}{vecEnvelope: r.envelope(), RestorePoints: pointsJSON(rows), Allowance: allowance}
		return r.printJSON(out)
	}
	if len(rows) == 0 {
		fmt.Fprintf(r.stdout, "No deleted collections to restore in %s.\n", workspaceLabel(client.Workspace()))
	} else {
		table := make([][]string, 0, len(rows))
		for _, p := range rows {
			table = append(table, []string{
				p.Collection.Name, p.ID, p.Collection.ID, shortDate(p.TakenAt), deletedBy(p.DeletedBy), restorableUntil(p.ExpiresAt),
				humanBytes(p.Bytes), pointStatus(p),
			})
		}
		output.RenderTable(r.stdout, []string{"Name", "Restore point", "Collection ID", "Deleted", "Deleted by", "Restorable until", "Size", "Status"}, table)
		fmt.Fprintf(r.stdout, "\n%d deleted collection(s) in %s\n", len(rows), workspaceLabel(client.Workspace()))
	}
	fmt.Fprintln(r.stdout, allowanceLine(allowance, false, true))
	if len(rows) > 0 && allowance.DailyDays > 0 {
		fmt.Fprintln(r.stdout, "Daily copies of a deleted collection, from before it was deleted: afy collections restore-points --collection-id <Collection ID>")
	}
	return 0
}

// pointTaken says when a copy was taken: a daily copy as its range, a deletion copy as the moment it was deleted.
func pointTaken(p vectors.RestorePoint) string {
	if p.Kind == "daily" {
		return takenRange(p.TakenFrom, p.TakenTo)
	}
	return takenRange(p.TakenAt, p.TakenAt) + " (deleted)"
}

// pointUntil is a deletion copy's last day; a daily copy has none of its own (the plan's days decide).
func pointUntil(p vectors.RestorePoint) string {
	if p.Kind == "daily" {
		return ""
	}
	return restorableUntil(p.ExpiresAt)
}

func pointStatus(p vectors.RestorePoint) string {
	if p.Restore != nil {
		return fmt.Sprintf("restoring %d/%d", p.Restore.RegionsReady, p.Restore.RegionsTotal)
	}
	return p.Status
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
func deletedBy(by *vectors.DeletedBy) string {
	if by != nil && by.Kind == "agent" {
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

// takenRange says when a copy was taken: one time when its shards were taken within a minute of each
// other, otherwise first to last.
func takenRange(from, to string) string {
	f, ferr := time.Parse(time.RFC3339, from)
	t, terr := time.Parse(time.RFC3339, to)
	if ferr != nil || terr != nil {
		return from
	}
	const layout = "2006-01-02 15:04"
	f, t = f.UTC(), t.UTC()
	if t.Sub(f) < sameInstant {
		return f.Format(layout)
	}
	// Within one UTC date the date is said once: the table stays one line.
	if f.Format("2006-01-02") == t.Format("2006-01-02") {
		return f.Format(layout) + " to " + t.Format("15:04")
	}
	return f.Format(layout) + " to " + t.Format(layout)
}

// allowanceLine is what the plan allows, in one sentence: how far back each kind of copy the list can hold
// reaches, and what is left of the month; a part the plan does not limit is left out.
func allowanceLine(a vectors.RestoreAllowance, daily, deleted bool) string {
	var parts []string
	if daily && a.DailyDays > 0 {
		parts = append(parts, fmt.Sprintf("Restore to any of the last %d days", a.DailyDays))
	}
	if deleted {
		if a.RecoveryDays == nil {
			parts = append(parts, "Deleted collections are kept without a time limit")
		} else {
			parts = append(parts, fmt.Sprintf("Deleted collections are kept %d days", *a.RecoveryDays))
		}
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
	Use:   "restore <restore-point>",
	Short: "Restore a restore point as a new collection",
	Long: `Restore a restore point, as a new collection.

Name the point by the id 'afy collections restore-points' shows. It can be the copy
taken when a collection was deleted, or one of its daily copies. The restored
collection is NEW (a new id): the collection the point is of is never changed. It
has the points, vectors, payloads, indexes and payload schema the point holds, in
the same regions. A copy taken at a deletion also brings its aliases back; a daily
copy brings the payload schema it had that day and no aliases (a live collection
keeps its own, and an alias names exactly one collection).

Without --as the name is the first free of the collection's old name and the old
name with the point's date (<name>-<date>): a deleted collection normally comes
back under its old name, a live collection's day as <name>-<date>. With --as it is
that name, and a name a collection or an alias already has in the workspace is
refused (409 COLLECTION_NAME_TAKEN_FOR_RESTORE) with nothing created. A daily copy
is as of a range of times, not one moment.

A restore counts against your plan's restores per month and the volume restored
per month, one count for both kinds of point; a refusal (403 PLAN_LIMIT_EXCEEDED)
names which, the plan's number and when it resets. A daily copy needs a plan that
has daily copies (403 PLAN_LIMIT_EXCEEDED otherwise). It runs in the background:
--wait follows it to the end (exit 1 if it fails, 4 if it is still running after
30 minutes).`,
	Example: `  # Restore a point by its id, and wait for it
  afy collections restore day_3f2b0c1e-0c61-4c0e-9b53-2f9f6a7a4d10_2026-10-27 --wait

  # Under another name
  afy collections restore 3f2b0c1e-0c61-4c0e-9b53-2f9f6a7a4d10 --as articles-v2 --wait --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int {
			return collectionsRestore(r, args[0], collectionRestoreAs, collectionRestoreWait)
		})
	},
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

// collectionsRestore restores a restore point into a new collection. The point's id goes to the API as
// given: it is opaque here, and the API answers a 404 for one that names nothing of the account's.
func collectionsRestore(r *vecRun, point, as string, wait bool) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	started, err := client.RestoreRestorePoint(point, strings.TrimSpace(as))
	if err != nil {
		return r.fail(err)
	}
	return reportRestore(r, client, started, wait)
}

// reportRestore follows a started restore when asked and says how it stands.
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
	collectionsRestorePointsCmd.Flags().StringVar(&collectionRestorePointsOf, "collection-id", "", "List the restore points of the collection with this id, live or deleted")
	collectionsRestoreCmd.Flags().StringVar(&collectionRestoreAs, "as", "", "Name for the restored collection (default: its old name, or <name>-<date> if that is taken)")
	collectionsRestoreCmd.Flags().BoolVar(&collectionRestoreWait, "wait", false, "Follow the restore to the end (exit 1 if it fails, 4 if still running after 30 minutes)")

	collectionsCmd.AddCommand(collectionsRestorePointsCmd)
	collectionsCmd.AddCommand(collectionsRestoreCmd)
}
