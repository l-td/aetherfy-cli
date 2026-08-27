package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Sentinels. A field still holding its sentinel was never stamped by ldflags,
// which is the one condition under which the build-info fallback may fill it.
const (
	unsetVersion = "dev"
	unsetOther   = "unknown"
)

// Version information - set via ldflags during release builds, and otherwise
// recovered from the build info the toolchain embeds (see init below).
var (
	Version   = unsetVersion
	Commit    = unsetOther
	BuildDate = unsetOther
)

func init() {
	fillFromBuildInfo()
}

// fillFromBuildInfo recovers the version stamp for builds goreleaser did not
// produce — every `go build` and every `go install ...@latest`. The toolchain
// already embeds the data (`go version -m afy` shows the pseudo-version and the
// vcs.* settings); nothing read it, so those builds reported "dev" forever and
// "what version are you on?" had no answer for that entire population.
//
// ldflags WIN. Each field is filled only while it still holds its sentinel, so
// a release build's output is byte-identical to what it was before this existed.
func fillFromBuildInfo() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		// No build info (e.g. a binary built by a toolchain that omits it).
		// Leave the sentinels alone — blanking them would replace a truthful
		// "dev" with an empty field.
		return
	}

	// "(devel)" is the toolchain's own placeholder for "no version to report",
	// so it is no better than the sentinel it would replace.
	if Version == unsetVersion && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if Commit == unsetOther && setting.Value != "" {
				Commit = setting.Value
			}
		case "vcs.time":
			if BuildDate == unsetOther && setting.Value != "" {
				BuildDate = setting.Value
			}
		}
	}
}

// Info returns formatted version information
func Info() string {
	return fmt.Sprintf("afy version %s (%s) built on %s", Version, Commit, BuildDate)
}

// Short returns just the version number
func Short() string {
	return Version
}

// Full returns detailed version information
func Full() string {
	return fmt.Sprintf(`afy - Aetherfy CLI

Version:    %s
Commit:     %s
Built:      %s
Go version: %s
Platform:   %s/%s
`, Version, Commit, BuildDate, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
