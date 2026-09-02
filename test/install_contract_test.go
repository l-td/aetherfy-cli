package test

// Gates the asset-name contract between .goreleaser.yaml, scripts/install.sh
// and cmd/update.go (via internal/release).
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
// This test therefore reads the files and derives the same string from each.
// It deliberately does not pin any side to a hardcoded literal: one-sided pins
// are zero gates wearing the costume of several, since renaming the asset means
// editing every pin and the test never notices they used to agree.
//
// THE THIRD KNOWER. `afy upgrade` (cmd/update.go) downloads the same asset, so
// there are now three places that construct the name and still none that
// mention each other. The Go side is real code, so it is checked by CALLING it
// — a table over every published platform — rather than by scanning its source:
// a regex over Go would only prove a literal exists somewhere, not that the
// function returns it.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/release"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

// archiveExtension maps a goreleaser archive format to the suffix the published
// file carries. Unknown formats fail loudly rather than defaulting: silently
// guessing an extension would let the Go side and the release disagree in the
// one way this file exists to prevent.
var archiveExtension = map[string]string{
	"tar.gz": ".tar.gz",
	"tgz":    ".tgz",
	"tar":    ".tar",
	"gz":     ".gz",
	"zip":    ".zip",
	"binary": "",
}

// platform is one goos/goarch the release builds for.
type platform struct{ goos, goarch string }

func (p platform) String() string { return p.goos + "/" + p.goarch }

// releaseConfig is the slice of .goreleaser.yaml this file reasons about.
//
// Parsed as YAML rather than scanned with regexes, unlike the checks above:
// `format_overrides` and `ignore` are nested lists of maps, and a regex over
// those would be reading indentation and calling it structure.
type releaseConfig struct {
	ProjectName string `yaml:"project_name"`
	Builds      []struct {
		Goos   []string `yaml:"goos"`
		Goarch []string `yaml:"goarch"`
		Ignore []struct {
			Goos   string `yaml:"goos"`
			Goarch string `yaml:"goarch"`
		} `yaml:"ignore"`
	} `yaml:"builds"`
	Archives []struct {
		Formats         []string `yaml:"formats"`
		NameTemplate    string   `yaml:"name_template"`
		FormatOverrides []struct {
			Goos    string   `yaml:"goos"`
			Formats []string `yaml:"formats"`
		} `yaml:"format_overrides"`
	} `yaml:"archives"`
	Snapshot struct {
		VersionTemplate string `yaml:"version_template"`
	} `yaml:"snapshot"`
}

// parseReleaseConfig reads .goreleaser.yaml, insisting on the shape the rest of
// this file assumes. Every require here is anti-vacuity: a config that parsed to
// empty lists would make the comparisons below agree with themselves.
func parseReleaseConfig(t *testing.T) releaseConfig {
	t.Helper()
	var cfg releaseConfig
	require.NoError(t, yaml.Unmarshal([]byte(readSuggestionSource(t, ".goreleaser.yaml")), &cfg),
		".goreleaser.yaml does not parse as YAML")

	require.Len(t, cfg.Builds, 1, ".goreleaser.yaml: expected exactly one `builds` entry; teach this test the new shape")
	require.Len(t, cfg.Archives, 1, ".goreleaser.yaml: expected exactly one `archives` entry; teach this test the new shape")
	require.NotEmpty(t, cfg.Builds[0].Goos, ".goreleaser.yaml: builds.goos is empty — the scan is dead")
	require.NotEmpty(t, cfg.Builds[0].Goarch, ".goreleaser.yaml: builds.goarch is empty — the scan is dead")
	require.NotEmpty(t, cfg.Archives[0].Formats, ".goreleaser.yaml: archives.formats is empty — the scan is dead")
	require.NotEmpty(t, cfg.ProjectName, ".goreleaser.yaml: project_name is empty — the scan is dead")
	return cfg
}

// ignored is the goos/goarch pairs .goreleaser.yaml refuses to build.
func (c releaseConfig) ignored() map[string]bool {
	out := map[string]bool{}
	for _, ig := range c.Builds[0].Ignore {
		out[platform{ig.Goos, ig.Goarch}.String()] = true
	}
	return out
}

// publishedMatrix is goos x goarch minus the ignore list — exactly the set of
// platforms a release has an asset for.
func (c releaseConfig) publishedMatrix(t *testing.T) []platform {
	t.Helper()
	skip := c.ignored()
	var out []platform
	for _, goos := range c.Builds[0].Goos {
		for _, goarch := range c.Builds[0].Goarch {
			p := platform{goos, goarch}
			if !skip[p.String()] {
				out = append(out, p)
			}
		}
	}
	require.NotEmpty(t, out, ".goreleaser.yaml builds nothing at all — every comparison below would be vacuous")
	return out
}

