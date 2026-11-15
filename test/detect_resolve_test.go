package test

import (
	"testing"

	"github.com/l-td/aetherfy-cli/internal/detect"
	"github.com/stretchr/testify/assert"
)

// What the platform offers, as GET /deploy-options lists it (the control plane's runtime table),
// and the defaults it names. Held here as a stand-in for the server's answer: the CLI carries no
// list of its own, so these are the inputs of every case below.
var offeredRuntimes = []string{
	"python3.11", "python3.12", "python3.13", "python3.14", "python3.15",
	"node22", "node24", "node26", "node22-ts", "node24-ts", "node26-ts",
	"bun", "dockerfile",
}

var defaultRuntimes = map[string]string{"python": "python3.14", "node": "node24"}

func resolve(t *testing.T, h detect.ProjectHints) detect.Resolution {
	t.Helper()
	r, err := detect.Resolve(h, offeredRuntimes, defaultRuntimes)
	assert.NoError(t, err)
	return r
}

func TestResolve_AVersionTheServerOffersIsThatRuntime(t *testing.T) {
	assert.Equal(t, detect.Resolution{Runtime: "python3.12"}, resolve(t, detect.ProjectHints{Language: detect.Python, Version: "3.12"}))
	assert.Equal(t, detect.Resolution{Runtime: "python3.15"}, resolve(t, detect.ProjectHints{Language: detect.Python, Version: "3.15"}))
	assert.Equal(t, detect.Resolution{Runtime: "node26"}, resolve(t, detect.ProjectHints{Language: detect.Node, Version: "26"}))
}

func TestResolve_TypeScriptIsTheTsVariantOfTheSameMajor(t *testing.T) {
	assert.Equal(t, detect.Resolution{Runtime: "node22-ts"}, resolve(t, detect.ProjectHints{Language: detect.Node, Version: "22", TypeScript: true}))
	assert.Equal(t, detect.Resolution{Runtime: "node24-ts"}, resolve(t, detect.ProjectHints{Language: detect.Node, TypeScript: true}),
		"no version named: the server's default Node, as TypeScript")
}

func TestResolve_NoVersionNamedIsTheServersDefault(t *testing.T) {
	assert.Equal(t, detect.Resolution{Runtime: "python3.14"}, resolve(t, detect.ProjectHints{Language: detect.Python}))
	assert.Equal(t, detect.Resolution{Runtime: "node24"}, resolve(t, detect.ProjectHints{Language: detect.Node}))
}

func TestResolve_AVersionNoLongerOfferedMovesUpToTheLowestOfferedAndSaysSo(t *testing.T) {
	r := resolve(t, detect.ProjectHints{Language: detect.Node, Version: "20"})
	assert.Equal(t, "node22", r.Runtime)
	assert.Equal(t, "Node 20 is not offered; using node22", r.Note)

	r = resolve(t, detect.ProjectHints{Language: detect.Node, Version: "20", TypeScript: true})
	assert.Equal(t, "node22-ts", r.Runtime)
	assert.Equal(t, "Node 20 is not offered; using node22-ts", r.Note)

	r = resolve(t, detect.ProjectHints{Language: detect.Python, Version: "3.10"})
	assert.Equal(t, "python3.11", r.Runtime)
	assert.Equal(t, "Python 3.10 is not offered; using python3.11", r.Note)
}

func TestResolve_AnOddNonLTSMajorMovesUpToo(t *testing.T) {
	r := resolve(t, detect.ProjectHints{Language: detect.Node, Version: "23"})
	assert.Equal(t, "node24", r.Runtime, "23 is not offered, so the lowest offered above it")
	assert.NotEmpty(t, r.Note)
}

func TestResolve_AVersionNewerThanAnyOfferedGetsTheNewestAndSaysSo(t *testing.T) {
	r := resolve(t, detect.ProjectHints{Language: detect.Python, Version: "3.99"})
	assert.Equal(t, "python3.15", r.Runtime)
	assert.Equal(t, "Python 3.99 is newer than any offered; using python3.15", r.Note)

	r = resolve(t, detect.ProjectHints{Language: detect.Node, Version: "40", TypeScript: true})
	assert.Equal(t, "node26-ts", r.Runtime)
	assert.Equal(t, "Node 40 is newer than any offered; using node26-ts", r.Note)
}

func TestResolve_ModesAreTheirOwnRuntime(t *testing.T) {
	assert.Equal(t, detect.Resolution{Runtime: "bun"}, resolve(t, detect.ProjectHints{Language: detect.Bun}))
	assert.Equal(t, detect.Resolution{Runtime: "dockerfile"}, resolve(t, detect.ProjectHints{Language: detect.Dockerfile}))
}

func TestResolve_NothingRecognisedResolvesToNothing(t *testing.T) {
	assert.Equal(t, detect.Resolution{}, resolve(t, detect.ProjectHints{}))
}

func TestResolve_ARuntimeListThatCannotAnswerIsAnError(t *testing.T) {
	_, err := detect.Resolve(detect.ProjectHints{Language: detect.Node, Version: "22"}, []string{"python3.14"}, defaultRuntimes)
	assert.EqualError(t, err, "the server offers no node runtime")

	_, err = detect.Resolve(detect.ProjectHints{Language: detect.Python}, offeredRuntimes, map[string]string{})
	assert.EqualError(t, err, "the server named no default python runtime")

	_, err = detect.Resolve(detect.ProjectHints{Language: detect.Python}, offeredRuntimes, map[string]string{"python": "python3.2"})
	assert.EqualError(t, err, "the server's default python runtime, python3.2, is not one it offers")

	_, err = detect.Resolve(detect.ProjectHints{Language: detect.Bun}, []string{"python3.14"}, defaultRuntimes)
	assert.EqualError(t, err, "the server does not offer the bun runtime")
}

// A runtime the server adds is picked up with no CLI release: the CLI holds no list.
func TestResolve_ARuntimeTheServerAddsIsResolvedWithNoCliChange(t *testing.T) {
	offered := append([]string{"python3.16"}, offeredRuntimes...)
	r, err := detect.Resolve(detect.ProjectHints{Language: detect.Python, Version: "3.16"}, offered, defaultRuntimes)
	assert.NoError(t, err)
	assert.Equal(t, detect.Resolution{Runtime: "python3.16"}, r)
}
