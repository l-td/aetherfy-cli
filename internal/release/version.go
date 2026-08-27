package release

import (
	"regexp"
	"strings"

	"github.com/l-td/aetherfy-cli/pkg/version"
)

// snapshotSuffix is what .goreleaser.yaml's snapshot.version_template appends
// ("{{ incpatch .Version }}-dev" -> 0.1.1-dev). A snapshot is a local dry run
// of the release pipeline, not a published one: no tag, no assets, nothing to
// download. Without this it read as a release build, because it is neither a
// sentinel nor a pseudo-version.
//
// It matches on the SUFFIX and nothing else, so a genuine pre-release tag like
// v0.2.0-rc.1 stays a release — those are published, downloadable, and updating
// to or from one is legitimate.
// TestSnapshotBuildsAreNotReleaseBuilds derives this string from
// .goreleaser.yaml rather than trusting the copy here.
const snapshotSuffix = "-dev"

// pseudoVersion matches the tail every Go module pseudo-version carries — a
// 14-digit UTC timestamp and a 12-character revision. All three shapes end that
// way, and only the separator in front of the timestamp differs:
//
//	v0.0.0-20260825102917-6be2d5e0890b              (no prior tag)
//	v0.1.1-0.20260825102917-6be2d5e0890b            (after a tag)
//	v0.2.0-rc.1.0.20260825102917-6be2d5e0890b       (after a pre-release tag)
//
// Hence [-.] and not just -: pinning the "v0.0.0-" prefix alone, or requiring a
// hyphen, lets the last two through and overwrites the build they name.
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}`)

// IsReleaseBuild reports whether v could only have come from a published
// release archive.
//
// A sentinel means `go build` with no ldflags. A pseudo-version means the
// toolchain stamped it from a working tree — `go install ...@latest`,
// `make install`, or a plain `go build` since Go 1.24. Neither was unpacked
// from a release, and replacing either with one silently discards the build
// the user has.
func IsReleaseBuild(v string) bool {
	v = strings.TrimSpace(v)
	// version.Unset is the ONE definition of "no version". Re-spelling "dev"
	// here would mean a rename in pkg/version silently stops matching, and this
	// function would then wave through the very builds it exists to protect.
	if version.Unset(v) {
		return false
	}
	if strings.HasSuffix(v, snapshotSuffix) {
		return false
	}
	return !pseudoVersion.MatchString(v)
}

// MustRefuseUpdate is the whole refuse-a-source-build rule, in one place:
// `afy update` replaces the running binary, and doing that to a build nobody
// can re-download destroys it. --force is the only override.
//
// It exists as a function rather than as `!force && !IsReleaseBuild(v)` inline
// in a cobra RunE because that expression is unreachable from any test — and
// getting the --force half backwards would refuse every legitimate update, or
// worse, silently overwrite every source build.
func MustRefuseUpdate(v string, force bool) bool {
	return !force && !IsReleaseBuild(v)
}

// InstalledFrom names the way a non-release binary was most likely produced,
// so the refusal can tell the user how to update the build they actually have
// instead of only saying no.
func InstalledFrom(v string) string {
	if pseudoVersion.MatchString(strings.TrimSpace(v)) {
		return "a Go module pseudo-version, which is what `go install`, `make install` " +
			"and `go build` stamp into a binary built from a working tree"
	}
	if strings.HasSuffix(strings.TrimSpace(v), snapshotSuffix) {
		return "a goreleaser snapshot version, which is a local dry run of the release " +
			"pipeline — no such release was ever published"
	}
	return "the unstamped-build sentinel, which is what `go build` produces when the " +
		"toolchain has no version to embed"
}

// NormalizeTag returns v in the shape release tags actually use (vX.Y.Z).
//
// Users type both "0.1.0" and "v0.1.0"; scripts/install.sh makes the same
// allowance with `VERSION="${VERSION#v}"` and puts the v back when it builds
// the URL. An empty string stays empty — it means "no version was requested",
// not "version v".
func NormalizeTag(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return "v" + strings.TrimPrefix(v, "v")
}
