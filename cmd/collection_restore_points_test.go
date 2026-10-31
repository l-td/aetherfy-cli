package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/vectors"
)

// afy collections restore-points / restore, against a fake vectors API: what they send, what --json
// prints, what a person reads, and the exit code a script sees. A restore point is addressed by the id a
// list gives, as given: the CLI never turns a name into one and never guesses what an argument is from
// its shape.

const (
	idNotes      = "11111111-1111-4111-8111-111111111111"
	idNotes2     = "22222222-2222-4222-8222-222222222222"
	idOther      = "33333333-3333-4333-8333-333333333333"
	pointsPath   = "/api/v1/restore-points"
	notesPoints  = "/api/v1/workspaces/research/collections/notes/restore-points"
	dailyPointID = "day_" + idNotes + "_2026-10-30"
)

const planAllowance = `{"recovery_days":30,"daily_days":7,"restores_per_month":10,"restores_used":3,"restores_left":7,` +
	`"restore_bytes_per_month":10737418240,"restore_bytes_used":0,"restore_bytes_left":10737418240,"resets_at":"2026-11-01T00:00:00.000Z"}`

// deletedPoint is a deletion copy: the copy taken when a collection was deleted.
func deletedPoint(id, collectionID, name, workspace, status, expires string) string {
	return deletedPointBy(id, collectionID, name, workspace, status, expires, `{"kind":"person"}`)
}

func deletedPointBy(id, collectionID, name, workspace, status, expires, by string) string {
	ws := `"` + workspace + `"`
	exp := `"` + expires + `"`
	if expires == "" {
		exp = "null"
	}
	return `{"id":"` + id + `","kind":"deletion","collection":{"id":"` + collectionID + `","name":"` + name + `","workspace":` + ws + `,"workspace_id":"w-1","deleted":true},` +
		`"taken_at":"2026-10-20T10:00:00.000Z","regions":["us-east-1","eu-central-1"],"points":120,"bytes":3221225472,` +
		`"deleted_by":` + by + `,"expires_at":` + exp + `,"status":"` + status + `","restore":null}`
}

// dailyPoint is one day's copy of the live collection notes.
func dailyPoint(day, from, to string) string {
	return `{"id":"day_` + idNotes + `_` + day + `","kind":"daily","collection":{"id":"` + idNotes + `","name":"notes","workspace":"research","workspace_id":"w-1","deleted":false},` +
		`"day":"` + day + `","taken_at":"` + to + `","taken_from":"` + from + `","taken_to":"` + to + `","regions":["us-east-1"],"points":1200,"bytes":5242880,"status":"ready","restore":null}`
}

func deletedPoints(rows ...string) string {
	return `{"restore_points":[` + strings.Join(rows, ",") + `],"allowance":` + planAllowance + `}`
}

func collectionPoints(deleted string, rows ...string) string {
	return `{"collection":{"id":"` + idNotes + `","name":"notes","workspace":"research","workspace_id":"w-1","deleted":` + deleted + `},` +
		`"restore_points":[` + strings.Join(rows, ",") + `],"allowance":` + planAllowance + `}`
}

func TestRestorePointsWithNoArgumentListsTheWorkspacesDeletedCollectionsAndTheAllowance(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, deletedPoints(
			deletedPoint(idNotes, idNotes2, "notes", "research", "ready", "2026-11-19T10:00:00.000Z"),
			deletedPoint(idOther, idOther, "other", "elsewhere", "ready", "2026-11-19T10:00:00.000Z"),
			deletedPoint(idNotes2, idNotes2, "drafts", "research", "backing_up", ""),
		)
	})
	r, stdout, _, _ := s.run("research", true)
	if code := collectionsRestorePoints(r, nil, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "GET" || req.Path != pointsPath || req.Query != "deleted=true" {
		t.Errorf("sent %s %s?%s", req.Method, req.Path, req.Query)
	}
	out := decode(t, stdout.String())
	if out["workspace"] != "research" {
		t.Errorf("envelope = %v", out)
	}
	rows := out["restore_points"].([]interface{})
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want the two of workspace research", rows)
	}
	first := rows[0].(map[string]interface{})
	if first["id"] != idNotes || first["kind"] != "deletion" || first["status"] != "ready" ||
		first["expires_at"] != "2026-11-19T10:00:00.000Z" || first["points"] != 120.0 || first["bytes"] != 3221225472.0 {
		t.Errorf("first = %v", first)
	}
	if rows[1].(map[string]interface{})["expires_at"] != nil {
		t.Errorf("a plan with no time limit has a null expiry: %v", rows[1])
	}
	a := out["allowance"].(map[string]interface{})
	if a["recovery_days"] != 30.0 || a["daily_days"] != 7.0 || a["restores_left"] != 7.0 || a["resets_at"] != "2026-11-01T00:00:00.000Z" {
		t.Errorf("allowance = %v", a)
	}
}

