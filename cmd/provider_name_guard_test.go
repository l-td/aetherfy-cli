package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The compute provider's name may not appear in anything a user reads.
//
// WHY IT IS A RULE. A vendor's name in a customer-facing string is a contract
// nobody meant to sign. It started with the agent URL — `<app>.fly.dev`, a
// permanent public address in someone else's namespace — but the URL was only
// the loudest instance. `afy deploy --help` listed "Deploys to Fly.io" as step
// five of the deployment process; `afy agents stop --help` explained the pause
// in terms of "the Fly.io proxy"; `archive`/`restore` described what they do to
// "its Fly.io app". Every one of those teaches a user something about our
// infrastructure that they will reasonably expect to keep being true.
//
// WHERE IT IS FINE: comments, identifiers, and anything else that is not a
// string literal. Go comments are not string literals, so scanning literals
// alone gets that carve-out for free — no comment stripper to be wrong about.
//
// WHAT IS SCANNED: every string literal in cmd/ and internal/, which between
// them hold all of this binary's help text (cobra Short/Long/Example), every
// output.Print* argument, and every error message. That is broader than
// "help text" on purpose: a provider name is no more acceptable in an error
// than in --help, and enumerating positions in Go would mean tracking cobra's
// struct literals, which is more machinery for less coverage.
//
// THE OTHER TWO LANES, and they cannot live in this process:
//   - aetherfy-control-plane/tests/unit/test_provider_name_guard.py — API
//     responses, OpenAPI schema, 422 bodies, run-failure messages
//   - aetherfy-dashboard/dashboard/tests/unit/vocabulary-test.js — product UI,
//     marketing, and the documentation site
// One rule, three repositories, three CI lanes, disjoint file sets. Not three
// copies of one gate: each covers files the others cannot see, and each names
// the other two so the whole rule is findable from any one of them.

