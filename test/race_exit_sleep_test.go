package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// THE RACE DETECTOR'S EXIT SLEEP, held off. A binary built with -race sleeps
// GORACE's atexit_sleep_ms (default 1000) before every process exit, and the
// tests re-run the test binary as `afy` hundreds of times: each run would wait
// a second for nothing. Every workflow step that runs `go test -race` sets it
// to 0, and the re-executed processes inherit it. A step that loses the
// variable gives the seconds back without failing anything, so this holds it.

type raceStep struct {
	Env map[string]string `yaml:"env"`
	Run string            `yaml:"run"`
}

type raceWorkflow struct {
	Jobs map[string]struct {
		Steps []raceStep `yaml:"steps"`
	} `yaml:"jobs"`
}

func raceSteps(t *testing.T) map[string]raceStep {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	out := map[string]raceStep{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w raceWorkflow
		if err := yaml.Unmarshal(raw, &w); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for job, j := range w.Jobs {
			for _, s := range j.Steps {
				if strings.Contains(s.Run, "go test") && strings.Contains(s.Run, "-race") {
					out[filepath.Base(f)+" / "+job] = s
				}
			}
		}
	}
	return out
}

func TestEveryRaceTestStepSkipsTheDetectorsExitSleep(t *testing.T) {
	steps := raceSteps(t)
	// Not vacuous: CI's Linux and macOS jobs and the release's test job run it.
	if len(steps) < 3 {
		t.Fatalf("found %d workflow steps running `go test -race`, want at least 3 (ci.yml twice, release.yml once): %v", len(steps), steps)
	}
	for where, s := range steps {
		if got := s.Env["GORACE"]; got != "atexit_sleep_ms=0" {
			t.Errorf("%s runs %q with GORACE=%q, want atexit_sleep_ms=0", where, strings.TrimSpace(s.Run), got)
		}
	}
}
