package cmd

// afy collections backups / restore --day: restoring a LIVE collection to one of
// the last days from the daily backup (docs: vectordb
// backend/docs/COLLECTION_BACKUP_RESTORE.md).
//
// The daily backup holds every collection, every day, for 7 days. `backups`
// lists the days of one collection with when its copy was taken, its points and
// size; `restore <collection> --day <date> --as <name>` makes a NEW collection
// from one. A restore never goes over the live collection, so --as is required
// and must be a name the workspace does not use. The restore runs in the
// background and is followed like a deleted collection's (`--wait`: exit 1 if it
// fails, 4 if still running after 30 minutes); it counts against the same
// monthly restores and volume.
//
// A copy is taken shard by shard, so it is as of a RANGE of times, not one
// moment: `backups` shows the range. Only a person's key may do this (an agent's
// key is refused 403); Free does not have it (403 PLAN_LIMIT_EXCEEDED, limit
// collection_backup_restore_days).

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

// backupDayShape is a backup day: YYYY-MM-DD (UTC).
var backupDayShape = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// sameInstant: shards taken within this of each other are shown as one time.
const sameInstant = time.Minute

// --- BACKUPS ---

var collectionsBackupsCmd = &cobra.Command{
	Use:   "backups <collection>",
	Short: "List the days a collection can be restored to, from its daily backup",
	Long: `List the days a collection can be restored to, from the daily backup.

Each day shows when its copy was taken, its points and its size. A collection's
shards are copied one after another, so a day is as of a RANGE of times, not one
moment; the range is shown (a single time where the shards were taken within a
minute of each other). Only days that have a backup are listed, within your plan's
days. The last line says how far back your plan goes and what is left of your
plan's restores this month.

Restore a day with 'afy collections restore <collection> --day <date> --as <name>'.
Free does not include this (403 PLAN_LIMIT_EXCEEDED).`,
	Example: `  # What can I get back of articles?
  afy collections backups articles --workspace research

  # The same, for a script
  afy collections backups articles --workspace research --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return collectionsBackups(r, args[0]) })
	},
}

type collectionBackupJSON struct {
	Day       string `json:"day"`
	TakenFrom string `json:"taken_from"`
	TakenTo   string `json:"taken_to"`
	Points    int64  `json:"points"`
	Bytes     int64  `json:"bytes"`
}

func collectionsBackups(r *vecRun, collection string) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	list, err := client.ListCollectionBackups(collection)
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		out := struct {
			vecEnvelope
			Collection string                    `json:"collection"`
			DaysBack   int                       `json:"days_back"`
			Backups    []collectionBackupJSON    `json:"backups"`
			Allowance  vectors.RecoveryAllowance `json:"allowance"`
		}{vecEnvelope: r.envelope(), Collection: collection, DaysBack: list.DaysBack, Backups: []collectionBackupJSON{}, Allowance: list.Allowance}
		for _, b := range list.Backups {
			out.Backups = append(out.Backups, collectionBackupJSON(b))
		}
		return r.printJSON(out)
	}
	if len(list.Backups) == 0 {
		fmt.Fprintf(r.stdout, "No backups of '%s' in %s in the last %d days yet. The daily backup runs once a day; a collection created today appears after its first run.\n",
			collection, workspaceLabel(client.Workspace()), list.DaysBack)
	} else {
		table := make([][]string, 0, len(list.Backups))
		for _, b := range list.Backups {
			table = append(table, []string{b.Day, takenRange(b.TakenFrom, b.TakenTo), fmt.Sprintf("%d", b.Points), humanBytes(b.Bytes)})
		}
		output.RenderTable(r.stdout, []string{"Day", "Taken (UTC)", "Points", "Size"}, table)
		fmt.Fprintf(r.stdout, "\n%d backup(s) of '%s' in %s\n", len(list.Backups), collection, workspaceLabel(client.Workspace()))
	}
	fmt.Fprintln(r.stdout, backupAllowanceLine(list.DaysBack, list.Allowance))
	return 0
}

// takenRange says when a day's copy was taken: one time when its shards were taken within a minute of
// each other, otherwise first to last.
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

// backupAllowanceLine is how far back the plan goes and what is left this month; a part the plan does
// not limit is left out.
func backupAllowanceLine(daysBack int, a vectors.RecoveryAllowance) string {
	parts := []string{fmt.Sprintf("Restore to any of the last %d days", daysBack)}
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

// --- RESTORE --day ---

// collectionsRestoreDay restores one day of a live collection into a new collection called as. Input is
// checked before anything is sent: a day that is not a date, a missing name.
func collectionsRestoreDay(r *vecRun, collection, day, as string, wait bool) int {
	if !backupDayShape.MatchString(day) {
		return r.fail(refuse("--day must be a backup day, YYYY-MM-DD, not %q. 'afy collections backups %s' lists the days", day, collection))
	}
	if strings.TrimSpace(as) == "" {
		return r.fail(refuse("--as is required with --day: a restore never goes over the live collection, so name the new one. Nothing was sent"))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	started, err := client.RestoreCollectionBackup(collection, day, strings.TrimSpace(as))
	if err != nil {
		return r.fail(err)
	}
	return reportRestore(r, client, started, wait)
}

func init() {
	collectionsCmd.AddCommand(collectionsBackupsCmd)
}
