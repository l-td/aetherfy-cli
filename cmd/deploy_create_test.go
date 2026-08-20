package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/archive"
)

// create-on-deploy: `afy deploy` may create the agent it is deploying to, but
// only with consent. The ratified rule, and what each test below pins:
//
//	INTERACTIVE (terminal, no --yes, no --create) → ask, default No.
//	NON-INTERACTIVE (no terminal, or --yes)       → never auto-confirm.
//	                                                --create is the only consent.
//	--yes covers the OVERAGE cost prompt only and never implies creation.
//
// decideCreateOnDeploy takes every input as a parameter, so the rule is
// exercised without a terminal and without a server. The two HTTP tests below
// then pin the call the decision turns into.

// yamlConfig is a parsed aetherfy.yaml the way `afy deploy` reads it.
func yamlConfig(t *testing.T, body string) *archive.AetherfyConfig {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "aetherfy.yaml", body)
	cfg, err := archive.ParseAetherfyConfig(dir)
	if err != nil {
		t.Fatalf("ParseAetherfyConfig: %v", err)
	}
	return cfg
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const helloYAML = `name: hello-agent
runtime: python3.12
type: service
`

// answerYes / answerNo are prompt seams. neverAsked fails the test if the
// prompt is reached at all — the non-interactive cases must not ask.
func answerYes(_ string) bool { return true }
func answerNo(_ string) bool  { return false }

func neverAsked(t *testing.T) func(string) bool {
	t.Helper()
	return func(q string) bool {
		t.Errorf("prompted %q on a non-interactive path — creation must never be offered there", q)
		return false
	}
}

// INTERACTIVE, answered y → creates, with the manifest's type/runtime.
func TestInteractiveYesCreatesFromManifest(t *testing.T) {
	got := decideCreateOnDeploy(createOnDeployInput{
		AgentName:   "hello-agent",
		Config:      yamlConfig(t, helloYAML),
		Interactive: true,
		Confirm:     answerYes,
	})

	if !got.Create {
		t.Fatalf("interactive y: want create, got %+v", got)
	}
	if got.AgentType != "service" || got.Runtime != "python3.12" {
		t.Errorf("create values: want service/python3.12, got %s/%s", got.AgentType, got.Runtime)
	}
	// Print-anchored: the question names exactly what will be sent.
	want := "Agent 'hello-agent' doesn't exist. Create it as service/python3.12 (from aetherfy.yaml)?"
	if got.Question != want {
		t.Errorf("prompt text:\n got %q\nwant %q", got.Question, want)
	}
}

// INTERACTIVE, answered n → no creation, and nothing is added to today's error.
func TestInteractiveNoDoesNotCreate(t *testing.T) {
	got := decideCreateOnDeploy(createOnDeployInput{
		AgentName:   "hello-agent",
		Config:      yamlConfig(t, helloYAML),
		Interactive: true,
		Confirm:     answerNo,
	})

	if got.Create {
		t.Error("interactive n: created anyway")
	}
	if got.Warn != "" {
		t.Errorf("declining must leave today's error verbatim, got extra output %q", got.Warn)
	}
}

// NON-TTY without --create → today's error, verbatim. Nothing asked, nothing
// created, nothing printed.
func TestNonInteractiveWithoutCreateFlagRefuses(t *testing.T) {
	got := decideCreateOnDeploy(createOnDeployInput{
		AgentName:   "hello-agent",
		Config:      yamlConfig(t, helloYAML),
		Interactive: false,
		Confirm:     neverAsked(t),
	})

	if got.Create {
		t.Error("no terminal and no --create: created anyway")
	}
	if got.Warn != "" || got.Announce != "" || got.Question != "" {
		t.Errorf("want today's error verbatim, got %+v", got)
	}
}

// NON-TTY with --create → creates, announcing exactly what it will send.
func TestNonInteractiveWithCreateFlagCreates(t *testing.T) {
	got := decideCreateOnDeploy(createOnDeployInput{
		AgentName:   "hello-agent",
		Config:      yamlConfig(t, helloYAML),
		CreateFlag:  true,
		Interactive: false,
		Confirm:     neverAsked(t), // --create is consent; it must not ask
	})

	if !got.Create {
		t.Fatalf("--create: want create, got %+v", got)
	}
	if got.AgentType != "service" || got.Runtime != "python3.12" {
		t.Errorf("create values: want service/python3.12, got %s/%s", got.AgentType, got.Runtime)
	}
	want := "Agent 'hello-agent' doesn't exist. Creating it as service/python3.12 (from aetherfy.yaml)."
	if got.Announce != want {
		t.Errorf("announcement:\n got %q\nwant %q", got.Announce, want)
	}
}

