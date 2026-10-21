package cmd

// THE PROMPTS ON A REAL TERMINAL. Everything else drives a question through a
// scripted reader; nothing there proves what a person at a keyboard gets. Here the
// shipped command runs as its own process behind a pseudo-terminal (the test
// binary re-executed as afy, see TestMain), so stdin IS a terminal and the question
// is asked, answered and ended the way a person does it:
//
//   - a decline (the wrong name, or "n"): exit 1, nothing changed;
//   - an accept: exit 0, the change made;
//   - end of input (Ctrl-D at the prompt): exit 2, nothing changed.
//
// For a delete, afy access, afy collections regions, afy init's overwrite and the
// deploy's overage question. Linux only (a pseudo-terminal is a Unix device; the
// file name carries the constraint): the other platforms do not compile it, and
// CI runs it (ci.yml runs `go test ./...` on ubuntu).

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/l-td/aetherfy-cli/internal/vectors"
)

const ptyStep = 20 * time.Second

// ptyRun is one afy process on a pseudo-terminal.
type ptyRun struct {
	t    *testing.T
	cmd  *exec.Cmd
	tty  *os.File
	mu   sync.Mutex
	seen bytes.Buffer
	done chan struct{}
}

func startOnPTY(t *testing.T, env []string, args ...string) *ptyRun {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		afyProcessArgs+"="+strings.Join(args, "\x1f"),
		"AETHERFY_CONFIG_DIR="+t.TempDir(),
		"AETHERFY_API_KEY=afy_test_0123456789abcdef0123456789abcdef",
		"NO_COLOR=1",
	)
	cmd.Env = append(cmd.Env, env...)
	tty, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("starting afy on a pseudo-terminal: %v", err)
	}
	p := &ptyRun{t: t, cmd: cmd, tty: tty, done: make(chan struct{})}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := tty.Read(buf)
			p.mu.Lock()
			p.seen.Write(buf[:n])
			p.mu.Unlock()
			if err != nil {
				close(p.done)
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = tty.Close()
	})
	return p
}

func (p *ptyRun) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen.String()
}

// expect waits for text to appear on the terminal.
func (p *ptyRun) expect(text string) {
	p.t.Helper()
	deadline := time.Now().Add(ptyStep)
	for time.Now().Before(deadline) {
		if strings.Contains(p.output(), text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatalf("never saw %q on the terminal; saw:\n%s", text, p.output())
}

func (p *ptyRun) send(s string) {
	p.t.Helper()
	if _, err := p.tty.WriteString(s); err != nil {
		p.t.Fatalf("typing %q: %v", s, err)
	}
}

// eof is Ctrl-D at an empty line: the end of input, as a person ends it.
func (p *ptyRun) eof() { p.send("\x04") }

// exitCode waits for the process to end and returns its exit code.
func (p *ptyRun) exitCode() int {
	p.t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- p.cmd.Wait() }()
	select {
	case err := <-waited:
		<-p.done
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &exitErr):
			return exitErr.ExitCode()
		default:
			p.t.Fatalf("waiting for afy: %v", err)
		}
	case <-time.After(ptyStep):
		_ = p.cmd.Process.Signal(syscall.SIGKILL)
		p.t.Fatalf("afy did not end after the answer; saw:\n%s", p.output())
	}
	return -1
}

// ptyCase is one prompt: how to start it, what it asks, what answers it, what
// declines it, and what "changed" means for it.
type ptyCase struct {
	name    string
	setup   func(t *testing.T) (env, args []string, changed func() int)
	prompt  string
	accept  string // typed to confirm (the whole script, for a flow that asks more)
	decline string
}

func (c ptyCase) run(t *testing.T, answer func(p *ptyRun), wantExit int, wantChanged bool) {
	t.Helper()
	env, args, changed := c.setup(t)
	p := startOnPTY(t, env, args...)
	p.expect(c.prompt)
	answer(p)
	if got := p.exitCode(); got != wantExit {
		t.Fatalf("exit %d, want %d; saw:\n%s", got, wantExit, p.output())
	}
	if n := changed(); (n > 0) != wantChanged {
		t.Errorf("changed = %d, want changed %v; saw:\n%s", n, wantChanged, p.output())
	}
}