func TestRestorePointsOfDeletedCollectionsNamesTheIDsAndWhatIsLeft(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, deletedPoints(deletedPoint(idNotes, idNotes2, "notes", "research", "ready", "2026-11-19T10:00:00.000Z"))
	})
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestorePoints(r, nil, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"RESTORE POINT", "COLLECTION ID", idNotes, idNotes2, "notes", "2026-10-20", "2026-11-19", "3 GB", "ready",
		"1 deleted collection(s) in workspace 'research'",
		"Deleted collections are kept 30 days · 7 of 10 restores left this month · 10 GB of 10 GB left to restore (resets 2026-11-01)",
		"afy collections restore-points --collection-id <Collection ID>",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Restore to any of the last") {
		t.Errorf("the deleted list says nothing of daily copies' days:\n%s", stdout.String())
	}
}

func TestRestorePointsOfDeletedCollectionsOnAPlanWithNoDailyCopiesPointsAtNothing(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, strings.Replace(deletedPoints(deletedPoint(idNotes, idNotes2, "notes", "research", "ready", "")), `"daily_days":7`, `"daily_days":0`, 1)
	})
	r, stdout, _, _ := s.run("research", false)
	collectionsRestorePoints(r, nil, "")
	if strings.Contains(stdout.String(), "--collection-id") {
		t.Errorf("a plan with no daily copies is not pointed at them:\n%s", stdout.String())
	}
}

func TestRestorePointsWithNothingToRestoreSaysSo(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, deletedPoints() })
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestorePoints(r, nil, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "No deleted collections to restore in workspace 'research'.") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestRestorePointsOfALiveCollectionAreAskedByNameAndShowTheRangeEachWasTakenIn(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, collectionPoints("false",
			dailyPoint("2026-10-30", "2026-10-30T04:31:10.000Z", "2026-10-30T06:02:40.000Z"),
			dailyPoint("2026-10-29", "2026-10-29T04:31:10.000Z", "2026-10-29T04:31:40.000Z"),
		)
	})
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestorePoints(r, []string{"notes"}, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "GET" || req.Path != notesPoints || req.Query != "" {
		t.Errorf("sent %s %s?%s", req.Method, req.Path, req.Query)
	}
	for _, want := range []string{
		dailyPointID, "daily", "2026-10-30 04:31 to 06:02", // a range where the shards were hours apart
		"2026-10-29", "5 MB",
		"2 restore point(s) of 'notes' in workspace 'research'",
		"Restore to any of the last 7 days · 7 of 10 restores left this month · 10 GB of 10 GB left to restore (resets 2026-11-01)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "2026-10-29 04:31 to") {
		t.Errorf("shards taken within a minute are one time, not a range:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Deleted collections are kept") {
		t.Errorf("a live collection's list says nothing of deleted collections:\n%s", stdout.String())
	}
}

func TestRestorePointsByCollectionIDAreAskedByIDAndHoldBothKindsOfADeletedCollection(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, collectionPoints("true",
			deletedPoint(idOther, idNotes, "notes", "research", "ready", "2026-11-19T10:00:00.000Z"),
			dailyPoint("2026-10-19", "2026-10-19T04:31:10.000Z", "2026-10-19T04:31:20.000Z"),
		)
	})
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestorePoints(r, nil, idNotes); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "GET" || req.Path != pointsPath || req.Query != "collection_id="+idNotes {
		t.Errorf("sent %s %s?%s", req.Method, req.Path, req.Query)
	}
	for _, want := range []string{"deletion", "(deleted)", "daily", "2 restore point(s) of 'notes'",
		"Restore to any of the last 7 days · Deleted collections are kept 30 days"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRestorePointsJSONIsTheEnvelopeWithThePointsAndTheAllowance(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, collectionPoints("false", dailyPoint("2026-10-30", "2026-10-30T04:31:10.000Z", "2026-10-30T06:02:40.000Z"))
	})
	r, stdout, _, _ := s.run("research", true)
	if code := collectionsRestorePoints(r, []string{"notes"}, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := decode(t, stdout.String())
	if out["workspace"] != "research" || out["collection"].(map[string]interface{})["name"] != "notes" {
		t.Errorf("envelope = %v", out)
	}
	points := out["restore_points"].([]interface{})
	first := points[0].(map[string]interface{})
	if len(points) != 1 || first["id"] != dailyPointID || first["kind"] != "daily" || first["day"] != "2026-10-30" ||
		first["taken_from"] != "2026-10-30T04:31:10.000Z" || first["taken_to"] != "2026-10-30T06:02:40.000Z" ||
		first["points"] != 1200.0 || first["bytes"] != 5242880.0 {
		t.Errorf("restore_points = %v", points)
	}
	if a := out["allowance"].(map[string]interface{}); a["restores_left"] != 7.0 || a["daily_days"] != 7.0 {
		t.Errorf("allowance = %v", a)
	}
}