// THE AUTOMATION RULE. --yes is the overage cost flag. On a terminal it
// suppresses the question (that is what non-interactive means) and, without
// --create, the deploy fails with today's error rather than creating anything.
// Mutation: make --yes imply creation and this test reds.
func TestYesAloneNeverCreates(t *testing.T) {
	for _, interactive := range []bool{true, false} {
		got := decideCreateOnDeploy(createOnDeployInput{
			AgentName:   "hello-agent",
			Config:      yamlConfig(t, helloYAML),
			YesFlag:     true,
			Interactive: interactive,
			Confirm:     neverAsked(t),
		})
		if got.Create {
			t.Errorf("--yes alone (interactive=%v) created an agent — --yes covers the cost prompt only", interactive)
		}
		if got.Warn != "" || got.Announce != "" {
			t.Errorf("--yes alone (interactive=%v): want today's error verbatim, got %+v", interactive, got)
		}
	}
}

// --yes AND --create still creates: the explicit flag is the consent, and
// --yes only means "don't ask me about cost".
func TestYesWithCreateFlagStillCreates(t *testing.T) {
	got := decideCreateOnDeploy(createOnDeployInput{
		AgentName:   "hello-agent",
		Config:      yamlConfig(t, helloYAML),
		YesFlag:     true,
		CreateFlag:  true,
		Interactive: false,
		Confirm:     neverAsked(t),
	})
	if !got.Create {
		t.Errorf("--yes --create: want create, got %+v", got)
	}
}

// A manifest with no 'type:' cannot be described, so creation is refused on
// BOTH consent paths rather than defaulting to service behind the user's back.
// (`afy agents create` defaults --type to SERVICE; here the value is not the
// user's to omit, because the prompt has to print it.)
func TestManifestWithoutTypeRefusesToCreate(t *testing.T) {
	noType := "name: hello-agent\nruntime: python3.12\n"

	for _, tc := range []struct {
		name string
		in   createOnDeployInput
	}{
		{"interactive", createOnDeployInput{Interactive: true, Confirm: neverAsked(t)}},
		{"--create", createOnDeployInput{CreateFlag: true, Confirm: neverAsked(t)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.AgentName = "hello-agent"
			in.Config = yamlConfig(t, noType)

			got := decideCreateOnDeploy(in)
			if got.Create {
				t.Fatal("created an agent from a manifest with no 'type:'")
			}
			if !strings.Contains(got.Warn, "type:") {
				t.Errorf("refusal must say which field is missing, got %q", got.Warn)
			}
		})
	}
}

// A type outside the SERVICE|JOB enum is refused with the same validation
// `afy agents create -t` applies — the deploy path accepts no less.
func TestManifestWithInvalidTypeRefusesToCreate(t *testing.T) {
	got := decideCreateOnDeploy(createOnDeployInput{
		AgentName:  "hello-agent",
		Config:     yamlConfig(t, "name: hello-agent\nruntime: python3.12\ntype: worker\n"),
		CreateFlag: true,
		Confirm:    neverAsked(t),
	})
	if got.Create {
		t.Fatal("created an agent with type 'worker'")
	}
	if !strings.Contains(got.Warn, "worker") {
		t.Errorf("refusal must name the rejected value, got %q", got.Warn)
	}
	// Parity check against the create command's own validator.
	if _, err := normalizeAgentType("worker"); err == nil {
		t.Error("normalizeAgentType accepted 'worker' — the two surfaces disagree")
	}
}

// Deploying at a UUID targets an existing record. The control-plane's name rule
// would happily accept that UUID as a NAME, so creation is refused instead of
// minting an agent named after a typo'd ID.
func TestUUIDTargetIsNeverCreated(t *testing.T) {
	got := decideCreateOnDeploy(createOnDeployInput{
		AgentName:  "550e8400-e29b-41d4-a716-446655440000",
		Config:     yamlConfig(t, helloYAML),
		CreateFlag: true,
		Confirm:    neverAsked(t),
	})
	if got.Create {
		t.Fatal("created an agent named after a UUID")
	}
	if !strings.Contains(got.Warn, "agent ID") {
		t.Errorf("refusal must explain why, got %q", got.Warn)
	}
}

// No manifest → nothing to describe the agent with → never offered. This is
// also what stops the post-create retry from looping (see below).
func TestNilConfigNeverCreates(t *testing.T) {
	got := decideCreateOnDeploy(createOnDeployInput{
		AgentName:   "hello-agent",
		Config:      nil,
		CreateFlag:  true,
		Interactive: true,
		Confirm:     neverAsked(t),
	})
	if got.Create {
		t.Errorf("created without a manifest: %+v", got)
	}
}

// --- the call the decision turns into ---

