package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

// ONE JSON CONTRACT. With --json:
//   - stdout carries ONLY JSON, on success; on a refusal it carries nothing,
//   - everything else (hints, progress, prompts) goes to stderr,
//   - an error is one JSON object on stderr, {"error":{status,code,message}},
//     the code being the server's when it sent one,
//   - exit codes are the text mode's (3 not logged in, 2 bad arguments, 1 refused).
//
// Each case runs the SHIPPED command as its own process (the test binary
// re-executed as afy), so what is asserted is what a script sees: two pipes
// and an exit code.

// oneJSONDocument decodes raw as exactly one JSON document and nothing else.
func oneJSONDocument(t *testing.T, label, raw string) interface{} {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s: stdout is not JSON: %v\n%q", label, err, raw)
	}
	if rest, _ := io.ReadAll(dec.Buffered()); strings.TrimSpace(string(rest)) != "" {
		t.Fatalf("%s: stdout carries more than one JSON document or trailing text:\n%q", label, raw)
	}
	return v
}

// jsonErrorOnStderr finds the {"error":{...}} object a refusal wrote: the
// last non-empty stderr line, which must parse as exactly that shape.
func jsonErrorOnStderr(t *testing.T, label, stderr string) map[string]interface{} {
	t.Helper()
	var last string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.TrimSpace(line) != "" {
			last = strings.TrimSpace(line)
		}
	}
	var wrap struct {
		Error map[string]interface{} `json:"error"`
	}
	if err := json.Unmarshal([]byte(last), &wrap); err != nil || wrap.Error == nil {
		t.Fatalf("%s: the last stderr line is not a JSON {\"error\":{...}}: %q\nstderr:\n%s", label, last, stderr)
	}
	if msg, _ := wrap.Error["message"].(string); msg == "" {
		t.Errorf("%s: the error object has no message: %v", label, wrap.Error)
	}
	return wrap.Error
}

const (
	refusalCode    = "AGENT_PAUSED_CANNOT_MOVE"
	refusalMessage = "the stand-in refuses this"
)

var refusalBody = `{"detail":{"code":"` + refusalCode + `","message":"` + refusalMessage + `"}}`

type route struct {
	method, path string
	status       int
	body         string
}

type jsonCase struct {
	class string
	args  []string
	// ok are the routes a success needs; refuse replaces the ones it names
	// with a 409 carrying refusalCode.
	ok     []route
	refuse []string // "METHOD /path" keys to refuse
	// exit is the exit code of the refusal (1 unless the case says).
	exit int
	// resultOnRefusal marks a command that reports PER-ITEM results (which
	// keys landed) and exits 1 when any did not: its stdout is still one JSON
	// document, and the error is on stderr as for every other refusal.
	resultOnRefusal bool
	// stdin is what the command reads (a confirmation), "" for none.
	stdin string
}

const (
	agentRow     = `{"id":"a1","name":"bot","status":"running","agent_type":"service","created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z"}`
	deploymentRw = `{"id":"d-1","agent_id":"a1","version":3,"state":"active","created_at":"2026-09-01T10:00:00Z"}`
	wsRow        = `{"id":"w-2","name":"acme","regions":["us-east-1"]}`
)

