package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// ExpectedSHA256 returns the hash checksums.txt lists for asset.
//
// A missing line is an ERROR, never an empty string. That distinction is the
// whole point: a matcher that returned ("", nil) for an asset the file does not
// mention would hand its caller a hash that trivially fails to compare, or —
// worse, and this is the shape the bug always takes — a caller that reads "no
// expected hash" as "nothing to verify" and installs an unverified binary.
// scripts/install.sh fails closed the same way: `grep " ${ASSET}$" || exit 1`.
func ExpectedSHA256(checksums []byte, asset string) (string, error) {
	if asset == "" {
		return "", fmt.Errorf("no asset name to look up in %s", ChecksumsFile)
	}
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// sha256sum writes "<hash>  <name>", and marks binary mode with a "*"
		// in front of the name.
		if strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in %s — the release may not publish an asset by that name", asset, ChecksumsFile)
}

// VerifyFile checks the file at path against the hash checksums.txt lists for
// asset. Callers must run it BEFORE extracting, never after: an archive that
// fails this check must never have been opened.
func VerifyFile(path, asset string, checksums []byte) error {
	want, err := ExpectedSHA256(checksums, asset)
	if err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("could not read the downloaded %s: %w", asset, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("could not hash the downloaded %s: %w", asset, err)
	}
	got := hex.EncodeToString(h.Sum(nil))

	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: %s lists %s, the downloaded file is %s. "+
			"Refusing to install it", asset, ChecksumsFile, want, got)
	}
	return nil
}
