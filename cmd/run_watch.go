package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// HOW `afy run --wait` FOLLOWS A RUN. It reads the run, then again every
// runWatchPoll, until the run ends or runWatchTimeout passes. Vars only so a
// process-level test can shorten them (TestMain); what ships is pinned by
// TestTheShippedRunWatchBounds.
var (
	runWatchPoll    = 5 * time.Second
	runWatchTimeout = 30 * time.Minute
)

// runWatchClock paces followRun, so a test drives it by events (one read per
// call to next) and never by sleeping.
type runWatchClock interface {
	// next blocks until the run is to be read again and returns true, or returns
	// false once the bound has passed.
	next() bool
	// stop releases what the clock holds.
	stop()
}

// timerRunWatchClock is the clock `afy run --wait` ships: a read every poll, and
// the bound counted from the clock's creation.
type timerRunWatchClock struct {
	ticker *time.Ticker
	bound  *time.Timer
}

func newTimerRunWatchClock(poll, bound time.Duration) *timerRunWatchClock {
	return &timerRunWatchClock{ticker: time.NewTicker(poll), bound: time.NewTimer(bound)}
}

func (c *timerRunWatchClock) next() bool {
	// A bound that has passed wins over a tick that is also due.
	select {
	case <-c.bound.C:
		return false
	default:
	}
	select {
	case <-c.bound.C:
		return false
	case <-c.ticker.C:
		return true
	}
}

func (c *timerRunWatchClock) stop() {
	c.ticker.Stop()
	c.bound.Stop()
}

// newRunWatchClock is the clock a follow of a run uses; a test replaces it.
var newRunWatchClock = func() runWatchClock { return newTimerRunWatchClock(runWatchPoll, runWatchTimeout) }

// The two ways a followed run ends badly. Each is printed by followRun, which
// returns it marked as shown; the exit code is 1, as for every failed operation.
var (
	errRunFailed    = errors.New("run failed")
	errRunCancelled = errors.New("run cancelled")
)

// inFlightRunStates are the states of a run that has not ended and that this CLI
// knows: it keeps following the run through them. A state in neither this list
// nor the ending ones is one this CLI does not know, which is followed too.
var inFlightRunStates = map[string]bool{
	"waiting": true, "queued": true, "building": true, "deploying": true, "active": true, "running": true,
	// Alive, waiting for a message: followed like any run in progress.
	"awaiting_message": true,
}

// followRun reads the run to the state it ends in, saying each change of state
// (and, while it waits in the line, of its place), and returns how it ended:
//
//   - completed: nil (exit 0);
//   - failed or error: errRunFailed (exit 1), with the run's reason;
//   - cancelled: errRunCancelled (exit 1), with the run's reason, the sentence the
//     server wrote;
//   - the clock's bound reached with the run still going: a *stillPendingError
//     (exit 4, as `afy start --wait`): the wait gave up, not the run, and the
//     message says how to follow it;
//   - a state this CLI does not know is followed like any other and said once,
//     raw, so a state added to the control plane is not a silent hang.
//
// While the platform retries an attempt of the run that met a temporary
// unavailability, each retry is said once (retrySaid), not once per read.
//
// The run is read at once and then once for every true the clock answers. A read
// that fails is said and the follow goes on: the bound is what ends it.
func followRun(client *api.Client, agentName, runID string, clock runWatchClock) error {
	defer clock.stop()

	lastState, lastPlace := "", (*int)(nil)
	said := false // whether a state has been said yet
	unknownSaid := map[string]bool{}
	var retrying retrySaid
	var last *api.Deployment

	for {
		d, err := client.GetDeployment(runID)
		if err != nil {
			output.PrintWarning("Failed to get run status: %v", err)
		} else {
			last = d
			state := strings.ToLower(d.Status)
			known := inFlightRunStates[state] || state == "completed" || state == "failed" || state == "error" || state == "cancelled"
			switch {
			case known && (!said || state != lastState || !samePlace(d.Position, lastPlace)):
				output.Printf("Status: %s\n", formatRunState(d.Status, d.Position))
			case !known && !unknownSaid[state]:
				unknownSaid[state] = true
				output.Printf("Status: %s (a state this CLI does not know; still following the run)\n", d.Status)
			}
			said, lastState, lastPlace = true, state, d.Position
			// While the platform retries an attempt that met a temporary unavailability, once for each retry.
			retrying.say(d.Retry)

			switch state {
			case "completed":
				output.PrintSuccess("Run completed.")
				return nil
			case "failed", "error":
				printRunEnd("Run failed.", d.ErrorMessage, d.FailureCode)
				return shown(errRunFailed)
			case "cancelled":
				// A cancelled run carries no failure code: its reason is cancellation_reason.
				printRunEnd("Run cancelled.", d.ErrorMessage, "")
				return shown(errRunCancelled)
			}
		}
		if !clock.next() {
			break
		}
	}

	return &stillPendingError{msg: runWatchGaveUp(agentName, runID, last)}
}

// samePlace reports whether two places in the line are the same place (or both none).
func samePlace(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// printRunEnd says how a followed run ended and why: "Error: Run failed.", a "Reason:"
// line and a "Failure code:" line, or with --json the one error object, the reason inside
// its message and the failure code as its code (stderr keeps its last line for that object).
func printRunEnd(headline, reason, failureCode string) {
	if output.IsJSON() {
		msg := headline
		if reason != "" {
			msg += " " + reason
		}
		output.PrintAPIError(0, failureCode, msg)
		return
	}
	output.PrintError("%s", headline)
	if reason != "" {
		output.Printf("Reason: %s\n", reason)
	}
	if failureCode != "" {
		output.Printf("Failure code: %s\n", failureCode)
	}
}

// runWatchGaveUp is the message of a wait that reached its bound: it is the WAIT
// that ended, the run is still going, and how to see it through.
func runWatchGaveUp(agentName, runID string, last *api.Deployment) string {
	still := "still going"
	if last != nil {
		still = "still " + strings.TrimSpace(plainRunState(last.Status, last.Position))
	}
	return fmt.Sprintf("stopped waiting after %s: run %s is %s. Giving up the wait does not stop the run: follow it with 'afy runs %s' or 'afy logs %s --run %s'.",
		formatWaitBound(runWatchTimeout), runID, still, agentName, agentName, runID)
}

// plainRunState is a run's state as formatRunState words it, with no colour.
func plainRunState(state string, position *int) string {
	if strings.EqualFold(state, "waiting") {
		return "waiting" + inLine(position)
	}
	return state
}

// formatWaitBound says a bound in whole minutes when it is one ("30 minutes").
func formatWaitBound(d time.Duration) string {
	if d%time.Minute == 0 && d >= time.Minute {
		n := int(d / time.Minute)
		if n == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", n)
	}
	return d.String()
}