func jsonCases() []jsonCase {
	cases := []jsonCase{
		// --- list ---
		{class: "list", args: []string{"list"}, ok: []route{{"GET", "/agents", 200, `[` + agentRow + `]`}}, refuse: []string{"GET /agents"}},
		{class: "list", args: []string{"deployments", "bot"}, ok: []route{{"GET", "/agents/bot/deployments", 200, `[` + deploymentRw + `]`}}, refuse: []string{"GET /agents/bot/deployments"}},
		{class: "list", args: []string{"deployments", "bot"}, ok: []route{{"GET", "/agents/bot/deployments", 200, `[]`}}},
		{class: "list", args: []string{"secrets", "list", "bot"}, ok: []route{{"GET", "/agents/bot/secrets", 200, `[]`}}, refuse: []string{"GET /agents/bot/secrets"}},
		{class: "list", args: []string{"workspaces", "list"}, ok: []route{{"GET", "/workspaces", 200, `[` + wsRow + `]`}}, refuse: []string{"GET /workspaces"}},
		{class: "list", args: []string{"runs", "bot"}, ok: []route{{"GET", "/agents/bot/runs", 200, `[]`}}, refuse: []string{"GET /agents/bot/runs"}},
		{class: "list", args: []string{"logs", "bot"}, ok: []route{{"GET", "/agents/bot/logs", 200, `[]`}}, refuse: []string{"GET /agents/bot/logs"}},
		{class: "list", args: []string{"workspaces", "agents", "acme"}, ok: []route{
			{"GET", "/workspaces/acme/agents", 200, `[]`}, {"GET", "/workspaces/acme/granted-agents", 200, `[]`}},
			refuse: []string{"GET /workspaces/acme/agents"}},
		{class: "list", args: []string{"github", "repos"}, ok: []route{{"GET", "/auth/github/repositories", 200, `{"repositories":[]}`}}, refuse: []string{"GET /auth/github/repositories"}},

		// --- show ---
		{class: "show", args: []string{"status", "bot"}, ok: []route{{"GET", "/agents/bot", 200, agentRow}, {"GET", "/agents/bot/deployments", 200, `[]`}}, refuse: []string{"GET /agents/bot"}},
		{class: "show", args: []string{"workspaces", "info", "acme"}, ok: []route{{"GET", "/workspaces/acme", 200, wsRow}}, refuse: []string{"GET /workspaces/acme"}},
		{class: "show", args: []string{"github", "status"}, ok: []route{{"GET", "/auth/github/status", 200, `{"connected":false,"installations":[]}`}}, refuse: []string{"GET /auth/github/status"}},
		{class: "show", args: []string{"whoami"}},
		{class: "show", args: []string{"version"}},

		// --- mutate ---
		{class: "mutate", args: []string{"create", "newbot", "--workspace", "acme"}, ok: []route{
			{"GET", "/workspaces/acme", 200, wsRow}, {"POST", "/agents", 201, agentRow}}, refuse: []string{"POST /agents"}},
		{class: "mutate", args: []string{"delete", "bot", "--force"}, ok: []route{{"DELETE", "/agents/bot", 200, `{}`}}, refuse: []string{"DELETE /agents/bot"}},
		{class: "mutate", args: []string{"stop", "bot"}, ok: []route{{"POST", "/agents/bot/stop", 200, `{}`}}, refuse: []string{"POST /agents/bot/stop"}},
		{class: "mutate", args: []string{"start", "bot"}, ok: []route{{"POST", "/agents/bot/start", 200, `{"readiness":"serving"}`}}, refuse: []string{"POST /agents/bot/start"}},
		{class: "mutate", args: []string{"archive", "bot"}, ok: []route{{"POST", "/agents/bot/archive", 200, `{}`}}, refuse: []string{"POST /agents/bot/archive"}},
		{class: "mutate", args: []string{"restore", "bot"}, ok: []route{{"POST", "/agents/bot/restore", 200, `{}`}}, refuse: []string{"POST /agents/bot/restore"}},
		{class: "mutate", args: []string{"cancel", "bot"}, ok: []route{{"GET", "/agents/bot/deployments", 200, `[]`}}, refuse: []string{"GET /agents/bot/deployments"}},
		{class: "mutate", args: []string{"update", "bot", "--description", "x"}, ok: []route{{"PATCH", "/agents/bot", 200, agentRow}}, refuse: []string{"PATCH /agents/bot"}},
		{class: "mutate", args: []string{"schedule", "resume", "bot"}, ok: []route{{"POST", "/agents/bot/schedule/resume", 200, `{"changed":true,"cron_paused":false}`}}, refuse: []string{"POST /agents/bot/schedule/resume"}},
		{class: "mutate", args: []string{"workspaces", "regions", "acme", "--regions", "us-east-1"}, ok: []route{{"PATCH", "/workspaces/acme/regions", 202, `{"operation_id":"op-1","status":"pending","from_regions":["us-east-1"],"to_regions":["us-east-1"]}`}}, refuse: []string{"PATCH /workspaces/acme/regions"}},
		{class: "mutate", args: []string{"schedule", "pause", "bot"}, ok: []route{{"POST", "/agents/bot/schedule/pause", 200, `{"changed":true,"cron_paused":true}`}}, refuse: []string{"POST /agents/bot/schedule/pause"}},
		{class: "mutate", args: []string{"run", "bot"}, ok: []route{{"POST", "/agents/bot/run", 200, `{"deployment_id":"d-1","version":3}`}}, refuse: []string{"POST /agents/bot/run"}},
		{class: "mutate", args: []string{"spawn", "parent", "child"}, ok: []route{{"POST", "/agents/parent/spawn", 200, `{"spawn_id":"s-1","deployment_id":"d-1","status":"queued"}`}}, refuse: []string{"POST /agents/parent/spawn"}},
		{class: "mutate", args: []string{"secrets", "set", "bot", "K=v"}, ok: []route{{"POST", "/agents/bot/secrets", 200, `{}`}}, refuse: []string{"POST /agents/bot/secrets"}, resultOnRefusal: true},
		{class: "mutate", args: []string{"secrets", "delete", "bot", "K"}, ok: []route{{"DELETE", "/agents/bot/secrets/K", 200, `{}`}}, stdin: "y\n", refuse: []string{"DELETE /agents/bot/secrets/K"}},
		{class: "mutate", args: []string{"workspaces", "create", "acme", "--regions", "eu-central-1"}, ok: []route{{"GET", "/deploy-options", 200, deployOptions}, {"POST", "/workspaces", 201, wsRow}}, refuse: []string{"POST /workspaces"}},
		{class: "mutate", args: []string{"workspaces", "update", "acme", "--description", "x"}, ok: []route{{"PATCH", "/workspaces/acme", 200, wsRow}}, refuse: []string{"PATCH /workspaces/acme"}},
		{class: "mutate", args: []string{"workspaces", "delete", "acme", "--force"}, ok: []route{{"DELETE", "/workspaces/acme", 200, `{"secrets_deleted":0}`}}, refuse: []string{"DELETE /workspaces/acme"}},
		{class: "mutate", args: []string{"github", "unlink", "bot"}, ok: []route{{"DELETE", "/agents/bot/github", 200, `{}`}}, refuse: []string{"DELETE /agents/bot/github"}},
		{class: "mutate", args: []string{"github", "disconnect"}, ok: []route{{"DELETE", "/auth/github", 200, `{}`}}, refuse: []string{"DELETE /auth/github"}},
		{class: "mutate", args: []string{"logout"}},

		// --- deploy ---
		{class: "deploy", args: []string{"redeploy", "bot", "3", "--detach"}, ok: []route{{"POST", "/agents/bot/deployments/3/redeploy", 202, `{"id":"d-9","version":4}`}}, refuse: []string{"POST /agents/bot/deployments/3/redeploy"}},
		{class: "deploy", args: []string{"rollback", "bot", "3", "--detach"}, ok: []route{{"POST", "/agents/bot/deployments/3/rollback", 202, `{"id":"d-9","version":4}`}}, refuse: []string{"POST /agents/bot/deployments/3/rollback"}},
		{class: "deploy", args: []string{"rollback", "bot"}, ok: []route{{"GET", "/agents/bot/deployments", 200, `[` + deploymentRw + `]`}}, refuse: []string{"GET /agents/bot/deployments"}},
	}
	// The evals commands' rows live beside their tests (evals_json_contract_test.go).
	return append(cases, evalsJSONCases()...)
}