// createCaptureServer answers POST /agents and POST /agents/{id}/deploy,
// recording every request. deployStatus lets a test keep the deploy 404ing.
type createCaptureServer struct {
	*httptest.Server
	mu           sync.Mutex
	createBodies []map[string]interface{}
	deployCalls  int
}

func newCreateCaptureServer(t *testing.T, deployAfterCreateOK bool) *createCaptureServer {
	t.Helper()
	s := &createCaptureServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/agents":
			body, _ := io.ReadAll(r.Body)
			var parsed map[string]interface{}
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Errorf("create body is not JSON: %v", err)
			}
			s.mu.Lock()
			s.createBodies = append(s.createBodies, parsed)
			s.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"a1","name":"hello-agent","agent_type":"service","status":"pending"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deploy"):
			s.mu.Lock()
			s.deployCalls++
			s.mu.Unlock()
			if !deployAfterCreateOK {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"detail":{"code":"AGENT_NOT_FOUND","message":"Agent 'hello-agent' not found"}}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"d1","agent_id":"a1","version":1,"state":"queued"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// withCreateFlags sets the deploy command's package-level flag vars for one
// test and restores them afterwards.
func withCreateFlags(t *testing.T, create, yes bool) {
	t.Helper()
	oldCreate, oldYes := deployCreate, deployYes
	deployCreate, deployYes = create, yes
	t.Cleanup(func() { deployCreate, deployYes = oldCreate, oldYes })
}

// notFound is the deploy error the control-plane returns for a missing agent —
// the one that today ends the command.
func notFound() *api.APIError {
	return &api.APIError{
		StatusCode: http.StatusNotFound,
		Code:       "AGENT_NOT_FOUND",
		Message:    "Agent 'hello-agent' not found",
	}
}

// The consented create goes out as the SAME POST /agents `afy agents create`
// sends, carrying the manifest's type and runtime, and the deploy is then
// re-sent with the archive already in hand.
func TestCreateOnDeploySendsTheSharedCreateCallThenRedeploys(t *testing.T) {
	withCreateFlags(t, true, false)
	srv := newCreateCaptureServer(t, true)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	resp, err := handleDeployResult(client, "hello-agent", yamlConfig(t, helloYAML),
		[]byte("tarball"), nil, notFound())
	if err != nil {
		t.Fatalf("create-then-deploy: %v", err)
	}
	if resp == nil || resp.DeploymentID != "d1" {
		t.Fatalf("want the redeployed deployment, got %+v", resp)
	}

	if len(srv.createBodies) != 1 {
		t.Fatalf("want exactly one POST /agents, got %d", len(srv.createBodies))
	}
	body := srv.createBodies[0]
	// Print-anchored: what the prompt/announcement named is what was sent.
	for field, want := range map[string]string{
		"name":       "hello-agent",
		"agent_type": "service",
		"runtime":    "python3.12",
	} {
		if got, _ := body[field].(string); got != want {
			t.Errorf("create body %s: want %q, got %q", field, want, got)
		}
	}
	if srv.deployCalls != 1 {
		t.Errorf("want one deploy re-send after create, got %d", srv.deployCalls)
	}
}

// A deploy that STILL answers AGENT_NOT_FOUND after a successful create must
// fail with that error — not create again, not loop.
func TestCreateOnDeployDoesNotLoop(t *testing.T) {
	withCreateFlags(t, true, false)
	srv := newCreateCaptureServer(t, false)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	_, err := handleDeployResult(client, "hello-agent", yamlConfig(t, helloYAML),
		[]byte("tarball"), nil, notFound())
	if err == nil {
		t.Fatal("want the AGENT_NOT_FOUND error back, got nil")
	}
	if len(srv.createBodies) != 1 {
		t.Errorf("want exactly one create attempt, got %d", len(srv.createBodies))
	}
	if srv.deployCalls != 1 {
		t.Errorf("want exactly one deploy re-send, got %d", srv.deployCalls)
	}
}

// Without consent the AGENT_NOT_FOUND error is returned unchanged — the caller
// prints today's "Deployment failed: [404] ... (AGENT_NOT_FOUND)" line — and no
// agent is created.
func TestNoConsentReturnsTodaysErrorAndCreatesNothing(t *testing.T) {
	withCreateFlags(t, false, true) // --yes, no --create
	srv := newCreateCaptureServer(t, true)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	in := notFound()
	_, err := handleDeployResult(client, "hello-agent", yamlConfig(t, helloYAML),
		[]byte("tarball"), nil, in)
	if err != in {
		t.Errorf("want the original error returned verbatim, got %v", err)
	}
	if len(srv.createBodies) != 0 || srv.deployCalls != 0 {
		t.Errorf("nothing should have been sent: %d create(s), %d deploy(s)",
			len(srv.createBodies), srv.deployCalls)
	}
}
