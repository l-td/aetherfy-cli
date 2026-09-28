package cmd

// `afy gateway` -- the AI Gateway, a new kind of object, so a new noun group
// beside secrets/workspaces/github (see the rule in cmd/root.go).
//
// The gateway (gateway.aetherfy.com) is where an agent's own OpenAI or
// Anthropic SDK calls go; these commands manage what it enforces and read what
// it recorded. status, set and usage go through the control plane's
// /api/v1/gateway/* routes with the stored key; models reads the gateway's
// public catalogue directly (internal/gateway) and needs no login.
//
// Exit codes, as the vector commands: 0 success, 1 a request failed, 2 the
// input was refused before any request, 3 not logged in.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/gateway"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Manage the AI Gateway: budgets, allowed models and usage",
	Long: `Manage the AI Gateway: one endpoint (https://gateway.aetherfy.com) through
which your agents' own OpenAI and Anthropic SDK calls reach OpenAI, Anthropic,
Google and OpenRouter, recorded per agent and capped by monthly budgets.

Subcommands:
  status [agent]   Budgets, allowed models and spend this month
  set [agent]      Set a monthly budget (--budget) or allowed models (--models)
  usage            Calls, tokens and list-price cost, grouped
  models           The models the gateway prices, from its public catalogue

Spend is a list-price estimate, per calendar month in UTC.`,
	Example: `  afy gateway status
  afy gateway set --budget 50
  afy gateway set my-bot --budget 10 --models gpt-4o-mini,claude-opus-5-5
  afy gateway set my-bot --models all
  afy gateway usage --by agent --since 7d
  afy gateway models`,
}

// --- status -------------------------------------------------------------------

