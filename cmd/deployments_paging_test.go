package cmd

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// `afy deployments` reads one PAGE of the history: the flags are the server's
// query (since, status, limit, before_id), the last line says how to continue,
// and a page that is not the first unfiltered one never talks about "the
// latest deployment".

const failedLatestRows = `[` +
	`{"id":"d3","agent_id":"a1","version":3,"state":"failed","error_message":"build broke","created_at":"2026-10-14T10:00:00Z"},` +
	`{"id":"d2","agent_id":"a1","version":2,"state":"superseded","can_rollback":true,"created_at":"2026-10-13T10:00:00Z"}]`

func pageWithNext(rows, next string) string {
	return `{"deployments":` + rows + `,"next_before_id":"` + next + `"}`
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\r\n \t"), "\n")
	return strings.TrimRight(lines[len(lines)-1], "\r")
}

func deploymentsQueryOf(t *testing.T, cp *fakeCP) url.Values {
	t.Helper()
	calls := cp.called("GET", "/agents/bot/deployments")
	if len(calls) != 1 {
		t.Fatalf("want exactly one read of the deployment list, got %d: %+v", len(calls), cp.all())
	}
	q, err := url.ParseQuery(calls[0].Query)
	if err != nil {
		t.Fatalf("the query %q does not parse: %v", calls[0].Query, err)
	}
	return q
}

func TestDeploymentsFlagsReachTheServerAsQueryParameters(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/deployments", 200, deploymentsPage(failedLatestRows))
	if _, _, err := runAfy(t, cp, "deployments", "bot", "--since", "24h", "--status", "failed", "--limit", "5", "--before", "dep-9"); err != nil {
		t.Fatalf("deployments: %v", err)
	}
	q := deploymentsQueryOf(t, cp)
	for k, want := range map[string]string{"since": "24h", "status": "failed", "limit": "5", "before_id": "dep-9"} {
		if got := q.Get(k); got != want {
			t.Errorf("the server was sent %s=%q, want %q (query %v)", k, got, want, q)
		}
	}
}

func TestDeploymentsWithNoFlagsAsksOnlyForTheDefaultPageSize(t *testing.T) {
	// THE CONTROL of the test above: a filter the person did not give is not sent.
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/deployments", 200, deploymentsPage(failedLatestRows))
	if _, _, err := runAfy(t, cp, "deployments", "bot"); err != nil {
		t.Fatalf("deployments: %v", err)
	}
	q := deploymentsQueryOf(t, cp)
	if len(q) != 1 || q.Get("limit") != "20" {
		t.Errorf("query = %v, want only limit=20", q)
	}
}

func TestDeploymentsRefusesFlagsTheCLICanJudgeBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"limit 0", []string{"--limit", "0"}, "--limit must be a whole number from 1 to 100, got 0"},
		{"limit 101", []string{"--limit", "101"}, "--limit must be a whole number from 1 to 100, got 101"},
		{"limit negative", []string{"--limit", "-3"}, "--limit must be a whole number from 1 to 100, got -3"},
		{"since 5x", []string{"--since", "5x"}, "--since must be a whole number followed by s, m, h or d, for example 30m, 24h or 7d"},
		{"since h", []string{"--since", "h"}, "--since must be a whole number followed by s, m, h or d"},
		{"since 1.5h", []string{"--since", "1.5h"}, "--since must be a whole number followed by s, m, h or d"},
		{"since empty", []string{"--since", ""}, "--since needs a value"},
		{"before empty", []string{"--before", ""}, "--before needs a value"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cp := newFakeCP(t)
			cp.on("GET", "/agents/bot/deployments", 200, noDeploymentsPage)
			stdout, stderr, err := runAfy(t, cp, append([]string{"deployments", "bot"}, tc.args...)...)
			if err == nil {
				t.Fatalf("the flags were accepted:\n%s", stdout)
			}
			if got := ExitCode(err); got != exitInputRefused {
				t.Errorf("exit code %d, want %d (input refused)", got, exitInputRefused)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr does not say %q:\n%s", tc.want, stderr)
			}
			if calls := cp.all(); len(calls) != 0 {
				t.Errorf("a refused command line still made requests: %+v", calls)
			}
		})
	}
}

