package cmd

import (
	"strings"
	"testing"
	"time"
)

// afy collections deleted / restore, against a fake vectors API: what they
// send, what --json prints, and the exit code a script sees. The deleted list
// is the ACCOUNT's, so the command keeps the workspace's own; a restore goes to
// the API by id, and a name is only a way to find that id.

const (
	idNotes     = "11111111-1111-4111-8111-111111111111"
	idNotes2    = "22222222-2222-4222-8222-222222222222"
	idOther     = "33333333-3333-4333-8333-333333333333"
	deletedPath = "/api/v1/deleted-collections"
)

func deletedRow(id, name, workspace, status, expires string) string {
	return deletedRowBy(id, name, workspace, status, expires, `{"kind":"person"}`)
}

func deletedRowBy(id, name, workspace, status, expires, by string) string {
	ws := `"` + workspace + `"`
	exp := `"` + expires + `"`
	if expires == "" {
		exp = "null"
	}
	return `{"id":"` + id + `","name":"` + name + `","workspace":` + ws + `,"workspace_id":"w-1","regions":["us-east-1","eu-central-1"],` +
		`"points":120,"bytes":3221225472,"deleted_at":"2026-10-20T10:00:00.000Z","deleted_by":` + by + `,"expires_at":` + exp + `,"status":"` + status + `","restore":null}`
}

const planAllowance = `{"recovery_days":30,"restores_per_month":10,"restores_used":3,"restores_left":7,` +
	`"restore_bytes_per_month":10737418240,"restore_bytes_used":0,"restore_bytes_left":10737418240,"resets_at":"2026-11-01T00:00:00.000Z"}`

func deletedList(rows ...string) string {
	return `{"deleted_collections":[` + strings.Join(rows, ",") + `],"allowance":` + planAllowance + `}`
}

func TestDeletedListsTheWorkspacesOwnAndTheAllowance(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, deletedList(
			deletedRow(idNotes, "notes", "research", "ready", "2026-11-19T10:00:00.000Z"),
			deletedRow(idOther, "other", "elsewhere", "ready", "2026-11-19T10:00:00.000Z"),
			deletedRow(idNotes2, "drafts", "research", "backing_up", ""),
		)
	})
	r, stdout, _, _ := s.run("research", true)
	if code := collectionsDeleted(r); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "GET" || req.Path != deletedPath {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	out := decode(t, stdout.String())
	if out["workspace"] != "research" {
		t.Errorf("envelope = %v", out)
	}
	rows := out["deleted_collections"].([]interface{})
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want the two of workspace research", rows)
	}
	first := rows[0].(map[string]interface{})
	if first["id"] != idNotes || first["name"] != "notes" || first["status"] != "ready" ||
		first["expires_at"] != "2026-11-19T10:00:00.000Z" || first["points"] != 120.0 || first["bytes"] != 3221225472.0 {
		t.Errorf("first = %v", first)
	}
	if rows[1].(map[string]interface{})["expires_at"] != nil {
		t.Errorf("a plan with no time limit has a null expiry: %v", rows[1])
	}
	a := out["allowance"].(map[string]interface{})
	if a["recovery_days"] != 30.0 || a["restores_left"] != 7.0 || a["resets_at"] != "2026-11-01T00:00:00.000Z" {
		t.Errorf("allowance = %v", a)
	}
}

func TestDeletedHumanOutputNamesTheIDTheWindowAndWhatIsLeft(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, deletedList(deletedRow(idNotes, "notes", "research", "ready", "2026-11-19T10:00:00.000Z"))
	})
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsDeleted(r); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		idNotes, "notes", "2026-10-20", "2026-11-19", "3 GB", "ready",
		"1 deleted collection(s) in workspace 'research'",
		"Deleted collections are kept 30 days · 7 of 10 restores left this month · 10 GB of 10 GB left to restore (resets 2026-11-01)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestDeletedWithNothingToRestoreSaysSo(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, deletedList() })
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsDeleted(r); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "No deleted collections to restore in workspace 'research'.") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestAnUnlimitedPlanStatesNoLimitsAndNoReset(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, `{"deleted_collections":[],"allowance":{"recovery_days":null,"restores_per_month":null,"restores_used":0,"restores_left":null,` +
			`"restore_bytes_per_month":null,"restore_bytes_used":0,"restore_bytes_left":null,"resets_at":"2026-11-01T00:00:00.000Z"}}`
	})
	r, stdout, _, _ := s.run("research", false)
	collectionsDeleted(r)
	if !strings.Contains(stdout.String(), "Deleted collections are kept without a time limit\n") ||
		strings.Contains(stdout.String(), "resets") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func restoreServer(t *testing.T, list string, handler func(vecRequest) (int, string)) *vecServer {
	return newVecServer(t, func(r vecRequest) (int, string) {
		if r.Method == "GET" && r.Path == deletedPath {
			return 200, list
		}
		return handler(r)
	})
}

const startedBody = `{"restore_id":"r-1","collection_id":"c-9","name":"notes","workspace_id":"w-1","regions":["us-east-1","eu-central-1"],` +
	`"state":"running","regions_ready":0,"regions_total":2}`

