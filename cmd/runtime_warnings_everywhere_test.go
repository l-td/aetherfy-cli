package cmd

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// EVERY COMMAND WHOSE ANSWER CARRIES `warnings` PRINTS THEM, wherever they sit in the answer: on the release a
// deploy, a redeploy, a rollback or a cancel answers with; on the agent `status`, `create` and `update` read;
// inside a move, on the redeploy it started (`deployment.warnings`); and in the two lists. The server words each
// sentence; the CLI prints it as sent and holds no runtime name and no date. A warning is not an error: no exit
// code changes.
//
// Two kinds of test hold that. Each command has a case that sends the answer with a warning and finds the sentence
// in what it prints, and a control that sends it with none and finds nothing. And a census of the call sites holds
// the rest: every call of a client method that returns an agent or a release is classified, so a command added
// tomorrow that reads one and says nothing of its warnings is red until someone decides.

const (
	twoWarnings = `[` + warningJSON + `]`
	noWarnings  = `[]`
)

// answering serves one POST route with one answer and anything else as 404.
func answering(t *testing.T, suffix string, status int, body string) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, suffix) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return api.NewClientWithURL(srv.URL, "afy_test_key")
}

func printedWarning(out string) bool { return strings.Contains(out, "Warning: "+deprecatedSentence) }

func released(warnings string) string {
	return `{"id":"d-4","agent_id":"a-1","version":4,"state":"queued","warnings":` + warnings + `}`
}

// ── a release: redeploy, rollback ────────────────────────────────────────────────────────────

func TestRedeployPrintsTheServersWarning(t *testing.T) {
	for warnings, want := range map[string]bool{twoWarnings: true, noWarnings: false} {
		client := answering(t, "/redeploy", http.StatusAccepted, released(warnings))
		var code int
		var stderr string
		stdout := captureStdout(t, func() {
			stderr = captureStderr(t, func() { code = redeployAgent(client, []string{"ranker", "2"}, true, fastPoll, time.Minute) })
		})
		if code != 0 {
			t.Fatalf("a queued redeploy exited %d", code)
		}
		if got := printedWarning(stdout + stderr); got != want {
			t.Errorf("redeploy with warnings %s: printed=%v, want %v\n%s%s", warnings, got, want, stdout, stderr)
		}
	}
}

func TestRollbackPrintsTheServersWarning(t *testing.T) {
	for warnings, want := range map[string]bool{twoWarnings: true, noWarnings: false} {
		client := answering(t, "/rollback", http.StatusAccepted, released(warnings))
		var code int
		var stderr string
		stdout := captureStdout(t, func() {
			stderr = captureStderr(t, func() { code = rollbackAgent(client, []string{"ranker", "2"}, true, fastPoll, time.Minute) })
		})
		if code != 0 {
			t.Fatalf("a queued rollback exited %d", code)
		}
		if got := printedWarning(stdout + stderr); got != want {
			t.Errorf("rollback with warnings %s: printed=%v, want %v\n%s%s", warnings, got, want, stdout, stderr)
		}
	}
}

// ── a move: the warning sits inside the answer, on the redeploy ──────────────────────────────

func movedWith(warnings string) string {
	return `{"agent_id":"7d1c2f4e-0000-4000-8000-000000000001","agent":"bot",
	"workspace":{"id":"w2","name":"acme"},"previous_workspace":{"id":"w1","name":"default"},
	"changed":true,"redeployed":true,
	"deployment":{"id":"d-9","agent_id":"7d1c2f4e-0000-4000-8000-000000000001","version":4,"state":"queued","warnings":` + warnings + `}}`
}

func moveRun(t *testing.T, warnings string, extra ...string) (stdout, stderr string) {
	t.Helper()
	onTerminal(t, false, "")
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/access", 200, botAccessJSON)
	cp.on("POST", "/agents/bot/move", 200, movedWith(warnings))
	stdout, stderr, err := runAfy(t, cp, append([]string{"update", "bot", "--workspace", "acme", "--yes"}, extra...)...)
	if err != nil {
		t.Fatalf("move: %v\n%s", err, stderr)
	}
	return stdout, stderr
}

