package cmd

import (
	"strings"
	"testing"
)

// The CLI's half of what the dashboard shows (owner, 2026-10-12: the CLI is
// paired with the dashboard for every functional change): a collection's id
// and the change running on it, the account's region-change window, and an
// agent's access as {id, name} pairs. Against a fake vectors API and a fake
// control plane, through the real clients.

const operationPath = "/api/v1/collections/c-1/operation"

func TestCollectionsGetShowsItsIDAndTheChangeRunningOnIt(t *testing.T) {
	vec := vecWithArticles(t)
	cp := cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + operationPath: func(vecRequest) (int, string) {
			return 200, `{"collection_id":"c-1","operation":{"operation_id":"op-7","kind":"regions_change",` +
				`"status":"in_progress","from_regions":["us-east-1"],"to_regions":["us-east-1","eu-central-1"],` +
				`"target_workspace":null,"accepted_at":"2026-10-12T10:00:00Z"}}`
		},
	})
	r, out, errOut := runWithCP(vec, cp, false)
	if code := collectionsGet(r, "articles"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"ID:", "c-1", "Change:", "replicating to us-east-1, eu-central-1 (in progress, operation op-7)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
}

func TestCollectionsGetNamesAMovesDestination(t *testing.T) {
	cp := cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + operationPath: func(vecRequest) (int, string) {
			return 200, `{"collection_id":"c-1","operation":{"operation_id":"op-8","kind":"move","status":"pending",` +
				`"from_regions":["us-east-1"],"to_regions":["us-east-1"],` +
				`"target_workspace":{"id":"w-2","name":"research"},"accepted_at":"2026-10-12T10:00:00Z"}}`
		},
	})
	r, out, errOut := runWithCP(vecWithArticles(t), cp, false)
	if code := collectionsGet(r, "articles"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "moving to workspace 'research' (pending, operation op-8)") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestCollectionsGetJSONCarriesTheIDAndTheOperation(t *testing.T) {
	cp := cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + operationPath: func(vecRequest) (int, string) { return 200, `{"collection_id":"c-1","operation":null}` },
	})
	r, out, errOut := runWithCP(vecWithArticles(t), cp, true)
	if code := collectionsGet(r, "articles"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	body := decode(t, out.String())
	if body["collection"].(map[string]interface{})["id"] != "c-1" {
		t.Errorf("collection = %v", body["collection"])
	}
	if op, present := body["operation"]; !present || op != nil {
		t.Errorf("operation = %v (present %v), want null when none runs", op, present)
	}
	if _, present := body["operation_error"]; present {
		t.Error("operation_error present on a read that succeeded")
	}
}

func TestCollectionsGetSaysWhenTheChangeCouldNotBeRead(t *testing.T) {
	// A failed read is SAID, never shown as "nothing running": the collection
	// itself was read, so the command still succeeds.
	cp := cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + operationPath: func(vecRequest) (int, string) {
			return 503, `{"detail":{"code":"SERVICE_UNAVAILABLE","message":"try again"}}`
		},
	})
	r, out, errOut := runWithCP(vecWithArticles(t), cp, false)
	if code := collectionsGet(r, "articles"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Change:") || !strings.Contains(out.String(), "could not be read") {
		t.Errorf("output:\n%s", out.String())
	}
}

const regionChangesPath = "/api/v1/users/me/region-changes"

func regionChangesCP(t *testing.T, body string) *vecServer {
	return cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + regionChangesPath: func(vecRequest) (int, string) { return 200, body },
	})
}

func TestRegionLimitsShowsUsedAgainstThePlansLimits(t *testing.T) {
	cp := regionChangesCP(t, `{"window_days":30,"count_used":2,"count_limit":10,"size_used_bytes":9017753,`+
		`"size_limit_bytes":429496729600,"frees_at":"2026-11-10T22:17:16+00:00"}`)
	r, out, errOut := runWithCP(noVectors(t), cp, false)
	if code := collectionsRegionLimits(r); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"last 30 days", "2 of 10", "8.6 MB of 400 GB"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "At the limit") {
		t.Errorf("not at the limit, but said so:\n%s", out.String())
	}
}

func TestRegionLimitsNoLimitAndAtTheLimit(t *testing.T) {
	cp := regionChangesCP(t, `{"window_days":30,"count_used":1,"count_limit":null,"size_used_bytes":10,`+
		`"size_limit_bytes":null,"frees_at":null}`)
	r, out, _ := runWithCP(noVectors(t), cp, false)
	collectionsRegionLimits(r)
	if !strings.Contains(out.String(), "1, no limit") || !strings.Contains(out.String(), "10 B, no limit") {
		t.Errorf("a null limit must read as no limit:\n%s", out.String())
	}

	full := regionChangesCP(t, `{"window_days":30,"count_used":10,"count_limit":10,"size_used_bytes":10,`+
		`"size_limit_bytes":2000,"frees_at":"2026-11-10T22:17:16+00:00"}`)
	r, out, _ = runWithCP(noVectors(t), full, false)
	collectionsRegionLimits(r)
	if !strings.Contains(out.String(), "At the limit") || !strings.Contains(out.String(), "2026-11-10T22:17:16+00:00") {
		t.Errorf("at the limit, the free-up time must be said:\n%s", out.String())
	}
}

func TestRegionLimitsJSONIsTheControlPlanesAnswer(t *testing.T) {
	cp := regionChangesCP(t, `{"window_days":30,"count_used":2,"count_limit":10,"size_used_bytes":5,`+
		`"size_limit_bytes":2000,"frees_at":null}`)
	r, out, errOut := runWithCP(noVectors(t), cp, true)
	if code := collectionsRegionLimits(r); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	rc := decode(t, out.String())["region_limits"].(map[string]interface{})
	if rc["count_used"] != 2.0 || rc["count_limit"] != 10.0 || rc["size_limit_bytes"] != 2000.0 || rc["frees_at"] != nil {
		t.Errorf("region_limits = %v", rc)
	}
}

func TestAccessJSONCarriesTheWorkspacePairs(t *testing.T) {
	withRefs := `{"agent":"bot","own_workspace":"alpha","own_workspace_ref":{"id":"w-a","name":"alpha"},` +
		`"granted_workspaces":["beta"],"granted_workspace_refs":[{"id":"w-b","name":"beta"}],` +
		`"allowed_workspaces":["alpha","beta"],` +
		`"allowed_workspace_refs":[{"id":"w-a","name":"alpha"},{"id":"w-b","name":"beta"}],` +
		`"changed":null}`
	cp := cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + accessPath: func(vecRequest) (int, string) { return 200, withRefs },
	})
	r, out, errOut := runWithCP(noVectors(t), cp, true)
	if code := agentsAccess(r, "bot", nil, nil, false, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	access := decode(t, out.String())["access"].(map[string]interface{})
	own := access["own_workspace_ref"].(map[string]interface{})
	if own["id"] != "w-a" || own["name"] != "alpha" {
		t.Errorf("own_workspace_ref = %v", own)
	}
	granted := access["granted_workspace_refs"].([]interface{})
	if len(granted) != 1 || granted[0].(map[string]interface{})["id"] != "w-b" {
		t.Errorf("granted_workspace_refs = %v", granted)
	}
	if allowed := access["allowed_workspace_refs"].([]interface{}); len(allowed) != 2 {
		t.Errorf("allowed_workspace_refs = %v", allowed)
	}
}
