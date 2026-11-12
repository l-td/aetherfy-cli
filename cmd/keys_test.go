package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/spf13/cobra"
)

// afy keys: create (the device flow, the key shown once), list, revoke.

const (
	keyRowA = `{"id":"k-1","name":"laptop","key_prefix":"afy_test_01...","created_at":"2026-11-01T09:30:00Z","last_used_at":"2026-11-10T18:00:00Z","is_system_managed":false}`
	keyRowB = `{"id":"k-2","name":"dashboard","key_prefix":"afy_live_zz...","created_at":"2026-10-01T00:00:00Z","last_used_at":null,"is_system_managed":true,"note":"an extra field the CLI does not know"}`
	keysDoc = `{"keys":[` + keyRowA + `,` + keyRowB + `]}`
)

// --- keys create ---

// occurrences counts how often s holds sub across the given texts.
func occurrences(sub string, texts ...string) int {
	n := 0
	for _, t := range texts {
		n += strings.Count(t, sub)
	}
	return n
}

// filesHolding lists the files under dir whose content holds sub.
func filesHolding(t *testing.T, dir, sub string) []string {
	t.Helper()
	var found []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if raw, rerr := os.ReadFile(path); rerr == nil && strings.Contains(string(raw), sub) {
			found = append(found, path)
		}
		return nil
	})
	return found
}

func TestKeysCreateShowsTheKeyOnceAndStoresNothing(t *testing.T) {
	waits := recordWaits(t, 0)
	opened := recordBrowser(t, nil)
	cp := deviceCP(t, pendingAnswer, approvedAnswer)

	stdout, stderr, err := runAfy(t, cp, "keys", "create", "--name", "ci")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, stdout, stderr)
	}

	// Once, on stdout, with the words the dashboard's copy uses.
	if n := occurrences(approvedKey, stdout, stderr); n != 1 || !strings.Contains(stdout, approvedKey) {
		t.Errorf("the key appears %d times across stdout and stderr, want once on stdout:\n%s\n%s", n, stdout, stderr)
	}
	if !strings.Contains(stdout, "This is the only time the full key is shown.") {
		t.Errorf("stdout lacks the sentence:\n%s", stdout)
	}
	if !strings.Contains(stdout, approvedKeyID) {
		t.Errorf("the key's id is what revoke takes; stdout lacks it:\n%s", stdout)
	}

	// Stored nowhere: not the credentials file, not any file in the config directory.
	if credentialsStored() {
		t.Error("keys create wrote the credentials file")
	}
	if files := filesHolding(t, os.Getenv("AETHERFY_CONFIG_DIR"), approvedKey); len(files) != 0 {
		t.Errorf("the key was written to %v", files)
	}

	// The two calls carry no credentials, though a key is in the environment; the
	// name rides in the start request; nothing validated the new key against /auth/me.
	for _, c := range append(cp.called("POST", "/auth/device"), cp.called("POST", "/auth/device/token")...) {
		if c.Authorization != "" {
			t.Errorf("%s %s carried credentials: a key must never mint a key", c.Method, c.Path)
		}
	}
	start := cp.called("POST", "/auth/device")
	if len(start) != 1 || start[0].Body["key_name"] != "ci" || start[0].Body["device_name"] != "test-host" {
		t.Errorf("start = %+v, want key_name ci and the machine's name", start)
	}
	if len(cp.called("GET", "/auth/me")) != 0 {
		t.Error("keys create validated the new key; it keeps nothing, so it has nothing to validate")
	}
	if !sameWaits(waits.all(), durations(5, 5)) {
		t.Errorf("waits = %v", waits.all())
	}
	if len(*opened) != 1 || (*opened)[0] != pageComplete {
		t.Errorf("browser opened %v", *opened)
	}
}

