package cmd

import (
	"strings"
	"testing"
)

// afy diff: the local aetherfy.yaml against the configuration of the agent the
// folder links, by id. A field the file leaves out is compared as its default.

func diffCP(t *testing.T) *fakeCP {
	cp := deployCP(t)
	cp.on("GET", "/agents/"+prodID+"/yaml", 200, serverYAML)
	return cp
}

func TestDiffComparesWithTheLinkedAgentsExport(t *testing.T) {
	// serverYAML: python3.11, service, main.py, 1024 MB, idle 5, keep_alive
	// false, eu-central-1, one GitHub dependency.
	dir := agentFolder(t, "runtime: python3.11\nentrypoint: main.py\nmemory_mb: 1024\n"+
		"regions: [eu-central-1]\ngithub_dependencies: [myorg/utils@v1.2.3]\n")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := diffCP(t)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir)
	if code != 0 {
		t.Fatalf("an identical configuration must exit 0, got %d\n%s%s", code, stdout, stderr)
	}
	if len(cp.called("GET", "/agents/"+prodID+"/yaml")) != 1 {
		t.Errorf("the export must be read by the linked id; calls: %+v", cp.all())
	}
	if !strings.Contains(stdout, "= idle_timeout_minutes: 5 (default)") {
		t.Errorf("an omitted field must show its default:\n%s", stdout)
	}
	if strings.Contains(stdout, "preserved") {
		t.Errorf("nothing is preserved any more:\n%s", stdout)
	}
}