func TestRestoreByIDSendsOnePostAndNoList(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 202, startedBody })
	r, stdout, _, _ := s.run("research", true)
	if code := collectionsRestore(r, idNotes, "notes-again", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "POST" || req.Path != deletedPath+"/"+idNotes+"/restore" {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	if req.Body["as"] != "notes-again" {
		t.Errorf("body = %v", req.Body)
	}
	out := decode(t, stdout.String())
	rs := out["restore"].(map[string]interface{})
	if rs["restore_id"] != "r-1" || rs["collection_id"] != "c-9" || rs["state"] != "running" ||
		rs["regions_ready"] != 0.0 || rs["regions_total"] != 2.0 {
		t.Errorf("restore = %v", rs)
	}
}

func TestRestoreWithoutAsSendsNoName(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 202, startedBody })
	r, _, _, _ := s.run("research", true)
	collectionsRestore(r, idNotes, "", false)
	if _, has := only(t, s.got()).Body["as"]; has {
		t.Error("an empty --as was sent")
	}
}

func TestRestoreByNameFindsTheOneDeletedCollectionOfThatNameInTheWorkspace(t *testing.T) {
	s := restoreServer(t,
		deletedList(
			deletedRow(idOther, "notes", "elsewhere", "ready", ""),
			deletedRow(idNotes, "notes", "research", "ready", ""),
		),
		func(r vecRequest) (int, string) { return 202, startedBody })
	r, _, _, _ := s.run("research", true)
	if code := collectionsRestore(r, "notes", "", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	posts := 0
	for _, q := range s.got() {
		if q.Method == "POST" {
			posts++
			if q.Path != deletedPath+"/"+idNotes+"/restore" {
				t.Errorf("restored %s, want the workspace's own (%s)", q.Path, idNotes)
			}
		}
	}
	if posts != 1 {
		t.Errorf("%d POSTs, want 1: %+v", posts, s.got())
	}
}

func TestRestoreByNameRefusesNoMatchAndAmbiguityWithoutSendingAnything(t *testing.T) {
	list := deletedList(
		deletedRow(idNotes, "notes", "research", "ready", ""),
		deletedRow(idNotes2, "notes", "research", "ready", ""),
	)
	s := restoreServer(t, list, func(vecRequest) (int, string) {
		t.Error("a restore was sent")
		return 202, startedBody
	})
	r, _, stderr, _ := s.run("research", false)
	if code := collectionsRestore(r, "missing", "", false); code != exitInputRefused {
		t.Errorf("no match: exit %d, want %d (input refused before anything was sent)", code, exitInputRefused)
	}
	if !strings.Contains(stderr.String(), "no deleted collection named 'missing' in workspace 'research'") ||
		!strings.Contains(stderr.String(), "afy collections deleted") {
		t.Errorf("stderr %q", stderr.String())
	}
	r2, _, stderr2, _ := s.run("research", false)
	if code := collectionsRestore(r2, "notes", "", false); code != exitInputRefused {
		t.Errorf("ambiguous: exit %d, want %d (input refused before anything was sent)", code, exitInputRefused)
	}
	for _, want := range []string{"2 deleted collections", idNotes, idNotes2, "restore one by its id"} {
		if !strings.Contains(stderr2.String(), want) {
			t.Errorf("ambiguity missing %q: %q", want, stderr2.String())
		}
	}
}

func TestARestoreTheAPIRefusesExitsOneWithItsCodeAndSentence(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   string
	}{
		{409, `{"error":{"code":"COLLECTION_NAME_TAKEN_FOR_RESTORE","message":"The name \"notes\" is in use in workspace \"research\". Restore it under another name with \"as\"."}}`, "COLLECTION_NAME_TAKEN_FOR_RESTORE"},
		{410, `{"error":{"code":"COLLECTION_BACKUP_EXPIRED","message":"expired"}}`, "COLLECTION_BACKUP_EXPIRED"},
		{403, `{"error":{"code":"PLAN_LIMIT_EXCEEDED","message":"Your plan allows 3 restores a month and 3 are used. The limit resets on 2026-11-01.","upgrade_url":"/billing/upgrade","limit":"collection_restores_per_month","allowed":3,"current":3}}`, "PLAN_LIMIT_EXCEEDED"},
		{409, `{"error":{"code":"COLLECTION_BACKUP_UNAVAILABLE","message":"The backup of this deleted collection could not be completed. Its data is kept, and support can retry it."}}`, "COLLECTION_BACKUP_UNAVAILABLE"},
		{403, `{"error":{"code":"AUTH_AGENT_KEY_OUT_OF_SCOPE","message":"an agent key cannot do this"}}`, "AUTH_AGENT_KEY_OUT_OF_SCOPE"},
	}
	for _, c := range cases {
		s := newVecServer(t, func(vecRequest) (int, string) { return c.status, c.body })
		r, _, stderr, _ := s.run("research", true)
		if code := collectionsRestore(r, idNotes, "", false); code != exitRequestFailed {
			t.Errorf("%s: exit %d", c.code, code)
		}
		e := decode(t, stderr.String())["error"].(map[string]interface{})
		if e["code"] != c.code || e["status"] != float64(c.status) {
			t.Errorf("%s: error = %v", c.code, e)
		}
	}
}