func TestRestorePointsOfACollectionWithNoneYetSaysWhenOneAppears(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, collectionPoints("false") })
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestorePoints(r, []string{"notes"}, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "No restore points of 'notes' in workspace 'research' in the last 7 days yet.") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestRestorePointsOfAPlanWithNoDailyCopiesIsNotToldItWillGetSomeTomorrow(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, strings.Replace(collectionPoints("false"), `"daily_days":7`, `"daily_days":0`, 1)
	})
	r, stdout, _, _ := s.run("research", false)
	collectionsRestorePoints(r, []string{"notes"}, "")
	if !strings.Contains(stdout.String(), "No restore points of 'notes' in workspace 'research'.") ||
		strings.Contains(stdout.String(), "daily backup runs") || strings.Contains(stdout.String(), "last 0 days") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestRestorePointsRefusesANameAndAnIDTogetherBeforeSendingAnything(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, collectionPoints("false") })
	r, _, stderr, connects := s.run("research", false)
	if code := collectionsRestorePoints(r, []string{"notes"}, idNotes); code != exitInputRefused {
		t.Errorf("exit %d, want %d", code, exitInputRefused)
	}
	if !strings.Contains(stderr.String(), "name a collection or give --collection-id, not both") {
		t.Errorf("stderr %q", stderr.String())
	}
	if *connects != 0 || len(s.got()) != 0 {
		t.Error("resolved or sent something")
	}
}

func TestEveryFormOfTheListReportsTheAPIsErrorAndExitsOne(t *testing.T) {
	// The three ways to ask (a live collection by name, the deleted ones, one collection by id) are one command,
	// so the table of every vector command holds one of them; this holds the rest.
	forms := map[string]func(r *vecRun) int{
		"a live collection": func(r *vecRun) int { return collectionsRestorePoints(r, []string{"notes"}, "") },
		"the deleted ones":  func(r *vecRun) int { return collectionsRestorePoints(r, nil, "") },
		"one by its id":     func(r *vecRun) int { return collectionsRestorePoints(r, nil, idNotes) },
	}
	for name, run := range forms {
		s := newVecServer(t, func(vecRequest) (int, string) {
			return 404, `{"error":{"code":"COLLECTION_NOT_FOUND","message":"There is no collection with that id on this account."}}`
		})
		r, stdout, stderr, _ := s.run("research", true)
		if code := run(r); code != exitRequestFailed {
			t.Errorf("%s: exit %d, want %d", name, code, exitRequestFailed)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: printed on stdout: %q", name, stdout.String())
		}
		if e := decode(t, stderr.String())["error"].(map[string]interface{}); e["code"] != "COLLECTION_NOT_FOUND" || e["status"] != 404.0 {
			t.Errorf("%s: error = %v", name, e)
		}
	}
}

