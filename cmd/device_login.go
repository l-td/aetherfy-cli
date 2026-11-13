package cmd

// THE DEVICE SIGN-IN. `afy login` (with no key to hand) and `afy keys create`
// both run it: the CLI asks the control plane for a code, prints the code and
// the page to approve it on, and polls until a person has approved it in the
// signed-in dashboard. The new key arrives ONCE, in the answer to the poll that
// finds the code approved.
//
// A KEY CAN NEVER MINT A KEY. Neither call carries the CLI's own credentials
// (api.NewAnonymousClient), so a stolen key cannot make more keys: that takes a
// person approving a code in a dashboard session.
//
// THE WAIT IS COUNTED, NOT TIMED. The code lives expires_in seconds; every wait
// the flow asks for is taken off that budget when it is asked, so the flow ends
// after the waits the server's own numbers add up to, whatever the machine's
// clock or load does, and a test that injects the wait (deviceWait) sees every
// wait as an event. The server stays the authority: its 410 ends the flow
// whenever it comes.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

const (
	// deviceDefaultInterval is the poll interval when the server states none.
	deviceDefaultInterval = 5 * time.Second
	// deviceMaxUnavailable is how many 503s in a row the flow waits out before it
	// gives up and says what the server said.
	deviceMaxUnavailable = 5

	// The device sign-in's server codes. The control plane's registry holds the
	// spellings; test/cp_error_codes_test.go holds this file to them.
	codeDeviceExpired      = "DEVICE_CODE_EXPIRED"
	codeDeviceDenied       = "DEVICE_ACCESS_DENIED"
	codeDeviceUsed         = "DEVICE_CODE_ALREADY_USED"
	codeDeviceNotFound     = "DEVICE_CODE_NOT_FOUND"
	codeDeviceTooFast      = "DEVICE_POLLING_TOO_FAST"
	codeAPIKeyLimit        = "API_KEY_LIMIT_REACHED"
	codeServiceUnavailable = "SERVICE_UNAVAILABLE"

	apiKeysPage = "https://app.aetherfy.com/dashboard/settings/api-keys"
)

// The seams a test replaces. deviceWait is how the flow lets time pass between
// polls; deviceOpenBrowser opens the page; deviceHostname names this machine in
// the request.
var (
	deviceWait        = waitFor
	deviceOpenBrowser = openBrowser
	deviceHostname    = os.Hostname
)

