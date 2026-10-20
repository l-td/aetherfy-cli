package cmd

import (
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/vectors"
)

// afy collections update <name> --name / --description: PATCH
// …/workspaces/{ws}/collections/{name} on the vectors API (sheet 1.3).

const renamedCollection = `{"name":"articles-v1","description":null,"config":{"params":{"vectors":{"size":768,"distance":"Cosine"}}},"status":"green","updated_at":"2026-10-14T10:00:00Z"}`

const collectionPathResearch = "/api/v1/workspaces/research/collections/articles"

func strp(s string) *string { return &s }

func TestCollectionsUpdateNameRenamesInTheWorkspace(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, renamedCollection })
	r, stdout, stderr, _ := s.run("research", false)

	if code := collectionsUpdate(r, "articles", strp("articles-v1"), nil); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	req := only(t, s.got())
	if req.Method != "PATCH" || req.Path != collectionPathResearch {
		t.Errorf("sent %s %s, want PATCH %s", req.Method, req.Path, collectionPathResearch)
	}
	if len(req.Body) != 1 || req.Body["name"] != "articles-v1" {
		t.Errorf("body = %v, want exactly {name}", req.Body)
	}
	for _, want := range []string{"renamed to 'articles-v1' in workspace 'research'", "must use 'articles-v1' now"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestCollectionsUpdateEmptyDescriptionClearsIt(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, strings.Replace(renamedCollection, `"articles-v1"`, `"articles"`, 1)
	})
	r, _, stderr, _ := s.run("research", false)
	if code := collectionsUpdate(r, "articles", nil, strp("")); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	req := only(t, s.got())
	if v, ok := req.Body["description"]; !ok || v != nil || len(req.Body) != 1 {
		t.Errorf(`--description "" must send exactly {description: null}, got %v`, req.Body)
	}
}

func TestCollectionsUpdateRefusals(t *testing.T) {
	t.Run("nothing to change: refused before any request", func(t *testing.T) {
		s := newVecServer(t, func(vecRequest) (int, string) { return 200, renamedCollection })
		r, _, _, connects := s.run("research", false)
		if code := collectionsUpdate(r, "articles", nil, nil); code != exitInputRefused {
			t.Errorf("exit %d, want %d", code, exitInputRefused)
		}
		if len(s.got()) != 0 || *connects != 0 {
			t.Error("nothing may be resolved or sent")
		}
	})
	t.Run("the server's refusal, as written", func(t *testing.T) {
		const msg = "A collection or alias named 'articles-v1' already exists in workspace 'research'."
		s := newVecServer(t, func(vecRequest) (int, string) {
			return 409, `{"error":{"code":"COLLECTION_NAME_TAKEN","message":"` + msg + `","collection_name":"articles-v1","workspace":"research","taken_by":"alias"}}`
		})
		r, _, stderr, _ := s.run("research", false)
		if code := collectionsUpdate(r, "articles", strp("articles-v1"), nil); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), msg) {
			t.Errorf("stderr must carry the server's sentence: %q", stderr.String())
		}
	})
}

func TestCollectionsUpdateJSONNamesBothNames(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, renamedCollection })
	r, stdout, _, _ := s.run("research", true)
	if code := collectionsUpdate(r, "articles", strp("articles-v1"), nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := decode(t, stdout.String())
	if out["collection"] != "articles-v1" || out["previous_name"] != "articles" || out["workspace"] != "research" {
		t.Errorf("json = %v", out)
	}
}

// Through the real command line: registered under `collections`, its flags
// parsed, and the workspace resolved like every collection command.
func TestCollectionsUpdateRunsAsAfy(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, renamedCollection })
	stdout, stderr, code := runAfyProcess(t, nil,
		[]string{vectors.EnvVectorsURL + "=" + s.srv.URL, vectors.EnvAPIRegion + "="},
		"collections", "update", "articles", "--name", "articles-v1", "--workspace", "research")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	got := s.got()
	if len(got) != 1 || got[0].Method != "PATCH" || got[0].Path != collectionPathResearch || got[0].Body["name"] != "articles-v1" {
		t.Errorf("sent %+v", got)
	}
}