func progressServer(t *testing.T, states ...string) *vecServer {
	n := 0
	return newVecServer(t, func(r vecRequest) (int, string) {
		if r.Method == "POST" {
			return 202, startedBody
		}
		s := states[len(states)-1]
		if n < len(states) {
			s = states[n]
		}
		n++
		ready := 1
		if s == "done" {
			ready = 2
		}
		extra := ""
		if s == "failed" {
			extra = `,"error":"The restore could not be completed; the deleted collection is still restorable."`
		}
		return 200, `{"id":"r-1","collection_id":"c-9","state":"` + s + `","regions_ready":` + string(rune('0'+ready)) + `,"regions_total":2` + extra + `}`
	})
}

func TestRestoreWaitFollowsTheRestoreToItsEnd(t *testing.T) {
	cases := []struct {
		name   string
		states []string
		bound  time.Duration
		want   int
		in     string
	}{
		{"done", []string{"running", "running", "done"}, time.Minute, 0, "restored"},
		{"failed: exit 1 with its reason", []string{"running", "failed"}, time.Minute, exitRequestFailed, "still restorable"},
		{"still running at the bound: exit 4", []string{"running"}, 3 * time.Second, exitStillPending, "still running (1 of 2 regions ready)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shortWait(t, c.bound)
			s := progressServer(t, c.states...)
			r, stdout, stderr, _ := s.run("research", false)
			if code := collectionsRestore(r, idNotes, "", true); code != c.want {
				t.Fatalf("exit %d, want %d (stderr %q)", code, c.want, stderr.String())
			}
			if !strings.Contains(stdout.String()+stderr.String(), c.in) {
				t.Errorf("want %q in:\nstdout %q\nstderr %q", c.in, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRestoreWaitBoundCountsTheTimeThatPassedNotTheTimeSlept(t *testing.T) {
	// Every poll takes ten minutes to answer (a slow server): three polls are thirty minutes
	// whatever was slept between them, and the bound is thirty minutes.
	clock := shortWait(t, 30*time.Minute)
	n := 0
	s := newVecServer(t, func(r vecRequest) (int, string) {
		if r.Method == "POST" {
			return 202, startedBody
		}
		n++
		clock.now = clock.now.Add(10 * time.Minute)
		return 200, `{"id":"r-1","collection_id":"c-9","state":"running","regions_ready":1,"regions_total":2}`
	})
	r, _, stderr, _ := s.run("research", false)
	if code := collectionsRestore(r, idNotes, "", true); code != exitStillPending {
		t.Fatalf("exit %d, want %d (stderr %q)", code, exitStillPending, stderr.String())
	}
	if n > 3 {
		t.Errorf("%d polls: the bound was passed after 30 minutes and it kept asking", n)
	}
	if !strings.Contains(stderr.String(), "still running") {
		t.Errorf("stderr %q", stderr.String())
	}
}

func TestDeletedSaysWhoDeletedIt(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, deletedList(
			deletedRowBy(idNotes, "notes", "research", "ready", "", `{"kind":"agent","agent_id":"a-77"}`),
			deletedRow(idNotes2, "drafts", "research", "ready", ""),
		)
	})
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsDeleted(r); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"DELETED BY", "agent a-77", "person"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q in:\n%s", want, stdout.String())
		}
	}
	rj, jout, _, _ := s.run("research", true)
	if code := collectionsDeleted(rj); code != 0 {
		t.Fatalf("exit %d", code)
	}
	rows := decode(t, jout.String())["deleted_collections"].([]interface{})
	by := rows[0].(map[string]interface{})["deleted_by"].(map[string]interface{})
	if by["kind"] != "agent" || by["agent_id"] != "a-77" {
		t.Errorf("deleted_by = %v", by)
	}
}

func TestRestoreWaitJSONCarriesTheFinalProgress(t *testing.T) {
	shortWait(t, time.Minute)
	s := progressServer(t, "running", "done")
	r, stdout, _, _ := s.run("research", true)
	if code := collectionsRestore(r, idNotes, "", true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	rs := decode(t, stdout.String())["restore"].(map[string]interface{})
	if rs["state"] != "done" || rs["regions_ready"] != 2.0 || rs["regions_total"] != 2.0 {
		t.Errorf("restore = %v", rs)
	}
}

func TestRestoreWithoutWaitSaysItRunsInTheBackground(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 202, startedBody })
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestore(r, idNotes, "", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Restoring 'notes'", "0 of 2 regions ready", "--wait"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q: %s", want, stdout.String())
		}
	}
}

func TestTheRecoveryCommandsAreOnTheCollectionsGroup(t *testing.T) {
	names := map[string]bool{}
	for _, c := range collectionsCmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"deleted", "restore"} {
		if !names[want] {
			t.Errorf("afy collections %s is not registered", want)
		}
	}
	if collectionsRestoreCmd.Flags().Lookup("as") == nil || collectionsRestoreCmd.Flags().Lookup("wait") == nil {
		t.Error("restore lacks --as or --wait")
	}
}
