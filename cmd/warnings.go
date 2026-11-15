package cmd

import (
	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// printWarnings prints what the server says to know about an agent or a release, one line each, as the
// server worded it. A warning is never an error: the request it rode on succeeded, so this adds no exit
// code and decides nothing. The sentence carries the dates and the runtime to move to; the CLI holds
// neither a runtime name nor a date of its own.
func printWarnings(warnings []api.Warning) {
	for _, w := range warnings {
		output.PrintWarning("%s", w.Message)
	}
}
