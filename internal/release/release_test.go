package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// (a) refusing a build that did not come from a release
// ---------------------------------------------------------------------------

// `afy update` replaces the running binary. Doing that to a `go install` or
// `make install` build destroys something nobody can re-download, so the rule
// is: refuse unless --force. Getting the --force half backwards is the failure
// that matters — it would silently overwrite every source build.
func TestUpdateRefusesBuildsThatDidNotComeFromARelease(t *testing.T) {
	cases := []struct {
		name         string
		version      string
		wantRefusal  bool
		wantSourceOf string // a fragment InstalledFrom must name
	}{
		{
			name:         "unstamped go build",
			version:      "dev",
			wantRefusal:  true,
			wantSourceOf: "go build",
		},
		{
			name:         "module pseudo-version from go install",
			version:      "v0.0.0-20260825102917-6be2d5e0890b",
			wantRefusal:  true,
			wantSourceOf: "pseudo-version",
		},
		{
			name:         "pseudo-version with a dirty working tree",
			version:      "v0.0.0-20260825102917-6be2d5e0890b+dirty",
			wantRefusal:  true,
			wantSourceOf: "pseudo-version",
		},
		{
			// The other two pseudo-version shapes: one built after a tag, one
			// after a pre-release tag. Pinning "v0.0.0-" alone would let both
			// through and overwrite the build.
			name:        "pseudo-version built on top of an existing tag",
			version:     "v0.1.1-0.20260825102917-6be2d5e0890b",
			wantRefusal: true,
		},
		{
			name:        "pseudo-version built on top of a pre-release tag",
			version:     "v0.2.0-rc.1.0.20260825102917-6be2d5e0890b",
			wantRefusal: true,
		},
		{
			name:        "a real release tag",
			version:     "v0.1.0",
			wantRefusal: false,
		},
		{
			name:        "a real pre-release tag",
			version:     "v0.2.0-rc.1",
			wantRefusal: false,
		},
	}
	require.NotEmpty(t, cases, "the table is empty — this test asserts nothing")

	sawRefuse, sawProceed := false, false
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantRefusal, MustRefuseUpdate(tc.version, false),
				"MustRefuseUpdate(%q, force=false)", tc.version)

			// --force flips every refusal and changes nothing else.
			assert.False(t, MustRefuseUpdate(tc.version, true),
				"--force must let %q through; it is the documented override and the only one", tc.version)

			if tc.wantSourceOf != "" {
				assert.Contains(t, InstalledFrom(tc.version), tc.wantSourceOf,
					"the refusal has to tell the user how they installed %q, or it is just a no", tc.version)
			}
		})
		if tc.wantRefusal {
			sawRefuse = true
		} else {
			sawProceed = true
		}
	}

	// Discrimination check: a table of only-refuse cases would pass against an
	// IsReleaseBuild that returns false for everything, and only-proceed cases
	// against one that returns true for everything.
	assert.True(t, sawRefuse && sawProceed,
		"the table must contain both refused and permitted versions, or it cannot tell a working "+
			"rule from a constant (refuse=%v proceed=%v)", sawRefuse, sawProceed)
}

func TestNormalizeTagAcceptsTheVersionWithOrWithoutTheV(t *testing.T) {
	assert.Equal(t, "v0.1.0", NormalizeTag("0.1.0"))
	assert.Equal(t, "v0.1.0", NormalizeTag("v0.1.0"))
	assert.Equal(t, NormalizeTag("0.1.0"), NormalizeTag("v0.1.0"),
		"scripts/install.sh accepts both spellings of AETHERFY_VERSION; --version must resolve them identically")
	assert.Equal(t, "v0.1.0", NormalizeTag("  v0.1.0 "))

	// Empty means "no version was requested", never "version v".
	assert.Equal(t, "", NormalizeTag(""))
	assert.Equal(t, "", NormalizeTag("   "))
}

// ---------------------------------------------------------------------------
// the asset name
// ---------------------------------------------------------------------------

func TestAssetNameCoversEveryPublishedPlatform(t *testing.T) {
	cases := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "afy-linux-amd64.tar.gz"},
		{"linux", "arm64", "afy-linux-arm64.tar.gz"},
		{"darwin", "amd64", "afy-darwin-amd64.tar.gz"},
		{"darwin", "arm64", "afy-darwin-arm64.tar.gz"},
		{"windows", "amd64", "afy-windows-amd64.zip"},
	}
	require.NotEmpty(t, cases, "the table is empty — this test asserts nothing")

	for _, tc := range cases {
		got, err := AssetName(tc.goos, tc.goarch)
		require.NoError(t, err, "%s/%s is published; AssetName must build it", tc.goos, tc.goarch)
		assert.Equal(t, tc.want, got)
	}
}

