package cperrors

// Unit tests for the extractor itself.
//
// This package had none, and that absence was the root cause of a real hole:
// the self-naming rule — `NAME = "NAME"`, the single thing separating an error
// code from a URL or a reason string sitting next to it in the same file — was
// implemented in one regexp comparison inside Extract and asserted nowhere.
// Deleting that comparison made the generator write UPGRADE_URL, SUPPORT_CONTACT
// and both FREEZE_REASON_* values into the snapshot as error codes, and every
// check passed, because Validate only ever looked at the shape of the KEY. The
// total moved 123 → 127 while error_codes.py still reported its usual 117, so
// the number a reader would notice did not move at all.
//
// The rule is now pinned twice over: here, against a fixture built to contain
// exactly the mix the control plane really has, and in ValidateExtraction, which
// re-asserts it over what the extraction accepted.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The non-self-named constants that really sit in shared/plan_validator.py,
// beside the one error code it owns. These are the things the form rule exists
// to leave behind.
var notCodes = map[string]string{
	"UPGRADE_URL":           "/billing/upgrade",
	"SUPPORT_CONTACT":       "mailto:sales@aetherfy.com",
	"FREEZE_REASON_CEILING": "ceiling",
	"FREEZE_REASON_DUNNING": "dunning",
}

// fakeCP writes a control-plane tree at the paths `sources` names, so the test
// drives the real reader over real files rather than a stubbed one.
//
// The top-level registry is padded past minTotalCodes because the floor is part
// of what makes an extraction trustworthy; a fixture that could not clear it
// would only ever exercise the failure path.
func fakeCP(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	var registry strings.Builder
	registry.WriteString("\"\"\"Docstring.\"\"\"\n")
	registry.WriteString("AGENT_NOT_FOUND = \"AGENT_NOT_FOUND\"\n")
	registry.WriteString("RESOURCE_BUSY = \"RESOURCE_BUSY\"  # with a trailing comment\n")
	// Shapes that must NOT be picked up.
	registry.WriteString("    INDENTED_NOT_MODULE_LEVEL = \"INDENTED_NOT_MODULE_LEVEL\"\n")
	registry.WriteString("lowercase_thing = \"lowercase_thing\"\n")
	for i := 0; i < 90; i++ {
		name := fmt.Sprintf("PADDING_CODE_%d", i)
		registry.WriteString(name + " = \"" + name + "\"\n")
	}

	var validator strings.Builder
	validator.WriteString("AGENT_COLLECTION_REGION_MISMATCH = \"AGENT_COLLECTION_REGION_MISMATCH\"\n")
	for name, value := range notCodes {
		validator.WriteString(name + " = \"" + value + "\"\n")
	}

	files := map[string]string{
		"shared/error_codes.py":    registry.String(),
		"api/routes/regions.py":    "INVALID_REGION = \"INVALID_REGION\"\n",
		"shared/plan_validator.py": validator.String(),
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}

	// The fixture must cover exactly the configured sources; if a source is added
	// and this is not, the tests below would silently stop covering it.
	require.Len(t, files, len(sources), "fakeCP is stale — `sources` has changed")
	for _, s := range sources {
		require.Contains(t, files, s.path, "fakeCP does not write %s", s.path)
	}

	return root
}

// THE FORM RULE. Self-named constants are codes; everything else in the same
// file is not.
func TestExtractTakesOnlySelfNamedConstants(t *testing.T) {
	reg, err := Extract(fakeCP(t))
	require.NoError(t, err)

	// Positive control first: a zero-violations result below means nothing unless
	// the reader is demonstrably reading.
	assert.Contains(t, reg.Codes, "AGENT_NOT_FOUND")
	assert.Contains(t, reg.Codes, "RESOURCE_BUSY", "a trailing `# comment` broke the match")
	assert.Contains(t, reg.Codes, "INVALID_REGION")
	assert.Contains(t, reg.Codes, "AGENT_COLLECTION_REGION_MISMATCH")

	for name, value := range notCodes {
		if _, taken := reg.Codes[name]; taken {
			assert.Fail(t, "the extractor took a constant that is not an error code",
				"%s = %q was extracted. It is not self-named, and the whole separation "+
					"between an error code and the URLs and reason strings beside it in "+
					"plan_validator.py is that one property.", name, value)
		}
	}

	assert.NotContains(t, reg.Codes, "INDENTED_NOT_MODULE_LEVEL",
		"an indented assignment is a class attribute or a local, not a registry entry")
	assert.NotContains(t, reg.Codes, "lowercase_thing")

	// Tier and source travel with the code.
	assert.Equal(t, Code{Source: "shared/error_codes.py", Tier: tierTopLevel}, reg.Codes["AGENT_NOT_FOUND"])
	assert.Equal(t, Code{Source: "api/routes/regions.py", Tier: tierViolation}, reg.Codes["INVALID_REGION"])
}

