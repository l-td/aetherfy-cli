package cmd

import (
	"strconv"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show this billing period's usage against your plan",
	Long: `Show this billing period's usage: the period, your plan, how much of the plan
has been used so far, what is owed beyond it (if anything), and each agent's
awake time against its budget. These are the figures of the dashboard's Usage
page.

--json prints the control plane's answer unchanged.`,
	Example: `  afy usage
  afy usage --json`,
	Args: cobra.NoArgs,
	RunE: runUsage,
}

func runUsage(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	sp := output.NewSpinner("Fetching usage...")
	sp.Start()
	u, err := api.NewClient().GetUsage()
	sp.Stop()
	if err != nil {
		return printRefusal("Failed to read usage", err)
	}

	// The server's document, unchanged.
	if output.IsJSON() {
		return output.JSON(u.Raw)
	}

	if !u.HasActivePeriod {
		printNoUsage(u)
		return nil
	}

	output.Header("Usage")
	output.Println("")
	output.KeyValue("Period", usagePeriod(u))
	if u.Tier != "" {
		output.KeyValue("Plan", u.Tier)
	}
	output.KeyValue("Used so far", usageGauge(u))
	if u.InOverage && u.OverageLabel != nil {
		output.KeyValue("Beyond plan", *u.OverageLabel)
	}
	if u.TotalCompositionLabel != nil {
		output.KeyValue("Total", strings.TrimPrefix(*u.TotalCompositionLabel, "This period's total: "))
	}
	if u.Today.Label != nil {
		output.KeyValue("Today", *u.Today.Label)
	}
	if u.Forecast.Message != nil {
		output.KeyValue("Forecast", *u.Forecast.Message)
	}

	if len(u.Agents) == 0 {
		output.Println("")
		output.PrintInfo("No agents to show.")
		return nil
	}
	output.Println("")
	output.Bold.Println("Agents")
	for _, a := range u.Agents {
		output.Printf("  %s: %s\n", usageAgentName(a), a.Display)
		for _, r := range a.AwakeTime {
			output.Dim.Printf("    %s\n", r.Label)
		}
	}
	if u.AwakeBudgetNote != "" {
		output.Println("")
		output.Dim.Println(u.AwakeBudgetNote)
	}
	return nil
}

// printNoUsage says plainly why there is nothing to show: the server's reason,
// in words.
func printNoUsage(u *api.Usage) {
	reason := strings.ReplaceAll(u.Reason, "_", " ")
	if reason == "" {
		reason = "no active period"
	}
	if u.Tier != "" {
		output.PrintInfo("No usage to show: %s (plan: %s).", reason, u.Tier)
		return
	}
	output.PrintInfo("No usage to show: %s.", reason)
}

// usagePeriod is "2026-11-01 to 2026-12-01".
func usagePeriod(u *api.Usage) string {
	return usageDay(u.PeriodStart) + " to " + usageDay(u.PeriodEnd)
}

func usageDay(iso string) string {
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return iso
}

// usageGauge is the plan gauge as the dashboard words it ("42% of plan"), with
// the bare percent when the server sent no label. Past the plan the dashboard's
// own label is the dollars, so this line is the percent of the plan used and the
// overage dollars have a line of their own.
func usageGauge(u *api.Usage) string {
	switch {
	case u.InOverage && u.GaugePercent != nil:
		return trimPercent(*u.GaugePercent) + " of plan"
	case u.GaugeLabel != "":
		return u.GaugeLabel
	case u.GaugePercent != nil:
		return trimPercent(*u.GaugePercent)
	}
	return "-"
}

func trimPercent(p float64) string {
	return strconv.FormatFloat(p, 'f', -1, 64) + "%"
}

func usageAgentName(a api.UsageAgent) string {
	name := a.AgentName
	if a.State != nil && *a.State != "" {
		name += " (" + *a.State + ")"
	}
	if a.UsagePaused {
		name += " (paused)"
	}
	return name
}
