// Package cpbudgets extracts the control plane's deploy-stage budgets -- how
// long a build may run, and how long a release's health gate may wait for it to
// serve -- from the sibling aetherfy-control-plane checkout.
//
// WHY IT EXISTS. `afy deploy`, `afy redeploy` and `afy rollback` wait for a
// deployment to go live, and a wait shorter than the stages it waits on reports
// "timed out" for a deploy that then succeeds. The control plane holds a release
// in `deploying` until its health gate has seen it serve (up to the gate's
// budget, after a build of up to the build budget), so the CLI's budget is built
// from those two numbers -- and this package is what keeps the CLI's copies of
// them equal to the control plane's own.
//
// NO COMMITTED SNAPSHOT, like cpreadiness and for the same reason: two numbers,
// and nothing in this repo needs them where the control plane is absent. This
// repo's CI checks out no sibling and so does not run the comparison; the e2e
// nightly, which sets cperrors.RequireEnv, does.
package cpbudgets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Budget is one control-plane constant, in seconds, and where it lives.
type Budget struct {
	Name string // the constant's name
	Path string // CP-repo-relative file that assigns it
}

// The budgets the deploy watch is built from.
var (
	Build      = Budget{Name: "BUILD_TIMEOUT_SECONDS", Path: "orchestrator/fly_builder.py"}
	HealthGate = Budget{Name: "DEPLOY_HEALTH_GATE_TIMEOUT_SECONDS", Path: "workers/deploy_worker.py"}
)

// Read returns the budget's value from cpRoot. Module level only (column 0), an
// integer literal, an optional trailing comment. Anything else -- the constant
// missing, assigned twice, or not a plain number -- is an error, never zero:
// a zero would make any CLI budget look long enough.
func Read(cpRoot string, b Budget) (time.Duration, error) {
	raw, err := os.ReadFile(filepath.Join(cpRoot, filepath.FromSlash(b.Path)))
	if err != nil {
		return 0, err
	}
	decl := regexp.MustCompile(`^` + regexp.QuoteMeta(b.Name) + `\s*=\s*([0-9]+)\s*(?:#.*)?$`)
	var found []int
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if m := decl.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			found = append(found, n)
		}
	}
	switch {
	case len(found) == 0:
		return 0, fmt.Errorf("%s is not assigned an integer at module level in %s -- the parser has "+
			"rotted or the constant moved; refusing to treat that as agreement", b.Name, b.Path)
	case len(found) > 1:
		return 0, fmt.Errorf("%s is assigned %d times in %s", b.Name, len(found), b.Path)
	case found[0] <= 0:
		return 0, fmt.Errorf("%s = %d in %s is not a budget", b.Name, found[0], b.Path)
	}
	return time.Duration(found[0]) * time.Second, nil
}