var gatewayStatusCmd = &cobra.Command{
	Use:   "status [agent]",
	Short: "Show AI Gateway budgets, allowed models and spend this month",
	Long: `Show the account's AI Gateway budget and spend this month, and every
agent's budget, allowed models and spend. With an agent, show only that agent.`,
	Example: `  afy gateway status
  afy gateway status my-bot -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return runGatewayStatus(api.NewClient(), args)
	},
}

func runGatewayStatus(client *api.Client, args []string) error {
	account, err := client.GatewaySettings()
	if err != nil {
		output.PrintError("Failed to read the AI Gateway settings: %v", err)
		return err
	}
	list, err := client.GatewayAgents()
	if err != nil {
		output.PrintError("Failed to read the agents' AI Gateway settings: %v", err)
		return err
	}
	if len(args) == 1 {
		for _, a := range list.Agents {
			if a.Name == args[0] || a.AgentID == args[0] {
				if config.Get().OutputFormat == "json" {
					return output.JSON(a)
				}
				printGatewayAgent(a)
				return nil
			}
		}
		err := fmt.Errorf("agent '%s' not found", args[0])
		output.PrintError("%v", err)
		return err
	}
	if config.Get().OutputFormat == "json" {
		return output.JSON(map[string]interface{}{"account": account, "agents": list.Agents})
	}
	output.Header("AI Gateway")
	output.KeyValue("Account budget", budgetText(account.MonthlyBudgetUSD))
	output.KeyValue("Spent this month", usd(account.MonthToDateUSD)+" (list-price estimate, since "+account.MonthStart.UTC().Format("2006-01-02")+" UTC)")
	output.Println("")
	if len(list.Agents) == 0 {
		output.PrintInfo("No agents yet.")
		return nil
	}
	table := output.Table([]string{"Agent", "Budget", "Spent this month", "Allowed models"})
	for _, a := range list.Agents {
		table.Append([]string{a.Name, budgetText(a.MonthlyBudgetUSD), usd(a.MonthToDateUSD), modelsText(a.AllowedModels)})
	}
	table.Render()
	return nil
}

func printGatewayAgent(a api.GatewayAgentSettings) {
	output.Header(a.Name)
	output.KeyValue("Budget", budgetText(a.MonthlyBudgetUSD))
	output.KeyValue("Spent this month", usd(a.MonthToDateUSD)+" (list-price estimate)")
	output.KeyValue("Allowed models", modelsText(a.AllowedModels))
}

// --- set ----------------------------------------------------------------------

var (
	gatewayBudget string
	gatewayModels string
)

var gatewaySetCmd = &cobra.Command{
	Use:   "set [agent]",
	Short: "Set an AI Gateway budget or allowed models",
	Long: `Set a monthly AI Gateway budget, in US dollars, or the models an agent may call.

Without an agent, --budget sets the ACCOUNT budget, which caps all agents
together. With an agent, --budget and --models set that agent's.

  --budget 25      a monthly budget of $25.00 (whole cents at most)
  --budget 0       blocks every call
  --budget none    removes the budget
  --models a,b     allows only these model ids (as the agent's code names them)
  --models all     allows every model

A budget is a soft cap: calls already running when it is reached still finish.`,
	Example: `  afy gateway set --budget 50
  afy gateway set my-bot --budget 10
  afy gateway set my-bot --models gpt-4o-mini,openrouter/anthropic/claude-3-haiku
  afy gateway set my-bot --budget none --models all`,
	Args: refuseArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return runGatewaySet(api.NewClient(), args, cmd.Flags().Changed("budget"), cmd.Flags().Changed("models"))
	},
}

var centsPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,2})?$`)

// maxBudgetUSD is the column's bound: numeric(12,2).
const maxBudgetUSD = 9999999999.99

// parseBudget reads --budget: "none" is nil (no budget), anything else a
// non-negative amount of dollars with at most two decimals.
func parseBudget(s string) (*float64, error) {
	if strings.EqualFold(s, "none") {
		return nil, nil
	}
	if !centsPattern.MatchString(s) {
		return nil, refuse("--budget must be an amount of US dollars with at most two decimals (e.g. 25 or 12.50), or none")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v > maxBudgetUSD {
		return nil, refuse("--budget must be at most %.2f", maxBudgetUSD)
	}
	return &v, nil
}

// parseModels reads --models: "all" is nil (every model), else a comma list.
func parseModels(s string) ([]string, error) {
	if strings.EqualFold(s, "all") {
		return nil, nil
	}
	var out []string
	for _, m := range strings.Split(s, ",") {
		m = strings.TrimSpace(m)
		if m == "" {
			return nil, refuse("--models is a comma-separated list of model ids, or all")
		}
		out = append(out, m)
	}
	return out, nil
}

func runGatewaySet(client *api.Client, args []string, budgetSet, modelsSet bool) error {
	if !budgetSet && !modelsSet {
		return refuse("nothing to set: pass --budget, --models, or both")
	}
	var budget *float64
	var models []string
	var err error
	if budgetSet {
		if budget, err = parseBudget(gatewayBudget); err != nil {
			return err
		}
	}
	if modelsSet {
		if models, err = parseModels(gatewayModels); err != nil {
			return err
		}
	}

	if len(args) == 0 {
		if modelsSet {
			return refuse("--models applies to one agent: afy gateway set <agent> --models ...")
		}
		account, err := client.PutGatewaySettings(budget)
		if err != nil {
			output.PrintError("Failed to set the account's AI Gateway budget: %v", err)
			return err
		}
		if config.Get().OutputFormat == "json" {
			return output.JSON(account)
		}
		output.PrintSuccess("Account AI Gateway budget: %s", budgetText(account.MonthlyBudgetUSD))
		output.KeyValue("Spent this month", usd(account.MonthToDateUSD)+" (list-price estimate)")
		return nil
	}

	agent, err := client.PutGatewayAgent(args[0], api.GatewayAgentUpdate{
		Budget: budget, BudgetSet: budgetSet, AllowedModels: models, ModelsSet: modelsSet,
	})
	if err != nil {
		output.PrintError("Failed to set the AI Gateway settings of '%s': %v", args[0], err)
		return err
	}
	if config.Get().OutputFormat == "json" {
		return output.JSON(agent)
	}
	output.PrintSuccess("AI Gateway settings of '%s' updated.", agent.Name)
	printGatewayAgent(*agent)
	return nil
}

// --- usage --------------------------------------------------------------------

var (
	gatewayUsageBy    string
	gatewayUsageSince string
	gatewayUsageAgent string
)

var gatewayUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show AI Gateway calls, tokens and cost, grouped",
	Long: `Show AI Gateway calls, tokens and list-price cost since a point in time,
grouped by model, agent or UTC day, with totals.

--since takes a duration back from now (30d, 12h, 90m) or a date (2026-09-01)
or an RFC 3339 time. The window is at most 366 days.`,
	Example: `  afy gateway usage
  afy gateway usage --by agent --since 7d
  afy gateway usage --by day --agent my-bot -o json`,
	Args: refuseArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return runGatewayUsage(api.NewClient(), time.Now())
	},
}

