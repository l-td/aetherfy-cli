package vectors

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Payload-index writes (create and delete). vectordb holds ONE of them for up
// to IndexWaitBudget while Qdrant applies it, then answers "acknowledged"
// (still in progress): backend/config/timeouts.js INDEX_WAIT_BUDGET_MS. When
// the region that answers does not host the collection it forwards the write
// first, which vectordb allows IndexForwardMargin for (FORWARD_MARGIN_MS in the
// same file). Each attempt's HTTP timeout, IndexAttemptTimeout, must outlast
// both plus this client's own hop, or the CLI gives up before the server's
// answer arrives.
//
// The values are the SDKs', copied: aetherfy-vectors-python-sdk
// aetherfy_vectors/client.py INDEX_WAIT_BUDGET_S, INDEX_FORWARD_MARGIN_S,
// INDEX_ATTEMPT_TIMEOUT_S, INDEX_DEFAULT_DEADLINE_S, INDEX_RESEND_PAUSE_FIRST_S,
// INDEX_RESEND_PAUSE_MAX_S, and the *_MS twins in aetherfy-vectors-js-sdk
// src/client.ts. aetherfy-e2e-tests tests/pyunit/test_index_timeouts_pair.py
// reads all six from THIS file (the first three against vectordb, the pacing
// three against both SDKs), so each stays one `Name = <integer> * time.Second`
// line.
const (
	IndexWaitBudget     = 25 * time.Second
	IndexForwardMargin  = 5 * time.Second
	IndexAttemptTimeout = 45 * time.Second
	// A create is never unbounded: with no --timeout it stops at
	// IndexDefaultDeadline with the same "still building" error. And an
	// "acknowledged" that came back faster than IndexWaitBudget means the
	// server did not hold the create (a vectordb from before db26396 answers
	// every create that way, at once), so the next create waits first:
	// IndexResendPauseFirst, doubling up to IndexResendPauseMax. An
	// "acknowledged" the server held for its budget is re-sent at once.
	IndexDefaultDeadline  = 600 * time.Second
	IndexResendPauseFirst = 1 * time.Second
	IndexResendPauseMax   = 10 * time.Second
)

// The retry budget of one create, the SDKs' retry_with_backoff defaults as
// _make_request calls it for a PUT: 3 retries, 1 s doubling, capped at 30 s,
// each delay jittered to 50-100% of itself.
const (
	indexRetries       = 3
	indexRetryBase     = 1 * time.Second
	indexRetryMaxDelay = 30 * time.Second
)

// ParseIndexTimeout reads --timeout: a finite number of seconds above 0.
// Anything else is refused naming the value, before any request, as the SDKs
// refuse it.
func ParseIndexTimeout(raw string) (time.Duration, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, fmt.Errorf("--timeout must be a finite number of seconds above 0, got '%s'", raw)
	}
	return time.Duration(v * float64(time.Second)), nil
}

// indexAnswer is the answer to a create: result.status is "completed" once
// the index is built, "acknowledged" while it is still building.
type indexAnswer struct {
	Result interface{} `json:"result"`
}