func TestDiffShowsAnOmittedFieldGoingToItsDefault(t *testing.T) {
	// memory_mb omitted: the deploy gives it 256, the agent has 1024.
	dir := agentFolder(t, "runtime: python3.11\nentrypoint: main.py\n"+
		"regions: [eu-central-1]\ngithub_dependencies: [myorg/utils@v1.2.3]\n")
	linkFolder(t, dir, map[string]string{"default": prodID})

	stdout, stderr, code := runAfyProcess(t, diffCP(t), nil, "diff", "--path", dir)
	if code != 1 {
		t.Fatalf("a change must exit 1, got %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "~ memory_mb: 1024 → 256 (default)") {
		t.Errorf("want the default as the change's new value:\n%s", stdout)
	}
}

// THE FOLDER `afy init` WRITES diffs clean against the release it made. The
// export (ccc10c6) is that release's recorded configuration, regions and
// github_dependencies included -- here as serialize_release_to_yaml writes it
// for that file -- so they compare like every other field.
const initReleaseExport = `# yaml-language-server: $schema=https://docs.aetherfy.com/aetherfy-yaml.schema.json
runtime: python3.11
type: service
entrypoint: main.py
memory_mb: 1024
idle_timeout_minutes: 5
keep_alive: false
regions:
- eu-central-1
github_dependencies: []
`

func TestAFreshlyInitialisedFolderDiffsClean(t *testing.T) {
	dir := agentFolder(t, buildAetherfyYAML("python3.11", "service", "eu-central-1", 1024, false, "main.py", ""))
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := diffCP(t)
	cp.on("GET", "/agents/"+prodID+"/yaml", 200, initReleaseExport)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir)
	if code != 0 {
		t.Fatalf("an init'd, deployed folder must diff clean; exit %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "= regions: [eu-central-1]") {
		t.Errorf("regions must be compared with the release's:\n%s", stdout)
	}
}

// Regions and dependencies that differ from the release are changes.
func TestDiffShowsRegionsAndDependenciesThatDiffer(t *testing.T) {
	dir := agentFolder(t, "runtime: python3.11\nentrypoint: main.py\nmemory_mb: 1024\n"+
		"regions: [us-east-1]\ngithub_dependencies: [myorg/utils@v2.0.0]\n")
	linkFolder(t, dir, map[string]string{"default": prodID})

	stdout, stderr, code := runAfyProcess(t, diffCP(t), nil, "diff", "--path", dir)
	if code != 1 {
		t.Fatalf("want exit 1, got %d\n%s%s", code, stdout, stderr)
	}
	for _, want := range []string{"~ regions: [eu-central-1] → [us-east-1]", "~ github_dependencies: [myorg/utils@v1.2.3] → [myorg/utils@v2.0.0]"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// A draft (never released) has no configuration to compare with: a plain
// sentence, exit 1, never the raw 404.
func TestDiffOnADraftSaysItHasNoReleaseYet(t *testing.T) {
	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := deployCP(t)
	cp.on("GET", "/agents/"+prodID+"/yaml", 404,
		`{"detail":{"code":"AGENT_NOT_DEPLOYED","message":"Agent 'invoice-parser' has no release to export: it has never been deployed.","agent":"invoice-parser"}}`)

	_, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir)
	if code != 1 || !strings.Contains(stderr, "invoice-parser has no release yet: deploy it first") {
		t.Errorf("want the plain sentence and exit 1; exit %d:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "404") || strings.Contains(stderr, "AGENT_NOT_DEPLOYED") {
		t.Errorf("no raw 404:\n%s", stderr)
	}
}

// A release from before recorded configurations: the server's sentence, as
// written, from both diff and init --agent.
func TestTheExportsReleaseNotOnRecordRefusalIsPrintedAsWritten(t *testing.T) {
	const msg = "Version 2's configuration isn't on record; deploy it again from its source."
	refusal := `{"detail":{"code":"RELEASE_CONFIG_NOT_ON_RECORD","message":"` + msg + `","version":2}}`

	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := deployCP(t)
	cp.on("GET", "/agents/"+prodID+"/yaml", 422, refusal)
	if _, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir); code == 0 || !strings.Contains(stderr, "Error: "+msg+"\n") {
		t.Errorf("diff: exit %d, want the sentence as written:\n%s", code, stderr)
	}

	empty := t.TempDir()
	cp2 := newFakeCP(t)
	cp2.on("GET", "/agents/invoice-parser", 200, `{"id":"`+prodID+`","name":"invoice-parser"}`)
	cp2.on("GET", "/agents/"+prodID+"/yaml", 422, refusal)
	_, stderr, err := runAfy(t, cp2, "init", empty, "--agent", "invoice-parser")
	if err == nil || !strings.Contains(stderr, "Error: "+msg+"\n") {
		t.Errorf("init --agent: %v, want the sentence as written:\n%s", err, stderr)
	}
}

func TestDiffRefusesAnUnlinkedFolderAndAPlatformField(t *testing.T) {
	unlinked := agentFolder(t, "")
	cp := diffCP(t)
	_, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", unlinked)
	if code == 0 || !strings.Contains(stderr, "not linked to an agent") {
		t.Errorf("an unlinked folder must be refused; exit %d: %s", code, stderr)
	}
	// Once, and without a sentence about deploys: this is a diff. (The built
	// binary printed it twice, with "A deploy never creates an agent" on it.)
	if n := strings.Count(stderr, "not linked to an agent"); n != 1 {
		t.Errorf("the refusal must be shown once, got %d:\n%s", n, stderr)
	}
	if strings.Contains(stderr, "never creates an agent") {
		t.Errorf("a diff does not deploy: %s", stderr)
	}

	named := agentFolder(t, "runtime: python3.11\nname: invoice-parser\n")
	linkFolder(t, named, map[string]string{"default": prodID})
	_, stderr, code = runAfyProcess(t, cp, nil, "diff", "--path", named)
	if code == 0 || !strings.Contains(stderr, "declares 'name:', which a deploy refuses") {
		t.Errorf("a yaml declaring name: must be refused; exit %d: %s", code, stderr)
	}
	if len(cp.called("GET", "/agents/"+prodID+"/yaml")) != 0 {
		t.Error("neither case may read an agent's configuration")
	}
}
