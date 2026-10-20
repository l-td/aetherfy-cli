package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// THE MAC GATE, held. goreleaser publishes darwin builds; a release must not
// ship one that has not run on a Mac. release.yml's publishing job needs a
// job that runs the tests on macOS, and CI runs them there on every push to
// main. Deleting either a `needs:` or the job would otherwise go unnoticed:
// nothing else fails when a test job silently stops existing.

type workflow struct {
	Jobs map[string]struct {
		RunsOn string      `yaml:"runs-on"`
		Needs  interface{} `yaml:"needs"`
		Steps  []struct {
			Run string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func readWorkflow(t *testing.T, name string) workflow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(raw, &w); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return w
}

// macTestJobs are the jobs of w that run `go test` on a macOS runner.
func macTestJobs(w workflow) []string {
	var out []string
	for id, j := range w.Jobs {
		if !strings.HasPrefix(j.RunsOn, "macos-") {
			continue
		}
		for _, s := range j.Steps {
			if strings.Contains(s.Run, "go test") {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

func needs(v interface{}) []string {
	switch n := v.(type) {
	case string:
		return []string{n}
	case []interface{}:
		out := make([]string, 0, len(n))
		for _, e := range n {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func TestTheReleaseNeedsTheTestsGreenOnAMac(t *testing.T) {
	rel := readWorkflow(t, "release.yml")
	macs := macTestJobs(rel)
	if len(macs) == 0 {
		t.Fatal("release.yml has no job running `go test` on a macOS runner")
	}
	publish, ok := rel.Jobs["release"]
	if !ok {
		t.Fatal("release.yml has no `release` job")
	}
	for _, m := range macs {
		for _, n := range needs(publish.Needs) {
			if n == m {
				return
			}
		}
	}
	t.Errorf("release.yml's `release` job needs %v, none of the macOS test jobs %v: a darwin build could ship untested on a Mac",
		needs(publish.Needs), macs)
}

func TestCIRunsTheTestsOnAMac(t *testing.T) {
	if macs := macTestJobs(readWorkflow(t, "ci.yml")); len(macs) == 0 {
		t.Error("ci.yml has no job running `go test` on a macOS runner")
	}
}
