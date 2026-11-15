package test

import (
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/detect"
	"github.com/stretchr/testify/assert"
)

// What the platform offers, as GET /deploy-options sends it (the control plane's runtime table): the
// names, and for each language runtime its fields. Held here as a stand-in for the server's answer:
// the CLI carries no list of its own, and picks a runtime by these fields, so they are the inputs of
// every case below.
var offeredRuntimes = []string{
	"python3.11", "python3.12", "python3.13", "python3.14", "python3.15",
	"node22", "node24", "node26", "node22-ts", "node24-ts", "node26-ts",
	"bun", "dockerfile",
}

var defaultRuntimes = map[string]string{"python": "python3.14", "node": "node24"}

func python(version string) api.RuntimeDetail {
	return api.RuntimeDetail{Language: "python", Version: version, Entrypoint: "main.py"}
}

func node(major string, typescript bool) api.RuntimeDetail {
	entry := "index.js"
	if typescript {
		entry = "index.ts"
	}
	return api.RuntimeDetail{Language: "node", Version: major, TypeScript: typescript, Entrypoint: entry}
}

var runtimeDetails = map[string]api.RuntimeDetail{
	"python3.11": python("3.11"), "python3.12": python("3.12"), "python3.13": python("3.13"),
	"python3.14": python("3.14"), "python3.15": python("3.15"),
	"node22": node("22", false), "node24": node("24", false), "node26": node("26", false),
	"node22-ts": node("22", true), "node24-ts": node("24", true), "node26-ts": node("26", true),
	"bun": {Language: "bun", Version: "1", Entrypoint: "main.ts"},
}

func resolve(t *testing.T, h detect.ProjectHints) detect.Resolution {
	t.Helper()
	r, err := detect.Resolve(h, offeredRuntimes, defaultRuntimes, runtimeDetails)
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

func TestResolve_AVersionIsComparedAsNumbersNotText(t *testing.T) {
	// "3.9" is a lower Python than "3.11" though it sorts after it as text; "100" is a higher Node than "22".
	offered := []string{"p-3-9", "p-3-11", "n-100", "n-22"}
	details := map[string]api.RuntimeDetail{
		"p-3-9": python("3.9"), "p-3-11": python("3.11"), "n-100": node("100", false), "n-22": node("22", false),
	}
	r, err := detect.Resolve(detect.ProjectHints{Language: detect.Python, Version: "3.10"}, offered, nil, details)
	assert.NoError(t, err)
	assert.Equal(t, "p-3-11", r.Runtime, "the lowest offered above 3.10 is 3.11, not 3.9")
	r, err = detect.Resolve(detect.ProjectHints{Language: detect.Node, Version: "50"}, offered, nil, details)
	assert.NoError(t, err)
	assert.Equal(t, "n-100", r.Runtime)
}

func TestResolve_BunAndTheDockerfileAreResolvedToo(t *testing.T) {
	assert.Equal(t, detect.Resolution{Runtime: "bun"}, resolve(t, detect.ProjectHints{Language: detect.Bun}),
		"Bun names no default: it is the one runtime of its language the server offers")
	assert.Equal(t, detect.Resolution{Runtime: "dockerfile"}, resolve(t, detect.ProjectHints{Language: detect.Dockerfile}))
}

func TestResolve_NothingRecognisedResolvesToNothing(t *testing.T) {
	assert.Equal(t, detect.Resolution{}, resolve(t, detect.ProjectHints{}))
}

func TestResolve_ARuntimeListThatCannotAnswerIsAnError(t *testing.T) {
	_, err := detect.Resolve(detect.ProjectHints{Language: detect.Node, Version: "22"}, []string{"python3.14"}, defaultRuntimes, runtimeDetails)
	assert.EqualError(t, err, "the server offers no node runtime")

	_, err = detect.Resolve(detect.ProjectHints{Language: detect.Python}, offeredRuntimes, map[string]string{}, runtimeDetails)
	assert.EqualError(t, err, "the server named no default python runtime")

	_, err = detect.Resolve(detect.ProjectHints{Language: detect.Python}, offeredRuntimes, map[string]string{"python": "python3.2"}, runtimeDetails)
	assert.EqualError(t, err, "the server's default python runtime, python3.2, is not one it offers")

	_, err = detect.Resolve(detect.ProjectHints{Language: detect.Bun}, []string{"python3.14"}, defaultRuntimes, runtimeDetails)
	assert.EqualError(t, err, "the server offers no bun runtime")

	_, err = detect.Resolve(detect.ProjectHints{Language: detect.Dockerfile}, []string{"python3.14"}, defaultRuntimes, runtimeDetails)
	assert.EqualError(t, err, "the server does not offer the dockerfile runtime")

	_, err = detect.Resolve(detect.ProjectHints{Language: detect.Python, Version: "three"}, offeredRuntimes, defaultRuntimes, runtimeDetails)
	assert.EqualError(t, err, `cannot read the python version "three"`)
}

// A runtime listed with no fields is not one a project can be resolved to: it is picked by --runtime or not at all.
func TestResolve_ARuntimeTheServerSendsNoFieldsForIsNotResolvedTo(t *testing.T) {
	_, err := detect.Resolve(detect.ProjectHints{Language: detect.Node, Version: "22"}, offeredRuntimes, defaultRuntimes, map[string]api.RuntimeDetail{})
	assert.EqualError(t, err, "the server offers no node runtime")
}

// A runtime the server adds is picked up with no CLI release, WHATEVER its name: the CLI chooses by the fields.
func TestResolve_ARuntimeOfANewNameShapeIsResolvedByItsFields(t *testing.T) {
	offered := []string{"cpython-316", "wb-26", "tsx-26", "dockerfile"}
	details := map[string]api.RuntimeDetail{
		"cpython-316": {Language: "python", Version: "3.16", Entrypoint: "app.py"},
		"wb-26":       {Language: "node", Version: "26", Entrypoint: "serve.js"},
		"tsx-26":      {Language: "node", Version: "26", TypeScript: true, Entrypoint: "main.ts"},
	}
	defaults := map[string]string{"python": "cpython-316", "node": "wb-26"}
	for _, c := range []struct {
		name  string
		hints detect.ProjectHints
		want  string
	}{
		{"Python, the version it names", detect.ProjectHints{Language: detect.Python, Version: "3.16"}, "cpython-316"},
		{"Python, no version", detect.ProjectHints{Language: detect.Python}, "cpython-316"},
		{"Node, the version it names", detect.ProjectHints{Language: detect.Node, Version: "26"}, "wb-26"},
		{"TypeScript, the version it names", detect.ProjectHints{Language: detect.Node, Version: "26", TypeScript: true}, "tsx-26"},
		{"TypeScript, no version: the default's version, as TypeScript", detect.ProjectHints{Language: detect.Node, TypeScript: true}, "tsx-26"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := detect.Resolve(c.hints, offered, defaults, details)
			assert.NoError(t, err)
			assert.Equal(t, detect.Resolution{Runtime: c.want}, r)
		})
	}
}

