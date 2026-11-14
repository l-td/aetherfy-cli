package cmd

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// afy usage reads the dashboard's usage meter (GET /usage). The fixtures in
// testdata/usage-meter-*.json are the output of the dashboard's own
// translateUsagePreview (dashboard/src/lib/billing/usageMeter.js) over four
// previews: past the plan, within it, a Free account, and no subscription. They
// are the shape the control plane's answer has, key for key.

func usageFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/usage-meter-" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestUsagePastThePlanShowsPeriodPlanPercentOverageAndAwakeTime(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/usage", 200, usageFixture(t, "overage"))

	stdout, stderr, err := runAfy(t, cp, "usage")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{
		"2026-11-01 to 2026-12-01",                // period
		"starter",                                 // plan
		"125% of plan",                            // usage so far, as a percent of the plan
		"+$2.50 this period",                      // overage dollars
		"$27.50 ($25 plan + $2.50 usage)",         // the total, composed
		"invoice-parser: $1.90",                   // a per-agent row
		"Awake time in eu-central-1: 10% of this", // per-agent awake time, per region
		"Awake time in us-east-1: 40% of this",
		"old-bot (archived)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(strings.ToLower(stdout), "credit") {
		t.Errorf("the credit unit must not reach the person:\n%s", stdout)
	}
	if got := cp.called("GET", "/usage"); len(got) != 1 || !strings.HasPrefix(got[0].Authorization, "Bearer afy_test_") {
		t.Errorf("GET /usage = %+v, want one, with the CLI's key", got)
	}
}

func TestUsageWithinThePlanShowsThePercentAndNoOverage(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/usage", 200, usageFixture(t, "within"))
	stdout, _, err := runAfy(t, cp, "usage")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "40% of plan") {
		t.Errorf("stdout lacks the percent:\n%s", stdout)
	}
	if strings.Contains(stdout, "Beyond plan") || strings.Contains(stdout, "$") {
		t.Errorf("within the plan there is no overage and no dollar figure to show:\n%s", stdout)
	}
}

func TestUsageForAFreeAccountShowsItsAwakeBudgetNotAPlanPercent(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/usage", 200, usageFixture(t, "free"))
	stdout, _, err := runAfy(t, cp, "usage")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"free", "5% of this month's awake budget", "hello"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "of plan") {
		t.Errorf("a Free account's gauge is awake time, not a plan percent:\n%s", stdout)
	}
}

func TestUsageWithNoActivePeriodSaysWhyAndSucceeds(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/usage", 200, usageFixture(t, "none"))
	stdout, stderr, err := runAfy(t, cp, "usage")
	if err != nil {
		t.Fatalf("no usage to show is an answer, not a failure: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "No usage to show: no subscription.") {
		t.Errorf("stdout = %q, want the reason plainly", stdout)
	}

	cp.on("GET", "/usage", 200, `{"hasActivePeriod":false,"reason":"no_metered_plan","tier":"free"}`)
	stdout, _, err = runAfy(t, cp, "usage")
	if err != nil || !strings.Contains(stdout, "No usage to show: no metered plan (plan: free).") {
		t.Errorf("err %v; stdout = %q", err, stdout)
	}
}

func TestUsageJSONIsTheServersDocumentUnchanged(t *testing.T) {
	for _, name := range []string{"overage", "within", "free", "none"} {
		t.Run(name, func(t *testing.T) {
			cp := newFakeCP(t)
			// An extra field the CLI does not decode must survive.
			body := strings.Replace(usageFixture(t, name), "{", `{"someFutureField":{"a":1},`, 1)
			cp.on("GET", "/usage", 200, body)
			stdout, stderr, err := runAfy(t, cp, "usage", "--json")
			if err != nil {
				t.Fatalf("%v\n%s", err, stderr)
			}
			var got, want interface{}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, stdout)
			}
			_ = json.Unmarshal([]byte(body), &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("usage --json = %v\nwant the server's document %v", got, want)
			}
		})
	}
}

func TestUsageNeedsALoginAndRelaysARefusal(t *testing.T) {
	_, stderr, code := runAfyProcess(t, nil, []string{"AETHERFY_API_KEY="}, "usage", "--json")
	if code != exitNotLoggedIn {
		t.Errorf("exit %d, want %d\n%s", code, exitNotLoggedIn, stderr)
	}

	cp := newFakeCP(t)
	cp.on("GET", "/usage", 503, errAnswer(503, "SERVICE_UNAVAILABLE", "Usage is unavailable.", nil).body)
	stdout, stderr, err := runAfy(t, cp, "usage", "--json")
	if err == nil || ExitCode(err) != exitRequestFailed {
		t.Fatalf("want exit %d, got %v", exitRequestFailed, err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout = %q", stdout)
	}
	if e := jsonErrorOnStderr(t, "usage", stderr); e["code"] != "SERVICE_UNAVAILABLE" || e["status"] != 503.0 {
		t.Errorf("error = %v, want the server's", e)
	}
}

func TestUsageTakesNoArguments(t *testing.T) {
	cp := newFakeCP(t)
	_, _, err := runAfy(t, cp, "usage", "agents")
	if err == nil || ExitCode(err) != exitInputRefused {
		t.Errorf("want exit %d, got %v", exitInputRefused, err)
	}
	if n := len(cp.all()); n != 0 {
		t.Errorf("%d request(s) sent", n)
	}
}
