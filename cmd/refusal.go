package cmd

import (
	"errors"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
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
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		output.PrintError("%s", apiErr.Message)
		return shown(err)
	}
	output.PrintError("%s: %v", what, err)
	return shown(err)
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
