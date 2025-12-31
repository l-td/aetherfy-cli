package version

import (
	"fmt"
	"runtime"
)

// Version information - set via ldflags during build
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

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
