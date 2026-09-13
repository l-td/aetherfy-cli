package test

// Pins internal/api.GitHubLinkStatus's json tags to the control plane's
// GitHubLinkStatusResponse.
//
// WHAT GOES WRONG WITHOUT IT. `afy status` reads an agent's GitHub link through
// that struct, by json tag. Rename a field on the control-plane side and
// encoding/json does not complain: it leaves the Go field at its zero value. So
// `root_dir` becoming `directory` makes every linked agent's directory read as
// the repository root; `account_connected` becoming anything else makes the
// "pushes are not deploying" warning stop firing, or start firing on every
// healthy link, depending on which way the zero value falls. No error, no red
// test — this repo's own tests mock the server and agree with themselves about
// the spellings, which is exactly how the auth-code drift in bf93cd1 survived
// until someone read it.
//
// So the spellings are checked against the server's, not against ourselves. The
// snapshot is the contract CI compares to, and where the control plane IS
// checked out the extraction re-runs and reds on drift.
//
// WHAT IT DOES NOT CHECK: types, nullability, defaults. The field NAMES are what
// a rename breaks silently. A type change breaks loudly at decode time, and the
// server's own default for account_connected is pinned where it is acted on, in
// cmd/agents_github_link_test.go.

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cplink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// linkStatusTags returns the json tag of every field on api.GitHubLinkStatus.
//
// Read by reflection rather than listed here, because a list here would be a
// third copy of the same names and the one nothing checks.
func linkStatusTags(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(api.GitHubLinkStatus{})
	var tags []string
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		require.NotEmpty(t, name,
			"%s.%s has no json tag, so nothing pins what it decodes",
			typ.Name(), typ.Field(i).Name)
		tags = append(tags, name)
	}
	// ANTI-NO-OP: a reflection walk that found nothing would agree with every
	// snapshot by having nothing to disagree with.
	require.NotEmpty(t, tags, "the reflection walk over %s found no fields at all", typ.Name())
	sort.Strings(tags)
	return tags
}

func loadLinkSnapshot(t *testing.T) *cplink.Snapshot {
	t.Helper()
	snap, err := cplink.Load(repoRoot + "/" + cplink.SnapshotPath)
	require.NoError(t, err, "cannot read %s — regenerate it: go run ./%s",
		cplink.SnapshotPath, cplink.GeneratorPath)
	return snap
}

// THE PIN ITSELF.
func TestLinkStatusStructMatchesTheControlPlaneFieldNames(t *testing.T) {
	snap := loadLinkSnapshot(t)
	want := snap.Fields
	got := linkStatusTags(t)

	// Reported as two lists rather than one equality, because which direction
	// drifted decides what to do about it.
	var missing, extra []string
	inGo := map[string]bool{}
	for _, g := range got {
		inGo[g] = true
	}
	inCP := map[string]bool{}
	for _, w := range want {
		inCP[w] = true
		if !inGo[w] {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !inCP[g] {
			extra = append(extra, g)
		}
	}

	assert.Empty(t, missing,
		"the control plane sends %v, and api.GitHubLinkStatus has no json tag for them. "+
			"A field nothing decodes is a field `afy status` silently reports as empty.", missing)
	assert.Empty(t, extra,
		"api.GitHubLinkStatus decodes %v, which %s does not declare. A tag the server never "+
			"sends reads as a zero value forever — and if it was renamed, the old spelling is "+
			"what this struct is still asking for.", extra, cplink.ModelName)
}

// The committed snapshot must be believable on its own terms, since CI has no
// control-plane checkout to compare it against.
func TestCommittedLinkSnapshotIsTrustworthy(t *testing.T) {
	snap := loadLinkSnapshot(t)

	assert.Equal(t, cplink.SchemaVersion, snap.SchemaVersion,
		"snapshot schemaVersion %d, this guard reads %d — regenerate with `go run ./%s`",
		snap.SchemaVersion, cplink.SchemaVersion, cplink.GeneratorPath)
	assert.Equal(t, cplink.GeneratorPath, snap.Generator)
	require.NoError(t, cplink.Validate(snap.FieldList()))

	// The snapshot claims to describe pushed code. If its own provenance says
	// otherwise it was committed from a working copy, and every other machine
	// is comparing against something it cannot see.
	assert.Empty(t, cplink.Unshareable(snap.Source),
		"the committed snapshot records an unshareable source: %s", cplink.Unshareable(snap.Source))
	assert.Equal(t, cplink.SourcePath, snap.Source.Path)
	assert.Equal(t, cplink.ModelName, snap.Source.Model)
}

// Where the control plane IS checked out, re-extract and red on any difference,
// so a stale snapshot cannot survive a dev build. CI checks out no siblings and
// skips this — same trust model as cp-error-codes-snapshot.
func TestLinkSnapshotMatchesTheLiveControlPlane(t *testing.T) {
	cpRoot := cperrors.Root(repoRoot)
	if !cperrors.RootExists(cpRoot) {
		t.Skipf("SKIPPED the live-drift check: no control-plane checkout at %s "+
			"(set %s to point elsewhere). The committed snapshot was checked instead.",
			cpRoot, cperrors.RootEnv)
	}

	// A checkout IS here. From this point absence is a failure, never a skip: a
	// model that merely MOVED must not read as "no control plane" and go green.
	if cplink.SourceMissing(cpRoot) {
		t.Fatalf("the control plane IS checked out at %s, but %s is not there.\n\n"+
			"That is a move or a rename, not a missing checkout — do not read it as 'skip'. "+
			"Update SourcePath in test/cplink/extract.go, then regenerate: go run ./%s",
			cpRoot, cplink.SourcePath, cplink.GeneratorPath)
	}

	live, err := cplink.Extract(cpRoot)
	require.NoError(t, err, "extracting %s from %s", cplink.ModelName, cpRoot)
	require.NoError(t, cplink.Validate(live),
		"the extraction from %s is not trustworthy — refusing to compare against it, because "+
			"an empty extraction agrees with everything", cpRoot)

	// PUSH GATE. A dirty or unpushed source file describes code nobody else can
	// see, so a diff here would report the developer's own work as staleness —
	// and the advice that failure gives, "regenerate", is the one thing that must
	// not happen: it would commit a field list from a working copy.
	if why := cplink.Unshareable(live.Source); why != "" {
		t.Skipf("NOT COMPARED — %s. The committed snapshot is unchanged and still governs; "+
			"push the control-plane change, then regenerate: go run ./%s", why, cplink.GeneratorPath)
	}

	snap := loadLinkSnapshot(t)
	assert.Equal(t, snap.Fields, live.Names,
		"the committed snapshot no longer matches %s in %s.\n"+
			"Regenerate: go run ./%s\n"+
			"If a field is GONE, that is a rename or a removal — check what in this repo "+
			"decodes it before you regenerate, because the tag is now dead.",
		cplink.ModelName, cpRoot, cplink.GeneratorPath)
}
