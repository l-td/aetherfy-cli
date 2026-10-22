package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// afy collections protect / unprotect, against a fake vectors API (which
// answers the collection's id) and a fake control plane: the change goes by
// id, alone; protecting asks nothing; unprotecting asks first and needs --yes
// where nobody can answer.

func protectCP(t *testing.T, answer string) *vecServer {
	return cpFake(t, map[string]func(vecRequest) (int, string){
		"PATCH " + changePath: func(vecRequest) (int, string) { return 200, answer },
	})
}

func TestProtectSendsOnlyProtectedByTheCollectionsIDAndAsksNothing(t *testing.T) {
	cp := protectCP(t, `{"collection_id":"c-1","protected":true,"no_op":false}`)
	r, stdout, stderr := runWithCP(vecWithArticles(t), cp, false)
	if code := collectionsProtect(r, "articles"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	reqs := requestsTo(cp, "PATCH", changePath)
	if len(reqs) != 1 || reqs[0].Body["protected"] != true || len(reqs[0].Body) != 1 {
		t.Fatalf("PATCH = %v", reqs)
	}
	if strings.Contains(stderr.String(), "Proceed?") {
		t.Error("protecting asked")
	}
	if !strings.Contains(stdout.String(), "An agent with write access can still change or overwrite points; to stop all changes, give it read access.") {
		t.Errorf("the confirmation must say what protection does not stop: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Collection 'articles' is protected: no agent's key may delete it or remove data from it") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestUnprotectAsksAndSendsNothingUnlessConfirmed(t *testing.T) {
	cp := protectCP(t, `{"collection_id":"c-1","protected":false,"no_op":false}`)
	r, stdout, stderr := runWithCP(vecWithArticles(t), cp, false)
	// A "no" is a cancelled change: exit 1 (a script must never read it as done), nothing sent.
	if code := collectionsUnprotect(r, "articles", false, strings.NewReader("n\n"), true); code != exitRequestFailed {
		t.Fatalf("exit %d, want %d", code, exitRequestFailed)
	}
	if len(requestsTo(cp, "PATCH", changePath)) != 0 {
		t.Error("unprotected without a yes")
	}
	if !strings.Contains(stderr.String(), "Proceed?") || !strings.Contains(stderr.String(), "Cancelled: collection 'articles' was not changed") {
		t.Errorf("stderr %q", stderr.String())
	}
	// The canonical sentence, word for word (cmd/access_words.go; the design doc).
	if !strings.Contains(stderr.String(), "Unprotecting 'articles' lets agents with read and write access delete its points, "+
		"and agents with full access delete the collection.") {
		t.Errorf("the canonical sentence is missing: %q", stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout %q", stdout.String())
	}

	cp2 := protectCP(t, `{"collection_id":"c-1","protected":false,"no_op":false}`)
	r2, _, stderr2 := runWithCP(vecWithArticles(t), cp2, false)
	if code := collectionsUnprotect(r2, "articles", false, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr2.String())
	}
	if reqs := requestsTo(cp2, "PATCH", changePath); len(reqs) != 1 || reqs[0].Body["protected"] != false {
		t.Fatalf("PATCH = %v", reqs)
	}
}

func TestUnprotectWithoutATerminalNeedsYesAndSendsNothing(t *testing.T) {
	cp := protectCP(t, `{}`)
	vec := vecWithArticles(t)
	r, _, stderr := runWithCP(vec, cp, false)
	if code := collectionsUnprotect(r, "articles", false, strings.NewReader("y\n"), false); code != exitInputRefused {
		t.Fatalf("exit %d", code)
	}
	if len(cp.got()) != 0 || len(vec.got()) != 0 {
		t.Errorf("sent before refusing: control plane %v, vectors %v", cp.got(), vec.got())
	}
	if !strings.Contains(stderr.String(), "This turns protection off for collection 'articles'; pass --yes to confirm without a prompt.") {
		t.Errorf("stderr %q", stderr.String())
	}
}

// --json keeps stdout to one object and still asks at a terminal, on stderr.
func TestUnprotectWithJSONAtATerminalAsksOnStderr(t *testing.T) {
	cp := protectCP(t, `{"collection_id":"c-1","protected":false,"no_op":false}`)
	r, stdout, stderr := runWithCP(vecWithArticles(t), cp, true)
	if code := collectionsUnprotect(r, "articles", false, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Proceed?") {
		t.Errorf("--json did not ask on stderr: %q", stderr.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil || got["protected"] != false {
		t.Errorf("stdout is not the one object: %v\n%s", err, stdout.String())
	}
}

func TestProtectJSONAndNoOp(t *testing.T) {
	cp := protectCP(t, `{"collection_id":"c-1","protected":true,"no_op":true}`)
	r, stdout, stderr := runWithCP(vecWithArticles(t), cp, true)
	if code := collectionsProtect(r, "articles"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got struct {
		Collection string `json:"collection"`
		Protected  bool   `json:"protected"`
		NoOp       bool   `json:"no_op"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, stdout.String())
	}
	if got.Collection != "articles" || !got.Protected || !got.NoOp {
		t.Errorf("json = %+v", got)
	}
}
