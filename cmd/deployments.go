package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var deploymentsCmd = &cobra.Command{
	Use:   "deployments <agent>",
	Short: "List deployment history for an agent",
	Long: `List the deployments of an agent, ordered newest first.

Shows version, state, the release a run executed, creation time, and error
message for failed deployments. Shows 20 at a time; when older ones exist the
last line gives the --before to pass for the next page.
Use --json for machine-readable output.`,
	Example: `  # List deployments for an agent
  afy deployments my-agent

  # Only the failed ones from the last 7 days
  afy deployments my-agent --status failed --since 7d

  # The 50 newest, then the page after them
  afy deployments my-agent --limit 50
  afy deployments my-agent --limit 50 --before <id>

  # JSON output
  afy deployments my-agent --json`,
	Args: cobra.ExactArgs(1),
	RunE: runDeployments,
}

var (
	deploymentsSince  string
	deploymentsStatus string
	deploymentsLimit  int
	deploymentsBefore string
)

func init() {
	deploymentsCmd.Flags().StringVar(&deploymentsSince, "since", "", "Only deployments created within this long ago: a number followed by s, m, h or d (e.g., 30m, 24h, 7d)")
	deploymentsCmd.Flags().StringVar(&deploymentsStatus, "status", "", "Only deployments in this state (e.g., failed)")
	deploymentsCmd.Flags().IntVar(&deploymentsLimit, "limit", 20, "Maximum number of deployments to show (max 100)")
	deploymentsCmd.Flags().StringVar(&deploymentsBefore, "before", "", "The id to continue after: the previous page's next page id")
}

// deploymentsSinceForm is the shape of --since: a whole number and a unit, the
// form `afy logs --since` sends.
var deploymentsSinceForm = regexp.MustCompile(`^[0-9]+[smhd]$`)

// deploymentsQueryFromFlags is the one place the flags of `afy deployments`
// become a query, and where the ones the CLI can judge are refused, before
// anything is sent. --status is the server's to judge (it knows the states);
// --before is an id it will look up.
func deploymentsQueryFromFlags() (api.DeploymentsQuery, error) {
	if deploymentsLimit < 1 || deploymentsLimit > 100 {
		return api.DeploymentsQuery{}, refuse("--limit must be a whole number from 1 to 100, got %d", deploymentsLimit)
	}
	if deploymentsSince != "" && !deploymentsSinceForm.MatchString(deploymentsSince) {
		return api.DeploymentsQuery{}, refuse("--since must be a whole number followed by s, m, h or d, for example 30m, 24h or 7d, got %q", deploymentsSince)
	}
	return api.DeploymentsQuery{
		Since:    deploymentsSince,
		Status:   deploymentsStatus,
		Limit:    deploymentsLimit,
		BeforeID: deploymentsBefore,
	}, nil
}

// deploymentsDocument is `afy deployments --json`: one page of deployments and
// the id of the next, older page (null when there is none).
type deploymentsDocument struct {
	Deployments  []api.Deployment `json:"deployments"`
	NextBeforeID *string          `json:"next_before_id"`
}

func runDeployments(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}
	// A flag given with nothing after it (--since "", --before "") would read as
	// not given and show the unfiltered first page; it is refused instead.
	for _, name := range []string{"since", "before"} {
		if cmd.Flags().Changed(name) && cmd.Flags().Lookup(name).Value.String() == "" {
			return refuse("--%s needs a value", name)
		}
	}

	return printDeployments(api.NewClient(), args[0])
}