func TestMovePrintsTheWarningOnTheRedeployItStarted(t *testing.T) {
	stdout, stderr := moveRun(t, twoWarnings)
	if !printedWarning(stdout + stderr) {
		t.Errorf("afy update --workspace did not print the warning inside the answer:\n%s%s", stdout, stderr)
	}
}

func TestControlAMoveWithNoWarningPrintsNone(t *testing.T) {
	stdout, stderr := moveRun(t, noWarnings)
	if strings.Contains(stdout+stderr, "Warning:") {
		t.Errorf("a move with nothing to warn about printed a warning:\n%s%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "version 4") {
		t.Errorf("the control did not run the move to its end:\n%s", stdout)
	}
}

func TestMoveJSONCarriesTheNestedWarnings(t *testing.T) {
	stdout, _ := moveRun(t, twoWarnings, "--json")
	var doc struct {
		Move struct {
			Deployment struct {
				Warnings []struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"warnings"`
			} `json:"deployment"`
		} `json:"move"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document (%v):\n%s", err, stdout)
	}
	w := doc.Move.Deployment.Warnings
	if len(w) != 1 || w[0].Code != "RUNTIME_DEPRECATED" || w[0].Message != deprecatedSentence {
		t.Errorf("--json lost the warning nested in the move: %+v", w)
	}
}

// ── an agent: update, create ─────────────────────────────────────────────────────────────────

func TestUpdatePrintsTheAgentsWarning(t *testing.T) {
	for warnings, want := range map[string]bool{twoWarnings: true, noWarnings: false} {
		cp := newFakeCP(t)
		cp.on("PATCH", "/agents/bot", 200,
			`{"id":"7d1c2f4e-0000-4000-8000-000000000001","name":"invoice-parser","description":"","warnings":`+warnings+`}`)
		stdout, stderr, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser")
		if err != nil {
			t.Fatalf("update: %v\n%s", err, stderr)
		}
		if got := printedWarning(stdout + stderr); got != want {
			t.Errorf("update with warnings %s: printed=%v, want %v\n%s%s", warnings, got, want, stdout, stderr)
		}
	}
}

func TestCreatePrintsTheAgentsWarning(t *testing.T) {
	for warnings, want := range map[string]bool{twoWarnings: true, noWarnings: false} {
		cp := newFakeCP(t)
		cp.on("GET", "/workspaces", 200, oneWorkspace)
		cp.on("POST", "/agents", 201,
			`{"id":"a-1","name":"invoice-parser","workspace_id":"w-1","workspace_name":"default","warnings":`+warnings+`}`)
		stdout, stderr, err := runAfy(t, cp, "create", "invoice-parser")
		if err != nil {
			t.Fatalf("create: %v\n%s", err, stderr)
		}
		if got := printedWarning(stdout + stderr); got != want {
			t.Errorf("create with warnings %s: printed=%v, want %v\n%s%s", warnings, got, want, stdout, stderr)
		}
	}
}

// ── a release: cancel ────────────────────────────────────────────────────────────────────────

func TestCancelPrintsTheReleasesWarning(t *testing.T) {
	for warnings, want := range map[string]bool{twoWarnings: true, noWarnings: false} {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot/deployments", 200,
			`{"deployments":[{"id":"d-3","agent_id":"a-1","version":3,"state":"queued"}],"next_before_id":null}`)
		cp.on("POST", "/agents/bot/deployments/3/cancel", 202,
			`{"id":"d-3","agent_id":"a-1","version":3,"state":"failed","warnings":`+warnings+`}`)
		stdout, stderr, err := runAfy(t, cp, "cancel", "bot")
		if err != nil {
			t.Fatalf("cancel: %v\n%s", err, stderr)
		}
		if got := printedWarning(stdout + stderr); got != want {
			t.Errorf("cancel with warnings %s: printed=%v, want %v\n%s%s", warnings, got, want, stdout, stderr)
		}
	}
}

// ── the lists: one line a sentence, saying whom it covers ────────────────────────────────────

func warnedAgentRow(name, warnings string) string {
	return `{"id":"id-` + name + `","name":"` + name + `","status":"running","agent_type":"service","warnings":` + warnings + `}`
}

func TestListSaysOnceWhomASentenceCovers(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/agents", 200, `[`+warnedAgentRow("api", twoWarnings)+`,`+warnedAgentRow("worker", twoWarnings)+`,`+warnedAgentRow("quiet", noWarnings)+`]`)
	stdout, stderr, err := runAfy(t, cp, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, stderr)
	}
	out := stdout + stderr
	if n := strings.Count(out, deprecatedSentence); n != 1 {
		t.Errorf("the same sentence on two agents must print once, printed %d times:\n%s", n, out)
	}
	if !strings.Contains(out, "Warning: "+deprecatedSentence+" (api, worker)") {
		t.Errorf("the line must say which agents it covers, and not the one with no warning:\n%s", out)
	}
}

func TestControlAListWithNoWarningPrintsNone(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/agents", 200, `[`+warnedAgentRow("api", noWarnings)+`,`+warnedAgentRow("worker", noWarnings)+`]`)
	stdout, stderr, err := runAfy(t, cp, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, stderr)
	}
	if strings.Contains(stdout+stderr, "Warning:") || !strings.Contains(stdout, "worker") {
		t.Errorf("a list of agents with nothing to warn about printed a warning, or none of it:\n%s%s", stdout, stderr)
	}
}

