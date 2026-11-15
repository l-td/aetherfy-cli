package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// A runtime past its end of life is a WARNING on the agent and on the release, never an error: the
// request succeeded. The server words it (the dates, what to move to); the CLI prints it as sent after
// `afy deploy` and in `afy status`, and carries it in --json. The CLI holds no runtime name and no date
// of its own: what it prints is what the stand-in sent, so a sentence that names a runtime this binary
// has never heard of is printed the same.

const (
	deprecatedSentence = "zz-lang9 reached its end of life on 2031-02-03. Deploys on it stop on 2031-05-04, " +
		"and agents already running on it keep running. To move, create a new agent on zz-lang10."
	warningJSON = `{"code":"RUNTIME_DEPRECATED","message":"` + deprecatedSentence + `",` +
		`"runtime":"zz-lang9","deploys_stop_on":"2031-05-04","replacement":"zz-lang10"}`
)

func warningDeployCP(t *testing.T, warnings string) *fakeCP {
	t.Helper()
	cp := newFakeCP(t)
	cp.on("GET", "/agents/"+prodID, 200, `{"id":"`+prodID+`","name":"invoice-parser"}`)
	cp.on("POST", "/agents/"+prodID+"/deploy", 202,
		`{"id":"d-1","agent_id":"`+prodID+`","version":3,"state":"pending","warnings":`+warnings+`}`)
	return cp
}

func deployWarningFolder(t *testing.T) string {
	t.Helper()
	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID})
	return dir
}

func TestDeployPrintsTheServersWarningAfterItAcceptsTheRelease(t *testing.T) {
	cp := warningDeployCP(t, `[`+warningJSON+`]`)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", deployWarningFolder(t), "--detach")
	if code != 0 {
		t.Fatalf("a warning is not a failure: exit %d\n%s%s", code, stdout, stderr)
	}
	if want := "Warning: " + deprecatedSentence; !strings.Contains(stdout, want) {
		t.Errorf("afy deploy did not print the server's sentence as sent; stdout:\n%s", stdout)
	}
	if strings.Index(stdout, "Deployment ID") > strings.Index(stdout, "Warning: ") {
		t.Errorf("the warning belongs after the release was accepted, not before it; stdout:\n%s", stdout)
	}
}

func TestControlADeployWithNoWarningPrintsNone(t *testing.T) {
	cp := warningDeployCP(t, `[]`)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", deployWarningFolder(t), "--detach")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "Warning:") {
		t.Errorf("a release with nothing to warn about printed a warning:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Deployment queued.") {
		t.Errorf("the control did not run the deploy to its end:\n%s", stdout)
	}
}

func TestDeployJSONCarriesTheWarningsAndStdoutStaysJSON(t *testing.T) {
	cp := warningDeployCP(t, `[`+warningJSON+`]`)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", deployWarningFolder(t), "--detach", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	var result struct {
		Deployment struct {
			Warnings []struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"warnings"`
		} `json:"deployment"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout is not one JSON document (%v):\n%s", err, stdout)
	}
	if len(result.Deployment.Warnings) != 1 || result.Deployment.Warnings[0].Code != "RUNTIME_DEPRECATED" ||
		result.Deployment.Warnings[0].Message != deprecatedSentence {
		t.Errorf("--json lost the warning: %+v", result.Deployment.Warnings)
	}
}

func statusCP(t *testing.T, warnings string) *fakeCP {
	t.Helper()
	cp := newFakeCP(t)
	cp.on("GET", "/agents/reporter", 200,
		`{"id":"a1","user_id":"u1","name":"reporter","status":"running","agent_type":"service","deployed":true,`+
			`"created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z","warnings":`+warnings+`}`)
	cp.on("GET", "/agents/a1/deployments", 200, `{"deployments":[]}`)
	return cp
}

func TestStatusPrintsTheServersWarning(t *testing.T) {
	stdout, stderr, code := runAfyProcess(t, statusCP(t, `[`+warningJSON+`]`), nil, "status", "reporter")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	var line string
	for _, l := range strings.Split(stdout, "\n") {
		if strings.Contains(l, deprecatedSentence) {
			line = l
		}
	}
	if line == "" || !strings.Contains(line, "Warning") {
		t.Errorf("afy status did not show the sentence on a Warning line; stdout:\n%s", stdout)
	}
}

func TestControlAStatusWithNoWarningShowsNone(t *testing.T) {
	stdout, stderr, code := runAfyProcess(t, statusCP(t, `[]`), nil, "status", "reporter")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "Warning") {
		t.Errorf("an agent with nothing to warn about showed a warning line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "reporter") {
		t.Errorf("the control did not print the status:\n%s", stdout)
	}
}

func TestStatusJSONCarriesTheWarnings(t *testing.T) {
	stdout, stderr, code := runAfyProcess(t, statusCP(t, `[`+warningJSON+`]`), nil, "status", "reporter", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	var agent struct {
		Warnings []struct {
			Code          string `json:"code"`
			DeploysStopOn string `json:"deploys_stop_on"`
			Replacement   string `json:"replacement"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &agent); err != nil {
		t.Fatalf("stdout is not one JSON document (%v):\n%s", err, stdout)
	}
	if len(agent.Warnings) != 1 || agent.Warnings[0].DeploysStopOn != "2031-05-04" || agent.Warnings[0].Replacement != "zz-lang10" {
		t.Errorf("--json lost the warning's fields: %+v", agent.Warnings)
	}
}