// The compute provider, spelled every way it appears. The bare proper noun is
// case-SENSITIVE so the English verb ("bytes fly over the tunnel") and words
// like "flyweight" are not swept up.
var providerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)fly\.io`),
	regexp.MustCompile(`(?i)fly\.dev`),
	regexp.MustCompile(`(?i)flyctl`),
	regexp.MustCompile(`(?i)machines\.dev`),
	regexp.MustCompile(`\bFly\b`),
}

// providerExemption is a declared, COUNTED allowance. Exact counts, so an
// exempt file that grows a SECOND provider mention still turns this red —
// "this file already says it once" is not a licence to say it twice.
type providerExemption struct {
	file  string
	text  string
	count int
	why   string
}

// Empty, and that is the finding rather than an omission: after the sweep no
// string literal in this binary names the provider. TestProviderExemptionsAreLoadBearing
// keeps it that way — an entry added here must correspond to a real site.
var providerExemptions = []providerExemption{}

var scannedDirs = []string{"../cmd", "../internal"}

// providerHits returns one entry per occurrence, across every spelling.
func providerHits(s string) []string {
	var out []string
	for _, re := range providerPatterns {
		out = append(out, re.FindAllString(s, -1)...)
	}
	return out
}

// stringLiterals walks a Go source file and returns every string literal with
// its line. Comments are not literals, so they are excluded structurally.
func stringLiterals(path string) (map[int][]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[int][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		line := fset.Position(lit.Pos()).Line
		out[line] = append(out[line], lit.Value)
		return true
	})
	return out, nil
}

func goFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range scannedDirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	return files
}

func TestNoProviderNameInUserVisibleStrings(t *testing.T) {
	files := goFiles(t)

	// Anti-no-op: a walker pointed at the wrong directory finds nothing and
	// looks exactly like a clean corpus. ~40 files at the time of writing.
	if len(files) < 25 {
		t.Fatalf("only %d Go files scanned across %v — the walker is not reaching the source", len(files), scannedDirs)
	}

	budget := map[string]int{}
	for _, e := range providerExemptions {
		budget[e.file+"\x00"+e.text] = e.count
	}
	seen := map[string]int{}

	for _, path := range files {
		// This guard's own patterns and negative controls are provider names by
		// necessity. Skipping the file by name is a structural exclusion, not an
		// allowance: nothing in it is ever printed.
		if strings.HasSuffix(path, "provider_name_guard_test.go") {
			continue
		}
		lits, err := stringLiterals(path)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		rel := filepath.ToSlash(path)
		for line, values := range lits {
			for _, v := range values {
				for _, hit := range providerHits(v) {
					key := rel + "\x00" + hit
					if allowed, ok := budget[key]; ok {
						seen[key]++
						if seen[key] <= allowed {
							continue
						}
					}
					t.Errorf(
						"%s:%d names the compute provider in a string literal: %q\n"+
							"    Say what the PLATFORM does, not which vendor does it — "+
							"'the platform', 'the app', 'the machine'. The name is fine "+
							"in comments and identifiers; this guard only reads literals.\n"+
							"    If it genuinely has to name the provider, add a "+
							"providerExemption with a count and a reason.",
						rel, line, hit,
					)
				}
			}
		}
	}
}

func TestProviderDetectorFiresOnEverySpelling(t *testing.T) {
	// Negative controls FIRST: a zero from a pattern that matches nothing looks
	// exactly like a clean corpus.
	for _, s := range []string{
		"  5. Deploys to Fly.io",
		"prevent the Fly.io proxy from waking it",
		"https://reporter.fly.dev",
		"run `flyctl apps list`",
		"https://api.machines.dev/v1",
		"destroy its Fly app",
	} {
		if len(providerHits(s)) == 0 {
			t.Errorf("the detector missed %q", s)
		}
	}
}

func TestProviderDetectorIgnoresOrdinaryEnglish(t *testing.T) {
	// Over-matching would push real copy into the exemption table, which is how
	// an allowlist grows until it means nothing.
	for _, s := range []string{
		"bytes fly over the tunnel",
		"a flyweight client",
		"the butterfly effect",
		"Flying between regions",
	} {
		if hits := providerHits(s); len(hits) > 0 {
			t.Errorf("the detector over-matched %q: %v", s, hits)
		}
	}
}

func TestStringLiteralScannerSeesLiteralsAndNotComments(t *testing.T) {
	// Anti-no-op on the scanner, both directions. A scanner that found nothing
	// would report every file clean; one that read comments would contradict the
	// rule this guard enforces.
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	src := "package p\n\n// Deploys to Fly.io\nvar Help = \"Deploys to the platform\"\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	lits, err := stringLiterals(path)
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, vs := range lits {
		all = append(all, vs...)
	}
	joined := strings.Join(all, " ")
	if !strings.Contains(joined, "Deploys to the platform") {
		t.Fatalf("the scanner missed a real string literal, got %q", joined)
	}
	if strings.Contains(joined, "Fly.io") {
		t.Fatalf("the scanner read a comment as copy, got %q", joined)
	}
}

func TestProviderExemptionsAreLoadBearing(t *testing.T) {
	// An exemption whose site is gone silently widens the guard for whoever next
	// writes that word in that file.
	for _, e := range providerExemptions {
		lits, err := stringLiterals(e.file)
		if err != nil {
			t.Errorf("exemption for %s: %v (file gone? drop the entry). It claimed: %s", e.file, err, e.why)
			continue
		}
		actual := 0
		for _, values := range lits {
			for _, v := range values {
				for _, hit := range providerHits(v) {
					if strings.EqualFold(hit, e.text) {
						actual++
					}
				}
			}
		}
		if actual != e.count {
			t.Errorf("%s: %q exempted %dx, found %dx — a shrinking count means the exemption outlived its site. It claimed: %s",
				e.file, e.text, e.count, actual, e.why)
		}
	}
}

func TestTheSiblingGatesAreNamedInThisFile(t *testing.T) {
	// One rule, three repositories. The other two lanes are unreachable from
	// this process, so the only thing keeping them findable is that each names
	// the others.
	src, err := os.ReadFile("provider_name_guard_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, sibling := range []string{"test_provider_name_guard.py", "vocabulary-test.js"} {
		if !strings.Contains(string(src), sibling) {
			t.Errorf("this guard no longer names its sibling lane %s", sibling)
		}
	}
}
