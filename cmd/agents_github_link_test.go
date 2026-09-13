package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// `afy status` grew a GitHub section, and every claim it makes is a claim about
// a field it decoded from the server. That is the part worth running rather
// than reading: a wrong json tag, a string where the server sends null, a
// boolean read the wrong way round — all of them compile, and all of them fail
// as a missing or inverted line rather than as an error.
//
// So these tests drive the REAL client against an httptest server and hand the
// decoded value to the real printer. A fixture built in Go would prove the
// fixture.

// linkStatusServer answers GET /agents/{idOrName}/github with body, or 500 when
// body is empty, and records the path it was asked for.
func linkStatusServer(t *testing.T, body string) (*httptest.Server, *string) {
	t.Helper()
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if body == "" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":{"code":"INTERNAL_ERROR","message":"boom"}}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// readLinkStatus performs the real GET through the real helper, so the tests
// hold exactly what the call site in runAgentsStatus holds: a value and a note,
// with no error anywhere to hand back.
func readLinkStatus(t *testing.T, body string) (agentLinkRead, *string) {
	t.Helper()
	srv, asked := linkStatusServer(t, body)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")
	return readAgentGitHubLink(client, "reporter"), asked
}

// captureStderr lives in github_connect_test.go — the same swap of os.Stderr,
// which printNote resolves at call time.

func TestTheLinkNoteGoesToStderrAtAll(t *testing.T) {
	// Positive control on the capture. Every "wrote nothing to stderr"
	// assertion below is worthless if this stream never reaches the harness.
	note := agentLinkRead{note: "link state could not be read: " + io.EOF.Error()}
	if out := captureStderr(t, note.printNote); !strings.Contains(out, "EOF") {
		t.Fatalf("stderr capture saw %q, which does not contain the note it was given", out)
	}
	// And silence when there is nothing to say, which is every healthy status.
	if out := captureStderr(t, agentLinkRead{state: &api.GitHubLinkStatus{}}.printNote); out != "" {
		t.Errorf("a read with no note still wrote to stderr: %q", out)
	}
}

const linkedBody = `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":"services/scraper",` +
	`"webhook_id":"558899","account_connected":true,"branch_deleted_at":null}`

func TestStatusPrintsTheWholeGitHubLink(t *testing.T) {
	read, asked := readLinkStatus(t, linkedBody)
	if read.note != "" {
		t.Fatalf("the read failed: %s", read.note)
	}

	var errOut string
	out := captureStdout(t, func() {
		errOut = captureStderr(t, func() { printAgentGitHubLink(read) })
	})

	// The four fields, each one a separate json tag that could be wrong on its
	// own and would then simply not appear.
	for _, want := range []string{"myorg/agents", "production", "services/scraper", "558899"} {
		if !strings.Contains(out, want) {
			t.Errorf("the GitHub section is missing %q; got:\n%s", want, out)
		}
	}
	// The route the section reads. A wrong path 500s and the section silently
	// becomes a warning, which is exactly the shape of "not linked".
	if *asked != "/agents/reporter/github" {
		t.Errorf("asked for %q, want /agents/reporter/github", *asked)
	}
	// THE CONTROL THE TWO WARNINGS NEED. A healthy link warns about nothing;
	// without this, a warning that fires unconditionally still passes both of
	// the tests below.
	if strings.Contains(out, "pushes are not deploying") {
		t.Errorf("a healthy link printed an inert-state warning:\n%s", out)
	}
	if strings.TrimSpace(errOut) != "" {
		t.Errorf("a healthy link wrote to stderr:\n%s", errOut)
	}
}

func TestStatusCallsAnEmptyRootDirTheRepositoryRoot(t *testing.T) {
	// The server normalises '', '.' and './x/' before storing, so a repo-root
	// link arrives as null. Printing an empty "Directory:" line would read as
	// missing information about a link that is complete.
	body := `{"linked":true,"repo":"myorg/agents","branch":"main","root_dir":null,` +
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":null}`
	read, _ := readLinkStatus(t, body)
	if read.note != "" {
		t.Fatalf("the read failed: %s", read.note)
	}

	out := captureStdout(t, func() { printAgentGitHubLink(read) })
	if !strings.Contains(out, "repository root") {
		t.Errorf("a link at the repository root did not say so; got:\n%s", out)
	}
}

func TestStatusSaysNothingAboutGitHubForAnUnlinkedAgent(t *testing.T) {
	// The negative control. Most agents have no link, and a "GitHub: not
	// linked" line would be a fact about nothing on every status ever run.
	// `linked:false` still carries account_connected:false here — the account
	// can be disconnected while this agent was never linked, and that is not
	// this agent's problem to report.
	body := `{"linked":false,"repo":null,"branch":null,"root_dir":null,` +
		`"webhook_id":null,"account_connected":false,"branch_deleted_at":null}`
	read, _ := readLinkStatus(t, body)
	if read.note != "" {
		t.Fatalf("the read failed: %s", read.note)
	}

	var errOut string
	out := captureStdout(t, func() {
		errOut = captureStderr(t, func() { printAgentGitHubLink(read) })
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("an unlinked agent printed a GitHub section:\n%s", out)
	}
	if strings.TrimSpace(errOut) != "" {
		t.Errorf("an unlinked agent wrote to stderr:\n%s", errOut)
	}
}

func TestStatusSaysWhenTheLinkStateCouldNotBeRead(t *testing.T) {
	// FAIL-SOFT, NOT SILENT. The read hands back a value and a note, with no
	// error in it, so nothing in runAgentsStatus can fail the command over this
	// — the remaining way to get it wrong is to print nothing at all, leaving a
	// linked agent looking exactly like an unlinked one.
	read, _ := readLinkStatus(t, "")
	if read.note == "" {
		t.Fatal("the 500 lane produced no note; the rest of this test would be vacuous")
	}

	var errOut string
	out := captureStdout(t, func() {
		errOut = captureStderr(t, func() { printAgentGitHubLink(read) })
	})
	if !strings.Contains(errOut, "GitHub link state could not be read") {
		t.Errorf("a failed read said nothing; stderr was:\n%s", errOut)
	}
	// The rest of the status block is already on stdout by now. Nothing about
	// the failure belongs there — under `-o json` that stream is the payload.
	if strings.TrimSpace(out) != "" {
		t.Errorf("the failure note landed on stdout, which `-o json` must keep parseable:\n%s", out)
	}
}

func TestStatusWarnsWhenTheAccountIsDisconnected(t *testing.T) {
	// Linked, and inert. Nothing on GitHub reports this: announcing a skipped
	// push means posting a commit status, which needs the installation token
	// that is gone.
	body := `{"linked":true,"repo":"myorg/agents","branch":"main","root_dir":null,` +
		`"webhook_id":"1","account_connected":false,"branch_deleted_at":null}`
	read, _ := readLinkStatus(t, body)
	if read.note != "" {
		t.Fatalf("the read failed: %s", read.note)
	}

	out := captureStdout(t, func() { printAgentGitHubLink(read) })
	if !strings.Contains(out, "pushes are not deploying") {
		t.Errorf("a disconnected account printed no warning; got:\n%s", out)
	}
	// The remedy is the ACCOUNT. This is the one place the CLI may name a
	// command where the dashboard names a settings page.
	if !strings.Contains(out, "afy github connect") {
		t.Errorf("the warning did not name the command that fixes it; got:\n%s", out)
	}
	// The link itself is still printed: it is still true, and it is what comes
	// back to life on reconnect.
	if !strings.Contains(out, "myorg/agents") {
		t.Errorf("the warning replaced the link instead of preceding it; got:\n%s", out)
	}
	// Not the other inert state. The two are separate facts with separate
	// remedies, and one must not stand in for the other.
	if strings.Contains(out, "branch was deleted") {
		t.Errorf("a disconnected account also claimed the branch was deleted:\n%s", out)
	}
}

func TestStatusTreatsAnAbsentAccountConnectedAsConnected(t *testing.T) {
	// The field is `bool = True` server-side, so a response that omits it says
	// connected. Go's zero value says the opposite, and the CLI and the control
	// plane ship on separate schedules — so the untouched-by-default direction
	// decides whether an old binary against a newer server (or the reverse)
	// tells everyone their deploys are broken.
	body := `{"linked":true,"repo":"myorg/agents","branch":"main","root_dir":null,` +
		`"webhook_id":"1","branch_deleted_at":null}`
	read, _ := readLinkStatus(t, body)
	if read.note != "" {
		t.Fatalf("the read failed: %s", read.note)
	}

	out := captureStdout(t, func() { printAgentGitHubLink(read) })
	if strings.Contains(out, "pushes are not deploying") {
		t.Errorf("an absent account_connected was read as a disconnect:\n%s", out)
	}
	// And the same absence reaches a script as the server's own default, not as
	// a null and not as false.
	raw, marshalErr := json.Marshal(agentStatusJSON(&api.Agent{ID: "a1"}, read.state))
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	if !strings.Contains(string(raw), `"account_connected":true`) {
		t.Errorf("`-o json` did not carry the server's default for an absent field: %s", raw)
	}
}

func TestStatusWarnsWhenTheTrackedBranchWasDeleted(t *testing.T) {
	// The sharper half of the pair: a branch deletion has no commit to hang a
	// status on — the SHA GitHub sends is forty zeros — so there is no surface
	// left but this one.
	body := `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":null,` +
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":"2026-09-10T14:32:00Z"}`
	read, _ := readLinkStatus(t, body)
	if read.note != "" {
		t.Fatalf("the read failed: %s", read.note)
	}

	out := captureStdout(t, func() { printAgentGitHubLink(read) })
	if !strings.Contains(out, "pushes are not deploying") {
		t.Errorf("a deleted branch printed no warning; got:\n%s", out)
	}
	// WHICH branch and WHEN — the two things a person needs in order to act,
	// and the reason the server sends a timestamp rather than a boolean.
	for _, want := range []string{"production", "myorg/agents", "2026-09-10 14:32 UTC"} {
		if !strings.Contains(out, want) {
			t.Errorf("the deleted-branch warning is missing %q; got:\n%s", want, out)
		}
	}
	// Relinking to a branch that does not exist SUCCEEDS and changes nothing,
	// so offering it sends someone to fix the wrong thing.
	if strings.Contains(out, "afy github link") {
		t.Errorf("the deleted-branch warning offered a relink, which would change nothing:\n%s", out)
	}
	// Not the other inert state.
	if strings.Contains(out, "no longer connected to GitHub") {
		t.Errorf("a deleted branch also claimed the account was disconnected:\n%s", out)
	}
}

func TestStatusJSONCarriesTheLinkWithTheServersFieldNames(t *testing.T) {
	// `-o json` is a contract with scripts, and the two fields a script would
	// branch on are exactly the two that a well-meaning omitempty deletes when
	// they are false.
	read, _ := readLinkStatus(t, `{"linked":false,"repo":null,"branch":null,"root_dir":null,`+
		`"webhook_id":null,"account_connected":false,"branch_deleted_at":null}`)
	if read.note != "" {
		t.Fatalf("the read failed: %s", read.note)
	}

	raw, marshalErr := json.Marshal(agentStatusJSON(&api.Agent{ID: "a1", Name: "reporter"}, read.state))
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The agent's own keys stay where they were. Nesting them under an "agent"
	// key would break every existing consumer of `afy status -o json`.
	if got["id"] != "a1" || got["name"] != "reporter" {
		t.Errorf("the agent's own fields left the top level: %s", raw)
	}
	gh, ok := got["github"].(map[string]interface{})
	if !ok {
		t.Fatalf("no github object in `afy status -o json`: %s", raw)
	}
	// Present, and false. Not absent.
	for _, key := range []string{"linked", "account_connected"} {
		v, present := gh[key]
		if !present {
			t.Errorf("github.%s is missing — a script cannot read a key that is not there: %s", key, raw)
			continue
		}
		if v != false {
			t.Errorf("github.%s came back %v, want false", key, v)
		}
	}
	// The server's spellings, not Go's.
	for _, key := range []string{"repo", "branch", "root_dir", "webhook_id", "branch_deleted_at"} {
		if _, present := gh[key]; !present {
			t.Errorf("github.%s is missing from %s", key, raw)
		}
	}
}

func TestStatusJSONOmitsTheLinkWhenItCouldNotBeRead(t *testing.T) {
	// The other half of the rule above: because a successful read ALWAYS emits
	// the object, an absent key can only mean "not read". A zero-valued object
	// here would claim `linked: false` about an agent that may well be linked.
	read, _ := readLinkStatus(t, "")
	if read.note == "" {
		t.Fatal("the 500 lane produced no note; this test would be vacuous")
	}

	raw, marshalErr := json.Marshal(agentStatusJSON(&api.Agent{ID: "a1", Name: "reporter"}, read.state))
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	if strings.Contains(string(raw), "github") {
		t.Errorf("a failed read still emitted a github key, which would read as an answer: %s", raw)
	}
}
