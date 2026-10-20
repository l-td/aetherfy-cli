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
	if code := collectionsProtect(r, "articles", true, false, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	reqs := requestsTo(cp, "PATCH", changePath)
	if len(reqs) != 1 || reqs[0].Body["protected"] != true || len(reqs[0].Body) != 1 {
		t.Fatalf("PATCH = %v", reqs)
	}
	if strings.Contains(stderr.String(), "Proceed?") {
		t.Error("protecting asked")
	}
	if !strings.Contains(stdout.String(), "Collection 'articles' is protected") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestUnprotectAsksAndSendsNothingUnlessConfirmed(t *testing.T) {
	cp := protectCP(t, `{"collection_id":"c-1","protected":false,"no_op":false}`)
	r, stdout, stderr := runWithCP(vecWithArticles(t), cp, false)
	if code := collectionsProtect(r, "articles", false, false, strings.NewReader("n\n"), true); code != exitInputRefused {
		t.Fatalf("exit %d", code)
	}
	if len(requestsTo(cp, "PATCH", changePath)) != 0 {
		t.Error("unprotected without a yes")
	}
	if !strings.Contains(stdout.String(), "will be able to delete collection 'articles'") || !strings.Contains(stderr.String(), "Proceed?") {
		t.Errorf("stdout %q stderr %q", stdout.String(), stderr.String())
	}

	cp2 := protectCP(t, `{"collection_id":"c-1","protected":false,"no_op":false}`)
	r2, _, stderr2 := runWithCP(vecWithArticles(t), cp2, false)
	if code := collectionsProtect(r2, "articles", false, false, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr2.String())
	}
	if reqs := requestsTo(cp2, "PATCH", changePath); len(reqs) != 1 || reqs[0].Body["protected"] != false {
		t.Fatalf("PATCH = %v", reqs)
	}
}

func TestUnprotectWithJSONOrNoTerminalNeedsYesAndSendsNothing(t *testing.T) {
	for _, asJSON := range []bool{true, false} {
		cp := protectCP(t, `{}`)
		r, _, stderr := runWithCP(vecWithArticles(t), cp, asJSON)
		if code := collectionsProtect(r, "articles", false, false, strings.NewReader("y\n"), false); code != exitInputRefused {
			t.Fatalf("json=%v: exit %d", asJSON, code)
		}
		if len(cp.got()) != 0 || !strings.Contains(stderr.String(), "--yes") {
			t.Errorf("json=%v: sent %v, stderr %q", asJSON, cp.got(), stderr.String())
		}
	}
}

func TestProtectJSONAndNoOp(t *testing.T) {
	cp := protectCP(t, `{"collection_id":"c-1","protected":true,"no_op":true}`)
	r, stdout, stderr := runWithCP(vecWithArticles(t), cp, true)
	if code := collectionsProtect(r, "articles", true, false, strings.NewReader(""), false); code != 0 {
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
