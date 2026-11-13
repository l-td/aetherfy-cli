package cmd

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// WHAT `afy init` SAYS ALWAYS-ON COSTS. It used to offer "always-on (+$10/mo, 0ms cold start)" and "auto-sleep ($0 when
// idle, ~500ms cold start)" to every account. The price differs by plan and by memory (a 256 MB agent and a 4 GB one are
// far apart), Free bills nothing for it, an idle agent still pays its flat price, and a wake-up is a resume of about half
// a second, not a cold start. So the billing choice and the flag that skips it quote no price and no delay in
// milliseconds, say what always-on does (never sleeps, no wake-up delay) and point at the billing page.

// priceOrMilliseconds matches a dollar amount ("$10", "$ 0") or a delay in milliseconds ("0ms", "500 ms").
var priceOrMilliseconds = regexp.MustCompile(`\$\s?\d|\d\s?ms\b`)

func TestTheBillingChoiceQuotesNoPriceAndNoMillisecondDelay(t *testing.T) {
	dir := t.TempDir()
	cp := twoWorkspaceInitCP(t)
	asked := scriptPrompts(t, strings.Join([]string{
		"invoice-parser", "acme", "python3.12", "1", "", "eu-central-1", "2", "2", "worker.py",
	}, "\n")+"\n")
	if _, stderr, err := runAfy(t, cp, "init", dir); err != nil {
		t.Fatalf("init: %v\n%s\nasked:\n%s", err, stderr, asked)
	}

	shown := asked.String()
	for _, line := range []string{billingModeAutoSleep, billingModeAlwaysOn} {
		if !strings.Contains(shown, line) {
			t.Errorf("the billing choice did not show %q:\n%s", line, shown)
		}
	}
	for name, text := range map[string]string{
		"the auto-sleep choice": billingModeAutoSleep, "the always-on choice": billingModeAlwaysOn,
		"the --keep-alive help": keepAliveFlagHelp, "everything the prompts printed": shown,
	} {
		if m := priceOrMilliseconds.FindString(text); m != "" {
			t.Errorf("%s quotes %q: %s", name, m, text)
		}
	}
	for _, want := range []string{"never sleeps", "no wake-up delay", "docs.aetherfy.com/platform/billing"} {
		if !strings.Contains(billingModeAlwaysOn, want) {
			t.Errorf("the always-on choice does not say %q: %s", want, billingModeAlwaysOn)
		}
	}
	if !strings.Contains(keepAliveFlagHelp, "docs.aetherfy.com/platform/billing") {
		t.Errorf("the --keep-alive help does not point at the billing page: %s", keepAliveFlagHelp)
	}
	if got := initCmd.Flags().Lookup("keep-alive").Usage; got != keepAliveFlagHelp {
		t.Errorf("--keep-alive prints %q, not the help the test holds", got)
	}
}

// CONTROL: the check above is red for the lines this replaced, so it can fail.
func TestTheCheckRejectsTheOldBillingLines(t *testing.T) {
	for _, old := range []string{
		"auto-sleep  ($0 when idle, ~500ms cold start)",
		"always-on   (+$10/mo, 0ms cold start)",
		"Enable always-on billing for $10/mo (skips billing prompt)",
	} {
		if priceOrMilliseconds.FindString(old) == "" {
			t.Errorf("the check lets %q through", old)
		}
	}
	for _, fine := range []string{billingModeAutoSleep, billingModeAlwaysOn, keepAliveFlagHelp, "Memory in MB, a size your plan allows"} {
		if m := priceOrMilliseconds.FindString(fine); m != "" {
			t.Errorf("the check flags %q in %q", m, fine)
		}
	}
}

// ALWAYS-ON IS A SERVICE'S. A task (type: job) is suspended between runs and has nothing for always-on to keep awake, so the
// platform rejects keep_alive on it. Interactive init used to ask a task for its billing mode too, and --keep-alive on a task
// wrote an aetherfy.yaml the first deploy refused.
func TestOnlyAServiceIsOfferedAlwaysOn(t *testing.T) {
	answers := func(agentType string, billing ...string) string {
		return strings.Join(append([]string{"invoice-parser", "acme", "python3.12", agentType, "", "eu-central-1", "2"},
			append(billing, "worker.py")...), "\n") + "\n"
	}
	run := func(t *testing.T, input string) (string, string) {
		dir := t.TempDir()
		cp := twoWorkspaceInitCP(t)
		asked := scriptPrompts(t, input)
		if _, stderr, err := runAfy(t, cp, "init", dir); err != nil {
			t.Fatalf("init: %v\n%s\nasked:\n%s", err, stderr, asked)
		}
		return asked.String(), readFile(t, filepath.Join(dir, "aetherfy.yaml"))
	}

	// A task: no billing question, and the file says keep_alive: false.
	shown, yamlFile := run(t, answers("2"))
	if strings.Contains(shown, "Billing mode") || strings.Contains(shown, billingModeAlwaysOn) {
		t.Errorf("a task was asked its billing mode:\n%s", shown)
	}
	if !strings.Contains(yamlFile, "keep_alive: false") || !strings.Contains(yamlFile, "type: job") {
		t.Errorf("a task's file:\n%s", yamlFile)
	}

	// CONTROL: a service is asked, and its answer lands in the file.
	shown, yamlFile = run(t, answers("1", "2"))
	if !strings.Contains(shown, "Billing mode") || !strings.Contains(shown, billingModeAlwaysOn) {
		t.Errorf("a service was not asked its billing mode:\n%s", shown)
	}
	if !strings.Contains(yamlFile, "keep_alive: true") || !strings.Contains(yamlFile, "type: service") {
		t.Errorf("a service's file:\n%s", yamlFile)
	}
}

func TestKeepAliveOnATaskIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	dir := t.TempDir()
	cp := initCP(t)
	_, stderr, err := runAfy(t, cp, "init", dir, "-y", "--runtime", "python3.12", "--type", "job", "--keep-alive", "--name", "invoice-parser")
	if err == nil || !strings.Contains(stderr+err.Error(), "--keep-alive is for a service") {
		t.Fatalf("a task with --keep-alive was not refused: err=%v\n%s", err, stderr)
	}
	if fileExists(filepath.Join(dir, "aetherfy.yaml")) || len(cp.called("POST", "/agents")) != 0 {
		t.Errorf("something was written or created before the refusal")
	}
	// CONTROL: the same flag on a service is accepted.
	dir2 := t.TempDir()
	if _, stderr, err := runAfy(t, initCP(t), "init", dir2, "-y", "--runtime", "python3.12", "--type", "service", "--keep-alive", "--name", "invoice-parser"); err != nil {
		t.Fatalf("--keep-alive on a service: %v\n%s", err, stderr)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir2, "aetherfy.yaml")), "keep_alive: true") {
		t.Errorf("a service with --keep-alive did not get keep_alive: true")
	}
}