func TestRestorePointsLineOnAnUnlimitedPlanStatesNoRestoreLimits(t *testing.T) {
	line := allowanceLine(vectors.RestoreAllowance{DailyDays: 7, ResetsAt: "2026-11-01T00:00:00.000Z"}, true, true)
	if line != "Restore to any of the last 7 days · Deleted collections are kept without a time limit" {
		t.Errorf("line = %q", line)
	}
	if strings.Contains(line, "resets") {
		t.Errorf("an unlimited plan has nothing to reset: %q", line)
	}
}

const startedBody = `{"restore_id":"r-1","collection_id":"c-9","name":"notes","workspace_id":"w-1","regions":["us-east-1","eu-central-1"],` +
	`"state":"running","regions_ready":0,"regions_total":2}`

func TestRestoreOfAPointSendsOnePostWithTheIDAsGivenAndTheName(t *testing.T) {
	for _, point := range []string{idNotes, dailyPointID, "anything-the-api-will-call-a-404"} {
		s := newVecServer(t, func(vecRequest) (int, string) { return 202, startedBody })
		r, stdout, _, _ := s.run("research", true)
		if code := collectionsRestore(r, point, "notes-again", false); code != 0 {
			t.Fatalf("%s: exit %d", point, code)
		}
		req := only(t, s.got())
		if req.Method != "POST" || req.Path != pointsPath+"/"+point+"/restore" {
			t.Errorf("sent %s %s", req.Method, req.Path)
		}
		if req.Body["as"] != "notes-again" {
			t.Errorf("body = %v", req.Body)
		}
		rs := decode(t, stdout.String())["restore"].(map[string]interface{})
		if rs["restore_id"] != "r-1" || rs["collection_id"] != "c-9" || rs["state"] != "running" ||
			rs["regions_ready"] != 0.0 || rs["regions_total"] != 2.0 {
			t.Errorf("restore = %v", rs)
		}
	}
}