func TestKeysCreateWithoutANameSendsNone(t *testing.T) {
	recordWaits(t, 0)
	recordBrowser(t, nil)
	cp := deviceCP(t, approvedAnswer)
	if _, stderr, err := runAfy(t, cp, "keys", "create", "--no-browser"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if body := cp.called("POST", "/auth/device")[0].Body; len(body) != 1 || body["device_name"] != "test-host" {
		t.Errorf("start body = %v, want only the machine's name", body)
	}
}

func TestKeysCreateRefusesAnEmptyNameBeforeSendingAnything(t *testing.T) {
	cp := deviceCP(t, approvedAnswer)
	for _, name := range []string{"", "  "} {
		_, _, err := runAfy(t, cp, "keys", "create", "--name", name)
		if err == nil || ExitCode(err) != exitInputRefused {
			t.Errorf("--name %q: want exit %d, got %v", name, exitInputRefused, err)
		}
	}
	if n := len(cp.all()); n != 0 {
		t.Errorf("%d request(s) sent", n)
	}
}

func TestKeysCreateJSONIsTheKeyAndItsRowAndTheKeyIsPrintedOnce(t *testing.T) {
	recordWaits(t, 0)
	recordBrowser(t, nil)
	cp := deviceCP(t, approvedAnswer)

	// With -v too: verbose must not log the answer that carries the key.
	stdout, stderr, err := runAfy(t, cp, "keys", "create", "--json", "-v")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	doc, ok := oneJSONDocument(t, "keys create", stdout).(map[string]interface{})
	if !ok || doc["api_key"] != approvedKey {
		t.Fatalf("keys create --json = %v, want api_key", doc)
	}
	row, _ := doc["key"].(map[string]interface{})
	if row["id"] != approvedKeyID || row["key_prefix"] != "afy_live_ab..." {
		t.Errorf("key = %v, want the approved row", row)
	}
	if n := occurrences(approvedKey, stdout, stderr); n != 1 {
		t.Errorf("the key appears %d times across stdout and stderr, want once (in the document)", n)
	}
	if strings.Contains(stderr, "This is the only time") && strings.Contains(stdout, "This is the only time") {
		t.Error("the sentence is for a person; under --json the result is the document")
	}
}

func TestKeysCreateAsAProcessNeedsNoKeyAndLeavesStoredCredentialsAlone(t *testing.T) {
	// Not logged in at all, with credentials on disk it must not touch, run as its
	// own process: the whole of what a script sees.
	dir := t.TempDir()
	stored := []byte("api_key: afy_live_storedstoredstoredstoredstored00\n")
	credPath := filepath.Join(dir, "credentials.yaml")
	if err := os.WriteFile(credPath, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	cp := deviceCP(t, pendingAnswer, approvedAnswer)

	stdout, stderr, code := runAfyProcess(t, cp,
		[]string{"AETHERFY_API_KEY=", "AETHERFY_CONFIG_DIR=" + dir}, "keys", "create", "--json", "-v")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if n := occurrences(approvedKey, stdout, stderr); n != 1 {
		t.Errorf("the key appears %d times across stdout and stderr, want once", n)
	}
	after, err := os.ReadFile(credPath)
	if err != nil || string(after) != string(stored) {
		t.Errorf("credentials changed: %q (%v)", after, err)
	}
	if files := filesHolding(t, dir, approvedKey); len(files) != 0 {
		t.Errorf("the key was written to %v", files)
	}
	for _, c := range cp.all() {
		if c.Authorization != "" {
			t.Errorf("%s %s carried credentials", c.Method, c.Path)
		}
	}
}

func TestKeysCreateWhenTheAccountIsAtItsKeyLimitPrintsNoKeyAndSaysWhatToDo(t *testing.T) {
	recordWaits(t, 0)
	recordBrowser(t, nil)
	cp := deviceCP(t, errAnswer(400, "API_KEY_LIMIT_REACHED", "The plan allows 5 keys.", nil))

	stdout, stderr, err := runAfy(t, cp, "keys", "create", "--json")
	if err == nil || ExitCode(err) != exitRequestFailed {
		t.Fatalf("want exit %d, got %v", exitRequestFailed, err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	e := jsonErrorOnStderr(t, "keys create", stderr)
	if e["code"] != "API_KEY_LIMIT_REACHED" || !strings.Contains(e["message"].(string), "'afy keys create' again") {
		t.Errorf("error = %v", e)
	}
}

// --- keys list ---

func TestKeysListShowsTheRowsWithoutAnySecret(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/keys", 200, keysDoc)

	stdout, stderr, err := runAfy(t, cp, "keys", "list")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{"k-1", "laptop", "afy_test_01...", "2026-11-0", "k-2", "dashboard (system-managed)", "afy_live_zz...", "never", "ID", "LAST USED"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if got := cp.called("GET", "/keys"); len(got) != 1 || got[0].Authorization != "Bearer afy_test_0123456789abcdef0123456789abcdef" {
		t.Errorf("GET /keys = %+v, want one, with the CLI's key", got)
	}
}

func TestKeysListJSONIsTheServersDocumentUnchanged(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/keys", 200, keysDoc)

	stdout, stderr, err := runAfy(t, cp, "keys", "list", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	var got, want interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	_ = json.Unmarshal([]byte(keysDoc), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("keys list --json = %v\nwant the server's document %v (the extra field included)", got, want)
	}
}

func TestKeysListWithNoKeysSaysSoAndJSONStaysADocument(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/keys", 200, `{"keys":[]}`)
	stdout, _, err := runAfy(t, cp, "keys", "list")
	if err != nil || !strings.Contains(stdout, "No API keys.") {
		t.Errorf("err %v; stdout %q", err, stdout)
	}
	stdout, _, err = runAfy(t, cp, "keys", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if doc, ok := oneJSONDocument(t, "keys list", stdout).(map[string]interface{}); !ok || doc["keys"] == nil {
		t.Errorf("keys list --json = %s, want {\"keys\": []}", stdout)
	}
}

func TestKeysListNeedsALoginAndRelaysARefusal(t *testing.T) {
	_, stderr, code := runAfyProcess(t, nil, []string{"AETHERFY_API_KEY="}, "keys", "list", "--json")
	if code != exitNotLoggedIn {
		t.Errorf("exit %d, want %d\n%s", code, exitNotLoggedIn, stderr)
	}

	cp := newFakeCP(t)
	cp.on("GET", "/keys", 401, errAnswer(401, "INVALID_API_KEY", "That key was refused.", nil).body)
	_, stderr, err := runAfy(t, cp, "keys", "list", "--json")
	if err == nil {
		t.Fatal("want a refusal")
	}
	if e := jsonErrorOnStderr(t, "keys list", stderr); e["code"] != "INVALID_API_KEY" || e["status"] != 401.0 {
		t.Errorf("error = %v, want the server's", e)
	}
}

func TestKeysListRefusesAnAnswerOfAShapeItCannotRead(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/keys", 200, `["not","a","document"]`)
	_, stderr, err := runAfy(t, cp, "keys", "list")
	if err == nil {
		t.Fatal("a list that is not the documented shape must not read as no keys")
	}
	if !strings.Contains(stderr, "does not understand") {
		t.Errorf("stderr = %q, want the unexpected-shape sentence", stderr)
	}
}

// --- keys revoke ---

func TestKeysRevokeWithYesRevokesWithoutReadingTheList(t *testing.T) {
	onTerminal(t, false, "")
	cp := newFakeCP(t)
	cp.on("DELETE", "/keys/k-1", 200, `{"revoked":true,"id":"k-1"}`)

	stdout, stderr, err := runAfy(t, cp, "keys", "revoke", "k-1", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if got := oneJSONDocument(t, "keys revoke", stdout); !reflect.DeepEqual(got, map[string]interface{}{"revoked": true, "id": "k-1"}) {
		t.Errorf("keys revoke --json = %v", got)
	}
	if n := len(cp.called("GET", "/keys")); n != 0 {
		t.Errorf("--yes read the list %d time(s); there is no question to name the key in", n)
	}
}

func TestKeysRevokeWithoutATerminalOrYesAsksNothingAndReadsNothing(t *testing.T) {
	onTerminal(t, false, "")
	cp := newFakeCP(t)
	_, _, err := runAfy(t, cp, "keys", "revoke", "k-1")
	if err == nil || ExitCode(err) != exitInputRefused {
		t.Fatalf("want exit %d, got %v", exitInputRefused, err)
	}
	if n := len(cp.all()); n != 0 {
		t.Errorf("%d request(s) sent before a refusal that needs none: %+v", n, cp.all())
	}
}

func TestKeysRevokeQuestionSaysWhenTheKeyIsTheOneInUse(t *testing.T) {
	// The CLI's key (runAfy's AETHERFY_API_KEY) is afy_test_0123456789abcdef....
	cases := []struct {
		name, list, id string
		want, notWant  string
	}{
		{"the key in use", `{"keys":[` + keyRowA + `,` + keyRowB + `]}`, "k-1",
			"This is the key this CLI is using", ""},
		{"another key", `{"keys":[` + keyRowA + `,` + keyRowB + `]}`, "k-2",
			"", "using"},
		{"a key that starts like the one in use", `{"keys":[` + keyRowA + `,` +
			strings.Replace(keyRowA, `"k-1"`, `"k-3"`, 1) + `]}`, "k-1",
			"This may be the key this CLI is using", "This is the key"},
		{"an id the account does not have", `{"keys":[` + keyRowA + `]}`, "k-9",
			"", "using"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onTerminal(t, true, "n\n")
			cp := newFakeCP(t)
			cp.on("GET", "/keys", 200, tc.list)
			cp.on("DELETE", "/keys/"+tc.id, 200, `{"revoked":true}`)
			stdout, stderr, err := runAfy(t, cp, "keys", "revoke", tc.id)
			if err == nil || ExitCode(err) != exitRequestFailed {
				t.Fatalf("a declined revoke must exit %d, got %v\n%s%s", exitRequestFailed, err, stdout, stderr)
			}
			all := stdout + stderr
			if tc.want != "" && !strings.Contains(all, tc.want) {
				t.Errorf("the question lacks %q:\n%s", tc.want, all)
			}
			if tc.notWant != "" && strings.Contains(all, tc.notWant) {
				t.Errorf("the question holds %q:\n%s", tc.notWant, all)
			}
			if !strings.Contains(all, "[y/N]") {
				t.Errorf("no question asked:\n%s", all)
			}
			if n := len(cp.called("DELETE", "/keys/"+tc.id)); n != 0 {
				t.Error("revoked after a no")
			}
		})
	}
}

func TestKeysRevokeRelaysTheServersRefusalWithItsCode(t *testing.T) {
	onTerminal(t, false, "")
	cp := newFakeCP(t)
	cp.on("DELETE", "/keys/k-9", 404, errAnswer(404, "API_KEY_NOT_FOUND", "No such key.", nil).body)
	stdout, stderr, err := runAfy(t, cp, "keys", "revoke", "k-9", "--yes", "--json")
	if err == nil || ExitCode(err) != exitRequestFailed {
		t.Fatalf("want exit %d, got %v", exitRequestFailed, err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout = %q", stdout)
	}
	if e := jsonErrorOnStderr(t, "revoke", stderr); e["code"] != "API_KEY_NOT_FOUND" || e["status"] != 404.0 {
		t.Errorf("error = %v", e)
	}
}

func TestKeysRevokeEscapesTheIdInThePath(t *testing.T) {
	onTerminal(t, false, "")
	cp := newFakeCP(t)
	cp.on("DELETE", "/keys/a b", 200, `{"revoked":true}`)
	if _, stderr, err := runAfy(t, cp, "keys", "revoke", "a b", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if n := len(cp.called("DELETE", "/keys/a b")); n != 1 {
		t.Errorf("%d DELETE(s) of the id, want 1: %+v", n, cp.all())
	}
}

func TestCurrentKeyRelationNamesTheRowAndHowSureItIs(t *testing.T) {
	rows := []api.APIKey{
		{ID: "a", KeyPrefix: "afy_live_ab..."},
		{ID: "b", KeyPrefix: "afy_live_cd\u2026"},
		{ID: "c", KeyPrefix: "afy_live_ab..."},
	}
	held := "afy_live_cdXXXXXXXX"
	if row, rel := currentKeyRelation(rows, "b", held); row == nil || row.ID != "b" || rel != relationIsCurrent {
		t.Errorf("unique prefix (ellipsis form): row %v, relation %d", row, rel)
	}
	if row, rel := currentKeyRelation(rows, "a", "afy_live_abYYYY"); row == nil || rel != relationMayBeCurrent {
		t.Errorf("two rows share the prefix: row %v, relation %d", row, rel)
	}
	if row, rel := currentKeyRelation(rows, "a", held); row == nil || rel != relationNotCurrent {
		t.Errorf("another key: row %v, relation %d", row, rel)
	}
	if row, rel := currentKeyRelation(rows, "zzz", held); row != nil || rel != relationNotCurrent {
		t.Errorf("unknown id: row %v, relation %d", row, rel)
	}
	if _, rel := currentKeyRelation(rows, "b", ""); rel != relationNotCurrent {
		t.Errorf("no key held: relation %d", rel)
	}
}

// --- the surface ---

func TestKeysAndUsageAreInTheAccountGroupAndKeysHoldExactlyItsCommands(t *testing.T) {
	keys, usage := rootByName(t, "keys"), rootByName(t, "usage")
	if keys == nil || usage == nil {
		t.Fatalf("keys %v, usage %v: both must be registered at the root", keys, usage)
	}
	for _, c := range []*cobra.Command{keys, usage} {
		if c.GroupID != groupAccount {
			t.Errorf("`%s` is in help group %q, want %q beside login and whoami", c.CommandPath(), c.GroupID, groupAccount)
		}
	}
	var got []string
	for _, c := range keys.Commands() {
		got = append(got, c.Name())
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "create,list,revoke" {
		t.Errorf("`afy keys` holds %v, want create, list, revoke: a key is never edited, and none is created by a command that holds one", got)
	}
}

func TestEveryKeysCommandSaysKeysAreMadeByApprovingACode(t *testing.T) {
	// The words the help gives, said where a person will look: login, keys, and keys create.
	for _, c := range []*cobra.Command{loginCmd, keysCmd, keysCreateCmd} {
		text := strings.Join(strings.Fields(c.Long), " ")
		if !strings.Contains(text, "approv") || !strings.Contains(text, "dashboard") {
			t.Errorf("`%s` help does not say a key is made by approving a code in the dashboard:\n%s", c.CommandPath(), c.Long)
		}
	}
	for _, c := range []*cobra.Command{loginCmd, keysCmd} {
		if text := strings.Join(strings.Fields(c.Long), " "); !strings.Contains(text, "approving a code in the dashboard, not by a command that holds a key") {
			t.Errorf("`%s` help lacks \"approving a code in the dashboard, not by a command that holds a key\"", c.CommandPath())
		}
	}
}

func TestTheDeviceSentencesNameCommandsThatExist(t *testing.T) {
	// The sentences that end a sign-in tell the person what to run. Each `afy ...`
	// in them must be a command that accepts what the sentence passes it.
	checked := 0
	for _, code := range []string{codeDeviceExpired, codeDeviceDenied, codeDeviceUsed, codeDeviceNotFound, codeAPIKeyLimit} {
		for _, opts := range []deviceOptions{{again: "afy login"}, {again: "afy keys create"}} {
			sentence, ok := deviceSentence(code, opts)
			if !ok {
				t.Fatalf("no sentence for %s", code)
			}
			n, problems := checkInvocations(code, sentence)
			checked += n
			for _, p := range problems {
				t.Error(p)
			}
			if !strings.Contains(sentence, "'"+opts.again+"'") {
				t.Errorf("%s: %q does not name %q", code, sentence, opts.again)
			}
		}
	}
	if checked == 0 {
		t.Error("no invocation with a placeholder was checked: the limit sentence names `afy keys revoke <id>`")
	}
	if _, ok := deviceSentence("an_unlisted_code", deviceOptions{}); ok {
		t.Error("a code the flow does not know got a sentence")
	}
}

// A key that may be leaked must always be revocable. An account that is suspended, or whose subscription is
// not active, is refused by the API on every request made with its keys; the dashboard still lets it list
// and revoke, and the CLI says so instead of leaving the person with a bare 403.
func TestKeysListAndRevokeNameTheDashboardWhenTheAccountIsNotInGoodStanding(t *testing.T) {
	for _, tc := range []struct {
		code string
		want string
	}{
		{"ACCOUNT_SUSPENDED", "Your account is suspended"},
		{"AUTH_SUBSCRIPTION_INACTIVE", "Your subscription is not active"},
	} {
		for _, argv := range [][]string{{"keys", "list", "--json"}, {"keys", "revoke", "k-1", "--yes", "--json"}} {
			tc, argv := tc, argv
			t.Run(tc.code+"/"+argv[1], func(t *testing.T) {
				onTerminal(t, false, "")
				cp := newFakeCP(t)
				method, path := "GET", "/keys"
				if argv[1] == "revoke" {
					method, path = "DELETE", "/keys/k-1"
				}
				cp.on(method, path, 403, errAnswer(403, tc.code, "The server's own words.", nil).body)
				stdout, stderr, err := runAfy(t, cp, argv...)
				if err == nil || ExitCode(err) != exitRequestFailed {
					t.Fatalf("want exit %d, got %v", exitRequestFailed, err)
				}
				if strings.TrimSpace(stdout) != "" {
					t.Errorf("a refusal wrote to stdout: %q", stdout)
				}
				e := jsonErrorOnStderr(t, argv[1], stderr)
				if e["code"] != tc.code || e["status"] != 403.0 {
					t.Errorf("error = %v, want the server's code and status", e)
				}
				msg, _ := e["message"].(string)
				for _, w := range []string{tc.want, "revoke your keys in the dashboard", "settings/api-keys"} {
					if !strings.Contains(msg, w) {
						t.Errorf("message %q lacks %q", msg, w)
					}
				}
			})
		}
	}
}

// CONTROL of the test above: a 403 for another reason keeps the server's own words, with no talk of the dashboard.
func TestKeysRefusalsOfOtherKindsKeepTheServersWords(t *testing.T) {
	onTerminal(t, false, "")
	cp := newFakeCP(t)
	cp.on("GET", "/keys", 403, errAnswer(403, "AUTH_AGENT_KEY_OUT_OF_SCOPE", "An agent key cannot list keys.", nil).body)
	_, stderr, err := runAfy(t, cp, "keys", "list", "--json")
	if err == nil {
		t.Fatal("want a refusal")
	}
	e := jsonErrorOnStderr(t, "keys list", stderr)
	msg, _ := e["message"].(string)
	if !strings.Contains(msg, "An agent key cannot list keys.") || strings.Contains(msg, "dashboard") {
		t.Errorf("message = %q, want the server's words only", msg)
	}
}