func TestInitLinkingAnExistingAgentPrintsItsWarning(t *testing.T) {
	for warnings, want := range map[string]bool{twoWarnings: true, noWarnings: false} {
		dir := filepath.Join(t.TempDir(), "empty-folder")
		cp := newFakeCP(t)
		cp.on("GET", "/agents/invoice-parser", 200, `{"id":"`+prodID+`","name":"invoice-parser","warnings":`+warnings+`}`)
		cp.on("GET", "/agents/"+prodID+"/yaml", 200, serverYAML)
		stdout, stderr, err := runAfy(t, cp, "init", dir, "--agent", "invoice-parser")
		if err != nil {
			t.Fatalf("init --agent: %v\n%s", err, stderr)
		}
		if got := printedWarning(stdout + stderr); got != want {
			t.Errorf("init --agent with warnings %s: printed=%v, want %v\n%s%s", warnings, got, want, stdout, stderr)
		}
	}
}

func TestDeploymentsSaysOnceWhichVersionsASentenceCovers(t *testing.T) {
	cp := newFakeCP(t)
	row := func(v, warnings string) string {
		return `{"id":"d-` + v + `","agent_id":"a-1","version":` + v + `,"state":"active","created_at":"2026-11-01T10:00:00Z","warnings":` + warnings + `}`
	}
	cp.on("GET", "/agents/bot/deployments", 200,
		`{"deployments":[`+row("3", twoWarnings)+`,`+row("2", twoWarnings)+`,`+row("1", noWarnings)+`],"next_before_id":null}`)
	cp.on("GET", "/agents/bot", 200, `{"id":"a-1","name":"bot"}`)
	stdout, stderr, err := runAfy(t, cp, "deployments", "bot")
	if err != nil {
		t.Fatalf("deployments: %v\n%s", err, stderr)
	}
	out := stdout + stderr
	if !strings.Contains(out, "Warning: "+deprecatedSentence+" (v3, v2)") || strings.Count(out, deprecatedSentence) != 1 {
		t.Errorf("want one line naming v3 and v2 and not v1:\n%s", out)
	}
}

// ── the census: no command reads one of these answers and says nothing of its warnings ──────

// watchedMethods return an agent or a release (or a list of them) from the control plane: each carries
// `warnings`, so each call site must be classified.
var watchedMethods = []string{
	"ListAgents", "GetAgent", "GetAgentStatus", "CreateAgent", "UpdateAgent", "MoveAgent",
	"Deploy", "Redeploy", "Rollback", "CancelDeployment", "GetDeployment", "ListDeployments", "ListDeploymentsPage",
	"createAgentRecord",
}

