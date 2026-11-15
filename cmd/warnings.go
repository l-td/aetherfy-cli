package cmd

import (
	"strconv"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// printWarnings prints what the server says to know about an agent or a release, one line each, as the
// server worded it. A warning is never an error: the request it rode on succeeded, so this adds no exit
// code and decides nothing. The sentence carries the dates and the runtime to move to; the CLI holds
// neither a runtime name nor a date of its own.
//
// EVERY COMMAND WHOSE ANSWER CARRIES `warnings` CALLS IT (or the list form below), wherever they sit in the
// answer: afy deploy, redeploy, rollback and cancel (a release), afy status, create and update (an agent),
// a move (inside its redeploy, `deployment.warnings`), afy list and afy deployments (a list). A command that
// reads one of these answers and says nothing of its warnings is a defect: cmd/runtime_warnings_everywhere_test.go.
func printWarnings(warnings []api.Warning) {
	for _, w := range warnings {
		output.PrintWarning("%s", w.Message)
	}
}

// warned is one entry of a list answer: what to call it, and its warnings.
type warned struct {
	who      string
	warnings []api.Warning
}

// printWarningsOf is printWarnings for a LIST: the same sentence on ten agents is one line saying whom it
// covers, not ten lines. Distinct sentences print in the order they first appear.
func printWarningsOf(items []warned) {
	var order []string
	covers := map[string][]string{}
	for _, it := range items {
		for _, w := range it.warnings {
			if _, seen := covers[w.Message]; !seen {
				order = append(order, w.Message)
			}
			covers[w.Message] = append(covers[w.Message], it.who)
		}
	}
	if len(order) == 0 {
		return
	}
	output.Println("")
	for _, message := range order {
		output.PrintWarning("%s (%s)", message, strings.Join(covers[message], ", "))
	}
}

// printAgentListWarnings says, under `afy list`'s table, what the server warns about the agents in it.
func printAgentListWarnings(agents []api.Agent) {
	items := make([]warned, 0, len(agents))
	for i := range agents {
		items = append(items, warned{who: agents[i].Name, warnings: agents[i].Warnings})
	}
	printWarningsOf(items)
}

// printDeploymentListWarnings says, under `afy deployments`' table, what the server warns about the
// releases in it, by version.
func printDeploymentListWarnings(deployments []api.Deployment) {
	items := make([]warned, 0, len(deployments))
	for _, d := range deployments {
		items = append(items, warned{who: "v" + strconv.Itoa(d.Version), warnings: d.Warnings})
	}
	printWarningsOf(items)
}
