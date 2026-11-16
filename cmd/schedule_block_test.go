package cmd

import (
	"strings"
	"testing"
)

// `schedule:` IS A BLOCK in aetherfy.yaml: `cron:` and an optional
// `deadline_minutes:`. The string form is refused before anything is uploaded or
// fetched, with the block to write instead.

const stringScheduleYAML = "runtime: python3.11\nentrypoint: main.py\nschedule: \"0 9 * * *\"\n"

func TestDeployRefusesAStringScheduleBeforeUploadingAndShowsTheBlock(t *testing.T) {
	dir := agentFolder(t, stringScheduleYAML)
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := deployCP(t)

	stdout, stderr, code := runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
	if code == 0 {
		t.Fatalf("a string schedule must be refused\n%s%s", stdout, stderr)
	}
	for _, want := range []string{
		"Config validation failed: schedule: is a block: write",
		"schedule:\n  cron: \"0 9 * * *\"\n  deadline_minutes: 60   # optional: how late an occurrence may still start",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	// The help for a folder with no aetherfy.yaml is printed on stdout.
	if strings.Contains(stdout+stderr, "afy init --agent") {
		t.Errorf("the file exists: the 'create one' help does not apply:\n%s%s", stdout, stderr)
	}
	if n := deploysSent(cp); n != 0 {
		t.Errorf("nothing may be uploaded, got %d deploy(s)", n)
	}

	// THE CONTROL: the same file with the block goes up.
	dir = agentFolder(t, "runtime: python3.11\nentrypoint: main.py\nschedule:\n  cron: \"0 9 * * *\"\n  deadline_minutes: 30\n")
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp = deployCP(t)
	stdout, stderr, code = runAfyProcess(t, cp, nil, "deploy", dir, "--detach")
	if code != 0 || deploysSent(cp) != 1 {
		t.Errorf("a schedule block must deploy: exit %d, %d deploy(s)\n%s%s", code, deploysSent(cp), stdout, stderr)
	}
}

func TestDiffRefusesAStringScheduleAndFetchesNothing(t *testing.T) {
	dir := agentFolder(t, stringScheduleYAML)
	linkFolder(t, dir, map[string]string{"default": prodID})
	cp := diffCP(t)

	_, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir)
	if code == 0 {
		t.Fatalf("a string schedule must be refused by diff too\n%s", stderr)
	}
	if !strings.Contains(stderr, "Config validation failed: schedule: is a block: write") {
		t.Errorf("stderr:\n%s", stderr)
	}
	for _, c := range cp.all() {
		if strings.HasSuffix(c.Path, "/yaml") {
			t.Errorf("nothing is fetched for a file a deploy refuses: %+v", c)
		}
	}
}

// THE FOLDER `afy init` WRITES, WITH A SCHEDULE, diffs clean against the export of
// the release it made, whether the export writes the deadline as null or leaves
// it out; a different expression is a change.
func TestAFolderWithAScheduleBlockDiffsCleanAgainstItsExport(t *testing.T) {
	dir := agentFolder(t, buildAetherfyYAML("python3.11", "service", "eu-central-1", 1024, false, "main.py", "", "0 3 * * *"))
	linkFolder(t, dir, map[string]string{"default": prodID})

	for name, export := range map[string]string{
		"the deadline left out": initReleaseExport + "schedule:\n  cron: 0 3 * * *\n",
		"the deadline null":     initReleaseExport + "schedule:\n  cron: 0 3 * * *\n  deadline_minutes: null\n",
	} {
		cp := diffCP(t)
		cp.on("GET", "/agents/"+prodID+"/yaml", 200, export)
		stdout, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir)
		if code != 0 {
			t.Errorf("%s: the same schedule must diff clean; exit %d\n%s%s", name, code, stdout, stderr)
		}
	}

	cp := diffCP(t)
	cp.on("GET", "/agents/"+prodID+"/yaml", 200, initReleaseExport+"schedule:\n  cron: 0 4 * * *\n")
	stdout, stderr, code := runAfyProcess(t, cp, nil, "diff", "--path", dir)
	if code != 1 || !strings.Contains(stdout, "schedule") {
		t.Errorf("another expression must be a change on the schedule; exit %d\n%s%s", code, stdout, stderr)
	}
}

// `afy init --schedule` writes the block, with the deadline offered as a comment.
func TestInitWritesTheScheduleAsABlock(t *testing.T) {
	dir := t.TempDir()
	cp := initCP(t)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "init", dir, "-y", "--name", "invoice-parser",
		"--runtime", "python3.11", "--entrypoint", "main.py", "--type", "job", "--schedule", "0 3 * * *")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	written := readFile(t, dir+"/aetherfy.yaml")
	if !strings.Contains(written, "schedule:\n  cron: \"0 3 * * *\"") {
		t.Errorf("the schedule is not written as a block:\n%s", written)
	}
	if !strings.Contains(written, "#   deadline_minutes: 30") {
		t.Errorf("the deadline is not offered beside the schedule:\n%s", written)
	}
	if !strings.Contains(stdout+stderr, "Schedule:") || !strings.Contains(stdout+stderr, "0 3 * * * (UTC)") {
		t.Errorf("the summary does not name the schedule:\n%s%s", stdout, stderr)
	}
}
