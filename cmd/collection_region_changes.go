package cmd

// afy collections region-changes: the account's region changes that copy
// data, against the plan's limits over its rolling window -- the figures the
// dashboard's Usage page shows (REGION CHANGES, DATA COPIED), read from the
// same control-plane route (GET /users/me/region-changes). It is the window
// `afy collections regions` checks a change against, read without proposing
// one. Only a change that adds a region to a collection counts: dropping a
// region and moving a collection never do.

import (
	"fmt"

	"github.com/spf13/cobra"
)

var collectionsRegionChangesCmd = &cobra.Command{
	Use:   "region-changes",
	Short: "Show your region changes that copy data, against your plan's limits",
	Long: `Show the region changes that copy data made in your account's rolling window,
and the data they copied, against your plan's limits -- the window 'afy
collections regions' checks a change against.

Only a change that adds a region to a collection counts. Dropping a region,
or moving a collection to another workspace, does not.`,
	Example: `  # How many region changes are left this window
  afy collections region-changes

  # The same, as JSON
  afy collections region-changes --json`,
	Args: refuseArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, collectionsRegionChanges)
	},
}

func collectionsRegionChanges(r *vecRun) int {
	usage, err := r.controlPlane().GetRegionChangeUsage()
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			RegionChanges interface{} `json:"region_changes"`
		}{usage})
	}
	count := fmt.Sprintf("%d, no limit", usage.CountUsed)
	if usage.CountLimit != nil {
		count = fmt.Sprintf("%d of %d", usage.CountUsed, *usage.CountLimit)
	}
	copied := humanBytes(usage.SizeUsedBytes) + ", no limit"
	if usage.SizeLimitBytes != nil {
		copied = humanBytes(usage.SizeUsedBytes) + " of " + humanBytes(*usage.SizeLimitBytes)
	}
	kv := func(k, v string) { fmt.Fprintf(r.stdout, "%-15s %s\n", k+":", v) }
	fmt.Fprintf(r.stdout, "Region changes that copy data, last %d days\n", usage.WindowDays)
	kv("Changes", count)
	kv("Data copied", copied)
	atLimit := (usage.CountLimit != nil && usage.CountUsed >= *usage.CountLimit) ||
		(usage.SizeLimitBytes != nil && usage.SizeUsedBytes >= *usage.SizeLimitBytes)
	if atLimit && usage.FreesAt != nil {
		kv("At the limit", "until "+*usage.FreesAt+", when the oldest change leaves the window")
	}
	return 0
}

func init() {
	collectionsCmd.AddCommand(collectionsRegionChangesCmd)
}
