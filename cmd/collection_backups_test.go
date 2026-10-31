package cmd

import (
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/vectors"
)

// afy collections backups / restore --day, against a fake vectors API: what they send, what --json
// prints, what a person reads, and the exit code a script sees. A restore of a day always goes into a
// NEW collection, so --as is required and the input is checked before anything is sent.

const (
	backupsPath     = "/api/v1/workspaces/research/collections/notes/backups"
	backupRestoreAt = "/api/v1/workspaces/research/collections/notes/backups/restore"
)

const backupsAllowance = `{"restores_per_month":10,"restores_used":3,"restores_left":7,` +
	`"restore_bytes_per_month":10737418240,"restore_bytes_used":0,"restore_bytes_left":10737418240,"resets_at":"2026-11-01T00:00:00.000Z"}`

func backupsList(days ...string) string {
	return `{"collection_id":"` + idNotes + `","days_back":7,"backups":[` + strings.Join(days, ",") + `],"allowance":` + backupsAllowance + `}`
}

func backupDay(day, from, to string) string {
	return `{"day":"` + day + `","taken_from":"` + from + `","taken_to":"` + to + `","points":1200,"bytes":5242880}`
}

func TestBackupsListsTheDaysWithTheRangeEachWasTakenIn(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, backupsList(
			backupDay("2026-10-30", "2026-10-30T04:31:10.000Z", "2026-10-30T06:02:40.000Z"),
			backupDay("2026-10-29", "2026-10-29T04:31:10.000Z", "2026-10-29T04:31:40.000Z"),
		)
	})
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsBackups(r, "notes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "GET" || req.Path != backupsPath {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	for _, want := range []string{
		"2026-10-30", "2026-10-30 04:31 to 06:02", // a range where the shards were hours apart
		"2026-10-29", "5 MB",
		"2 backup(s) of 'notes' in workspace 'research'",
		"Restore to any of the last 7 days · 7 of 10 restores left this month · 10 GB of 10 GB left to restore (resets 2026-11-01)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "2026-10-29 04:31 to") {
		t.Errorf("shards taken within a minute are one time, not a range:\n%s", stdout.String())
	}
}

func TestBackupsJSONIsTheEnvelopeWithTheDaysAndTheAllowance(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, backupsList(backupDay("2026-10-30", "2026-10-30T04:31:10.000Z", "2026-10-30T06:02:40.000Z"))
	})
	r, stdout, _, _ := s.run("research", true)
	if code := collectionsBackups(r, "notes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := decode(t, stdout.String())
	if out["workspace"] != "research" || out["collection"] != "notes" || out["days_back"] != 7.0 {
		t.Errorf("envelope = %v", out)
	}
	days := out["backups"].([]interface{})
	first := days[0].(map[string]interface{})
	if len(days) != 1 || first["day"] != "2026-10-30" || first["taken_from"] != "2026-10-30T04:31:10.000Z" ||
		first["taken_to"] != "2026-10-30T06:02:40.000Z" || first["points"] != 1200.0 || first["bytes"] != 5242880.0 {
		t.Errorf("backups = %v", days)
	}
	if a := out["allowance"].(map[string]interface{}); a["restores_left"] != 7.0 {
		t.Errorf("allowance = %v", a)
	}
}

func TestBackupsWithNothingYetSaysWhenOneAppears(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, backupsList() })
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsBackups(r, "notes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "No backups of 'notes' in workspace 'research' in the last 7 days yet.") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestAnUnlimitedPlanStatesNoRestoreLimitsOnTheBackupsLine(t *testing.T) {
	line := backupAllowanceLine(7, vectors.RecoveryAllowance{})
	if line != "Restore to any of the last 7 days" {
		t.Errorf("line = %q", line)
	}
}

