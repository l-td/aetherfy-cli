// Package release knows how the published release artifacts are named, where
// they live, and how to put one in place of the running binary. `afy update`
// (cmd/update.go) is its only consumer.
//
// It is a package rather than a handful of helpers inside cmd/ for one reason:
// the asset name is a contract between three files that never mention each
// other — .goreleaser.yaml publishes it, scripts/install.sh downloads it, and
// this constructs it. test/install_contract_test.go gates all three against
// each other, and it can only do that if the Go side has ONE exported source
// to call instead of a literal per call site.
package release

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/l-td/aetherfy-cli/pkg/version"
)

const (
	// BinaryName is .goreleaser.yaml's `project_name` and scripts/install.sh's
	// BINARY_NAME. All three spellings are pair-gated by
	// TestInstallScriptAndReleaseConfigAgreeOnTheAssetName.
	BinaryName = "afy"

	// Repo is the GitHub repository releases are published to — the same one
	// scripts/install.sh puts in GITHUB_REPO.
	Repo = "l-td/aetherfy-cli"

	// ChecksumsFile is .goreleaser.yaml's `checksum.name_template`.
	ChecksumsFile = "checksums.txt"

	gitHubBase = "https://github.com"
)

// supportedPlatforms is .goreleaser.yaml's builds matrix (goos x goarch) with
// its `ignore` list removed. windows/arm64 is ignored there, so no asset is
// ever published for it; asking for one would build a URL that 404s with
// nothing in the message to explain why.
//
// TestUpdateRefusesThePlatformsTheReleaseIgnores derives the same set from
// .goreleaser.yaml and fails if these two ever disagree.
var supportedPlatforms = map[string]bool{
	"linux/amd64":   true,
	"linux/arm64":   true,
	"darwin/amd64":  true,
	"darwin/arm64":  true,
	"windows/amd64": true,
}

// UnsupportedPlatformError says which platform has no published asset, and
// which ones do. A raw 404 from a constructed URL says neither.
type UnsupportedPlatformError struct {
	OS   string
	Arch string
}

func (e *UnsupportedPlatformError) Error() string {
	return fmt.Sprintf("no release is published for %s/%s; releases exist for %s. "+
		"Build from source instead: go install github.com/%s/cmd/%s@latest",
		e.OS, e.Arch, strings.Join(SupportedPlatforms(), ", "), Repo, BinaryName)
}

// SupportedPlatforms lists the "<goos>/<goarch>" pairs a release publishes an
// asset for, sorted so error messages are stable.
func SupportedPlatforms() []string {
	out := make([]string, 0, len(supportedPlatforms))
	for p := range supportedPlatforms {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ArchiveExt is the archive extension a release publishes for goos: tar.gz
// everywhere, zip on Windows. That split is .goreleaser.yaml's `archives.formats`
// plus its `format_overrides`. scripts/install.sh is Linux/macOS only, so it
// never sees the zip and could never have gated this half of the contract.
func ArchiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// BinaryFileName is the name the binary carries INSIDE the archive. goreleaser
// appends .exe on Windows; scripts/install.sh looks for the bare name because
// it never runs there.
func BinaryFileName(goos string) string {
	if goos == "windows" {
		return BinaryName + ".exe"
	}
	return BinaryName
}

// AssetName is the release asset for goos/goarch, e.g. afy-linux-amd64.tar.gz.
func AssetName(goos, goarch string) (string, error) {
	if !supportedPlatforms[goos+"/"+goarch] {
		return "", &UnsupportedPlatformError{OS: goos, Arch: goarch}
	}
	return BinaryName + "-" + goos + "-" + goarch + ArchiveExt(goos), nil
}

// CurrentAssetName is AssetName for the platform this binary is running on.
func CurrentAssetName() (string, error) {
	return AssetName(runtime.GOOS, runtime.GOARCH)
}

// LatestURL is the page whose redirect names the newest published tag.
//
// Deliberately not api.github.com. The anonymous API allows 60 requests per
// hour per IP and everyone behind one corporate NAT shares that budget, so
// `afy update` would start failing for a reason that has nothing to do with
// the user or the release. This endpoint costs no token, no JSON parse and no
// rate limit. scripts/install.sh's resolve_release() takes the same route for
// the same reason; the two comments describe one decision.
func LatestURL() string {
	return gitHubBase + "/" + Repo + "/releases/latest"
}

// DownloadPrefix is the URL prefix every asset of tag hangs off — the same
// shape scripts/install.sh pins in URL_PREFIX.
func DownloadPrefix(tag string) string {
	return gitHubBase + "/" + Repo + "/releases/download/" + tag
}

// UserAgent identifies this CLI to GitHub.
//
// Never send Go's default ("Go-http-client/1.1"): edges in front of download
// endpoints block or challenge default agent strings, and the 403 that
// produces names nothing anyone could act on.
func UserAgent() string {
	return BinaryName + "/" + version.Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