// waitFor lets d pass, or returns the context's error when it ends first.
func waitFor(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// deviceOptions are what differs between the callers of the flow.
type deviceOptions struct {
	// purpose finishes "Open this page ... and approve the code to <purpose>:".
	purpose string
	// again is the command that starts the flow over, named in the sentences
	// that end it.
	again string
	// keyName names the new key; empty lets the server choose.
	keyName   string
	noBrowser bool
}

// deviceResult is an approved sign-in: the new key's plaintext, shown to the
// person once and stored (or not) by the caller, and its row.
type deviceResult struct {
	APIKey string
	Key    *api.APIKey
}

// deviceNameMaxRunes is the longest device name the sign-in accepts (1 to 64 characters). It is the control
// plane's and the dashboard's number, copied here because this module cannot import theirs, and held equal
// by aetherfy-e2e-tests tests/pyunit/test_device_signin_values_pair.py.
const deviceNameMaxRunes = 64

// deviceName names this machine in the dashboard's approval page: its host name, cut to what the sign-in
// accepts. A fully qualified host name can be 253 characters, and a longer one than the limit is refused
// 422, which would be a login that never starts.
func deviceName() string {
	if h, err := deviceHostname(); err == nil && h != "" {
		if r := []rune(h); len(r) > deviceNameMaxRunes {
			return string(r[:deviceNameMaxRunes])
		}
		return h
	}
	return "afy CLI"
}

// runDeviceFlow runs the whole flow. Every error it returns has been printed
// already (shown), so the caller returns it as it is.
func runDeviceFlow(ctx context.Context, client *api.Client, opts deviceOptions) (*deviceResult, error) {
	auth, err := client.StartDevice(deviceName(), opts.keyName)
	if err != nil {
		return nil, deviceFailure(err, opts, "Could not start the sign-in")
	}
	if auth.DeviceCode == "" || auth.UserCode == "" || auth.ExpiresIn <= 0 ||
		(auth.VerificationURI == "" && auth.VerificationURIComplete == "") {
		return nil, printRefusal("", &api.ResponseInvalidError{
			Message: "The control plane's answer to the sign-in request lacks the code or the page to approve it on. Update afy: run 'afy upgrade'."})
	}

	announceDevice(auth, opts)

	interval := deviceDefaultInterval
	if auth.Interval > 0 {
		interval = time.Duration(auth.Interval) * time.Second
	}
	remaining := time.Duration(auth.ExpiresIn) * time.Second
	wait := interval
	unavailable := 0
	lastWarning := ""

	for wait <= remaining {
		if err := deviceWait(ctx, wait); err != nil {
			output.Println("")
			output.PrintAPIError(0, "", fmt.Sprintf("Stopped waiting. Run '%s' again for a new code.", opts.again))
			return nil, shown(&stoppedError{})
		}
		remaining -= wait

		tok, err := client.PollDevice(auth.DeviceCode)
		if err == nil {
			unavailable = 0
			lastWarning = ""
			if tok.Status == api.DeviceStatusApproved {
				return &deviceResult{APIKey: tok.APIKey, Key: tok.Key}, nil
			}
			if tok.Interval > 0 {
				interval = time.Duration(tok.Interval) * time.Second
			}
			wait = interval
			continue
		}

		var apiErr *api.APIError
		if !errors.As(err, &apiErr) {
			var invalid *api.ResponseInvalidError
			if errors.As(err, &invalid) {
				return nil, printRefusal("", err)
			}
			// No answer (the network, a timeout): the code is still good, so keep
			// asking until it is not. Said once per distinct message, not per poll.
			if msg := err.Error(); msg != lastWarning {
				output.PrintWarning("Still waiting: %v", err)
				lastWarning = msg
			}
			wait = interval
			continue
		}

		switch {
		case apiErr.Code == codeDeviceTooFast:
			// Slowed down by the server: wait as long as it says, then go on.
			wait = interval
			if apiErr.RetryAfter > 0 {
				wait = apiErr.RetryAfter
			}
		case apiErr.Code == codeServiceUnavailable:
			unavailable++
			if unavailable > deviceMaxUnavailable {
				return nil, deviceFailure(err, opts, "The sign-in could not be completed")
			}
			wait = interval
			if apiErr.RetryAfter > 0 {
				wait = apiErr.RetryAfter
			}
		default:
			return nil, deviceFailure(err, opts, "The sign-in could not be completed")
		}
	}

	// The counted budget ran out with the code still pending. No server said so,
	// so there is no status; the code is the one the server would have used.
	sentence, _ := deviceSentence(codeDeviceExpired, opts)
	output.PrintAPIError(0, codeDeviceExpired, sentence)
	return nil, shown(&api.APIError{Code: codeDeviceExpired, Message: sentence})
}

// stoppedError is a flow the person ended (Ctrl-C).
type stoppedError struct{}

func (*stoppedError) Error() string { return "stopped waiting for the code to be approved" }

// announceDevice tells the person what to do: the page, the code to compare,
// and (unless --no-browser) opens the page.
func announceDevice(auth *api.DeviceAuthorization, opts deviceOptions) {
	page := auth.VerificationURIComplete
	if page == "" {
		page = auth.VerificationURI
	}
	output.Println("")
	output.Printf("Open this page, signed in to Aetherfy, and approve the code to %s:\n", opts.purpose)
	output.Println("")
	output.Bold.Println("  " + page)
	output.Println("")
	output.Printf("The page must show this code: %s\n", auth.UserCode)
	if auth.VerificationURIComplete == "" {
		output.Printf("Enter it at %s\n", auth.VerificationURI)
	}
	output.Println("")
	if !opts.noBrowser {
		// Best effort. A browser that does not open is not an error: the page is
		// printed above and can be opened anywhere.
		if err := deviceOpenBrowser(page); err == nil {
			output.PrintInfo("Opening your browser...")
		} else {
			output.PrintInfo("Open the page above in your browser.")
		}
	}
	output.PrintInfo("Waiting for approval. The code expires in %s.", humanWait(time.Duration(auth.ExpiresIn)*time.Second))
}

// humanWait spells a duration for a person: minutes when it is whole minutes.
func humanWait(d time.Duration) string {
	secs := int(d.Seconds())
	if secs >= 60 && secs%60 == 0 {
		if secs == 60 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", secs/60)
	}
	return fmt.Sprintf("%d seconds", secs)
}

// deviceSentence is the plain sentence that ends the flow for one of the codes
// it knows: what happened, and what to do. ok is false for any other code.
func deviceSentence(code string, opts deviceOptions) (sentence string, ok bool) {
	switch code {
	case codeDeviceExpired:
		return fmt.Sprintf("The code expired before it was approved. Run '%s' again for a new one.", opts.again), true
	case codeDeviceDenied:
		return fmt.Sprintf("The request was denied in the dashboard, so no key was created. If that was a mistake, run '%s' again.", opts.again), true
	case codeDeviceUsed:
		return fmt.Sprintf("This code was already used. If you did not receive the key, it was made and is listed by 'afy keys list': revoke it there. Run '%s' again for a new one.", opts.again), true
	case codeDeviceNotFound:
		return fmt.Sprintf("The control plane does not know this code. Run '%s' again for a new one.", opts.again), true
	case codeAPIKeyLimit:
		return fmt.Sprintf("Your plan's limit on API keys is reached, so no key was created. Revoke one you no longer use "+
			"('afy keys list' shows them, 'afy keys revoke <id>' revokes one) or at %s, then run '%s' again.",
			apiKeysPage, opts.again), true
	}
	return "", false
}

// deviceFailure prints the end of a flow the server refused. A code the flow
// knows gets its sentence (the code and status stay the server's, for --json);
// anything else is the server's own words.
func deviceFailure(err error, opts deviceOptions, what string) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		if sentence, ok := deviceSentence(apiErr.Code, opts); ok {
			output.PrintAPIError(apiErr.StatusCode, apiErr.Code, sentence)
			return shown(err)
		}
	}
	return printRefusal(what, err)
}