// printsWarnings are the identifiers whose call, or whose `.Warnings` read, says the warnings.
var printsWarnings = map[string]bool{
	"printWarnings": true, "printAgentListWarnings": true, "printDeploymentListWarnings": true, "printWarningsOf": true,
}

const prints = "prints"

// classified is every call site of a watched method: the function that makes it, and what that function does with
// the answer. "prints" means the function says the warnings itself; "via X" means it hands the answer to the helper X,
// which must be called there and say them; anything else is a reason the answer is only a lookup there.
var classified = map[string]map[string]string{
	"ListAgents": {
		"printAgentList":          prints,
		"printSpawnRelationships": "lookup: resolves the names of the agents a spawn graph names; the answer is the graph (afy status prints the agent's own warnings)",
	},
	"GetAgent": {
		"cancelAgentRun":       "lookup: resolves the agent a run must belong to before the run is cancelled; a run is not a release, so there is nothing of the agent's to say",
		"showAgentStatus":      prints,
		"initLinkExisting":     prints,
		"waitForAcceptedStart": "lookup: polls the agent's status until a start is accepted; the answer is the start's outcome",
		"resolveLinkedAgent":   "lookup: resolves the folder's link to an agent for the deploy that follows, which prints the release's warnings",
		"printAgentURL":        "lookup: reads the address after a deploy that already printed the release's warnings",
		"printPushLines":       "lookup: reads the agent's pushes under `afy deployments`, which prints the releases' warnings",
	},
	"CreateAgent": {
		"createAgentRecord": "lookup: the one place the POST is made; its callers are classified under createAgentRecord",
	},
	"createAgentRecord": {
		"runAgentsCreate": prints,
		"initNewAgent":    "lookup: a fresh agent is on the default runtime, which the control plane offers (its own tests hold that), so it carries none; the deploy after it prints the release's",
	},
	"UpdateAgent": {"runAgentsUpdate": prints},
	"MoveAgent":   {"runAgentsUpdate": "via printAgentMove"},
	"Deploy": {
		"deployDir":          prints,
		"handleDeployResult": "retries the same upload after the overage question; the answer goes back to deployDir, which prints",
	},
	"Redeploy":         {"redeployAgent": prints},
	"Rollback":         {"rollbackAgent": prints},
	"CancelDeployment": {"runAgentsCancel": prints},
	"GetDeployment": {
		"watchDeployment": "poll: the release's warnings were printed when it was accepted",
		"cancelAgentRun":  "reads a RUN to learn whose it is, which carries no warnings (a run is not a release)",
		"watchRun":        "reads a RUN, which carries no warnings (a run is not a release)",
	},
	"ListDeployments": {
		"runAgentsCancel":        prints,
		"printLastDeployFailure": "lookup: reads the newest release's failure; the status it sits in prints the agent's warnings",
		"redeployAgent":          prints,
		"rollbackAgent":          prints,
	},
	"ListDeploymentsPage": {"printDeployments": prints},
}

type siteReport struct {
	method, caller, file string
}

// scanCallSites parses the given Go sources (file name -> text) and returns, for every call of a watched method,
// the function that makes it; and for every function, whether it reads `.Warnings` or calls a print helper, and
// what it calls.
func scanCallSites(t *testing.T, sources map[string]string) (sites []siteReport, handles map[string]bool, calls map[string]map[string]bool) {
	t.Helper()
	handles = map[string]bool{}
	calls = map[string]map[string]bool{}
	watched := map[string]bool{}
	for _, m := range watchedMethods {
		watched[m] = true
	}
	fset := token.NewFileSet()
	for name, text := range sources {
		file, err := parser.ParseFile(fset, name, text, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			caller := fn.Name.Name
			if calls[caller] == nil {
				calls[caller] = map[string]bool{}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if x.Sel.Name == "Warnings" {
						handles[caller] = true
					}
				case *ast.CallExpr:
					var id string
					switch f := x.Fun.(type) {
					case *ast.SelectorExpr:
						id = f.Sel.Name
					case *ast.Ident:
						id = f.Name
					}
					if id == "" {
						return true
					}
					calls[caller][id] = true
					if printsWarnings[id] {
						handles[caller] = true
					}
					if watched[id] {
						sites = append(sites, siteReport{method: id, caller: caller, file: name})
					}
				}
				return true
			})
		}
	}
	return sites, handles, calls
}

