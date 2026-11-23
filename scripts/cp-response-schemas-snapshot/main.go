// Command cp-response-schemas-snapshot regenerates
// test/cp-response-schemas-snapshot.json from the sibling aetherfy-control-plane
// checkout.
//
//	go run ./scripts/cp-response-schemas-snapshot            # siblings on disk
//	go run ./scripts/cp-response-schemas-snapshot -cp ../cp  # or point it somewhere
//
// It imports the control plane's FastAPI app and reads app.openapi(), so it needs
// a Python with the control plane's requirements: CONTROL_PLANE_PYTHON names it
// (default `python` on PATH).
//
// Run it when a control-plane answer the CLI reads gains, loses, renames or
// retypes a field. The result is committed, because CI checks out no sibling
// repos; test/cp_response_schemas_test.go re-runs the extraction wherever the
// control plane IS present and reds on any difference.
//
// It refuses to write anything it cannot vouch for:
//
//   - an extraction under its floors, or with a reference it cannot resolve, is
//     a broken extraction, and a snapshot with nothing in it agrees with every
//     struct;
//   - a control-plane checkout with uncommitted Python or unpushed commits
//     describes answers nobody else gets yet (cperrors.DescribeCheckout, the one
//     rule for every control-plane snapshot this repo takes).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cpschemas"
)

func main() {
	repoRoot := flag.String("repo", ".", "path to the aetherfy-cli repo root")
	cpRoot := flag.String("cp", "", "path to the aetherfy-control-plane checkout (default: sibling of -repo)")
	flag.Parse()

	if err := run(*repoRoot, *cpRoot); err != nil {
		fmt.Fprintf(os.Stderr, "[cp-response-schemas-snapshot] FAILED — %v\n", err)
		os.Exit(1)
	}
}

func run(repoRoot, cpRoot string) error {
	if cpRoot == "" {
		cpRoot = cperrors.Root(repoRoot)
	}
	if !cperrors.RootExists(cpRoot) {
		return fmt.Errorf("aetherfy-control-plane is not checked out at %s.\n"+
			"  This script only runs where the sibling is present; CI consumes the committed\n"+
			"  snapshot. Point it elsewhere with -cp, or set %s.", cpRoot, cperrors.RootEnv)
	}
	if cpschemas.SourceMissing(cpRoot) {
		return fmt.Errorf("the control plane is checked out at %s, but it has no main.py, the module the\n"+
			"  app is imported from. That is a move, not a missing checkout: teach test/cpschemas/dump.py\n"+
			"  where the app lives now", cpRoot)
	}
	if why := cpschemas.Unshareable(cpRoot); why != "" {
		return fmt.Errorf("%s.\n"+
			"  Commit and push the control-plane change first, then regenerate. The snapshot is what\n"+
			"  CI reads, so it must describe answers CI could also get.", why)
	}

	ext, err := cpschemas.Extract(cpRoot)
	if err != nil {
		return err
	}
	if err := cpschemas.Validate(ext); err != nil {
		return fmt.Errorf("extraction from %s is not trustworthy: %v.\n"+
			"  Refusing to write a snapshot that would agree with any struct.", cpRoot, err)
	}

	src := cpschemas.DescribeSource(cpRoot)
	body, err := cpschemas.Marshal(ext, src)
	if err != nil {
		return err
	}
	out := filepath.Join(repoRoot, filepath.FromSlash(cpschemas.SnapshotPath))
	if err := os.WriteFile(out, body, 0o644); err != nil {
		return err
	}
	fmt.Printf("[cp-response-schemas-snapshot] wrote %s: %d operation(s), %d schema(s), from %s@%s\n",
		out, len(ext.Operations), len(ext.Schemas), src.Branch, src.Commit)
	return nil
}