func (c jsonCase) routes(refuse bool) []route {
	out := make([]route, 0, len(c.ok))
	for _, r := range c.ok {
		if refuse {
			for _, k := range c.refuse {
				if k == r.method+" "+r.path {
					r.status, r.body = 409, refusalBody
				}
			}
		}
		out = append(out, r)
	}
	return out
}

func newCasesCP(t *testing.T, routes []route) *fakeCP {
	cp := newFakeCP(t)
	for _, r := range routes {
		cp.on(r.method, r.path, r.status, r.body)
	}
	return cp
}

// runCase runs one case as a process and returns what a script would see.
func runCase(t *testing.T, c jsonCase, asJSON, refuse bool) (stdout, stderr string, code int) {
	t.Helper()
	cp := newCasesCP(t, c.routes(refuse))
	args := append([]string{}, c.args...)
	if asJSON {
		args = append(args, "--json")
	}
	return runAfyProcessWith(t, cp, nil, c.stdin, args...)
}

func TestJSONContractOnSuccessStdoutIsOnlyJSON(t *testing.T) {
	for _, c := range jsonCases() {
		c := c
		t.Run(c.class+"/"+strings.Join(c.args, "_"), func(t *testing.T) {
			stdout, stderr, code := runCase(t, c, true, false)
			if code != 0 {
				t.Fatalf("exit %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			oneJSONDocument(t, strings.Join(c.args, " "), stdout)
		})
	}
}

func TestJSONContractOnARefusalStdoutIsEmptyAndStderrCarriesTheServersCode(t *testing.T) {
	n := 0
	for _, c := range jsonCases() {
		if len(c.refuse) == 0 {
			continue
		}
		c := c
		n++
		t.Run(c.class+"/"+strings.Join(c.args, "_"), func(t *testing.T) {
			stdout, stderr, code := runCase(t, c, true, true)
			want := c.exit
			if want == 0 {
				want = 1
			}
			if code != want {
				t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, want, stdout, stderr)
			}
			if c.resultOnRefusal {
				oneJSONDocument(t, strings.Join(c.args, " "), stdout)
			} else if strings.TrimSpace(stdout) != "" {
				t.Errorf("a refusal wrote to stdout, which --json keeps for the result:\n%s", stdout)
			}
			e := jsonErrorOnStderr(t, strings.Join(c.args, " "), stderr)
			if e["code"] != refusalCode {
				t.Errorf("the error carries code %v, want the server's %q: %v", e["code"], refusalCode, e)
			}
			if e["status"] != 409.0 {
				t.Errorf("the error carries status %v, want the server's 409: %v", e["status"], e)
			}
		})
	}
	if n < 25 {
		t.Fatalf("only %d refusal cases ran: the table is not reaching the commands", n)
	}
}

func TestJSONContractControlTextModeIsNotJSON(t *testing.T) {
	// The control for the two tests above: the same commands WITHOUT --json
	// print text that does not parse as one JSON document, and a refusal is
	// the text "Error: ..." on stderr, not JSON. If this passed for a command
	// printing JSON regardless of the flag, the assertions above would be
	// satisfied by a switch that does nothing.
	for _, c := range []jsonCase{jsonCases()[0], jsonCases()[9], jsonCases()[19]} {
		c := c
		t.Run(strings.Join(c.args, "_"), func(t *testing.T) {
			stdout, _, code := runCase(t, c, false, false)
			if code != 0 {
				t.Fatalf("exit %d", code)
			}
			if json.Valid([]byte(strings.TrimSpace(stdout))) {
				t.Errorf("text mode printed JSON: %q", stdout)
			}
			_, stderr, code := runCase(t, c, false, true)
			if code != 1 {
				t.Fatalf("refusal exit %d, want 1", code)
			}
			if !strings.Contains(stderr, "Error: ") || strings.Contains(stderr, `{"error"`) {
				t.Errorf("text mode must print the text error, got:\n%s", stderr)
			}
		})
	}
}

func TestJSONFlagReplacesOutputAndHasNoAlias(t *testing.T) {
	for _, bad := range [][]string{{"-o", "json"}, {"--output", "json"}, {"--output=json"}, {"-ojson"}} {
		args := append([]string{"version"}, bad...)
		_, stderr, code := runAfyProcess(t, nil, nil, args...)
		if code == 0 {
			t.Errorf("afy %s succeeded: -o/--output are gone, with no alias", strings.Join(bad, " "))
		}
		if !strings.Contains(stderr, "unknown") {
			t.Errorf("afy %s: want cobra's unknown flag, got %q", strings.Join(bad, " "), stderr)
		}
	}
	// The one flag is a persistent flag of the root: every command has it.
	if rootCmd.PersistentFlags().Lookup("json") == nil {
		t.Fatal("--json is not a persistent flag of the root command")
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c != rootCmd {
			if c.LocalNonPersistentFlags().Lookup("json") != nil || c.PersistentFlags().Lookup("json") != nil {
				t.Errorf("`%s` declares its own --json; there is one switch, on the root", c.CommandPath())
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

func TestJSONErrorsKeepTheExitCodes(t *testing.T) {
	// Not logged in: 3. Bad arguments: 2. A refusal: 1 (above). With --json
	// the exit code is the text mode's, and the error is JSON on stderr.
	cases := []struct {
		name string
		env  []string
		args []string
		want int
	}{
		{"not logged in", []string{"AETHERFY_API_KEY="}, []string{"list", "--json"}, exitNotLoggedIn},
		{"bad arguments", nil, []string{"secrets", "set", "bot", "--json"}, exitInputRefused},
		{"a flag that does not exist", nil, []string{"list", "--nope", "--json"}, exitInputRefused},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runAfyProcess(t, nil, tc.env, tc.args...)
			if code != tc.want {
				t.Errorf("exit %d, want %d\nstderr:\n%s", code, tc.want, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("stdout carries %q on an error", stdout)
			}
			jsonErrorOnStderr(t, tc.name, stderr)
		})
	}
}

// --- deploy, init, upgrade: the commands that run as folders / processes ---

func TestJSONContractDeployIsJSONOnSuccessAndOnRefusal(t *testing.T) {
	dir := agentFolder(t, "")
	linkFolder(t, dir, map[string]string{"default": prodID})

	cp := deployCP(t)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	doc, ok := oneJSONDocument(t, "deploy", stdout).(map[string]interface{})
	if !ok || doc["agent_id"] != prodID || doc["deployment"] == nil {
		t.Errorf("deploy --json = %v, want the agent id and the deployment", doc)
	}
	if !strings.Contains(stderr, "Deploying to invoice-parser") {
		t.Errorf("progress belongs on stderr with --json; stderr:\n%s", stderr)
	}

	refused := deployCP(t)
	refused.on("POST", "/agents/"+prodID+"/deploy", 409, refusalBody)
	stdout, stderr, code = runAfyProcess(t, refused, nil, "deploy", dir, "--detach", "--json")
	if code != 1 {
		t.Errorf("refused deploy exits %d, want 1", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a refused deploy wrote to stdout:\n%s", stdout)
	}
	if e := jsonErrorOnStderr(t, "deploy", stderr); e["code"] != refusalCode {
		t.Errorf("the refusal's code is %v, want the server's %q", e["code"], refusalCode)
	}
}

func TestJSONContractDeployWatchesToTheEndAndPrintsTheDeployment(t *testing.T) {
	// Without --detach the command waits; its one JSON document is the
	// deployment as it settled, and the status lines it printed on the way
	// are on stderr.
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot", 200, agentRow)
	cp.on("GET", "/deployments/d-9", 200, `{"id":"d-9","agent_id":"a1","version":4,"state":"active","created_at":"2026-09-01T10:00:00Z"}`)
	cp.on("POST", "/agents/bot/deployments/3/redeploy", 202, `{"id":"d-9","version":4}`)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "redeploy", "bot", "3", "--json")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc, _ := oneJSONDocument(t, "redeploy", stdout).(map[string]interface{})
	dep, _ := doc["deployment"].(map[string]interface{})
	if dep == nil || dep["state"] != "active" {
		t.Errorf("redeploy --json = %v, want the settled deployment", doc)
	}

	failed := newFakeCP(t)
	failed.on("POST", "/agents/bot/deployments/3/redeploy", 202, `{"id":"d-9","version":4}`)
	failed.on("GET", "/deployments/d-9", 200, `{"id":"d-9","agent_id":"a1","version":4,"state":"failed","error_message":"the build broke","created_at":"2026-09-01T10:00:00Z"}`)
	stdout, stderr, code = runAfyProcess(t, failed, nil, "redeploy", "bot", "3", "--json")
	if code != 1 {
		t.Errorf("a failed deployment exits %d, want 1", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a failed deployment wrote to stdout:\n%s", stdout)
	}
	if e := jsonErrorOnStderr(t, "redeploy", stderr); !strings.Contains(e["message"].(string), "the build broke") {
		t.Errorf("the error does not carry the reason: %v", e)
	}
}

func TestJSONContractInitIsJSONOnSuccessAndOnRefusal(t *testing.T) {
	// A new agent.
	dir := t.TempDir()
	cp := initCP(t)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "init", dir, "-y", "--name", "invoice-parser", "--runtime", "python3.11", "--entrypoint", "main.py", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	doc, _ := oneJSONDocument(t, "init", stdout).(map[string]interface{})
	if doc["created"] != true || doc["agent"] == nil {
		t.Errorf("init --json = %v, want the created agent", doc)
	}
	if !strings.Contains(stderr, "Next") && !strings.Contains(stderr, "afy deploy") {
		t.Errorf("the next-steps hint belongs on stderr; stderr:\n%s", stderr)
	}

	// An existing agent.
	dir = t.TempDir()
	cp = newFakeCP(t)
	cp.on("GET", "/agents/invoice-parser", 200, `{"id":"`+prodID+`","name":"invoice-parser"}`)
	cp.on("GET", "/agents/"+prodID+"/yaml", 200, initReleaseExport)
	stdout, stderr, code = runAfyProcess(t, cp, nil, "init", dir, "--agent", "invoice-parser", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	doc, _ = oneJSONDocument(t, "init --agent", stdout).(map[string]interface{})
	if doc["linked"] != true {
		t.Errorf("init --agent --json = %v, want linked", doc)
	}

	// A refusal.
	cp = newFakeCP(t)
	cp.on("GET", "/agents/invoice-parser", 409, refusalBody)
	stdout, stderr, code = runAfyProcess(t, cp, nil, "init", t.TempDir(), "--agent", "invoice-parser", "--json")
	if code != 1 {
		t.Errorf("refused init exits %d, want 1", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a refused init wrote to stdout:\n%s", stdout)
	}
	if e := jsonErrorOnStderr(t, "init", stderr); e["code"] != refusalCode {
		t.Errorf("the refusal's code is %v, want %q", e["code"], refusalCode)
	}
}

func TestJSONContractUpdateIsJSON(t *testing.T) {
	// `afy upgrade` (the self-updater) --check against a stand-in release
	// host is covered by the update contract test; here, that it honours the
	// switch: a refusal to run (no network key needed) is JSON on stderr.
	stdout, stderr, code := runAfyProcess(t, nil, []string{"AETHERFY_RELEASE_URL=http://127.0.0.1:1"}, "upgrade", "--check", "--json")
	if code == 0 {
		t.Skip("the release host answered; nothing to refuse")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a failed upgrade wrote to stdout:\n%s", stdout)
	}
	jsonErrorOnStderr(t, "upgrade", stderr)
}

// --- the vector commands ---

func TestJSONContractVectorCommands(t *testing.T) {
	ok := newVecServer(t, func(r vecRequest) (int, string) {
		return 200, `{"collections":[` + collectionRow + `]}`
	})
	stdout, stderr, code := runAfyProcess(t, nil, []string{vectors.EnvVectorsURL + "=" + ok.srv.URL}, "collections", "list", "--workspace", "acme", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	doc, _ := oneJSONDocument(t, "collections list", stdout).(map[string]interface{})
	if doc["workspace"] != "acme" || doc["collections"] == nil {
		t.Errorf("collections list --json = %v", doc)
	}

	refused := newVecServer(t, func(r vecRequest) (int, string) {
		return 404, `{"error":{"code":"NOT_FOUND","message":"` + refusalMessage + `"},"detail":{"code":"NOT_FOUND","message":"` + refusalMessage + `"}}`
	})
	stdout, stderr, code = runAfyProcess(t, nil, []string{vectors.EnvVectorsURL + "=" + refused.srv.URL}, "collections", "list", "--workspace", "acme", "--json")
	if code != 1 {
		t.Errorf("a refused vector call exits %d, want 1", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a refusal wrote to stdout:\n%s", stdout)
	}
	e := jsonErrorOnStderr(t, "collections list", stderr)
	if e["code"] != "NOT_FOUND" || e["status"] != 404.0 {
		t.Errorf("the vector refusal is %v, want status 404 code NOT_FOUND", e)
	}
}

// --- every leaf command, mechanically ---

// jsonExempt are the commands whose output is not a result: a shell script.
var jsonExempt = map[string]string{
	"afy completion": "prints a shell script; --json is refused",
	"afy help":       "cobra's built-in: the usage text is its whole output",
}

func TestEveryLeafCommandHasAJSONContract(t *testing.T) {
	// Every runnable leaf is either exercised by a table above (by its argv
	// prefix), by a dedicated test in this file, by the vector table, or
	// exempt with a reason. A new command that is none of them fails here:
	// the contract cannot silently cover "most" commands.
	covered := map[string]bool{}
	for _, c := range jsonCases() {
		covered[leafPath(c.args)] = true
	}
	for _, p := range []string{
		"deploy", "init", "upgrade", "redeploy", "login", "diff", "access", "github connect", "github link",
		"datasets push", // reads two files: TestJSONContractDatasetsPush
	} {
		covered[p] = true
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.HasSubCommands() || !c.Runnable() || c == rootCmd {
			return
		}
		path := strings.TrimPrefix(c.CommandPath(), "afy ")
		if _, ok := jsonExempt[c.CommandPath()]; ok {
			return
		}
		if isVectorPath(path) {
			return // covered by TestJSONContractVectorCommands and vectors_commands_test.go
		}
		if !covered[path] {
			t.Errorf("`afy %s` has no JSON-contract case: add it to jsonCases, or exempt it with a reason", path)
		}
	}
	walk(rootCmd)
}

func leafPath(args []string) string {
	var parts []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		parts = append(parts, a)
	}
	// Find the longest prefix that is a command.
	for n := len(parts); n > 0; n-- {
		if c, _, err := rootCmd.Find(parts[:n]); err == nil && c != rootCmd && strings.TrimPrefix(c.CommandPath(), "afy ") == strings.Join(parts[:n], " ") {
			return strings.Join(parts[:n], " ")
		}
	}
	return strings.Join(parts, " ")
}

func isVectorPath(path string) bool {
	for _, g := range []string{"collections ", "index ", "points "} {
		if strings.HasPrefix(path, g) {
			return true
		}
	}
	return false
}

// --- the stdout writers ---

func TestNoCommandWritesToStdoutOutsideTheOutputPackage(t *testing.T) {
	// In JSON mode stdout is the result and nothing else, so no command may
	// write to it by itself: text goes through output.* (which moves to
	// stderr with --json), results through output.JSON/Result. The few
	// places that legitimately own stdout are listed with their reason.
	allowed := map[string]string{
		"completion.go": "the shell script is the output; --json is refused",
		"version.go":    "plain version text, only in text mode (JSON mode returns first)",
		"vectors.go":    "the vector commands write through their own vecRun, with --json decided there",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "//") {
				continue
			}
			for _, banned := range []string{"fmt.Print(", "fmt.Printf(", "fmt.Println(", "os.Stdout"} {
				if strings.Contains(trim, banned) {
					t.Errorf("%s:%d writes to stdout directly (%s): use output.* so --json can move it to stderr\n\t%s", name, i+1, banned, trim)
				}
			}
		}
	}
}

func TestEveryEmptyListIsAnEmptyJSONDocument(t *testing.T) {
	// A list command with nothing to list still writes its JSON on stdout: []
	// (an object with an empty list for `github repos`), never only a sentence
	// on stderr. Each case answers the command's read with an empty list.
	cases := []struct {
		args  []string
		route route
		want  string
	}{
		{[]string{"list"}, route{"GET", "/agents", 200, `[]`}, "[]"},
		{[]string{"workspaces", "list"}, route{"GET", "/workspaces", 200, `[]`}, "[]"},
		{[]string{"deployments", "bot"}, route{"GET", "/agents/bot/deployments", 200, `[]`}, "[]"},
		{[]string{"rollback", "bot"}, route{"GET", "/agents/bot/deployments", 200, `[]`}, "[]"},
		{[]string{"secrets", "list", "bot"}, route{"GET", "/agents/bot/secrets", 200, `[]`}, "[]"},
		{[]string{"runs", "bot"}, route{"GET", "/agents/bot/runs", 200, `[]`}, "[]"},
		{[]string{"logs", "bot"}, route{"GET", "/agents/bot/logs", 200, `[]`}, "[]"},
		{[]string{"github", "repos"}, route{"GET", "/auth/github/repositories", 200, `{"repositories":[]}`}, `{"repositories":[]}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			cp := newCasesCP(t, []route{tc.route})
			stdout, stderr, code := runAfyProcess(t, cp, nil, append(tc.args, "--json")...)
			if code != 0 {
				t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
			}
			doc := oneJSONDocument(t, strings.Join(tc.args, " "), stdout)
			raw, _ := json.Marshal(doc)
			if string(raw) != tc.want {
				t.Errorf("stdout = %s, want %s", raw, tc.want)
			}
		})
	}
}
