// Command cp-error-codes-snapshot regenerates test/cp-error-codes-snapshot.json
// from the sibling aetherfy-control-plane checkout.
//
//	go run ./scripts/cp-error-codes-snapshot            # siblings on disk
//	go run ./scripts/cp-error-codes-snapshot -cp ../cp  # or point it somewhere
//
// Run it when the control plane's error codes change. The result is committed,
// because CI checks out no sibling repos; test/cp_error_codes_test.go re-runs
// the extraction whenever the control plane IS present and reds on any
// difference, so the committed file cannot quietly rot.
//
// It refuses to write anything it cannot vouch for. An empty or shrunken
// extraction is a bug in this script, and writing it would turn the guard into
// a rubber stamp: a snapshot with nothing in it agrees with everything.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/l-td/aetherfy-cli/test/cperrors"
)

func main() {
	repoRoot := flag.String("repo", ".", "path to the aetherfy-cli repo root")
	cpRoot := flag.String("cp", "", "path to the aetherfy-control-plane checkout (default: sibling of -repo)")
	flag.Parse()

	if err := run(*repoRoot, *cpRoot); err != nil {
		fmt.Fprintf(os.Stderr, "[cp-error-codes-snapshot] FAILED — %v\n", err)
		os.Exit(1)
	}
}

func run(repoRoot, cpRoot string) error {
	if cpRoot == "" {
		cpRoot = cperrors.Root(repoRoot)
	}

	if !cperrors.Present(cpRoot) {
		return fmt.Errorf("aetherfy-control-plane is not checked out at %s.\n"+
			"  This script only runs where the sibling is present; CI consumes the committed\n"+
			"  snapshot. Point it elsewhere with -cp, or set %s.", cpRoot, cperrors.RootEnv)
	}

	reg, err := cperrors.Extract(cpRoot)
	if err != nil {
		return err
	}
	// Refuse to write what we cannot vouch for — before touching the file.
	if err := cperrors.Validate(reg); err != nil {
		return fmt.Errorf("extraction from %s is not trustworthy: %v.\n"+
			"  Refusing to write a snapshot that would green-light any error code.", cpRoot, err)
	}

	body, err := cperrors.Marshal(reg)
	if err != nil {
		return err
	}

	out := filepath.Join(repoRoot, filepath.FromSlash(cperrors.SnapshotPath))
	if err := os.WriteFile(out, body, 0o644); err != nil {
		return err
	}

	fmt.Printf("[cp-error-codes-snapshot] wrote %s (%d codes)\n", out, len(reg.Codes))
	for _, s := range reg.Sources {
		fmt.Printf("  %-28s %3d  (%s)\n", s.Path, s.Count, s.Tier)
	}
	return nil
}
