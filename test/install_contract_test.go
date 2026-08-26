package test

// Pair-gates the asset-name contract between .goreleaser.yaml and
// scripts/install.sh.
//
// These two files agree on a string that neither one states: the release
// publishes `<project_name>-<os>-<arch>.tar.gz` and the installer downloads
// `${BINARY_NAME}-${PLATFORM}.tar.gz`. Nothing connected them. `goreleaser
// check` validates the config in isolation and passes either way, and the
// installer's own guard (TestInstallScriptTargetsTheRealRepo) only pins the
// repository, not the filename. So a rename on one side ships green and every
// `curl | bash` 404s — discovered by a user, at the worst possible moment.
//
// A comment saying "never rename this" was the previous protection. Comments
// are claims, not gates.
//
// This test therefore reads BOTH files and derives the same string from each.
// It deliberately does not pin either side to a hardcoded literal: two
// one-sided pins are zero gates wearing the costume of two, since renaming the
// asset means editing both pins and the test never notices they used to agree.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stand-ins for the per-build values neither file knows at authoring time.
// Both sides must reduce to the same string once these are substituted.
const (
	osPlaceholder   = "<os>"
	archPlaceholder = "<arch>"
)

// mustMatch returns capture group 1, failing loudly when the pattern finds
// nothing. Every extraction below goes through it: an extraction that silently
// found nothing would leave the comparisons agreeing with themselves, which is
// the one way this test could be green while the contract is broken.
func mustMatch(t *testing.T, body, what string, re *regexp.Regexp) string {
	t.Helper()
	m := re.FindStringSubmatch(body)
	require.NotNil(t, m, "%s: %s matched nothing — the scan is dead and this test asserts nothing", what, re)
	return strings.TrimSpace(m[1])
}

// dropComments removes whole-line comments so the commented-out homebrew cask
// block in .goreleaser.yaml, and install.sh's header prose, stay inert. Only
// leading `#` counts, so `${VERSION#v}` survives.
func dropComments(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// yamlSection returns one top-level block, from `key:` to the next line that
// starts at column 0. Scoping matters: .goreleaser.yaml holds three different
// `name_template:` keys (archives, checksum, release), and a file-wide regex
// would silently pick whichever the file happens to list first.
func yamlSection(t *testing.T, cfg, key string) string {
	t.Helper()
	var (
		kept    []string
		inBlock bool
	)
	for _, line := range strings.Split(cfg, "\n") {
		if !inBlock {
			if strings.TrimRight(line, " \t") == key+":" {
				inBlock = true
			}
			continue
		}
		// A non-blank line at column 0 ends the block.
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break
		}
		kept = append(kept, line)
	}
	require.True(t, inBlock, ".goreleaser.yaml: no top-level `%s:` block — the scan is dead", key)
	body := strings.Join(kept, "\n")
	require.NotEmpty(t, strings.TrimSpace(body), ".goreleaser.yaml: `%s:` block is empty — the scan is dead", key)
	return body
}

// expandShell substitutes ${NAME} for the values given.
func expandShell(expr string, vars map[string]string) string {
	for name, value := range vars {
		expr = strings.ReplaceAll(expr, "${"+name+"}", value)
	}
	return expr
}

// templateField matches a goreleaser template field, e.g. {{ .ProjectName }}.
func templateField(field string) *regexp.Regexp {
	return regexp.MustCompile(`\{\{\s*\.` + field + `\s*\}\}`)
}

func TestInstallScriptAndReleaseConfigAgreeOnTheAssetName(t *testing.T) {
	cfg := dropComments(readSuggestionSource(t, ".goreleaser.yaml"))
	script := dropComments(readSuggestionSource(t, "scripts/install.sh"))
	archives := yamlSection(t, cfg, "archives")

	// ---- what the release publishes ----

	projectName := mustMatch(t, cfg, ".goreleaser.yaml project_name",
		regexp.MustCompile(`(?m)^project_name:\s*(\S+)\s*$`))
	nameTemplate := mustMatch(t, archives, ".goreleaser.yaml archives.name_template",
		regexp.MustCompile(`(?m)^\s*name_template:\s*"([^"]+)"\s*$`))

	published := templateField("ProjectName").ReplaceAllString(nameTemplate, projectName)
	published = templateField("Os").ReplaceAllString(published, osPlaceholder)
	published = templateField("Arch").ReplaceAllString(published, archPlaceholder)
	require.NotContains(t, published, "{{",
		"archives.name_template %q uses a field this test does not know how to expand (%q). "+
			"install.sh builds a fixed shape and cannot follow it — teach this test the new field "+
			"or the pair gate is silently comparing braces.", nameTemplate, published)

	// install.sh deals only in tar.gz; if the archive format ever changes, the
	// `.tar.gz` it appends becomes a lie.
	assert.Regexp(t, `(?m)^\s*-\s*tar\.gz\s*$`, archives,
		"archives.formats no longer lists tar.gz, but scripts/install.sh downloads and untars one")

	// install.sh expects ./afy at the archive ROOT (`[ ! -f \"./afy\" ]`).
	// goreleaser defaults wrap_in_directory to false; an explicit true would
	// break that check with "binary not found in archive".
	if m := regexp.MustCompile(`(?m)^\s*wrap_in_directory:\s*(\S+)`).FindStringSubmatch(archives); m != nil {
		assert.Equal(t, "false", strings.Trim(m[1], `"'`),
			"archives.wrap_in_directory is enabled, but scripts/install.sh looks for ./%s at the archive root", projectName)
	}

	// ---- what the installer downloads ----

	binaryName := mustMatch(t, script, "install.sh BINARY_NAME",
		regexp.MustCompile(`(?m)^BINARY_NAME="([^"]+)"`))
	platform := mustMatch(t, script, "install.sh PLATFORM",
		regexp.MustCompile(`(?m)^\s*PLATFORM="([^"]+)"`))
	assetExpr := mustMatch(t, script, "install.sh ASSET",
		regexp.MustCompile(`(?m)^\s*ASSET="([^"]+)"`))

	platform = expandShell(platform, map[string]string{"OS": osPlaceholder, "ARCH": archPlaceholder})
	downloaded := expandShell(assetExpr, map[string]string{"BINARY_NAME": binaryName, "PLATFORM": platform})
	require.NotContains(t, downloaded, "$",
		"install.sh ASSET=%q still holds an unexpanded variable after substitution (%q) — "+
			"teach this test the new variable or the pair gate is comparing shell syntax.", assetExpr, downloaded)

	// ---- the gate ----

	assert.Equal(t, published+".tar.gz", downloaded,
		"ASSET-NAME CONTRACT BROKEN.\n"+
			"  .goreleaser.yaml publishes: %s.tar.gz  (project_name %q + name_template %q)\n"+
			"  scripts/install.sh fetches: %s\n"+
			"Every install would 404. Change both files or neither.",
		published, projectName, nameTemplate, downloaded)
}

