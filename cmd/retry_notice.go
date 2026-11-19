package cmd

import (
	"fmt"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// WHILE THE PLATFORM RETRIES, A WAIT SAYS SO. A deployment or a run whose attempt met a temporary unavailability of
// the platform's own is retried by the platform, and the record says so (`retry`: the attempt made next and when).
// `afy deploy` (and `redeploy`, `rollback`) and `afy run --wait` print one line each time that changes, so a wait
// that is longer than usual is not a silent one.

// retryNow is the instant the retry line counts its seconds from. A variable so a test fixes it: the line is
// asserted by events, never by the time that passes.
var retryNow = time.Now

// retryLine is the sentence for one retry: the whole seconds up to the next attempt, rounded up, or "now" when it
// is due. It says the same for every code the server sends; the CLI branches on none.
func retryLine(r *api.RetryState, now time.Time) string {
	wait := r.NextAttemptAt.Sub(now)
	secs := int64(wait / time.Second)
	if wait > 0 && wait%time.Second != 0 {
		secs++ // the ceiling of a wait that is not whole seconds
	}
	if secs <= 0 {
		return fmt.Sprintf("The platform was temporarily unavailable: retrying now (attempt %d of %d).", r.Attempt, r.MaxAttempts)
	}
	return fmt.Sprintf("The platform was temporarily unavailable: retrying in %d s (attempt %d of %d).", secs, r.Attempt, r.MaxAttempts)
}

// retrySaid remembers the retry a watch last said, so a poll that finds the same retry says nothing. A retry is
// new when its attempt, its number of attempts or its time differs from the one said (the time compared as an
// instant, not as a spelling). When the record carries none the memory is cleared, so a retry that comes back
// after a stretch with none is said again: nothing was being retried in between.
type retrySaid struct {
	last *api.RetryState
}

// say prints the line for r unless it is the retry already said.
func (s *retrySaid) say(r *api.RetryState) {
	if r == nil {
		s.last = nil
		return
	}
	if s.last != nil && s.last.Attempt == r.Attempt && s.last.MaxAttempts == r.MaxAttempts &&
		s.last.NextAttemptAt.Equal(r.NextAttemptAt) {
		return
	}
	said := *r
	s.last = &said
	output.Printf("%s\n", retryLine(r, retryNow()))
}