var sincePattern = regexp.MustCompile(`^([0-9]+)([dhm])$`)

// parseSince turns --since into an RFC 3339 instant, relative to now.
func parseSince(s string, now time.Time) (string, error) {
	if m := sincePattern.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"d": 24 * time.Hour, "h": time.Hour, "m": time.Minute}[m[2]]
		return now.Add(-time.Duration(n) * unit).UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", refuse("--since must be a duration (30d, 12h, 90m), a date (2026-09-01) or an RFC 3339 time")
}

func runGatewayUsage(client *api.Client, now time.Time) error {
	switch gatewayUsageBy {
	case "model", "agent", "day":
	default:
		return refuse("--by must be model, agent or day")
	}
	since, err := parseSince(gatewayUsageSince, now)
	if err != nil {
		return err
	}
	report, err := client.GatewayUsage(api.GatewayUsageQuery{Since: since, GroupBy: gatewayUsageBy, Agent: gatewayUsageAgent})
	if err != nil {
		output.PrintError("Failed to read AI Gateway usage: %v", err)
		return err
	}
	if config.Get().OutputFormat == "json" {
		return output.JSON(report)
	}
	if report.Totals.Requests == 0 {
		output.PrintInfo("No AI Gateway calls since %s.", report.Since.UTC().Format("2006-01-02 15:04 UTC"))
		return nil
	}
	label := map[string]string{"model": "Model", "agent": "Agent", "day": "Day (UTC)"}[report.GroupBy]
	table := output.Table([]string{label, "Calls", "Input tokens", "Output tokens", "Cached input", "Cost (USD)"})
	for _, r := range report.Rows {
		table.Append(usageCells(usageKey(r, report.GroupBy), r))
	}
	table.Append(usageCells("Total", report.Totals))
	table.Render()
	output.Println("")
	output.Dim.Printf("%s to %s. Cost is a list-price estimate; %d call(s) unpriced, %d aborted, %d refused or failed.\n",
		report.Since.UTC().Format("2006-01-02 15:04"), report.Until.UTC().Format("2006-01-02 15:04 UTC"),
		report.Totals.UnpricedRequests, report.Totals.AbortedRequests, report.Totals.ErrorRequests)
	return nil
}

func usageKey(r api.GatewayUsageRow, groupBy string) string {
	if groupBy == "agent" {
		switch {
		case r.AgentName != nil:
			return *r.AgentName
		case r.Key == nil:
			return "(account key)"
		}
	}
	if r.Key == nil {
		return "-"
	}
	return *r.Key
}

func usageCells(key string, r api.GatewayUsageRow) []string {
	return []string{key, fmt.Sprint(r.Requests), fmt.Sprint(r.InputTokens), fmt.Sprint(r.OutputTokens),
		fmt.Sprint(r.CacheReadInputTokens), usd(r.CostUSD)}
}

// --- models -------------------------------------------------------------------

var gatewayURL string

var gatewayModelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List the models the AI Gateway prices, with list prices",
	Long: `List the models in the AI Gateway's public price catalogue, with their list
prices in US dollars per million tokens.

The catalogue is read from the gateway itself: --gateway-url, else
` + gateway.EnvGatewayURL + `, else ` + gateway.DefaultEndpoint + `. No login needed.

A model not listed here still works through the gateway; its calls are
recorded, unpriced.`,
	Example: `  afy gateway models
  afy gateway models -o json`,
	Args: refuseArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGatewayModels(&http.Client{}, os.Getenv)
	},
}

func runGatewayModels(client *http.Client, getenv func(string) string) error {
	endpoint, source := gateway.Resolve(gatewayURL, getenv)
	cat, err := gateway.Fetch(context.Background(), client, endpoint)
	if err != nil {
		output.PrintError("%v", err)
		return err
	}
	if config.Get().OutputFormat == "json" {
		return output.JSON(map[string]interface{}{"endpoint": endpoint, "endpoint_source": source, "catalogue": cat})
	}
	table := output.Table([]string{"Model", "Provider", "Format", "Input $/1M", "Output $/1M", "Cached input $/1M", "Context"})
	for _, id := range cat.IDs() {
		m := cat.Models[id]
		ctx := "-"
		if m.ContextWindow != nil {
			ctx = fmt.Sprint(*m.ContextWindow)
		}
		table.Append([]string{id, m.Provider, strings.Join(m.Formats, ","),
			priceText(m.PricesPer1M.Input), priceText(m.PricesPer1M.Output), priceText(m.PricesPer1M.CacheRead), ctx})
	}
	table.Render()
	output.Println("")
	output.Dim.Printf("%d models, catalogue %s, from %s.\n", len(cat.Models), cat.PriceVersion, endpoint)
	return nil
}

// --- formatting -----------------------------------------------------------------

// usd renders a spend: at least cents, and every digit the ledger kept below
// them, so a tiny spend never reads as $0.00.
func usd(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if i := strings.IndexByte(s, '.'); i < 0 {
		s += ".00"
	} else if len(s)-i-1 < 2 {
		s += strings.Repeat("0", 2-(len(s)-i-1))
	}
	return "$" + s
}

func budgetText(b *float64) string {
	if b == nil {
		return "none"
	}
	return fmt.Sprintf("$%.2f", *b)
}

func modelsText(m []string) string {
	if m == nil {
		return "all"
	}
	return strings.Join(m, ", ")
}

func priceText(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}

func init() {
	gatewaySetCmd.Flags().StringVar(&gatewayBudget, "budget", "", "monthly budget in US dollars, or none")
	gatewaySetCmd.Flags().StringVar(&gatewayModels, "models", "", "comma-separated allowed model ids, or all")
	gatewaySetCmd.SetFlagErrorFunc(refuseFlag)
	gatewayUsageCmd.Flags().StringVar(&gatewayUsageBy, "by", "model", "group by model, agent or day")
	gatewayUsageCmd.Flags().StringVar(&gatewayUsageSince, "since", "30d", "start: a duration back (30d, 12h, 90m), a date, or an RFC 3339 time")
	gatewayUsageCmd.Flags().StringVar(&gatewayUsageAgent, "agent", "", "only this agent (name or id)")
	gatewayUsageCmd.SetFlagErrorFunc(refuseFlag)
	gatewayModelsCmd.Flags().StringVar(&gatewayURL, "gateway-url", "", "AI Gateway endpoint (overrides "+gateway.EnvGatewayURL+")")
	gatewayModelsCmd.SetFlagErrorFunc(refuseFlag)

	gatewayCmd.AddCommand(gatewayStatusCmd)
	gatewayCmd.AddCommand(gatewaySetCmd)
	gatewayCmd.AddCommand(gatewayUsageCmd)
	gatewayCmd.AddCommand(gatewayModelsCmd)
}
