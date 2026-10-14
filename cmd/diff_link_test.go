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
	// serverYAML: python3.11, service, 1024 MB, idle 5, keep_alive false, main.py.
	dir := agentFolder(t, "runtime: python3.11\nentrypoint: main.py\nmemory_mb: 1024\n")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := diffCP(t)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir)
	if code != 0 {
		t.Fatalf("an identical configuration must exit 0, got %d\n%s%s", code, stdout, stderr)
	}
	if len(cp.called("GET", "/agents/"+prodID+"/yaml")) != 1 {
		t.Errorf("the export must be read by the linked id; calls: %+v", cp.calls)
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
	dir := agentFolder(t, "runtime: python3.11\nentrypoint: main.py\n")
	linkFolder(t, dir, map[string]string{"default": prodID})

	stdout, stderr, code := runAfyProcess(t, diffCP(t), nil, "diff", "--path", dir)
	if code != 1 {
		t.Fatalf("a change must exit 1, got %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "~ memory_mb: 1024 → 256 (default)") {
		t.Errorf("want the default as the change's new value:\n%s", stdout)
	}
}

func TestDiffRefusesAnUnlinkedFolderAndAPlatformField(t *testing.T) {
	unlinked := agentFolder(t, "")
	cp := diffCP(t)
	_, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", unlinked)
	if code == 0 || !strings.Contains(stderr, "not linked to an agent") {
		t.Errorf("an unlinked folder must be refused; exit %d: %s", code, stderr)
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