// printDeployments is `afy deployments` past the auth check. Takes the client so
// a test can point it at a server of its own, the same seam `afy status` has.
func printDeployments(client *api.Client, agentID string) error {
	query, err := deploymentsQueryFromFlags()
	if err != nil {
		return err
	}
	sp := output.NewSpinner("Fetching deployment history...")
	sp.Start()
	page, err := client.ListDeploymentsPage(agentID, query)
	sp.Stop()

	if err != nil {
		return printRefusal("Failed to list deployments", err)
	}
	deployments := page.Deployments

	if output.IsJSON() {
		if deployments == nil {
			deployments = []api.Deployment{}
		}
		return output.JSON(deploymentsDocument{Deployments: deployments, NextBeforeID: page.NextBeforeID})
	}

	// deployments[0] is the agent's LATEST deployment only on the first page of
	// the unfiltered list; the hints about "the latest" are true only there.
	firstPage := query.Since == "" && query.Status == "" && query.BeforeID == ""

	if len(deployments) == 0 && !firstPage {
		output.PrintInfo("No deployments match.")
		return nil
	}

	if len(deployments) == 0 {
		output.PrintInfo("No deployments found for agent '%s'", agentID)
		output.Println("")
		// A deploy goes to the agent a folder links (.aetherfy/agent.json).
		output.Printf("Deploy from its code: afy init --agent %s (once, to link the folder), then afy deploy\n", agentID)
		return nil
	}

	table := output.Table([]string{"Version", "State", "Release", "Created", "Error"})
	var cut []api.Deployment // rows whose error the table shortened
	for _, d := range deployments {
		errMsg, shortened := shortenError(d.ErrorMessage)
		if shortened {
			cut = append(cut, d)
		}
		// A degraded deploy stays state=active and converges in the background
		// (control-plane REVIEW_FAQ §63); flag it inline with the N/M region
		// readiness as a separate trailing marker (state value is unchanged).
		// Same DEGRADED term as the dashboard + `afy list`.
		stateCell := formatDeployState(d.Status)
		if tag := formatDegradedTag(d.IsDegraded, d.RegionsReady, d.RegionsTotal); tag != "" {
			stateCell += " " + tag
		}
		table.Append([]string{
			fmt.Sprintf("v%d", d.Version),
			stateCell,
			// The release a RUN executed (a dash on a release row, whose own
			// number is Version), as `afy runs` prints it.
			formatReleaseVersion(d.ReleaseVersion),
			d.CreatedAt.Format("2006-01-02 15:04"),
			errMsg,
		})
	}
	table.Render()

	// THE FULL SENTENCE OF EVERY ERROR THE TABLE CUT. A refusal is written for
	// the person reading it (a push without aetherfy.yaml names the repo,
	// commit, branch and path it looked in; a frozen account says what to
	// reduce), and its end is where the fix is. The table keeps its width;
	// the sentence is printed whole below it.
	if len(cut) > 0 {
		output.Println("")
		for _, d := range cut {
			output.Printf("v%d error: %s\n", d.Version, d.ErrorMessage)
		}
	}

	output.Println("")
	output.Dim.Printf("Total: %d deployment(s)\n", len(deployments))

	// Show hint if latest is failed
	if firstPage && len(deployments) > 0 && deployments[0].Status == "failed" {
		output.Println("")
		output.PrintWarning("Latest deployment failed.")
		// The ERROR column is truncated to fit the table, and the mapped
		// sentence it truncates is the same for every build failure anyway.
		// This is the only place `afy deployments` can say which step failed,
		// and someone reading a failed row is asking exactly that.
		printBuildFailureDetail(deployments[0].BuildFailureDetail)
		if d := newestRollbackTarget(deployments[1:]); d != nil {
			output.Printf("Roll back to v%d: afy rollback %s %d\n", d.Version, agentID, d.Version)
			// Rollback re-deploys that version's IMAGE when it still exists, which
			// carries the secrets it was built with. Offer the rebuild alongside it only
			// when the server says that version still has its source (CanRedeploy):
			// the two commands answer different questions, and the failed deployment
			// above may well be a missing secret.
			if d.CanRedeploy {
				output.Printf("Rebuild v%d with current secrets: afy redeploy %s %d\n", d.Version, agentID, d.Version)
			}
		}
	} else if firstPage && len(deployments) > 0 && deployments[0].IsDegraded {
		// Latest is serving but partially deployed — converging in the
		// background. Surface it so the user knows not every region is live.
		output.Println("")
		output.PrintWarning("Latest deployment is partially deployed — %d/%d regions ready (converging in the background).",
			deployments[0].RegionsReady, deployments[0].RegionsTotal)
		if deployments[0].PendingRegionAlertStage != "" {
			output.Printf("Convergence alert stage: %s\n", deployments[0].PendingRegionAlertStage)
		}
	}

	if firstPage {
		printWaitingPushLine(client, agentID)
	}

	// The last line, as `afy points list` ends its page.
	if page.NextBeforeID != nil {
		output.Println("")
		output.Printf("More deployments: pass --before %s for the next page.\n", *page.NextBeforeID)
	}
	return nil
}

// printWaitingPushLine names the push waiting for the deploy in progress, if
// any. It is the next entry of this history, and it has no deployment row yet,
// so without this line `afy deployments` would not show it at all. One read of
// the agent, which carries it (`github.waiting_push`); a read that fails only
// leaves the line out — the history above is already printed.
func printWaitingPushLine(client *api.Client, agentID string) {
	agent, err := client.GetAgent(agentID)
	if err != nil || agent == nil || agent.GitHub == nil || agent.GitHub.WaitingPush == nil {
		return
	}
	output.Println("")
	output.KeyValue("Push waiting", formatWaitingPush(agent.GitHub.WaitingPush))
}

// newestRollbackTarget is the first (newest) deployment the server says a
// rollback can target, or nil. The server's can_rollback is the rule -- its
// image or its stored code archive still exists -- so the suggestion is never a
// version `afy rollback` would refuse, and never skips one it would accept.
func newestRollbackTarget(deployments []api.Deployment) *api.Deployment {
	for i := range deployments {
		if deployments[i].CanRollback {
			return &deployments[i]
		}
	}
	return nil
}

// formatDeployState adds color/symbols to deployment states for table output.
func formatDeployState(state string) string {
	switch strings.ToLower(state) {
	case "active":
		return output.Success.Sprint("● active")
	case "failed":
		return output.Error.Sprint("✗ failed")
	case "superseded":
		return output.Dim.Sprint("○ superseded")
	case "queued":
		return "· queued"
	case "building":
		return "⟳ building"
	case "deploying":
		return "⟳ deploying"
	case "rolled_back":
		return output.Dim.Sprint("↩ rolled_back")
	case "completed":
		// Terminal end-state of an ephemeral JOB run (CP migration 093). Absent
		// here since that migration shipped, so it fell to `default` and printed
		// undecorated among decorated siblings.
		return output.Dim.Sprint("✓ completed")
	case "archived":
		// The agent was archived, which destroys its app (CP migration 107), so
		// this version stopped serving. Dim like every other terminal state: it
		// is history, not a failure.
		return output.Dim.Sprint("○ archived")
	default:
		return state
	}
}

// errorColumnWidth is how many characters of an error the deployments table
// shows; a longer one is cut there, with an ellipsis, and printed in full
// below the table.
const errorColumnWidth = 60

// shortenError fits msg to the table's error column, cutting on characters
// (never inside a multi-byte one, as a byte cut of "—" would), and says
// whether it cut.
func shortenError(msg string) (string, bool) {
	r := []rune(msg)
	if len(r) <= errorColumnWidth {
		return msg, false
	}
	return string(r[:errorColumnWidth-3]) + "...", true
}
