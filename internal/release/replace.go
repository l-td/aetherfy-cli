package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// backupSuffix is what the binary being replaced is set aside as while the new
// one takes its place.
const backupSuffix = ".old"

// verifyArgs is what a replacement is asked to do to prove it works.
//
// scripts/install.sh runs exactly this after installing (`"$INSTALL_DIR/afy"
// version`, install.sh:189) and fails loudly if it does not answer. An updater
// that skipped the same check would be weaker than the installer it replaces,
// and its failure mode is worse: the installer leaves you with no afy, this
// would leave you with a broken one and no way back.
//
// `version` specifically because it is the only subcommand that loads no
// config and touches no network — root.go's PersistentPreRunE skips both for it.
var verifyArgs = []string{"version"}

// verifyTimeout bounds the check. `afy version` is instant; a replacement that
// takes longer than this is not working, and hanging forever inside an update
// is the one outcome worse than failing it.
const verifyTimeout = 30 * time.Second

// removeBackup is a variable so the "the .old survived" branch below can be
// exercised from a test.
//
// The failure it tolerates is real and expected: on Windows the .old file is
// the image this process is running from, and a mapped image cannot be deleted
// until the process exits, share flags or not. It also cannot be reproduced
// from a test, because a test can only hold ordinary file handles, never the
// loader's image section. Without this seam the branch would be unreachable —
// and an unreachable branch that decides whether a SUCCESSFUL update reports
// failure is not one to leave untested.
var removeBackup = os.Remove

// NotWritableError says which directory could not be written and what to do.
//
// The raw error is "rename /tmp/afy.new-123 /usr/local/bin/afy: permission
// denied", which reads as a bug in afy rather than as "this needs sudo" — and
// /usr/local/bin is the default install directory, so it is the common case,
// not the edge one.
type NotWritableError struct {
	Dir string
	Err error
}

func (e *NotWritableError) Error() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("cannot write to %s — re-run this from a terminal opened as Administrator", e.Dir)
	}
	return fmt.Sprintf("cannot write to %s — re-run with elevated rights, e.g. `sudo %s update`", e.Dir, BinaryName)
}

func (e *NotWritableError) Unwrap() error { return e.Err }

// asWriteError turns a permission failure into the sentence that helps and
// leaves every other error alone.
func asWriteError(dir string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return &NotWritableError{Dir: dir, Err: err}
	}
	return err
}

// BackupPath is where the binary being replaced is set aside.
func BackupPath(target string) string { return target + backupSuffix }

// EnsureReplaceable reports whether the binary at target could be replaced,
// WITHOUT downloading anything first.
//
// /usr/local/bin is the installer's default directory and is not writable by
// the user who installed there, so a refusal here is the common case rather
// than the edge one. Discovering it only after pulling down a ~16 MB archive
// wastes the user's time and bandwidth to tell them something knowable up
// front. It writes and immediately removes an empty file, which is why callers
// must not run it on the --check path.
func EnsureReplaceable(target string) error {
	dir := filepath.Dir(target)
	probe, err := os.CreateTemp(dir, "."+filepath.Base(target)+".probe-*")
	if err != nil {
		return asWriteError(dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// ReplaceExecutable overwrites the binary at target with the file at src, and
// puts the original back if the replacement does not run.
//
// target is expected to be a real path, not a symlink — callers resolve it with
// filepath.EvalSymlinks first, so that updating through a symlink replaces the
// binary rather than the link.
//
// The order is: stage beside the target, set the current binary aside, swap,
// PROVE THE NEW ONE RUNS, and only then discard what was set aside. A user must
// never be left holding a CLI that does not start.
func ReplaceExecutable(target, src string) error {
	dir := filepath.Dir(target)

	staged, err := stageBeside(dir, filepath.Base(target), src)
	if err != nil {
		return err
	}
	// A no-op once one of the renames below has moved it; the safety net is for
	// every path that returns before that.
	defer os.Remove(staged)

	backup := BackupPath(target)
	// Whatever a previous run left behind. RemoveAll, not Remove: this path is
	// user-visible and something else may have taken the name.
	_ = os.RemoveAll(backup)

	if err := displace(target, backup); err != nil {
		return asWriteError(dir, fmt.Errorf("could not set the current binary aside at %s: %w", backup, err))
	}

	if err := os.Rename(staged, target); err != nil {
		return rollback(target, backup, asWriteError(dir, err))
	}

	if err := verifyRuns(target); err != nil {
		return rollback(target, backup, fmt.Errorf(
			"the downloaded %s does not run on this machine, so nothing was changed: %w",
			BinaryName, err))
	}

	// Best effort, deliberately unchecked: the update has already succeeded and
	// been proved. On Windows the .old is still mapped and the next run clears it.
	_ = removeBackup(backup)
	return nil
}

// displace sets the binary at target aside at backup, keeping it recoverable
// until the replacement has proved itself.
func displace(target, backup string) error {
	if runtime.GOOS == "windows" {
		// A running .exe cannot be deleted, overwritten or copied over — but it
		// CAN be renamed. On Windows moving it out of the way IS how the new
		// binary gets the name, so the rename is not optional here.
		return os.Rename(target, backup)
	}
	// Unix: COPY, leaving target in place, so the swap below stays a single
	// atomic os.Rename over a name that never stops existing. Renaming aside
	// here would open a window in which afy is not on disk at all, and buy
	// nothing — the copy is the rollback either way.
	return copyFile(target, backup, 0o755)
}

// rollback puts the displaced binary back and returns the error that caused it,
// or a louder one if the restore itself failed.
func rollback(target, backup string, cause error) error {
	if err := os.Rename(backup, target); err != nil {
		return fmt.Errorf("%w — AND the previous binary could not be put back: it is at %s, "+
			"rename it to %s by hand (%v)", cause, backup, target, err)
	}
	return fmt.Errorf("%w\n  the previous binary has been restored at %s", cause, target)
}

// verifyRuns executes the replacement and reports whether it works.
func verifyRuns(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, verifyArgs...)
	// Never inherit stdin: a replacement that waited for input would hang the
	// update behind a prompt nobody can see.
	cmd.Stdin = nil

	out, err := cmd.CombinedOutput()
	if err == nil {
		return err
	}
	if line := firstLine(string(out)); line != "" {
		return fmt.Errorf("%w (%s %s said: %s)", err, filepath.Base(path), strings.Join(verifyArgs, " "), line)
	}
	return err
}

// firstLine is the most useful part of a failed command's output; the rest is
// noise in an error message.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// stageBeside copies src to a fresh file in dir — the SAME directory as the
// target, never a temp dir. os.Rename is only atomic within one filesystem, and
// $TMPDIR is very often a different one from /usr/local/bin; across a filesystem
// boundary it fails outright with EXDEV.
func stageBeside(dir, base, src string) (string, error) {
	tmp, err := os.CreateTemp(dir, "."+base+".new-*")
	if err != nil {
		return "", asWriteError(dir, err)
	}
	staged := tmp.Name()
	_ = tmp.Close()

	// CreateTemp makes the file 0600; the binary has to be runnable.
	if err := copyFile(src, staged, 0o755); err != nil {
		os.Remove(staged)
		return "", asWriteError(dir, err)
	}
	return staged, nil
}

// copyFile writes src to dst with the given mode.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		// O_CREATE honours perm only when the file did not already exist, and
		// CreateTemp has usually just made it 0600.
		err = os.Chmod(dst, perm)
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}