// windows/arm64 is in .goreleaser.yaml's builds.ignore list, so no asset for it
// is ever published. Constructing the URL anyway would 404 with nothing in the
// message to explain why.
func TestAssetNameRefusesAPlatformWithNoPublishedAsset(t *testing.T) {
	got, err := AssetName("windows", "arm64")
	assert.Empty(t, got, "a refused platform must not also hand back a URL fragment to try")

	var unsupported *UnsupportedPlatformError
	require.ErrorAs(t, err, &unsupported, "callers distinguish this from a network failure")
	assert.Contains(t, err.Error(), "windows/arm64", "the message must name the platform asked for")
	assert.Contains(t, err.Error(), "linux/amd64", "and the platforms that do have one")
}

// ---------------------------------------------------------------------------
// (e) the checksum matcher
// ---------------------------------------------------------------------------

func sha256Of(t *testing.T, b []byte) string {
	t.Helper()
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestChecksumVerification(t *testing.T) {
	dir := t.TempDir()
	body := []byte("pretend this is afy-linux-amd64.tar.gz\n")
	archive := filepath.Join(dir, "afy-linux-amd64.tar.gz")
	require.NoError(t, os.WriteFile(archive, body, 0o644))

	const wrongHash = "0000000000000000000000000000000000000000000000000000000000000000"
	good := []byte(sha256Of(t, body) + "  afy-linux-amd64.tar.gz\n" +
		wrongHash + "  afy-darwin-arm64.tar.gz\n")

	t.Run("the right hash passes", func(t *testing.T) {
		assert.NoError(t, VerifyFile(archive, "afy-linux-amd64.tar.gz", good))
	})

	t.Run("a wrong hash fails", func(t *testing.T) {
		bad := []byte(wrongHash + "  afy-linux-amd64.tar.gz\n")
		err := VerifyFile(archive, "afy-linux-amd64.tar.gz", bad)
		require.Error(t, err, "a mismatched checksum must never install")
		assert.Contains(t, err.Error(), "checksum mismatch")
	})

	// The one that matters. An asset with no line in checksums.txt has NOT
	// been verified, and a matcher that reported success — or an empty hash a
	// caller read as "nothing to check" — is how an unverified binary gets
	// installed. install.sh fails closed here too.
	t.Run("an asset missing from the file fails", func(t *testing.T) {
		_, err := ExpectedSHA256(good, "afy-windows-amd64.zip")
		require.Error(t, err, "an unlisted asset must be an error, never an empty hash")
		assert.Contains(t, err.Error(), "not listed")

		require.Error(t, VerifyFile(archive, "afy-windows-amd64.zip", good),
			"and VerifyFile must not install what it could not check")
	})

	t.Run("an empty checksums file fails", func(t *testing.T) {
		require.Error(t, VerifyFile(archive, "afy-linux-amd64.tar.gz", nil),
			"a truncated or empty checksums.txt must fail closed, not verify everything")
	})

	t.Run("the hash is read case-insensitively", func(t *testing.T) {
		upper := []byte(strings.ToUpper(sha256Of(t, body)) + "  afy-linux-amd64.tar.gz\n")
		assert.NoError(t, VerifyFile(archive, "afy-linux-amd64.tar.gz", upper))
	})
}

// ---------------------------------------------------------------------------
// (f) extraction
// ---------------------------------------------------------------------------

func writeTarGz(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
}

func writeZip(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
}

func TestExtractBinary(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new\n")

	t.Run("tar.gz, the format every Unix release publishes", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "afy-linux-amd64.tar.gz")
		writeTarGz(t, src, map[string][]byte{"afy": payload, "LICENSE": []byte("apache")})

		out, err := ExtractBinary(src, dir, "afy")
		require.NoError(t, err)
		got, err := os.ReadFile(out)
		require.NoError(t, err)
		assert.Equal(t, payload, got)
	})

	t.Run("zip, which is what Windows releases publish", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "afy-windows-amd64.zip")
		writeZip(t, src, map[string][]byte{"afy.exe": payload, "README.md": []byte("readme")})

		out, err := ExtractBinary(src, dir, "afy.exe")
		require.NoError(t, err)
		got, err := os.ReadFile(out)
		require.NoError(t, err)
		assert.Equal(t, payload, got)
	})

	// An archive without the binary must say what it DID hold; "not found" on
	// its own sends the reader back with nothing to look for.
	t.Run("an archive without the binary names what it holds", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "afy-linux-amd64.tar.gz")
		writeTarGz(t, src, map[string][]byte{"LICENSE": []byte("apache")})

		_, err := ExtractBinary(src, dir, "afy")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LICENSE")
	})

	t.Run("an unknown archive format is refused", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "afy-linux-amd64.rar")
		require.NoError(t, os.WriteFile(src, []byte("nope"), 0o644))

		_, err := ExtractBinary(src, dir, "afy")
		require.Error(t, err)
		assert.Contains(t, err.Error(), ".tar.gz")
	})

	// A zero-byte entry would leave the user with a zero-byte afy and no
	// explanation of what happened.
	t.Run("an empty entry is refused", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "afy-linux-amd64.tar.gz")
		writeTarGz(t, src, map[string][]byte{"afy": {}})

		_, err := ExtractBinary(src, dir, "afy")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")
	})
}