// ValidateExtraction is the second lock on the same rule: even if the reader
// were loosened, what it accepted is re-checked.
func TestValidateExtractionCatchesANonSelfNamedConstant(t *testing.T) {
	reg, err := Extract(fakeCP(t))
	require.NoError(t, err)
	require.NoError(t, ValidateExtraction(reg), "the clean fixture must pass, or nothing below discriminates")

	// Exactly what a loosened `m[1] != m[2]` test would have produced.
	reg.Codes["UPGRADE_URL"] = Code{Source: "shared/plan_validator.py", Tier: tierViolation}
	reg.Literals["UPGRADE_URL"] = "/billing/upgrade"

	err = ValidateExtraction(reg)
	require.Error(t, err, "a constant bound to a URL passed validation as an error code")
	assert.Contains(t, err.Error(), "UPGRADE_URL")

	// And the old validator still says yes — which is the point. Validate sees
	// only the key, and "UPGRADE_URL" is a perfectly code-shaped key.
	assert.NoError(t, Validate(reg),
		"Validate is expected to miss this; if it now catches it, ValidateExtraction "+
			"may be redundant and this test's premise needs rewriting")
}

// An extraction that records no literals cannot prove anything about itself.
func TestValidateExtractionRefusesAnUnwitnessedExtraction(t *testing.T) {
	reg, err := Extract(fakeCP(t))
	require.NoError(t, err)

	reg.Literals = nil
	assert.Error(t, ValidateExtraction(reg),
		"an extraction with no record of what it read must not validate — the "+
			"self-naming check would iterate an empty map and pass vacuously")
}

// A moved registry is not a missing checkout, and the two must not answer alike.
func TestRootExistsAndMissingSourcesAreSeparateQuestions(t *testing.T) {
	root := fakeCP(t)

	assert.True(t, RootExists(root))
	assert.Empty(t, MissingSources(root), "the fixture writes every configured source")

	assert.False(t, RootExists(filepath.Join(root, "no-such-checkout")),
		"a path that does not exist is not a checkout")

	// Simulate the refactor: the registry moves, the checkout stays.
	moved := filepath.Join(root, "shared", "errors_registry.py")
	require.NoError(t, os.Rename(filepath.Join(root, "shared", "error_codes.py"), moved))

	assert.True(t, RootExists(root),
		"the checkout is still right there — reporting it absent is what let a moved "+
			"registry turn the drift check into a silent skip")
	assert.Equal(t, []string{"shared/error_codes.py"}, MissingSources(root),
		"the moved registry must be named, so the failure says what to fix")
}

// The floor is a parser-rot tripwire; prove it actually trips.
func TestValidateRejectsAnExtractionBelowTheFloor(t *testing.T) {
	reg, err := Extract(fakeCP(t))
	require.NoError(t, err)
	require.NoError(t, Validate(reg))

	// Deterministic: collect first, then delete a counted number. Deleting while
	// ranging a map is legal but the set of entries the range still yields is not
	// defined, so the loop could stop above the floor and the assertion below
	// would be testing nothing. (It did, on the first run — it left exactly
	// minTotalCodes, and the floor is `< minTotalCodes`.)
	names := make([]string, 0, len(reg.Codes))
	for name := range reg.Codes {
		names = append(names, name)
	}
	for i := 0; len(reg.Codes) >= minTotalCodes; i++ {
		require.Less(t, i, len(names), "ran out of codes to delete before reaching the floor")
		delete(reg.Codes, names[i])
	}

	require.Less(t, len(reg.Codes), minTotalCodes, "the mutation did not land")
	assert.Error(t, Validate(reg), "an extraction under the floor must not validate")
}