// extensionFor is the suffix the published archive carries on goos, honouring
// archives.format_overrides.
func (c releaseConfig) extensionFor(t *testing.T, goos string) string {
	t.Helper()
	formats := c.Archives[0].Formats
	for _, override := range c.Archives[0].FormatOverrides {
		if override.Goos == goos {
			formats = override.Formats
			break
		}
	}
	require.Len(t, formats, 1,
		".goreleaser.yaml publishes %d archive formats for %s; this test compares one extension per platform", len(formats), goos)
	ext, known := archiveExtension[formats[0]]
	require.True(t, known,
		".goreleaser.yaml publishes archive format %q for %s, which this test cannot turn into a file "+
			"extension — teach it the new format or the comparison below is guessing", formats[0], goos)
	return ext
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

	// ---- the third side: what `afy upgrade` downloads ----
	//
	// A table over every platform the release publishes, comparing what
	// internal/release.AssetName RETURNS against the string derived from
	// .goreleaser.yaml above. This is also the only gate on the extension
	// split — install.sh is Linux/macOS only, so it never sees the zip that
	// archives.format_overrides publishes for Windows, and update.go does.
	spec := parseReleaseConfig(t)
	matrix := spec.publishedMatrix(t)

	for _, p := range matrix {
		expand := strings.NewReplacer(osPlaceholder, p.goos, archPlaceholder, p.goarch)
		ext := spec.extensionFor(t, p.goos)

		assert.Equal(t, ext, release.ArchiveExt(p.goos),
			"ARCHIVE FORMAT CONTRACT BROKEN for %s.\n"+
				"  .goreleaser.yaml publishes: %s  (archives.formats + format_overrides)\n"+
				"  internal/release builds:    %s\n"+
				"`afy upgrade` would download a URL that 404s, or hand the wrong extractor an archive it "+
				"cannot open.", p.goos, ext, release.ArchiveExt(p.goos))

		want := expand.Replace(published) + ext
		got, err := release.AssetName(p.goos, p.goarch)
		require.NoError(t, err,
			".goreleaser.yaml publishes an asset for %s, but internal/release.AssetName refuses it: %v.\n"+
				"`afy upgrade` would tell that platform's users no release exists for them.", p, err)

		assert.Equal(t, want, got,
			"ASSET-NAME CONTRACT BROKEN for %s.\n"+
				"  .goreleaser.yaml publishes: %s  (project_name %q + name_template %q)\n"+
				"  internal/release builds:    %s\n"+
				"Every `afy upgrade` on that platform would 404. Change both or neither.",
			p, want, projectName, nameTemplate, got)
	}
}

// The repository is the fourth string this batch made `afy upgrade` a knower of,
// and it was the one left ungated.
//
// scripts/install.sh's GITHUB_REPO is pinned (TestInstallScriptTargetsTheRealRepo)
// and .goreleaser.yaml's release.github is pinned (TestReleaseConfigTargetsTheRealRepo).
// internal/release.Repo had nothing, so if the repository ever moved, both of
// those would red and `afy upgrade` would keep quietly fetching from the old
// path — which, on GitHub, someone else can then claim.
func TestUpdateFetchesFromTheRepositoryThatExists(t *testing.T) {
	owner := strings.TrimPrefix(realRepoURL, "https://github.com/")
	require.NotEqual(t, realRepoURL, owner,
		"realRepoURL %q does not start with https://github.com/ — this derivation is dead", realRepoURL)

	assert.Equal(t, owner, release.Repo,
		"REPOSITORY CONTRACT BROKEN.\n"+
			"  the repository is:        %s  (git remote origin, per realRepoURL)\n"+
			"  internal/release fetches: %s\n"+
			"Every `afy upgrade` would download from the wrong repository.", owner, release.Repo)
}

