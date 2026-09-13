// Command cp-link-status-snapshot regenerates test/cp-link-status-snapshot.json
// from the sibling aetherfy-control-plane checkout.
//
//	go run ./scripts/cp-link-status-snapshot            # siblings on disk
//	go run ./scripts/cp-link-status-snapshot -cp ../cp  # or point it somewhere
//
// Run it when the control plane's link-status response gains, loses or renames a
// field. The result is committed, because CI checks out no sibling repos;
// test/cp_link_status_test.go re-runs the extraction whenever the control plane
// IS present and reds on any difference, so the committed file cannot quietly
// rot.
//
// It refuses to write anything it cannot vouch for:
//
//   - an empty or shrunken extraction is a bug in the parser, and writing it
//     would turn the guard into a rubber stamp — a snapshot with nothing in it
//     agrees with everything;
//   - a source file that is dirty or sits in unpushed commits describes code
//     nobody else can see yet, and a snapshot of it would red on every other
//     machine until the change was pushed.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cplink"
)

func main() {
	repoRoot := flag.String("repo", ".", "path to the aetherfy-cli repo root")
	cpRoot := flag.String("cp", "", "path to the aetherfy-control-plane checkout (default: sibling of -repo)")
	flag.Parse()

	if err := run(*repoRoot, *cpRoot); err != nil {
		fmt.Fprintf(os.Stderr, "[cp-link-status-snapshot] FAILED — %v\n", err)
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
	// A checkout with a MOVED model is a different failure from no checkout, and
	// must never be reported as the latter.
	if cplink.SourceMissing(cpRoot) {
		return fmt.Errorf("the control plane is checked out at %s, but %s is not there.\n"+
			"  That is a move or a rename. Update SourcePath in test/cplink/extract.go to the\n"+
			"  new path — extracting from nothing would write a snapshot that agrees with\n"+
			"  every field name.", cpRoot, cplink.SourcePath)
	}

	fields, err := cplink.Extract(cpRoot)
	if err != nil {
		return err
	}
	// Refuse to write what we cannot vouch for — before touching the file.
	if err := cplink.Validate(fields); err != nil {
		return fmt.Errorf("extraction from %s is not trustworthy: %v.\n"+
			"  Refusing to write a snapshot that would green-light any field name.", cpRoot, err)
	}
	if why := cplink.Unshareable(fields.Source); why != "" {
		return fmt.Errorf("%s.\n"+
			"  The committed snapshot must describe PUSHED code, or it reds on every machine\n"+
			"  that does not have your working copy. Push the control-plane change first,\n"+
			"  then regenerate.", why)
	}

	body, err := cplink.Marshal(fields)
	if err != nil {
		return err
	}

	out := filepath.Join(repoRoot, filepath.FromSlash(cplink.SnapshotPath))
	if err := os.WriteFile(out, body, 0o644); err != nil {
		return err
	}

	fmt.Printf("[cp-link-status-snapshot] wrote %s (%d field(s) of %s)\n",
		out, len(fields.Names), cplink.ModelName)
	for _, n := range fields.Names {
		fmt.Printf("  %s\n", n)
	}
	return nil
}
