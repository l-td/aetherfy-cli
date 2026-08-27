package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/release"
	"github.com/l-td/aetherfy-cli/pkg/version"
	"github.com/spf13/cobra"
)

// updateTimeout bounds the whole exchange — resolve, two downloads. Generous,
// because the archive is tens of megabytes on a slow connection, but finite:
// an update that hangs forever is worse than one that fails.
const updateTimeout = 5 * time.Minute

var (
	updateCheckOnly bool
	updateToVersion string
	updateForce     bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update afy to the newest release",
	Long: `Replace the running afy binary with the newest published release.

Needs no Aetherfy account: updating the CLI is not an authenticated operation.

A binary that did not come from a release — a go install, make install or
go build build — is refused, because replacing it with a release archive would
silently discard the build you have. Pass --force to do it anyway.`,
	Example: `  # Update to the newest release
  afy update

  # Ask whether anything newer exists; change nothing
  afy update --check

  # Install a specific version
  afy update --version 0.1.0`,
	Args: cobra.NoArgs,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false, "Report whether a newer release exists; change nothing")
	updateCmd.Flags().StringVar(&updateToVersion, "version", "", "Install a specific version (accepts 0.1.0 or v0.1.0)")
	updateCmd.Flags().BoolVar(&updateForce, "force", false, "Proceed even when the running build did not come from a release")
}

// sourceBuildRefusal is the (a) path: this binary was built, not downloaded.
func sourceBuildRefusal(current string) error {
	return fmt.Errorf(`this afy did not come from a release, so there is nothing to update in place.
  running version: %s
  which is %s

`+"`afy update`"+` replaces the running binary with a published release archive — that
would silently discard the build you have. Update it the way you installed it:
  go install github.com/%s/cmd/%s@latest
  or, in a clone: git pull && make install

Re-run with --force if replacing it with a release is what you want.`,
		current, release.InstalledFrom(current), release.Repo, release.BinaryName)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	current := version.Version

	// (a) Refuse a build nobody can re-download. --force is the only override.
	if release.MustRefuseUpdate(current, updateForce) {
		return sourceBuildRefusal(current)
	}

	// Which asset this platform would need. Resolved before anything is
	// fetched so that windows/arm64 — which .goreleaser.yaml's ignore list
	// never publishes — says so, instead of reporting an available update it
	// could not then install.
	asset, err := release.CurrentAssetName()
	if err != nil {
		return err
	}

	// cobra always supplies a context through Execute; the fallback keeps this
	// runnable when the command is driven directly.
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	client := &http.Client{Timeout: updateTimeout}

	// (b) Which version are we going to?
	target := release.NormalizeTag(updateToVersion)
	if target == "" {
		target, err = release.ResolveLatestTag(ctx, client)
		if err != nil {
			return err
		}
	}

	// (c) Compare. Both branches below exit 0; --check stops at either.
	currentTag := release.NormalizeTag(current)
	if currentTag == target {
		output.PrintSuccess("already on %s", target)
		return nil
	}
	if updateCheckOnly {
		// Nothing above this point has written to disk, and nothing below it
		// runs: --check is read-only by construction, not by discipline.
		if updateToVersion == "" {
			// GitHub's /releases/latest IS the definition of the newest
			// published release, so "different from latest" means "older" —
			// no version comparison of our own is needed, or would be more
			// authoritative if we had one.
			output.PrintInfo("A newer release is available: %s (running %s)", target, current)
			output.Println("Run 'afy update' to install it.")
		} else {
			output.PrintInfo("Requested %s; running %s", target, current)
			output.Printf("Run 'afy update --version %s' to switch.\n", updateToVersion)
		}
		return nil
	}

	// (h, hoisted) Where this binary lives, and whether it can be written.
	// EvalSymlinks so that an afy reached through a symlink updates the binary,
	// not the link.
	//
	// Checked HERE — after --check has returned, before a byte is downloaded.
	// /usr/local/bin is the installer's default and is not writable by the user
	// who installed there, so "you need sudo" is the common answer, and making
	// someone pull ~16 MB before hearing it is a waste of their bandwidth.
	// It is below the --check return because the probe writes a file, and
	// --check must not.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not work out where this afy is installed: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if err := release.EnsureReplaceable(self); err != nil {
		return err
	}

	// (d) Download the asset and the checksums beside it, into a temp dir that
	// goes away on every path out of here.
	tmpDir, err := os.MkdirTemp("", "afy-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	prefix := release.DownloadPrefix(target)
	archivePath := filepath.Join(tmpDir, asset)
	sumsPath := filepath.Join(tmpDir, release.ChecksumsFile)

	sp := output.NewSpinner(fmt.Sprintf("Downloading %s %s...", release.BinaryName, target))
	sp.Start()
	err = release.Download(ctx, client, prefix+"/"+asset, archivePath)
	if err == nil {
		err = release.Download(ctx, client, prefix+"/"+release.ChecksumsFile, sumsPath)
	}
	sp.Stop()
	if err != nil {
		return err
	}

	// (e) Verify before extracting, never after.
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	if err := release.VerifyFile(archivePath, asset, sums); err != nil {
		return err
	}

	// (f) Extract.
	extracted, err := release.ExtractBinary(archivePath, tmpDir, release.BinaryFileName(runtime.GOOS))
	if err != nil {
		return err
	}

	// (g) Replace the running binary, and put the old one back if the new one
	// does not run. A permission failure that slipped past EnsureReplaceable
	// above still comes back as release.NotWritableError.
	if err := release.ReplaceExecutable(self, extracted); err != nil {
		return err
	}

	// (i)
	output.PrintSuccess("Updated %s: %s -> %s", self, current, target)
	return nil
}