// goreleaser's snapshot build is a local dry run of the release pipeline: it
// stamps a version, but no tag and no assets ever exist for it. It is neither a
// sentinel nor a pseudo-version, so `afy upgrade` treated it as a release build
// and would have tried to fetch a release that was never published.
//
// The suffix is read out of .goreleaser.yaml rather than written down again
// here — two copies of one string are zero gates.
func TestSnapshotBuildsAreNotTreatedAsReleases(t *testing.T) {
	spec := parseReleaseConfig(t)
	tmpl := spec.Snapshot.VersionTemplate
	require.NotEmpty(t, tmpl, ".goreleaser.yaml has no snapshot.version_template — the scan is dead")

	// "{{ incpatch .Version }}-dev" -> "-dev": the literal tail after the last
	// template expression is what actually lands in the binary.
	suffix := tmpl[strings.LastIndex(tmpl, "}}")+len("}}"):]
	require.NotEmpty(t, suffix,
		"snapshot.version_template %q ends in a template expression, so this test cannot derive "+
			"the literal suffix a snapshot build carries — teach it the new shape", tmpl)
	require.NotContains(t, suffix, "{{",
		"derived snapshot suffix %q is still a template — the extraction is wrong", suffix)

	snapshot := "0.1.1" + suffix
	assert.False(t, release.IsReleaseBuild(snapshot),
		"SNAPSHOT CONTRACT BROKEN.\n"+
			"  .goreleaser.yaml stamps snapshot builds as: %s (snapshot.version_template %q)\n"+
			"  internal/release calls that a release build.\n"+
			"`afy upgrade` would try to download a release that was never published.", snapshot, tmpl)

	// The line the rule must not cross. A pre-release tag IS published and
	// downloadable; refusing it would break every rc.
	assert.True(t, release.IsReleaseBuild("v0.2.0-rc.1"),
		"a real pre-release tag is a release — the snapshot rule has over-reached and would "+
			"refuse to update to or from every rc")
}

