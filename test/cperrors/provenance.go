package cperrors

// WHICH CONTROL PLANE THE SNAPSHOT DESCRIBES, AND WHETHER ANYONE ELSE CAN REACH IT.
//
// The committed snapshot is what CI reads (CI checks out no control plane), so it IS the contract. Generated
// from a working tree with uncommitted or unpushed Python, it describes a control plane nobody else has, and the
// guard then holds the CLI to codes that do not exist on any shared branch. So the generator refuses that tree.
//
// THE DOCS GENERATORS' RULE, PORTED, WITH THEIR WORDS. aetherfy-dashboard's docs-site collects this provenance in
// scripts/dump-cp-surface.py (_provenance) and words its verdict in scripts/lib/cp-surface.mjs
// (unshareableSource); the generator refuses with the sentence in scripts/generate-cp-surface-snapshot.mjs. A tree
// is shareable when no tracked Python file has uncommitted changes (untracked files and other extensions do not
// count: the extraction reads Python source), and HEAD is a branch with nothing ahead of its upstream, or a
// detached commit a remote branch points at. When git cannot answer, the provenance cannot be established, and
// that is refused too (scripts/lib/shareable-source.mjs, the same rail's fail-closed words): an unknown state must
// never read as pushed.

import (
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Provenance is what git says about a control-plane checkout.
type Provenance struct {
	// Problem is set when git could not answer; nothing else is then trusted.
	Problem string
	// Branch is the local branch, "" for a detached HEAD.
	Branch string
	// DirtyPythonFiles are tracked .py files modified or staged but not committed.
	DirtyPythonFiles []string
	// AheadOfUpstream counts the commits on HEAD its upstream does not have. Nil when there is no upstream:
	// a branch without one, or a detached commit no remote branch points at.
	AheadOfUpstream *int
}

// checkoutLabel names the control plane in the fail-closed sentences, as shareable-source.mjs names a repo.
const checkoutLabel = "aetherfy-control-plane"

// gitIn runs one git command in dir and trims the END only: `git status --porcelain` puts the state in the first
// two COLUMNS, so an unstaged change is " M path", and trimming the start would cut the first path by one.
func gitIn(dir string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), " \t\r\n"), true
}

// DescribeCheckout reads the provenance of the control-plane checkout at cpRoot.
func DescribeCheckout(cpRoot string) Provenance {
	if inside, ok := gitIn(cpRoot, "rev-parse", "--is-inside-work-tree"); !ok || inside != "true" {
		return Provenance{Problem: checkoutLabel + " is not a git checkout, so its provenance cannot be established"}
	}
	porcelain, ok := gitIn(cpRoot, "status", "--porcelain", "--untracked-files=no")
	if !ok {
		return Provenance{Problem: "git status failed in " + checkoutLabel + ", so its provenance cannot be established"}
	}
	var p Provenance
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		// Porcelain v1: two status columns, a space, the path. A rename reads "old -> new"; the new path is dirty.
		path := strings.Trim(strings.TrimSpace(line[3:]), `"`)
		if i := strings.Index(path, " -> "); i >= 0 {
			path = strings.Trim(path[i+4:], `"`)
		}
		if strings.HasSuffix(path, ".py") {
			p.DirtyPythonFiles = append(p.DirtyPythonFiles, path)
		}
	}
	sort.Strings(p.DirtyPythonFiles)

	branch, ok := gitIn(cpRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return Provenance{Problem: "git could not name HEAD in " + checkoutLabel + ", so its provenance cannot be established"}
	}
	if branch == "HEAD" {
		// DETACHED: pushed exactly when a remote branch points at this commit.
		tips, _ := gitIn(cpRoot, "branch", "-r", "--points-at", "HEAD")
		for _, tip := range strings.Split(tips, "\n") {
			if t := strings.TrimSpace(tip); t != "" && !strings.Contains(t, "->") {
				zero := 0
				p.AheadOfUpstream = &zero
				break
			}
		}
		return p
	}
	p.Branch = branch
	if ahead, ok := gitIn(cpRoot, "rev-list", "--count", "@{u}..HEAD"); ok {
		if n, err := strconv.Atoi(ahead); err == nil {
			p.AheadOfUpstream = &n
		}
	}
	return p
}

// Unshareable says why a checkout must not become the committed snapshot, or "" when it may. The sentences are
// docs-site's (cp-surface.mjs unshareableSource), word for word.
func Unshareable(p Provenance) string {
	if p.Problem != "" {
		return p.Problem
	}
	if len(p.DirtyPythonFiles) > 0 {
		return "the control-plane checkout has uncommitted Python: " + strings.Join(p.DirtyPythonFiles, ", ")
	}
	if p.AheadOfUpstream == nil {
		if p.Branch == "" {
			return "the control-plane checkout is on a detached HEAD that no remote branch points at, so it is not pushed"
		}
		return `branch "` + p.Branch + `" has no upstream, so its commits are on no shared branch`
	}
	if *p.AheadOfUpstream > 0 {
		return "the control-plane checkout is " + strconv.Itoa(*p.AheadOfUpstream) +
			" commit(s) ahead of its upstream, so the surface it describes is not pushed"
	}
	return ""
}
