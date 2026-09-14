package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
)

// `afy status` prints an agent's GitHub link, and every claim that section makes
// is a claim about a field decoded from the server. That is the part worth
// running rather than reading: a wrong json tag, a string where the server sends
// null, a boolean read the wrong way round — all of them compile, and all of
// them fail as a missing or inverted line rather than as an error.
//
// So these tests drive the REAL command body against an httptest server that
// answers with an AGENT carrying its link, and they count what the command asks
// for. The link used to cost a second request; it now arrives inside the agent,
// and a second request creeping back is what the count exists to catch.

// statusAgentJSON is a service agent as GET /agents/{name} returns it, with link
// under "github" — or with no "github" key at all when link is empty, which is
// what a control plane older than the nested object sends.
//
// A SERVICE, deliberately: for a job agent `afy status` also lists agents to
// resolve its spawn relationships, which is a request about something else.
func statusAgentJSON(link string) string {
	body := `{"id":"a1","user_id":"u1","name":"reporter","status":"running",` +
		`"agent_type":"service","spawn_enabled":false,"deployed":true,` +
		`"created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z"`
	if link != "" {
		body += `,"github":` + link
	}
	return body + "}"
}

// runStatus runs `afy status reporter` in the given output format against a
// server that answers every request with that agent, and returns what reached
// stdout together with every path the command requested.
func runStatus(t *testing.T, format, link string) (string, []string) {
	t.Helper()
	var (
		mu    sync.Mutex
		asked []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(statusAgentJSON(link)))
	}))
	t.Cleanup(srv.Close)

	previous := config.Get().OutputFormat
	config.SetOutputFormat(format)
	t.Cleanup(func() { config.SetOutputFormat(previous) })

	var runErr error
	out := captureStdout(t, func() {
		runErr = showAgentStatus(api.NewClientWithURL(srv.URL, "afy_test_key"), "reporter")
	})
	if runErr != nil {
		t.Fatalf("afy status failed: %v", runErr)
	}

	mu.Lock()
	defer mu.Unlock()
	return out, append([]string(nil), asked...)
}

// sectionLabels are the GitHub section's own lines. Asserted present for a
// linked agent, which is what makes asserting them ABSENT mean anything.
var sectionLabels = []string{"Repo:", "Branch:", "Directory:", "Webhook id:"}

const linkedJSON = `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":"services/scraper",` +
	`"webhook_id":"558899","account_connected":true,"branch_deleted_at":null}`

func TestStatusReadsTheLinkFromTheAgentInOneRequest(t *testing.T) {
	out, asked := runStatus(t, "text", linkedJSON)

	// ONE request, and it is for the agent. Asking the link route as well is the
	// second read this command no longer makes.
	if len(asked) != 1 || asked[0] != "/agents/reporter" {
		t.Errorf("afy status requested %v, want exactly [/agents/reporter]", asked)
	}
	// The four fields, each a separate json tag that could be wrong on its own
	// and would then simply not appear.
	for _, want := range append([]string{"myorg/agents", "production", "services/scraper", "558899"}, sectionLabels...) {
		if !strings.Contains(out, want) {
			t.Errorf("the GitHub section is missing %q; got:\n%s", want, out)
		}
	}
	// THE CONTROL THE TWO WARNINGS NEED. A healthy link warns about nothing;
	// without this, a warning that fires unconditionally still passes both of
	// the warning tests below.
	if strings.Contains(out, "pushes are not deploying") {
		t.Errorf("a healthy link printed an inert-state warning:\n%s", out)
	}
}

func TestStatusCallsAnEmptyRootDirTheRepositoryRoot(t *testing.T) {
	// The server normalises '', '.' and './x/' before storing, so a repo-root
	// link arrives as null. Printing an empty "Directory:" line would read as
	// missing information about a link that is complete.
	out, _ := runStatus(t, "text", `{"linked":true,"repo":"myorg/agents","branch":"main","root_dir":null,`+
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":null}`)
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
	out, _ := runStatus(t, "text", `{"linked":false,"repo":null,"branch":null,"root_dir":null,`+
		`"webhook_id":null,"account_connected":false,"branch_deleted_at":null}`)

	// Positive control: the status itself printed, so the absences below are
	// absences from real output and not from an empty capture.
	if !strings.Contains(out, "reporter") {
		t.Fatalf("the status block did not print at all; got:\n%s", out)
	}
	for _, unwanted := range append([]string{"pushes are not deploying"}, sectionLabels...) {
		if strings.Contains(out, unwanted) {
			t.Errorf("an unlinked agent printed %q:\n%s", unwanted, out)
		}
	}
}

func TestStatusSaysNothingAboutGitHubWhenTheAgentCarriesNoLink(t *testing.T) {
	// A control plane older than the nested object sends no "github" key. That
	// reads as nothing to say — and `-o json` must not invent an answer either:
	// a zero-valued object would claim `linked: false` and `account_connected:
	// false` about an agent the server said nothing about.
	out, _ := runStatus(t, "text", "")
	if !strings.Contains(out, "reporter") {
		t.Fatalf("the status block did not print at all; got:\n%s", out)
	}
	for _, unwanted := range append([]string{"pushes are not deploying"}, sectionLabels...) {
		if strings.Contains(out, unwanted) {
			t.Errorf("an agent with no link object printed %q:\n%s", unwanted, out)
		}
	}

	raw, _ := runStatus(t, "json", "")
	if !strings.Contains(raw, `"id": "a1"`) {
		t.Fatalf("`afy status -o json` did not print the agent; got:\n%s", raw)
	}
	if strings.Contains(raw, `"github"`) {
		t.Errorf("an agent with no link object still emitted a github key, which reads as an answer:\n%s", raw)
	}
}

func TestStatusWarnsWhenTheAccountIsDisconnected(t *testing.T) {
	// Linked, and inert. Nothing on GitHub reports this: announcing a skipped
	// push means posting a commit status, which needs the installation token
	// that is gone.
	out, _ := runStatus(t, "text", `{"linked":true,"repo":"myorg/agents","branch":"main","root_dir":null,`+
		`"webhook_id":"1","account_connected":false,"branch_deleted_at":null}`)

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
		t.Errorf("the warning replaced the link instead of following it; got:\n%s", out)
	}
	// Not the other inert state. The two are separate facts with separate
	// remedies, and one must not stand in for the other.
	if strings.Contains(out, "branch was deleted") {
		t.Errorf("a disconnected account also claimed the branch was deleted:\n%s", out)
	}
}

func TestStatusWarnsWhenTheTrackedBranchWasDeleted(t *testing.T) {
	// The sharper half of the pair: a branch deletion has no commit to hang a
	// status on — the SHA GitHub sends is forty zeros — so there is no surface
	// left but this one.
	out, _ := runStatus(t, "text", `{"linked":true,"repo":"myorg/agents","branch":"production","root_dir":null,`+
		`"webhook_id":"1","account_connected":true,"branch_deleted_at":"2026-09-10T14:32:00Z"}`)

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
	raw, asked := runStatus(t, "json", `{"linked":false,"repo":null,"branch":null,"root_dir":null,`+
		`"webhook_id":null,"account_connected":false,"branch_deleted_at":null}`)

	// One request in this lane too.
	if len(asked) != 1 || asked[0] != "/agents/reporter" {
		t.Errorf("afy status -o json requested %v, want exactly [/agents/reporter]", asked)
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("`afy status -o json` is not JSON: %v\n%s", err, raw)
	}
	// The agent's own keys stay at the top level. Nesting them under an "agent"
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
