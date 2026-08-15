package test

// Guards the customer-facing URLs the CLI prints.
//
// Two bugs shipped together and neither was catchable by any existing test,
// because both live in cobra help strings and echoed output rather than in
// code the suite exercises:
//
//  1. Wrong TLD. `login` and `root` pointed readers at aetherfy.run, but the
//     product is aetherfy.com — which is also what this CLI's own API default
//     (agents.aetherfy.com) has always used.
//  2. Wrong dashboard path. The api-keys link omitted the /dashboard prefix.
//     A bare /settings/... 404s on app.aetherfy.com; that exact shape was a
//     live incident before, and the dashboard repo pins the billing twin in
//     tests/unit/billing-route-exists-test.js for the same reason.
//
// So this scans the shipped source for both. A URL a user is told to visit is
// part of the contract even though no code path returns it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Files whose contents reach a user: help text, printed output, the installer
// they curl, and the release metadata that becomes the Homebrew cask.
var userFacingSources = []string{
	"cmd/login.go",
	"cmd/root.go",
	"README.md",
	"scripts/install.sh",
	".goreleaser.yaml",
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	// test/ sits one level under the repo root.
	body, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
	assert.NoError(t, err, "cannot read %s — has it moved? Update userFacingSources.", rel)
	// Normalize CRLF so a Windows checkout matches the same substrings.
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

func TestNoRunTLDInUserFacingStrings(t *testing.T) {
	for _, rel := range userFacingSources {
		body := readRepoFile(t, rel)
		assert.NotContains(t, body, "aetherfy.run",
			"%s references aetherfy.run — the product domain is aetherfy.com", rel)
	}
}

func TestDashboardLinksCarryTheDashboardPrefix(t *testing.T) {
	const appOrigin = "https://app.aetherfy.com"

	for _, rel := range userFacingSources {
		body := readRepoFile(t, rel)
		rest := body
		for {
			i := strings.Index(rest, appOrigin)
			if i < 0 {
				break
			}
			path := rest[i+len(appOrigin):]
			if end := strings.IndexAny(path, " \t\n\"'`)"); end >= 0 {
				path = path[:end]
			}
			assert.True(t, strings.HasPrefix(path, "/dashboard"),
				"%s links to %s%s — authenticated routes live under /dashboard "+
					"(a bare path 404s)", rel, appOrigin, path)
			rest = rest[i+len(appOrigin):]
		}
	}
}

// Anti-no-op: the two guards above assert absence, and an absence assertion is
// worthless if the reader silently returns nothing. Prove the corpus is real
// and that it still contains the links we mean to police.
func TestUserFacingSourcesAreActuallyScanned(t *testing.T) {
	joined := ""
	for _, rel := range userFacingSources {
		body := readRepoFile(t, rel)
		assert.NotEmpty(t, body, "%s is empty — the guard would pass vacuously", rel)
		joined += body
	}

	assert.Contains(t, joined, "https://app.aetherfy.com/dashboard/settings/api-keys",
		"the api-keys link vanished from the scanned files — either it moved to a "+
			"file missing from userFacingSources, or the guard is now vacuous")
	assert.Contains(t, joined, "https://docs.aetherfy.com",
		"the docs link vanished from the scanned files — see above")
}
