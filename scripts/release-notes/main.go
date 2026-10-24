// Command release-notes prints the CHANGELOG.md section of a release tag, for the
// release workflow to hand to goreleaser as the release body:
//
//	go run ./scripts/release-notes v0.4.2 > notes.md
//
// It exits 1, printing why on stderr and nothing on stdout, when the tag has no
// section or its section is empty, so a release with no written notes fails
// instead of publishing a commit list.
package main

import (
	"fmt"
	"os"

	"github.com/l-td/aetherfy-cli/internal/changelog"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: release-notes <tag> [CHANGELOG.md]")
		os.Exit(2)
	}
	path := "CHANGELOG.md"
	if len(os.Args) == 3 {
		path = os.Args[2]
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-notes:", err)
		os.Exit(1)
	}
	notes, err := changelog.Section(string(raw), os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-notes:", err)
		os.Exit(1)
	}
	fmt.Print(notes)
}