func TestDeploymentsAcceptsTheEdgesOfWhatItRefuses(t *testing.T) {
	// THE CONTROL of the refusals above: the values just inside the rule go through.
	for _, args := range [][]string{
		{"--limit", "1"}, {"--limit", "100"},
		{"--since", "30s"}, {"--since", "30m"}, {"--since", "24h"}, {"--since", "7d"},
	} {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot/deployments", 200, noDeploymentsPage)
		if _, stderr, err := runAfy(t, cp, append([]string{"deployments", "bot"}, args...)...); err != nil {
			t.Errorf("%v was refused: %v\n%s", args, err, stderr)
		}
		if n := len(cp.called("GET", "/agents/bot/deployments")); n != 1 {
			t.Errorf("%v made %d reads of the list, want 1", args, n)
		}
	}
}

func TestDeploymentsLastLineSaysHowToContinueOnlyWhenOlderOnesExist(t *testing.T) {
	// Even with a push waiting (its line also follows the table), the
	// continuation is the very last line.
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot", 200, statusAgentJSON(waitingLink))
	cp.on("GET", "/agents/bot/deployments", 200, pageWithNext(failedLatestRows, "dep-older-2"))
	stdout, _, err := runAfy(t, cp, "deployments", "bot")
	if err != nil {
		t.Fatalf("deployments: %v", err)
	}
	if got, want := lastLine(stdout), "More deployments: pass --before dep-older-2 for the next page."; got != want {
		t.Errorf("last line = %q, want %q\n%s", got, want, stdout)
	}
	if !strings.Contains(stdout, "Push waiting") {
		t.Errorf("the unfiltered first page must still name the waiting push:\n%s", stdout)
	}

	// The last page: nothing to continue.
	cp = newFakeCP(t)
	cp.on("GET", "/agents/bot", 200, statusAgentJSON(waitingLink))
	cp.on("GET", "/agents/bot/deployments", 200, deploymentsPage(failedLatestRows))
	stdout, _, err = runAfy(t, cp, "deployments", "bot")
	if err != nil {
		t.Fatalf("deployments: %v", err)
	}
	if strings.Contains(stdout, "More deployments") {
		t.Errorf("with next_before_id null there is no next page to name:\n%s", stdout)
	}
}

func TestDeploymentsJSONIsADocumentCarryingTheNextPageId(t *testing.T) {
	decode := func(t *testing.T, body string) map[string]any {
		t.Helper()
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot/deployments", 200, body)
		stdout, _, err := runAfy(t, cp, "deployments", "bot", "--json")
		if err != nil {
			t.Fatalf("deployments --json: %v", err)
		}
		doc, ok := oneJSONDocument(t, "deployments --json", stdout).(map[string]any)
		if !ok {
			t.Fatalf("--json is not an object: %s", stdout)
		}
		return doc
	}

	doc := decode(t, pageWithNext(failedLatestRows, "dep-older-2"))
	if doc["next_before_id"] != "dep-older-2" {
		t.Errorf("next_before_id = %v, want the id the server gave", doc["next_before_id"])
	}
	if rows, _ := doc["deployments"].([]any); len(rows) != 2 {
		t.Errorf("deployments = %v, want the two rows", doc["deployments"])
	}

	doc = decode(t, deploymentsPage(failedLatestRows))
	if v, present := doc["next_before_id"]; !present || v != nil {
		t.Errorf("a last page must say next_before_id null, got present=%v value=%v", present, v)
	}

	doc = decode(t, noDeploymentsPage)
	raw, _ := json.Marshal(doc["deployments"])
	if string(raw) != "[]" {
		t.Errorf("an empty page must say deployments [], got %s", raw)
	}
}