// ---------------------------------------------------------------------------
// (g) replacing the running binary
// ---------------------------------------------------------------------------

const (
	oldBytes = "I am the binary being replaced\n"
	newBytes = "I am the freshly downloaded release\n"
)

// currentAndReplacement lays out a fake install: the "running" binary and the
// extracted one that is to take its place.
func currentAndReplacement(t *testing.T) (target, src string) {
	t.Helper()
	installDir := t.TempDir()
	name := BinaryFileName(runtime.GOOS)
	target = filepath.Join(installDir, name)
	require.NoError(t, os.WriteFile(target, []byte(oldBytes), 0o755))

	src = filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(src, []byte(newBytes), 0o755))
	return target, src
}

func TestReplaceExecutableSwapsTheBytesInPlace(t *testing.T) {
	target, src := currentAndReplacement(t)

	require.NoError(t, ReplaceExecutable(target, src))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, newBytes, string(got), "the target still holds the old bytes — nothing was replaced")

	info, err := os.Stat(target)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		// os.CreateTemp makes the staged file 0600. Forgetting the chmod
		// leaves the user with a correct binary they cannot run.
		assert.NotZero(t, info.Mode().Perm()&0o111,
			"the replacement is not executable (mode %v)", info.Mode().Perm())
	}

	// The staging file lives beside the target, so it must not survive.
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "a staged temp file was left in the install directory")
}

// The Windows strategy is exercised directly rather than through
// ReplaceExecutable's runtime.GOOS branch, so it is covered on Linux CI too.
func TestRenamingAsideReplacesTheBinaryAndClearsTheBackup(t *testing.T) {
	target, src := currentAndReplacement(t)
	staged, err := stageBeside(filepath.Dir(target), filepath.Base(target), src)
	require.NoError(t, err)

	// A .old left over from a previous update must not block this one.
	require.NoError(t, os.WriteFile(BackupPath(target), []byte("stale"), 0o644))

	require.NoError(t, replaceByRenamingAside(target, staged))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, newBytes, string(got))
	assert.NoFileExists(t, BackupPath(target), "the backup should have been cleaned up")
}

// The tolerance branch. On Windows the .old may still be held open by this very
// process, so removing it can fail — and an update that has ALREADY SUCCEEDED
// must not report failure because of a leftover file it will clear next time.
//
// removeBackup is a seam because that failure cannot be provoked for real:
// os.Open takes FILE_SHARE_DELETE, so even a held handle permits the delete.
func TestRenamingAsideToleratesABackupItCannotRemove(t *testing.T) {
	target, src := currentAndReplacement(t)
	staged, err := stageBeside(filepath.Dir(target), filepath.Base(target), src)
	require.NoError(t, err)

	original := removeBackup
	t.Cleanup(func() { removeBackup = original })
	locked := errors.New("the file is being used by another process")
	called := 0
	removeBackup = func(string) error {
		called++
		return locked
	}

	err = replaceByRenamingAside(target, staged)

	require.NoError(t, err,
		"the swap succeeded and only the .old cleanup failed; reporting that as a failed update "+
			"would send the user chasing a problem that does not exist")
	assert.Equal(t, 1, called, "the seam was never reached — this test proved nothing")

	got, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, newBytes, string(got), "the update must have landed even though the cleanup did not")
	assert.FileExists(t, BackupPath(target), "the .old is left for the next run to clear")
}

func TestNotWritableErrorNamesThePathAndTheFix(t *testing.T) {
	err := &NotWritableError{Dir: "/usr/local/bin", Err: os.ErrPermission}

	assert.Contains(t, err.Error(), "/usr/local/bin", "the message must name the directory")
	assert.True(t, errors.Is(err, os.ErrPermission), "callers must still see it as a permission error")

	lower := strings.ToLower(err.Error())
	assert.True(t, strings.Contains(lower, "sudo") || strings.Contains(lower, "administrator"),
		"the message must say elevated rights are needed, not just that something failed: %q", err.Error())
}
