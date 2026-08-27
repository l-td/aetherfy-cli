package release

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// backupSuffix is what the running binary is renamed to on Windows while the
// new one takes its place.
const backupSuffix = ".old"

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

// BackupPath is where ReplaceExecutable moves the running binary on Windows.
func BackupPath(target string) string { return target + backupSuffix }

// ReplaceExecutable overwrites the binary at target with the file at src.
//
// target is expected to be a real path, not a symlink — callers resolve it
// with filepath.EvalSymlinks first, so that updating through a symlink
// replaces the binary rather than the link.
func ReplaceExecutable(target, src string) error {
	dir := filepath.Dir(target)

	staged, err := stageBeside(dir, filepath.Base(target), src)
	if err != nil {
		return err
	}
	// A no-op once one of the renames below has moved it; the safety net is
	// for every path that returns before that.
	defer os.Remove(staged)

	if runtime.GOOS == "windows" {
		return replaceByRenamingAside(target, staged)
	}

	// Unix: rename over the target. It is atomic — readers see the old file or
	// the new one, never a half-written one — and it works on a binary that is
	// currently running, because the running process holds the inode, not the
	// name.
	if err := os.Rename(staged, target); err != nil {
		return asWriteError(dir, err)
	}
	return nil
}

// stageBeside copies src to a fresh file in dir — the SAME directory as the
// target, never a temp dir. os.Rename is only atomic within one filesystem,
// and $TMPDIR is very often a different one from /usr/local/bin; across a
// filesystem boundary it fails outright with EXDEV.
func stageBeside(dir, base, src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(dir, "."+base+".new-*")
	if err != nil {
		return "", asWriteError(dir, err)
	}
	staged := tmp.Name()

	_, err = io.Copy(tmp, in)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		// CreateTemp makes the file 0600; the binary has to be runnable.
		err = os.Chmod(staged, 0o755)
	}
	if err != nil {
		os.Remove(staged)
		return "", asWriteError(dir, err)
	}
	return staged, nil
}

// replaceByRenamingAside is the Windows strategy.
//
// A running .exe cannot be deleted or overwritten there — but it CAN be
// renamed. So the running file moves to <name>.old, the new binary takes the
// name, and the .old is removed afterwards on a best-effort basis: this
// process still holds it open, so the removal may not be permitted until it
// exits. A .old that survives is cleaned up at the start of the NEXT update,
// which is strictly better than failing an update that already succeeded.
func replaceByRenamingAside(target, staged string) error {
	dir := filepath.Dir(target)
	backup := BackupPath(target)

	// Whatever a previous run left behind. RemoveAll, not Remove: this path is
	// user-visible and something else may have taken the name.
	_ = os.RemoveAll(backup)

	if err := os.Rename(target, backup); err != nil {
		return asWriteError(dir, fmt.Errorf("could not move the running binary aside to %s: %w", backup, err))
	}

	if err := os.Rename(staged, target); err != nil {
		// Put the original back rather than leave the user with no afy at all.
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return fmt.Errorf("the update failed AND the original could not be restored: it is now at %s — "+
				"rename it back to %s by hand (%v)", backup, target, err)
		}
		return asWriteError(dir, err)
	}

	// Best effort, deliberately unchecked: the update has already succeeded.
	_ = removeBackup(backup)
	return nil
}