func ptyCases() []ptyCase {
	cpEnv := func(url string) string { return "AETHERFY_API_URL=" + url + "/api/v1" }
	return []ptyCase{
		{
			name: "afy delete",
			setup: func(t *testing.T) ([]string, []string, func() int) {
				cp := newFakeCP(t)
				cp.on("DELETE", "/agents/bot", 200, `{}`)
				return []string{cpEnv(cp.srv.URL)}, []string{"delete", "bot"},
					func() int { return len(cp.called("DELETE", "/agents/bot")) }
			},
			prompt: "Type the agent name to confirm: ", accept: "bot\n", decline: "not-bot\n",
		},
		{
			name: "afy access",
			setup: func(t *testing.T) ([]string, []string, func() int) {
				cp := accessCP(t)
				return []string{cpEnv(cp.srv.URL)}, []string{"access", "bot", "--add", "gamma"},
					func() int { return len(requestsTo(cp, "POST", accessWsPath)) }
			},
			prompt: "Proceed? [y/N] ", accept: "y\n", decline: "n\n",
		},
		{
			name: "afy collections regions",
			setup: func(t *testing.T) ([]string, []string, func() int) {
				vec, cp := vecWithArticles(t), standardCP(t, previewBody(`,"refusal":null`))
				return []string{cpEnv(cp.srv.URL), vectors.EnvVectorsURL + "=" + vec.srv.URL, vectors.EnvAPIRegion + "="},
					[]string{"collections", "regions", "articles", "--regions", "us-east-1,eu-central-1", "--workspace", "default"},
					func() int { return len(requestsTo(cp, "PATCH", changePath)) }
			},
			prompt: "Proceed? [y/N] ", accept: "y\n", decline: "n\n",
		},
		{
			name: "afy init, overwriting aetherfy.yaml",
			setup: func(t *testing.T) ([]string, []string, func() int) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte("runtime: node22\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				cp := twoWorkspaceInitCP(t)
				return []string{cpEnv(cp.srv.URL)}, []string{"init", dir, "--runtime", "python3.11"},
					func() int { return len(cp.called("POST", "/agents")) }
			},
			prompt: "aetherfy.yaml already exists. Overwrite [y/N]: ",
			// The rest of init's questions, typed ahead as in TestInteractiveInitConfirmsAnOverwrite.
			accept:  "y\ninvoice-parser\n2\n\n\n\n\n\n\nmain.py\n",
			decline: "n\n",
		},
		{
			name: "afy deploy, the overage question",
			setup: func(t *testing.T) ([]string, []string, func() int) {
				dir := agentFolder(t, "")
				linkFolder(t, dir, map[string]string{"default": prodID})
				var mu sync.Mutex
				confirmed := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/agents/"+prodID):
						_, _ = w.Write([]byte(`{"id":"` + prodID + `","name":"invoice-parser"}`))
					case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/agents/"+prodID+"/deploy"):
						if r.URL.Query().Get("confirm_overage") == "true" {
							mu.Lock()
							confirmed++
							mu.Unlock()
							w.WriteHeader(http.StatusAccepted)
							_, _ = w.Write([]byte(`{"id":"d-1","agent_id":"` + prodID + `","version":3,"state":"pending"}`))
							return
						}
						w.WriteHeader(http.StatusPaymentRequired)
						_, _ = w.Write([]byte(`{"detail":{"code":"OVERAGE_CONFIRM_REQUIRED","message":"confirm","additional_monthly_usd":5.5}}`))
					default:
						w.WriteHeader(599)
						_, _ = w.Write([]byte(`{"detail":{"code":"unrouted","message":"` + r.Method + " " + r.URL.Path + `"}}`))
					}
				}))
				t.Cleanup(srv.Close)
				return []string{cpEnv(srv.URL)}, []string{"deploy", dir, "--detach"},
					func() int { mu.Lock(); defer mu.Unlock(); return confirmed }
			},
			prompt: "continue? [y/N] ", accept: "y\n", decline: "n\n",
		},
	}
}

func TestEveryPromptOnARealTerminal(t *testing.T) {
	for _, c := range ptyCases() {
		c := c
		t.Run(c.name+"/decline exits 1 and changes nothing", func(t *testing.T) {
			c.run(t, func(p *ptyRun) { p.send(c.decline) }, exitRequestFailed, false)
		})
		t.Run(c.name+"/accept exits 0 and makes the change", func(t *testing.T) {
			c.run(t, func(p *ptyRun) { p.send(c.accept) }, 0, true)
		})
		t.Run(c.name+"/end of input exits 2 and changes nothing", func(t *testing.T) {
			c.run(t, func(p *ptyRun) { p.eof() }, exitInputRefused, false)
		})
	}
}