// The control that the name is never read: a runtime NAMED like one language but DESCRIBED as another is the second.
func TestResolve_TheNameIsNeverRead(t *testing.T) {
	offered := []string{"node99", "python3.99-ts"}
	details := map[string]api.RuntimeDetail{
		"node99":        {Language: "python", Version: "3.99", Entrypoint: "main.py"},
		"python3.99-ts": {Language: "node", Version: "99", TypeScript: true, Entrypoint: "index.ts"},
	}
	r, err := detect.Resolve(detect.ProjectHints{Language: detect.Python, Version: "3.99"}, offered, nil, details)
	assert.NoError(t, err)
	assert.Equal(t, detect.Resolution{Runtime: "node99"}, r, "described as Python, so Python, whatever its name says")
	r, err = detect.Resolve(detect.ProjectHints{Language: detect.Node, Version: "99", TypeScript: true}, offered, nil, details)
	assert.NoError(t, err)
	assert.Equal(t, detect.Resolution{Runtime: "python3.99-ts"}, r, "described as TypeScript Node, so that, whatever its name says")
}

func TestResolve_ALanguageWithNoDefaultAndSeveralRuntimesIsAnError(t *testing.T) {
	offered := []string{"a", "b"}
	details := map[string]api.RuntimeDetail{
		"a": {Language: "bun", Version: "1", Entrypoint: "main.ts"}, "b": {Language: "bun", Version: "2", Entrypoint: "main.ts"},
	}
	_, err := detect.Resolve(detect.ProjectHints{Language: detect.Bun}, offered, nil, details)
	assert.EqualError(t, err, "the server named no default bun runtime")
	r, err := detect.Resolve(detect.ProjectHints{Language: detect.Bun, Version: "2"}, offered, nil, details)
	assert.NoError(t, err)
	assert.Equal(t, "b", r.Runtime)
}