func TestOnlyTheUnfilteredFirstPageSpeaksOfTheLatestDeployment(t *testing.T) {
	// The newest row of a filtered or later page is not the agent's latest
	// deployment, so "Latest deployment failed." and the rollback suggestion
	// would be false there.
	run := func(args ...string) string {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot", 200, statusAgentJSON(waitingLink))
		cp.on("GET", "/agents/bot/deployments", 200, deploymentsPage(failedLatestRows))
		stdout, _, err := runAfy(t, cp, append([]string{"deployments", "bot"}, args...)...)
		if err != nil {
			t.Fatalf("deployments %v: %v", args, err)
		}
		return stdout
	}

	// The control: the first unfiltered page (with and without a page size) does.
	for _, args := range [][]string{nil, {"--limit", "5"}} {
		out := run(args...)
		if !strings.Contains(out, "Latest deployment failed.") || !strings.Contains(out, "Roll back to v2") {
			t.Errorf("afy deployments %v: the first page must give the failed-latest hint:\n%s", args, out)
		}
		if !strings.Contains(out, "Push waiting") {
			t.Errorf("afy deployments %v: the first page must name the waiting push:\n%s", args, out)
		}
	}

	for _, args := range [][]string{{"--status", "failed"}, {"--since", "7d"}, {"--before", "dep-9"}} {
		out := run(args...)
		for _, banned := range []string{"Latest deployment failed.", "Roll back to", "Push waiting"} {
			if strings.Contains(out, banned) {
				t.Errorf("afy deployments %v is not the latest page, yet prints %q:\n%s", args, banned, out)
			}
		}
		if !strings.Contains(out, "v3") {
			t.Errorf("afy deployments %v: the rows are still listed:\n%s", args, out)
		}
	}
}

func TestOnlyTheUnfilteredFirstPageSpeaksOfADegradedLatestDeployment(t *testing.T) {
	degraded := `[{"id":"d3","agent_id":"a1","version":3,"state":"active","is_degraded":true,"regions_ready":1,"regions_total":2,"created_at":"2026-10-14T10:00:00Z"}]`
	run := func(args ...string) string {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot", 200, statusAgentJSON(""))
		cp.on("GET", "/agents/bot/deployments", 200, deploymentsPage(degraded))
		stdout, _, err := runAfy(t, cp, append([]string{"deployments", "bot"}, args...)...)
		if err != nil {
			t.Fatalf("deployments %v: %v", args, err)
		}
		return stdout
	}
	if out := run(); !strings.Contains(out, "Latest deployment is partially deployed") {
		t.Errorf("the first page must give the degraded hint:\n%s", out)
	}
	if out := run("--status", "active"); strings.Contains(out, "Latest deployment is partially deployed") {
		t.Errorf("a filtered page must not call its first row the latest:\n%s", out)
	}
}

func TestAnEmptyFilteredPageSaysNothingMatchesNotHowToDeploy(t *testing.T) {
	run := func(args ...string) string {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot/deployments", 200, noDeploymentsPage)
		stdout, _, err := runAfy(t, cp, append([]string{"deployments", "bot"}, args...)...)
		if err != nil {
			t.Fatalf("deployments %v: %v", args, err)
		}
		return stdout
	}
	for _, args := range [][]string{{"--status", "failed"}, {"--since", "30m"}, {"--before", "dep-9"}} {
		out := run(args...)
		if !strings.Contains(out, "No deployments match.") {
			t.Errorf("afy deployments %v: want the plain sentence:\n%s", args, out)
		}
		if strings.Contains(out, "afy init") || strings.Contains(out, "No deployments found") {
			t.Errorf("afy deployments %v: an empty filtered page is not an agent with no deployments:\n%s", args, out)
		}
	}
	// The control: the unfiltered first page keeps its deploy hint.
	if out := run(); !strings.Contains(out, "No deployments found for agent 'bot'") || !strings.Contains(out, "afy init --agent bot") {
		t.Errorf("the unfiltered empty page lost its deploy hint:\n%s", out)
	}
}