func TestRestoreWithoutAsSendsNoNameSoTheAPIPicksIt(t *testing.T) {
	for _, as := range []string{"", "   "} {
		s := newVecServer(t, func(vecRequest) (int, string) { return 202, startedBody })
		r, _, _, _ := s.run("research", true)
		collectionsRestore(r, idNotes, as, false)
		if _, has := only(t, s.got()).Body["as"]; has {
			t.Errorf("a blank --as %q was sent", as)
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
		{403, `{"error":{"code":"PLAN_LIMIT_EXCEEDED","message":"Your plan allows 3 restores a month and 3 are used. The limit resets on 2026-11-01.","upgrade_url":"https://app.aetherfy.com/dashboard/settings/billing","limit":"collection_restores_per_month","allowed":3,"current":3}}`, "PLAN_LIMIT_EXCEEDED"},
		{403, `{"error":{"code":"PLAN_LIMIT_EXCEEDED","message":"Your plan does not restore a collection from its daily copies.","upgrade_url":"https://app.aetherfy.com/dashboard/settings/billing","limit":"collection_backup_restore_days","allowed":0}}`, "PLAN_LIMIT_EXCEEDED"},
		{404, `{"error":{"code":"COLLECTION_BACKUP_NOT_FOUND","message":"There is no restore point with that id on this account."}}`, "COLLECTION_BACKUP_NOT_FOUND"},
		{409, `{"error":{"code":"COLLECTION_BACKUP_UNAVAILABLE","message":"The backup of this deleted collection could not be completed. Its data is kept, and support can retry it."}}`, "COLLECTION_BACKUP_UNAVAILABLE"},
		{409, `{"error":{"code":"COLLECTION_RESTORE_REGIONS_UNAVAILABLE","message":"The workspace no longer covers eu-central-1","missing_regions":["eu-central-1"]}}`, "COLLECTION_RESTORE_REGIONS_UNAVAILABLE"},
		{403, `{"error":{"code":"AUTH_AGENT_KEY_OUT_OF_SCOPE","message":"an agent key cannot do this"}}`, "AUTH_AGENT_KEY_OUT_OF_SCOPE"},
	}
	for _, c := range cases {
		s := newVecServer(t, func(vecRequest) (int, string) { return c.status, c.body })
		r, stdout, stderr, _ := s.run("research", true)
		if code := collectionsRestore(r, idNotes, "", false); code != exitRequestFailed {
			t.Errorf("%s: exit %d", c.code, code)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: printed on stdout: %q", c.code, stdout.String())
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
			extra = `,"error":"The restore could not be completed. The new collection was removed; the live collection and the restore point are untouched, and it can be restored again."`
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
		{"failed: exit 1 with its reason", []string{"running", "failed"}, time.Minute, exitRequestFailed, "can be restored again"},
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
			for _, q := range s.got()[1:] {
				if q.Method != "GET" || q.Path != pointsPath+"/restores/r-1" {
					t.Errorf("followed with %s %s", q.Method, q.Path)
				}
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

func TestRestorePointsSaysWhoDeletedIt(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, deletedPoints(
			deletedPointBy(idNotes, idNotes2, "notes", "research", "ready", "", `{"kind":"agent","agent_id":"a-77"}`),
			deletedPoint(idOther, idOther, "drafts", "research", "ready", ""),
		)
	})
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestorePoints(r, nil, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"DELETED BY", "agent a-77", "person"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q in:\n%s", want, stdout.String())
		}
	}
	rj, jout, _, _ := s.run("research", true)
	if code := collectionsRestorePoints(rj, nil, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	rows := decode(t, jout.String())["restore_points"].([]interface{})
	by := rows[0].(map[string]interface{})["deleted_by"].(map[string]interface{})
	if by["kind"] != "agent" || by["agent_id"] != "a-77" {
		t.Errorf("deleted_by = %v", by)
	}
}

func TestARestoreInProgressShowsItsRegionsInTheStatus(t *testing.T) {
	row := strings.Replace(deletedPoint(idNotes, idNotes2, "notes", "research", "restoring", ""), `"restore":null`,
		`"restore":{"id":"r-1","collection_id":"c-9","state":"running","regions_ready":1,"regions_total":2}`, 1)
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, deletedPoints(row) })
	r, stdout, _, _ := s.run("research", false)
	collectionsRestorePoints(r, nil, "")
	if !strings.Contains(stdout.String(), "restoring 1/2") {
		t.Errorf("stdout %q", stdout.String())
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

func TestTheRestoreCommandsAreOnTheCollectionsGroupAndTheOldOnesAreGone(t *testing.T) {
	names := map[string]bool{}
	for _, c := range collectionsCmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"restore-points", "restore"} {
		if !names[want] {
			t.Errorf("afy collections %s is not registered", want)
		}
	}
	for _, gone := range []string{"deleted", "backups"} {
		if names[gone] {
			t.Errorf("afy collections %s is still registered: it is one verb family now, restore-points and restore", gone)
		}
	}
	if collectionsRestoreCmd.Flags().Lookup("as") == nil || collectionsRestoreCmd.Flags().Lookup("wait") == nil {
		t.Error("restore lacks --as or --wait")
	}
	if collectionsRestoreCmd.Flags().Lookup("day") != nil {
		t.Error("restore still has --day: a day is a restore point now, named by its id")
	}
	if collectionsRestorePointsCmd.Flags().Lookup("collection-id") == nil {
		t.Error("restore-points lacks --collection-id")
	}
}

func TestTheHelpTypesNoPlanFigure(t *testing.T) {
	// The numbers of a plan (its days, its restores) are the API's to say and the docs' to list once; the
	// help names the limit and does not copy it.
	for _, c := range []string{collectionsRestorePointsCmd.Long, collectionsRestoreCmd.Long, collectionsRestorePointsCmd.Short, collectionsRestoreCmd.Short} {
		for _, bad := range []string{"7 days", "30 days", "90 days", "3 restores", "10 restores", "50 restores", "last 7", "(7 on Free"} {
			if strings.Contains(c, bad) {
				t.Errorf("help types a plan figure %q:\n%s", bad, c)
			}
		}
	}
}