// The platforms `afy upgrade` will build a URL for must be exactly the ones the
// release publishes an asset for.
//
// Both directions matter and they fail differently. A platform in
// .goreleaser.yaml but not in internal/release means `afy upgrade` tells real
// users no release exists for them. A platform in internal/release but not in
// .goreleaser.yaml — windows/arm64 is in goreleaser's `ignore` list today —
// means it constructs a URL that 404s, and a 404 says nothing about why.
func TestUpdateKnowsExactlyThePlatformsTheReleasePublishes(t *testing.T) {
	spec := parseReleaseConfig(t)

	var want []string
	for _, p := range spec.publishedMatrix(t) {
		want = append(want, p.String())
	}

	assert.ElementsMatch(t, want, release.SupportedPlatforms(),
		"PLATFORM CONTRACT BROKEN.\n"+
			"  .goreleaser.yaml publishes: %v  (builds.goos x builds.goarch minus builds.ignore)\n"+
			"  internal/release supports:  %v\n"+
			"Set these to the same list.", want, release.SupportedPlatforms())

	// And each ignored combination must be refused by name, not by 404.
	for ignored := range spec.ignored() {
		parts := strings.SplitN(ignored, "/", 2)
		require.Len(t, parts, 2, "malformed ignore entry %q in .goreleaser.yaml", ignored)

		asset, err := release.AssetName(parts[0], parts[1])
		assert.Empty(t, asset, "%s is in builds.ignore, so no asset for it is ever published", ignored)
		require.Error(t, err, "`afy upgrade` on %s must say so, not build a URL that 404s", ignored)
		assert.Contains(t, err.Error(), ignored, "the refusal must name the platform: %v", err)
	}
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

	// cmd/update.go fetches it too, and verifies before extracting for the same
	// reason. A third knower gets gated here rather than left to rot.
	assert.Equal(t, published, release.ChecksumsFile,
		"CHECKSUM FILE CONTRACT BROKEN.\n"+
			"  .goreleaser.yaml publishes: %s\n"+
			"  internal/release fetches:   %s\n"+
			"`afy upgrade` fails closed when it cannot read the checksums, so a wrong name blocks every update.",
		published, release.ChecksumsFile)
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

// The FOURTH knower: scripts/install.ps1.
//
// install.sh is Linux/macOS only, so the Windows half of the asset contract had
// exactly one consumer (internal/release, via `afy upgrade`) until install.ps1
// existed. Now a PowerShell script builds the same string a fourth way, and an
// ungated fourth copy is the drift this whole gate exists to prevent — with the
// extra sharpness that NOTHING ELSE reads the .zip path: install.sh cannot see
// it, and a Linux CI runner cannot execute install.ps1 to find out.
//
// Derived from the same .goreleaser.yaml the other three are checked against,
// never from a hardcoded literal, so a coordinated rename still reds in
// TestReleaseAssetNameIsThePublishedContract rather than passing here.
func TestWindowsInstallScriptAgreesOnTheAssetName(t *testing.T) {
	cfg := dropComments(readSuggestionSource(t, ".goreleaser.yaml"))
	ps := readSuggestionSource(t, "scripts/install.ps1")

	projectName := mustMatch(t, cfg, ".goreleaser.yaml project_name",
		regexp.MustCompile(`(?m)^project_name:\s*(\S+)\s*$`))

	// install.ps1 builds "$BinaryName-$platform.zip" from $BinaryName and a
	// $platform of "windows-$goarch". Read all three rather than the joined
	// result, so a change to any part is visible here.
	binaryName := mustMatch(t, ps, "install.ps1 $BinaryName",
		regexp.MustCompile(`(?m)^\s*\$BinaryName\s*=\s*'([^']+)'`))
	platformExpr := mustMatch(t, ps, "install.ps1 $platform",
		regexp.MustCompile(`(?m)^\s*\$platform\s*=\s*"([^"]+)"`))
	assetExpr := mustMatch(t, ps, "install.ps1 $asset",
		regexp.MustCompile(`(?m)^\s*\$asset\s*=\s*"([^"]+)"`))
	goarch := mustMatch(t, ps, "install.ps1 amd64 switch arm",
		regexp.MustCompile(`(?m)^\s*'AMD64'\s*\{\s*\$goarch\s*=\s*'([^']+)'`))

	expand := strings.NewReplacer(
		"$goarch", goarch,
		"$BinaryName", binaryName,
		"$platform", strings.NewReplacer("$goarch", goarch).Replace(platformExpr),
	)
	downloaded := expand.Replace(assetExpr)
	require.NotContains(t, downloaded, "$",
		"install.ps1 $asset=%q still holds an unexpanded variable after substitution (%q) — "+
			"teach this test the new variable or the gate is comparing PowerShell syntax.", assetExpr, downloaded)

	// windows/amd64 is the only Windows platform .goreleaser.yaml publishes;
	// its `ignore` list drops windows/arm64, which is why install.ps1 refuses
	// every arch but AMD64 instead of building a URL that 404s.
	want := projectName + "-windows-amd64" + release.ArchiveExt("windows")

	assert.Equal(t, want, downloaded,
		"WINDOWS ASSET-NAME CONTRACT BROKEN.\n"+
			"  .goreleaser.yaml publishes: %s\n"+
			"  scripts/install.ps1 fetches: %s\n"+
			"Every `irm ... | iex` install would 404, and no other test covers this path:\n"+
			"install.sh is Unix-only and CI cannot run PowerShell against a real release.",
		want, downloaded)

	// The Windows binary carries .exe INSIDE the archive; install.ps1 must look
	// for the name goreleaser actually writes, not the bare one install.sh uses.
	binaryFile := mustMatch(t, ps, "install.ps1 $BinaryFile",
		regexp.MustCompile(`(?m)^\s*\$BinaryFile\s*=\s*'([^']+)'`))
	assert.Equal(t, release.BinaryFileName("windows"), binaryFile,
		"install.ps1 looks for %q inside the archive but goreleaser writes %q — "+
			"the download and checksum would both pass and the install would then fail.",
		binaryFile, release.BinaryFileName("windows"))

	// Same repo as every other side of the contract.
	repo := mustMatch(t, ps, "install.ps1 $GitHubRepo",
		regexp.MustCompile(`(?m)^\s*\$GitHubRepo\s*=\s*'([^']+)'`))
	assert.Equal(t, release.Repo, repo,
		"install.ps1 downloads from %q but the release publishes to %q", repo, release.Repo)

	// checksums.txt is verified before extracting; a wrong name fails closed,
	// so this would block every Windows install rather than weakening one.
	checksums := mustMatch(t, ps, "install.ps1 $ChecksumFile",
		regexp.MustCompile(`(?m)^\s*\$ChecksumFile\s*=\s*'([^']+)'`))
	assert.Equal(t, release.ChecksumsFile, checksums,
		"install.ps1 fetches %q but the release publishes %q", checksums, release.ChecksumsFile)
}

// install.ps1 must stay pure ASCII.
//
// Not style. Windows PowerShell 5.1 reads a BOM-less file as ANSI, so a UTF-8
// em-dash decodes to three characters — one of which is a double quote. That
// closes the surrounding string early and the script dies with a parse error
// naming a token nowhere near the real cause. It happened while writing this
// file: eight em-dashes produced twelve parse errors, none of them at the
// em-dash. The file is served over HTTP to `iex`, so no reader can be assumed.
func TestWindowsInstallScriptIsAscii(t *testing.T) {
	ps := readSuggestionSource(t, "scripts/install.ps1")
	require.NotEmpty(t, ps, "scripts/install.ps1 is empty — the scan is dead")

	for i, line := range strings.Split(ps, "\n") {
		for _, r := range line {
			if r > 127 {
				assert.Fail(t, "scripts/install.ps1 contains a non-ASCII character",
					"line %d contains %q (U+%04X).\n\n"+
						"Windows PowerShell 5.1 reads a BOM-less file as ANSI, so this decodes to "+
						"mojibake — and if the replacement contains a quote it terminates the "+
						"enclosing string and the script fails to parse, pointing at the wrong line. "+
						"Use an ASCII equivalent (-- for an em-dash).", i+1, r, r)
				return
			}
		}
	}
}
