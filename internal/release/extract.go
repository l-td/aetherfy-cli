package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxBinaryBytes caps what one archive entry may expand to. The archive's
// checksum has already been verified against the release's own checksums.txt
// by the time anything is extracted, so this is a backstop against a corrupt
// stream filling the disk, not a security boundary.
const maxBinaryBytes = 256 << 20

// ExtractBinary pulls the single entry named binaryName out of the release
// archive at archivePath and writes it into destDir, returning the path it
// wrote.
//
// internal/archive was checked first: it only CREATES the tarball a deploy is
// uploaded in and has no extraction side at all, so there was nothing there to
// reuse and nothing here duplicates it.
//
// The archive's own paths are never used to build an output path — the entry
// is matched by name and written to destDir/binaryName — so a crafted archive
// cannot escape destDir.
func ExtractBinary(archivePath, destDir, binaryName string) (string, error) {
	switch {
	case strings.HasSuffix(archivePath, ".zip"):
		return extractFromZip(archivePath, destDir, binaryName)
	case strings.HasSuffix(archivePath, ".tar.gz"), strings.HasSuffix(archivePath, ".tgz"):
		return extractFromTarGz(archivePath, destDir, binaryName)
	default:
		return "", fmt.Errorf("cannot extract %s: expected a .tar.gz or a .zip", filepath.Base(archivePath))
	}
}

// writeBinary puts the contents of r at destDir/binaryName, executable.
func writeBinary(r io.Reader, destDir, binaryName string) (string, error) {
	out := filepath.Join(destDir, binaryName)
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	written, err := io.Copy(f, io.LimitReader(r, maxBinaryBytes+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(out)
		return "", err
	}
	if written > maxBinaryBytes {
		os.Remove(out)
		return "", fmt.Errorf("%s in the archive is larger than %d bytes; refusing to extract it", binaryName, maxBinaryBytes)
	}
	// A release archive holds one binary. An empty entry means the archive is
	// not what it claims to be, and installing it would leave the user with a
	// zero-byte afy and no explanation.
	if written == 0 {
		os.Remove(out)
		return "", fmt.Errorf("%s in the archive is empty", binaryName)
	}
	return out, nil
}

// entryMatches reports whether an archive entry IS the binary: same name, at
// the archive root. goreleaser leaves wrap_in_directory off, which the pair
// gate in test/install_contract_test.go pins, so the root is where it is.
func entryMatches(entryName, binaryName string) bool {
	return path.Clean(strings.TrimPrefix(filepath.ToSlash(entryName), "./")) == binaryName
}

// notFound reports what the archive DID hold. "binary not found" on its own
// sends the reader back to the archive with no idea what to look for.
func notFound(binaryName, archivePath string, seen []string) error {
	const show = 10
	listed := seen
	if len(listed) > show {
		listed = append(append([]string{}, listed[:show]...), fmt.Sprintf("... and %d more", len(seen)-show))
	}
	return fmt.Errorf("%s is not in %s (it holds: %s)",
		binaryName, filepath.Base(archivePath), strings.Join(listed, ", "))
}

func extractFromTarGz(archivePath, destDir, binaryName string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("%s is not a gzip stream: %w", filepath.Base(archivePath), err)
	}
	defer gz.Close()

	var seen []string
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("could not read %s: %w", filepath.Base(archivePath), err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		seen = append(seen, hdr.Name)
		if entryMatches(hdr.Name, binaryName) {
			return writeBinary(tr, destDir, binaryName)
		}
	}
	return "", notFound(binaryName, archivePath, seen)
}

func extractFromZip(archivePath, destDir, binaryName string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("%s is not a zip archive: %w", filepath.Base(archivePath), err)
	}
	defer zr.Close()

	var seen []string
	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		seen = append(seen, entry.Name)
		if !entryMatches(entry.Name, binaryName) {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return "", fmt.Errorf("could not read %s from %s: %w", entry.Name, filepath.Base(archivePath), err)
		}
		out, err := writeBinary(rc, destDir, binaryName)
		rc.Close()
		return out, err
	}
	return "", notFound(binaryName, archivePath, seen)
}
