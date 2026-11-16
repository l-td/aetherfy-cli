package cmd

import (
	"fmt"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// cancelRun is --run of `afy cancel`: the run to cancel, by its id.
var cancelRun string

func init() {
	agentsCancelCmd.Flags().StringVar(&cancelRun, "run", "", "Cancel this run (its id is in 'afy runs <name>') instead of the agent's pending release")
}

// cancelRunFlagRefusal is the refusal of a --run value that is no run id: nothing
// after the flag, or the next flag taken for its value (`--run --json`). Nothing
// is sent.
func cancelRunFlagRefusal(runID string) error {
	if runID == "" {
		return refuse("--run needs a run id: the id 'afy runs <name>' lists in its Run ID column. Nothing was sent.")
	}
	if strings.HasPrefix(runID, "-") {
		return refuse("--run got %q as its value, and no run id starts with \"-\": name the run id after --run. Nothing was sent.", runID)
	}
	return nil
}

// cancelAgentRun is `afy cancel <agent> --run <run-id>`.
//
// THE RUN MUST BE THE AGENT'S, and that is decided BEFORE anything is cancelled:
// the cancel route takes a run id alone, and the run's agent is only in its own
// record. So the agent is resolved the way every command resolves one (GET
// /agents/{name-or-id}: its id is the agent's) and the run is read (GET
// /deployments/{id}), and a run of another agent is refused as input, exit 2,
// with nothing changed. Only then is the cancel sent. Both reads are refusals the
// server words (an unknown agent, an unknown run, a run of another account).
//
// The answer is the server's: what the call did (cancelled now, being stopped, or
// already finished, which is not an error) and its sentence, printed as the
// control plane wrote it for the one outcome it ends cleanly.
func cancelAgentRun(client *api.Client, agentName, runID string) error {
	sp := output.NewSpinner(fmt.Sprintf("Cancelling run %s of '%s'...", runID, agentName))
	sp.Start()

	agent, err := client.GetAgent(agentName)
	if err != nil {
		sp.Stop()
		return printRefusal("Failed to get agent", err)
	}
	run, err := client.GetDeployment(runID)
	if err != nil {
		sp.Stop()
		return printRefusal("Failed to get run", err)
	}
	if run.AgentID != agent.ID {
		sp.Stop()
		return refuse("run %s is not a run of '%s' (it belongs to the agent with id %s). Nothing was cancelled.", runID, agentName, run.AgentID)
	}

	resp, err := client.CancelRun(runID)
	sp.Stop()
	if err != nil {
		return printRefusal("Failed to cancel run", err)
	}

	printRunCancelled(runID, resp)
	return output.Result(resp)
}

// printRunCancelled says what the cancel did. "cancelled" and "stopping" are the
// CLI's sentences (they say what the outcome means for this run), "already_finished"
// is the server's message and not an error. A cancel that took the runs below
// the run with it says how many. An outcome this CLI does not know prints the
// server's message.
func printRunCancelled(runID string, resp *api.RunCancelResponse) {
	switch resp.Outcome {
	case "cancelled":
		output.PrintSuccess("Run %s cancelled.", runID)
		// cancelled_count counts the runs this call cancelled, the run itself among
		// them, so the runs below it are one fewer.
		if below := resp.CancelledCount - 1; below > 0 {
			if below == 1 {
				output.Println("1 run below it was cancelled too.")
			} else {
				output.Println(fmt.Sprintf("%d runs below it were cancelled too.", below))
			}
		}
	case "stopping":
		output.PrintInfo("Run %s is being stopped; it ends cancelled.", runID)
	default:
		// already_finished, and any outcome added after this CLI: the server's own
		// sentence, or a plain statement of the outcome when it sent none.
		if resp.Message != "" {
			output.PrintInfo("%s", resp.Message)
		} else {
			output.PrintInfo("Run %s: %s.", runID, resp.Outcome)
		}
	}
}
