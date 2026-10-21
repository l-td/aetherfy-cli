package cmd

import (
	"strings"
	"testing"
)

// A yaml that declares `name:`, `description:` or `workspace:` is refused
// BEFORE the upload, with the control plane's exact sentence (held to
// shared/config_parser.py by archive's pair test), and nothing is sent.

func TestDeployRefusesAPlatformKeyBeforeUploadingWithTheControlPlanesSentence(t *testing.T) {
	dir := agentFolder(t, "runtime: python3.11\nentrypoint: main.py\nname: invoice-parser\nworkspace: acme\n")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := deployCP(t)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
	if code == 0 {
		t.Fatalf("a yaml declaring a platform key must be refused\n%s%s", stdout, stderr)
	}
	want := "aetherfy.yaml says how the agent runs, not what it is called or where it lives. Remove " +
		"`name:` (the agent's name is set on the platform: rename it with `afy update <agent> --name <new>` or in the dashboard); " +
		"`workspace:` (the workspace an agent lives in is set on the platform: move it with " +
		"`afy update <agent> --workspace <name>` or the dashboard's Move)."
	if !strings.Contains(stderr, want) {
		t.Errorf("want the control plane's sentence:\n%s\nin:\n%s", want, stderr)
	}
	if strings.Contains(stderr, "afy init --agent") {
		t.Errorf("the file exists: the 'create one' help does not apply:\n%s", stderr)
	}
	if n := deploysSent(cp); n != 0 {
		t.Errorf("nothing may be uploaded, got %d deploy(s)", n)
	}
}

func TestDiffRefusesAPlatformKeyInTheControlPlanesSentence(t *testing.T) {
	dir := agentFolder(t, "runtime: python3.11\nentrypoint: main.py\ndescription: parses invoices\n")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := deployCP(t)

	_, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir)
	if code == 0 {
		t.Fatalf("a yaml declaring a platform key must be refused by diff too\n%s", stderr)
	}
	if !strings.Contains(stderr, "`description:` (the agent's description is set on the platform: `afy update <agent> --description <text>` or the dashboard)") {
		t.Errorf("stderr:\n%s", stderr)
	}
	for _, c := range cp.all() {
		if strings.HasSuffix(c.Path, "/yaml") || strings.Contains(c.Path, "/export") {
			t.Errorf("nothing is fetched for a file a deploy refuses: %+v", c)
		}
	}
}