// CreateFieldIndex creates a payload index on one field and returns only once
// it is built: aetherfy-vectors-python-sdk create_field_index (3993b57,
// 1cbf891, 2af3874), aetherfy-vectors-js-sdk createFieldIndex (a66cf63,
// 84e6e30, d698aeb).
//
// It re-sends the create while the server answers "acknowledged" (a re-issued
// create waits for the running build) and returns nil only on "completed". Any
// other answer is an error. timeout is the deadline for the whole call, every
// create, retry and pause included; 0 means IndexDefaultDeadline. The caller
// has already refused a timeout that is not above 0 (ParseIndexTimeout).
//
// fieldSchema is forwarded verbatim: a type name, or a parameterised object.
func (c *Client) CreateFieldIndex(collection, field string, fieldSchema interface{}, timeout time.Duration) error {
	if timeout == 0 {
		timeout = IndexDefaultDeadline
	}
	deadline := c.now().Add(timeout)
	attemptCap := maxDuration(c.timeout, IndexAttemptTimeout)
	pause := IndexResendPauseFirst
	stillBuilding := &TimeoutError{Message: fmt.Sprintf(
		"The payload index on '%s' in collection '%s' is still building after the %s s "+
			"deadline. The build carries on server-side; calling afy index create again "+
			"waits for it.", field, collection, seconds(timeout))}
	noAnswer := &TimeoutError{Message: fmt.Sprintf(
		"The payload index create on '%s' in collection '%s' got no answer within the "+
			"%s s deadline, so it is not known whether it was taken. Calling afy index "+
			"create again is safe.", field, collection, seconds(timeout))}
	path := c.collectionPath(collection, "/index")
	body := map[string]interface{}{"field_name": field, "field_schema": fieldSchema}

	acknowledged := false
	for {
		if c.now().Sub(deadline) >= 0 {
			// Only a build some answer reported is claimed.
			if acknowledged {
				return stillBuilding
			}
			return noAnswer
		}
		started := c.now()
		var answer indexAnswer
		err := c.putWithinDeadline(path, body, deadline, timeout, attemptCap, &answer)
		if err != nil {
			var te *TimeoutError
			if errors.As(err, &te) {
				// Before the deadline, this is one create outliving its own HTTP
				// timeout, and that error stands. At the deadline, after an
				// "acknowledged" the build is known to be running; on the first
				// create it is not known the create was even taken, so that is
				// not claimed.
				if c.now().Before(deadline) {
					return err
				}
				if acknowledged {
					return stillBuilding
				}
				return noAnswer
			}
			return err
		}
		status, isString := statusOf(answer.Result)
		if isString && status == "completed" {
			return nil
		}
		if !isString || status != "acknowledged" {
			shown := "null"
			if isString {
				shown = "'" + status + "'"
			}
			return fmt.Errorf("afy index create got status %s for the payload index on '%s' in "+
				"collection '%s', expected 'completed' or 'acknowledged'; the index is not "+
				"confirmed built.", shown, field, collection)
		}
		acknowledged = true
		if c.now().Sub(started) < IndexWaitBudget {
			// The server did not hold this create, so re-sending at once would
			// hammer it.
			c.sleep(minDuration(pause, maxDuration(deadline.Sub(c.now()), 0)))
			pause = minDuration(pause*2, IndexResendPauseMax)
		}
	}
}

func statusOf(result interface{}) (string, bool) {
	obj, ok := result.(map[string]interface{})
	if !ok {
		return "", false
	}
	s, ok := obj["status"].(string)
	return s, ok
}

// putWithinDeadline is the SDKs' _make_request for a PUT with a deadline and
// an attempt timeout: each attempt gets the smaller of attemptCap and what
// remains of the deadline, a transient failure is retried with backoff, and no
// retry starts whose backoff would end at or past the deadline.
func (c *Client) putWithinDeadline(path string, body interface{}, deadline time.Time, timeout, attemptCap time.Duration, out interface{}) error {
	deadlinePassed := &TimeoutError{Message: fmt.Sprintf(
		"Request to %s exceeded its %s s deadline", path, seconds(timeout))}
	var last error
	for attempt := 0; attempt <= indexRetries; attempt++ {
		remaining := deadline.Sub(c.now())
		if remaining <= 0 {
			return deadlinePassed
		}
		last = c.do(http.MethodPut, path, body, minDuration(remaining, attemptCap), out)
		if last == nil {
			return nil
		}
		var te *TimeoutError
		if errors.As(last, &te) && !c.now().Before(deadline) {
			last = deadlinePassed
		}
		if !retryable(last) || attempt == indexRetries {
			break
		}
		delay := minDuration(indexRetryBase<<attempt, indexRetryMaxDelay)
		delay = time.Duration(float64(delay) * (0.5 + 0.5*c.jitter()))
		if !c.now().Add(delay).Before(deadline) {
			break
		}
		c.sleep(delay)
	}
	return last
}

// DeleteFieldIndex drops the payload index on one field: one request, not
// retried, with the create's per-attempt timeout, because vectordb may hold a
// delete as long as a create (25 s, plus a forward). The SDKs' delete_field_index.
//
// The server answers 200 for a field that was never indexed; a missing
// COLLECTION is a 404, which the CLI reports as the error it is (the SDKs
// return False for it).
func (c *Client) DeleteFieldIndex(collection, field string) error {
	path := c.collectionPath(collection, "/index/"+url.PathEscape(field))
	return c.do(http.MethodDelete, path, nil, maxDuration(c.timeout, IndexAttemptTimeout), nil)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