func TestRestoreWithADayStartsOneIntoANewCollectionAndNamesIt(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 202, startedBody })
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestoreDay(r, "notes", "2026-10-30", "notes-before", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "POST" || req.Path != backupRestoreAt {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	if req.Body["day"] != "2026-10-30" || req.Body["as"] != "notes-before" || len(req.Body) != 2 {
		t.Errorf("body = %v", req.Body)
	}
	if !strings.Contains(stdout.String(), "Restoring '") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestRestoreWithADayRefusesBadInputBeforeSendingAnything(t *testing.T) {
	for _, c := range []struct {
		name, day, as, want string
	}{
		{"no name", "2026-10-30", "", "--as is required with --day"},
		{"a blank name", "2026-10-30", "  ", "--as is required with --day"},
		{"a day that is not a date", "yesterday", "x", "--day must be a backup day, YYYY-MM-DD"},
		{"a day with the wrong shape", "30-10-2026", "x", "--day must be a backup day, YYYY-MM-DD"},
	} {
		s := newVecServer(t, func(vecRequest) (int, string) { return 202, startedBody })
		r, _, stderr, connects := s.run("research", false)
		if code := collectionsRestoreDay(r, "notes", c.day, c.as, false); code != exitInputRefused {
			t.Errorf("%s: exit %d, want %d", c.name, code, exitInputRefused)
		}
		if !strings.Contains(stderr.String(), c.want) {
			t.Errorf("%s: stderr %q", c.name, stderr.String())
		}
		if *connects != 0 || len(s.got()) != 0 {
			t.Errorf("%s: resolved or sent something", c.name)
		}
	}
}

func TestRestoreWithADayIsTheRestoreCommandAndTheDeletedFormStillWorks(t *testing.T) {
	flag := collectionsRestoreCmd.Flags().Lookup("day")
	if flag == nil {
		t.Fatal("afy collections restore has no --day")
	}
	if collectionsRestoreCmd.Flags().Lookup("as") == nil || collectionsRestoreCmd.Flags().Lookup("wait") == nil {
		t.Error("restore lost --as or --wait")
	}
	// the deleted-collection form: no --day, the backup list is asked first for a name
	s := newVecServer(t, func(r vecRequest) (int, string) {
		if r.Method == "POST" {
			return 202, startedBody
		}
		return 200, deletedList(deletedRow(idNotes, "notes", "research", "ready", "2026-11-19T10:00:00.000Z"))
	})
	r, _, _, _ := s.run("research", false)
	if code := collectionsRestore(r, "notes", "", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	reqs := s.got()
	if len(reqs) != 2 || reqs[1].Path != deletedPath+"/"+idNotes+"/restore" {
		t.Errorf("requests = %+v", reqs)
	}
}

func TestARestoreOfADayTheAPIRefusesExitsOneWithItsCodeAndSentence(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   string
	}{
		{403, `{"error":{"code":"PLAN_LIMIT_EXCEEDED","message":"Your plan does not restore a collection from its daily backup. Upgrade your plan to restore one to a recent day.","upgrade_url":"https://app.aetherfy.com/dashboard/settings/billing","limit":"collection_backup_restore_days","allowed":0}}`, "PLAN_LIMIT_EXCEEDED"},
		{404, `{"error":{"code":"COLLECTION_BACKUP_DAY_NOT_FOUND","message":"There is no backup of this collection for 2026-10-20 within your plan's 7 days."}}`, "COLLECTION_BACKUP_DAY_NOT_FOUND"},
		{409, `{"error":{"code":"COLLECTION_NAME_TAKEN_FOR_RESTORE","message":"The name \"notes\" is in use in workspace \"research\". Restore it under another name with \"as\"."}}`, "COLLECTION_NAME_TAKEN_FOR_RESTORE"},
		{409, `{"error":{"code":"COLLECTION_RESTORE_REGIONS_UNAVAILABLE","message":"The workspace no longer covers eu-central-1","missing_regions":["eu-central-1"]}}`, "COLLECTION_RESTORE_REGIONS_UNAVAILABLE"},
		{403, `{"error":{"code":"AUTH_AGENT_KEY_OUT_OF_SCOPE","message":"an agent key cannot do this"}}`, "AUTH_AGENT_KEY_OUT_OF_SCOPE"},
	}
	for _, c := range cases {
		s := newVecServer(t, func(vecRequest) (int, string) { return c.status, c.body })
		r, stdout, stderr, _ := s.run("research", true)
		if code := collectionsRestoreDay(r, "notes", "2026-10-30", "x", false); code != exitRequestFailed {
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

func TestARestoreOfADayIsFollowedLikeADeletedOnes(t *testing.T) {
	s := progressServer(t, "running", "done")
	r, stdout, _, _ := s.run("research", false)
	if code := collectionsRestoreDay(r, "notes", "2026-10-30", "notes-before", true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "restored in workspace 'research'") {
		t.Errorf("stdout %q", stdout.String())
	}
	reqs := s.got()
	if reqs[0].Method != "POST" || reqs[0].Path != backupRestoreAt || reqs[1].Method != "GET" {
		t.Errorf("requests = %+v", reqs)
	}
}
