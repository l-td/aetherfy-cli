package version

import (
	"runtime/debug"
	"testing"
)

// THE FALLBACK'S RULES, independent of how this checkout was made. Whether a
// real `go build` gets a stamp to fall back on depends on the toolchain and the
// checkout (test/version_stamp_contract_test.go covers the real binary); what
// the fallback does with a stamp, or without one, does not.

func withSentinels(t *testing.T) {
	t.Helper()
	v, c, d := Version, Commit, BuildDate
	Version, Commit, BuildDate = UnsetVersion, unsetOther, unsetOther
	t.Cleanup(func() { Version, Commit, BuildDate = v, c, d })
}

func stamped(version string) *debug.BuildInfo {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "58763e3c7dff1b56ca2623314fee9b939b0e6c96"},
		{Key: "vcs.time", Value: "2026-09-16T15:57:29Z"},
	}}
	info.Main.Version = version
	return info
}

func TestAnUnstampedBuildTakesTheVersionTheToolchainEmbedded(t *testing.T) {
	withSentinels(t)
	fillFromBuildInfo(stamped("v0.1.2-0.20260916155729-58763e3c7dff"), true)

	if Version != "v0.1.2-0.20260916155729-58763e3c7dff" {
		t.Fatalf("Version = %q, want the embedded pseudo-version", Version)
	}
	if Commit != "58763e3c7dff1b56ca2623314fee9b939b0e6c96" {
		t.Fatalf("Commit = %q, want vcs.revision", Commit)
	}
	if BuildDate != "2026-09-16T15:57:29Z" {
		t.Fatalf("BuildDate = %q, want vcs.time", BuildDate)
	}
}

func TestNoEmbeddedVersionLeavesTheSentinels(t *testing.T) {
	for name, info := range map[string]*debug.BuildInfo{
		"devel placeholder": stamped("(devel)"),
		"empty version":     {},
	} {
		t.Run(name, func(t *testing.T) {
			withSentinels(t)
			fillFromBuildInfo(info, true)
			if Version != UnsetVersion {
				t.Fatalf("Version = %q; a build with nothing embedded must keep %q", Version, UnsetVersion)
			}
		})
	}
	t.Run("no build info at all", func(t *testing.T) {
		withSentinels(t)
		fillFromBuildInfo(nil, false)
		if Version != UnsetVersion || Commit != unsetOther || BuildDate != unsetOther {
			t.Fatalf("sentinels changed with no build info: %q %q %q", Version, Commit, BuildDate)
		}
	})
}

func TestAnLdflagsStampWinsOverTheEmbeddedVersion(t *testing.T) {
	withSentinels(t)
	Version, Commit, BuildDate = "v1.2.3", "abc1234", "2026-01-01"
	fillFromBuildInfo(stamped("v0.1.2-0.20260916155729-58763e3c7dff"), true)

	if Version != "v1.2.3" || Commit != "abc1234" || BuildDate != "2026-01-01" {
		t.Fatalf("the fallback overwrote a release stamp: %q %q %q", Version, Commit, BuildDate)
	}
}
