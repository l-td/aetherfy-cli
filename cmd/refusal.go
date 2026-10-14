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
// in its own prefix. (The returned error is printed again by cobra, with its
// status and code, as for every command.) Anything else (no answer, or an
// answer without a message) is printed after `what`.
func printRefusal(what string, err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		output.PrintError("%s", apiErr.Message)
		return err
	}
	output.PrintError("%s: %v", what, err)
	return err
}
