package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/vectors"
)

// printRefusal prints a failed request and returns its error.
//
// A REFUSAL THE CONTROL PLANE EXPLAINS IS PRINTED AS IT WROTE IT. Its message
// is written for the person reading it (the frozen-account deploy refusals
// say what to reduce; RELEASE_CONFIG_NOT_ON_RECORD says to deploy again from
// source; a yaml declaring name: says where the name is set now), and the
// server is that sentence's one owner: the CLI neither rewords it nor wraps it
// in its own prefix. Anything else (no answer, or an answer without a
// message) is printed after `what`.
// isNotDeployed reports the export's answer for a draft (an agent never
// released): 404 AGENT_NOT_DEPLOYED.
func isNotDeployed(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "AGENT_NOT_DEPLOYED"
}

// noReleaseYet is what `afy init --agent` and `afy diff` say about a draft.
func noReleaseYet(agent string) string {
	return agent + " has no release yet: deploy it first"
}

// The returned error is marked as printed, so Execute does not print it a
// second time; its exit code and its type (errors.As) are unchanged.
func printRefusal(what string, err error) error {
	if status, code, msg, ok := serverRefusal(err); ok && msg != "" {
		output.PrintAPIError(status, code, msg)
		return shown(err)
	}
	status, code, _, _ := serverRefusal(err)
	msg := err.Error()
	if what != "" {
		msg = fmt.Sprintf("%s: %v", what, err)
	}
	output.PrintAPIError(status, code, msg)
	return shown(err)
}

// narrowBlockedSentence is the refusal of a workspace regions change that
// would remove a region a resource still uses (422
// WORKSPACE_NARROW_BLOCKED_BY_RESOURCES), with the regions named when the
// server says which. ok is false for any other error.
func narrowBlockedSentence(err error) (sentence string, ok bool) {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "WORKSPACE_NARROW_BLOCKED_BY_RESOURCES" || apiErr.Message == "" {
		return "", false
	}
	if len(apiErr.RegionsInUse) == 0 {
		return apiErr.Message, true
	}
	return fmt.Sprintf("%s Regions still in use: %s.", strings.TrimSpace(apiErr.Message), strings.Join(apiErr.RegionsInUse, ", ")), true
}

// serverRefusal reads the status, code and message off an error an API
// answered with: the control plane's or the vectors API's. ok is false for an
// error no server wrote (no answer, a local refusal).
func serverRefusal(err error) (status int, code, message string, ok bool) {
	var cp *api.APIError
	if errors.As(err, &cp) {
		return cp.StatusCode, cp.Code, withViolations(cp.Message, cp.Violations), true
	}
	var vec *vectors.APIError
	if errors.As(err, &vec) {
		return vec.Status, vec.Code, vec.Message, true
	}
	return 0, "", "", false
}

// withViolations is a server's summary followed by the rules it names, one per
// line: "Request validation failed." alone does not say which one.
func withViolations(message string, violations []string) string {
	if len(violations) == 0 {
		return message
	}
	return message + "\n  - " + strings.Join(violations, "\n  - ")
}

// printUnprinted is Execute's report of an error no command printed (a flag
// that does not exist, a refusal raised before any request): text "Error: ..."
// or, with --json, the same {"error":{status,code,message}} object on stderr
// with the server's code when there is one.
func printUnprinted(err error) {
	status, code, msg, ok := serverRefusal(err)
	if !ok || msg == "" {
		msg = err.Error()
	}
	var in *inputError
	if !ok && errors.As(err, &in) {
		code = usageErrorCode
	}
	if !output.IsJSON() && (jsonOut || argsAskForJSON(os.Args[1:])) {
		// A flag error stops cobra before PersistentPreRunE: the run has not
		// been switched yet, but --json was typed.
		output.SetJSON(true)
	}
	output.PrintAPIError(status, code, msg)
}

// printedError is an error its command has already shown to the user.
type printedError struct{ error }

// shown marks err as already printed by its command: Execute does not print
// it a second time. A command that prints its own error with output.PrintError
// and then returns it returns shown(err); TestNoCommandPrintsAnErrorItAlsoReturns
// holds every command to that.
func shown(err error) error { return &printedError{err} }

func (e *printedError) Unwrap() error { return e.error }

func alreadyPrinted(err error) bool {
	var p *printedError
	return errors.As(err, &p)
}

// argsAskForJSON reports whether argv carries --json, for an error raised
// before cobra parsed it (an unknown flag typed ahead of it).
func argsAskForJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--json" || a == "--json=true" {
			return true
		}
	}
	return false
}

// resultCode is the exit code of a command that returns a code, not an error
// (redeploy, rollback): 0, or 1 after printing why its result could not be
// written.
func resultCode(err error) int {
	if err == nil {
		return 0
	}
	output.PrintError("%v", err)
	return 1
}
