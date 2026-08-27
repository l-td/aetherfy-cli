// Command afy is the Aetherfy CLI entry point.
//
// This lives in cmd/afy/ rather than at the repository root for one reason:
// `go install` names the binary after the last element of the package path, so
// a root main package produced `aetherfy-cli` while every documented command,
// the Makefile, and the release archives all say `afy`. Anyone installing with
// `go install github.com/l-td/aetherfy-cli@latest` got a binary whose name did
// not match a single line of the README. `go install
// github.com/l-td/aetherfy-cli/cmd/afy@latest` produces `afy`.
//
// The sibling files in cmd/ are `package cmd` — the cobra command tree this
// imports. This directory is its own `package main`.
package main

import (
	"os"

	"github.com/l-td/aetherfy-cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