func TestInstallScriptAndReleaseConfigAgreeOnTheChecksumFile(t *testing.T) {
	cfg := dropComments(readSuggestionSource(t, ".goreleaser.yaml"))
	script := dropComments(readSuggestionSource(t, "scripts/install.sh"))

	published := mustMatch(t, yamlSection(t, cfg, "checksum"), ".goreleaser.yaml checksum.name_template",
		regexp.MustCompile(`(?m)^\s*name_template:\s*['"]([^'"]+)['"]\s*$`))
	// Anchored to the download call: `DOWNLOAD_URL="${URL_PREFIX}/${ASSET}"`
	// also starts with ${URL_PREFIX}/ and is the archive, not the checksums.
	downloaded := mustMatch(t, script, "install.sh checksum download",
		regexp.MustCompile(`download\s+"\$\{URL_PREFIX\}/([^"]+)"`))
	require.NotContains(t, downloaded, "$",
		"install.sh now builds the checksum filename from a variable (%q); this test compares literals — teach it the new shape", downloaded)

	assert.Equal(t, published, downloaded,
		"CHECKSUM FILE CONTRACT BROKEN.\n"+
			"  .goreleaser.yaml publishes: %s\n"+
			"  scripts/install.sh fetches: %s\n"+
			"install.sh verifies before extracting and fails closed, so a wrong name blocks every install.",
		published, downloaded)
}

func TestPinnedInstallURLMatchesTheReleaseTagShape(t *testing.T) {
	workflow := readSuggestionSource(t, ".github/workflows/release.yml")
	script := dropComments(readSuggestionSource(t, "scripts/install.sh"))

	// The tag filter that decides which pushes cut a release, e.g. 'v*'.
	tagFilter := mustMatch(t, workflow, "release.yml push tag filter",
		regexp.MustCompile(`tags:\s*\n\s*-\s*'([^']+)'`))
	prefix := strings.TrimSuffix(tagFilter, "*")
	require.NotEmpty(t, prefix,
		"release.yml tag filter %q has no literal prefix — AETHERFY_VERSION cannot be turned into a tag", tagFilter)

	pinned := mustMatch(t, script, "install.sh pinned URL_PREFIX",
		regexp.MustCompile(`(?m)^\s*URL_PREFIX="([^"]*releases/download[^"]*)"`))

	assert.Contains(t, pinned, "/"+prefix+"${VERSION}",
		"TAG SHAPE CONTRACT BROKEN.\n"+
			"  release.yml releases tags matching: %s\n"+
			"  scripts/install.sh pins:            %s\n"+
			"AETHERFY_VERSION=0.1.0 must resolve to the tag the release workflow actually published.",
		tagFilter, pinned)
}

// The published-contract freeze. The pair gate above proves the two files
// AGREE — it is satisfied by a coordinated rename of both sides, and before
// the first release such a rename is legal. After it, the name is a published
// contract: a version-pinned install (AETHERFY_VERSION=0.1.0) builds the old
// URL shape against an already-shipped release, and a rename 404s every one
// of them. The pair gate structurally cannot catch a coordinated rename; this
// pin is the freeze that does.
func TestReleaseAssetNameIsThePublishedContract(t *testing.T) {
	cfg := dropComments(readSuggestionSource(t, ".goreleaser.yaml"))

	projectName := mustMatch(t, cfg, ".goreleaser.yaml project_name",
		regexp.MustCompile(`(?m)^project_name:\s*(\S+)\s*$`))

	assert.Equal(t, "afy", projectName,
		"project_name is the published asset-name contract (afy-<os>-<arch>.tar.gz). "+
			"If no release has EVER shipped, a rename is still legal — update this pin in "+
			"the same commit, deliberately. If one has, do not rename: every version-pinned "+
			"install of an already-published release would 404.")
}