// censusProblems is the census's verdict on a set of sources, against a classification.
func censusProblems(t *testing.T, sources map[string]string, table map[string]map[string]string) []string {
	sites, handles, calls := scanCallSites(t, sources)
	var problems []string
	seen := map[string]bool{}
	for _, s := range sites {
		if s.file != "" && strings.HasSuffix(s.file, "_test.go") {
			continue
		}
		seen[s.method+"|"+s.caller] = true
		how, ok := table[s.method][s.caller]
		if !ok {
			problems = append(problems, s.method+" is called in "+s.caller+" ("+filepath.Base(s.file)+"), which is not classified: does it print the answer's warnings, or is it a lookup?")
			continue
		}
		switch {
		case how == prints:
			if !handles[s.caller] {
				problems = append(problems, s.caller+" reads "+s.method+" and is classified as printing its warnings, but neither reads `.Warnings` nor calls a print helper")
			}
		case strings.HasPrefix(how, "via "):
			helper := strings.TrimPrefix(how, "via ")
			if !calls[s.caller][helper] || !handles[helper] {
				problems = append(problems, s.caller+" reads "+s.method+" and hands it to "+helper+", which must be called there and print the warnings")
			}
		}
	}
	for method, callers := range table {
		for caller := range callers {
			if !seen[method+"|"+caller] {
				problems = append(problems, "the classification names "+method+" in "+caller+", which no longer calls it: delete the entry")
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func readCommandSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	if len(out) < 20 {
		t.Fatalf("read only %d source files: the census would compare nothing", len(out))
	}
	return out
}

func TestEveryReaderOfAnAgentOrAReleaseSaysItsWarnings(t *testing.T) {
	if problems := censusProblems(t, readCommandSources(t), classified); len(problems) > 0 {
		t.Errorf("the census of the commands that read an agent or a release found:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestControlTheCensusFindsACommandThatSaysNothing(t *testing.T) {
	silent := `package cmd
func redeployAgent() { resp, _ := client.Redeploy("a", 1); _ = resp }
func rollbackAgent() { resp, _ := client.Rollback("a", 1); printWarnings(resp.Warnings) }
func newCommand() { _, _ = client.CancelDeployment("a", 1) }
`
	problems := censusProblems(t, map[string]string{"x.go": silent}, map[string]map[string]string{
		"Redeploy": {"redeployAgent": prints},
		"Rollback": {"rollbackAgent": prints},
	})
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		"redeployAgent reads Redeploy and is classified as printing its warnings",
		"CancelDeployment is called in newCommand",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the census missed %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "rollbackAgent") {
		t.Errorf("the census blamed a command that prints:\n%s", joined)
	}
}

func TestControlTheCensusFindsAStaleEntryAndAHelperThatDoesNotPrint(t *testing.T) {
	src := `package cmd
func runMove() { m, _ := client.MoveAgent("a", "w"); printAgentMove(m) }
func printAgentMove(m int) { _ = m }
`
	problems := censusProblems(t, map[string]string{"x.go": src}, map[string]map[string]string{
		"MoveAgent": {"runMove": "via printAgentMove"},
		"GetAgent":  {"gone": prints},
	})
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "hands it to printAgentMove, which must be called there and print the warnings") {
		t.Errorf("a helper that does not print was accepted:\n%s", joined)
	}
	if !strings.Contains(joined, "the classification names GetAgent in gone, which no longer calls it") {
		t.Errorf("a stale entry was accepted:\n%s", joined)
	}
}
