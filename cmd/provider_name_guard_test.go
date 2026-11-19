package cmd

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The compute provider's name may not appear in anything a user reads, and neither may the word "provider" said
// of OUR infrastructure.
//
// WHY IT IS A RULE. A vendor's name in a customer-facing string is a contract
// nobody meant to sign. It started with the agent URL — `<app>.fly.dev`, a
// permanent public address in someone else's namespace — but the URL was only
// the loudest instance. `afy deploy --help` listed "Deploys to Fly.io" as step
// five of the deployment process; `afy stop --help` explained the pause
// in terms of "the Fly.io proxy"; `archive`/`restore` described what they do to
// "its Fly.io app". Every one of those teaches a user something about our
// infrastructure that they will reasonably expect to keep being true.
//
// THE WORD "provider" (owner, 2026-11-17: nothing a customer reads names an infrastructure vendor or says
// "provider" for OUR infrastructure). Leaving the vendor's name out and writing "the provider is unavailable" or
// "the provider's rate limit" tells the customer the same thing with the name filed off: there is a vendor under
// the platform, and it is the one that failed. So the word is refused in the same literals, by a second pattern
// (providerWord): "provider", "providers" and "provider's", in any case. What a customer meets instead is "the
// platform", "the machine" or "temporarily unavailable".
//
// WHAT STAYS ALLOWED: a provider the CUSTOMER chose. "their embedding provider" and "any model provider" are
// about the customer's own accounts and keys, and say nothing about our infrastructure, so a rule against the
// word everywhere would forbid sentences the product has to be able to say. The allowance is exactly those two
// phrases (customerOwnProvider): a third one needs its own reason and its own row in the detector test, so the
// allowance cannot widen into "provider is fine when it reads naturally".
//
// WHERE IT IS FINE: comments, identifiers, and anything else that is not a
// string literal. Go comments are not string literals, so scanning literals
// alone gets that carve-out for free — no comment stripper to be wrong about.
// The word has a second carve-out the vendor names do not: a _test.go file. Its literals are fixtures and test
// names, which no customer reads, and the binary does not contain them, so the word is not looked for there. (A
// vendor name is still refused in a test file, as before.)
//
// WHAT IS SCANNED: every string literal in cmd/, internal/ and pkg/ (pkg/version
// words `afy version`), which between them hold all of this binary's help text
// (cobra Short/Long/Example), every output.Print* argument, every error message,
// and the keys of the JSON it prints (a struct tag is a literal too). That is broader than
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

// providerWord is the word itself, whole, in any case: "provider", "providers", "Provider's". The word
// boundary keeps "provides", "provided" and "providing" out.
var providerWord = regexp.MustCompile(`(?i)\bproviders?\b`)

// customerOwnProvider is the allowance, and the reason is the one above: a provider the customer chose is theirs
// to name. It is removed from a literal before providerWord looks at it, so "the model provider and our provider"
// still has one hit.
var customerOwnProvider = regexp.MustCompile(`(?i)\b(?:embedding|model) providers?\b`)

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

var scannedDirs = []string{"../cmd", "../internal", "../pkg"}

// providerHits returns one entry per occurrence of the compute provider's NAME, across every spelling.
func providerHits(s string) []string {
	var out []string
	for _, re := range providerPatterns {
		out = append(out, re.FindAllString(s, -1)...)
	}
	return out
}

// providerWordHits returns one entry per occurrence of the word "provider" that is not a customer's own.
func providerWordHits(s string) []string {
	return providerWord.FindAllString(customerOwnProvider.ReplaceAllString(s, " "), -1)
}

// isTestFile reports whether path is a Go test file, which the binary does not contain.
func isTestFile(path string) bool { return strings.HasSuffix(path, "_test.go") }

// literalHits is every hit in one string literal of the file at path: the vendor's names everywhere, and the
// word "provider" in the files the binary is built from.
func literalHits(path, literal string) []string {
	hits := providerHits(literal)
	if !isTestFile(path) {
		hits = append(hits, providerWordHits(literal)...)
	}
	return hits
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

// providerMessage is the failure for one hit: the sentence says which of the two rules it broke.
func providerMessage(rel string, line int, hit string) string {
	if providerWord.MatchString(hit) {
		return fmt.Sprintf(
			"%s:%d says %q in a string literal.\n"+
				"    A customer reads this, and our infrastructure is never a 'provider' to them: say what the PLATFORM does — "+
				"'the platform', 'the machine', 'temporarily unavailable'. A provider the customer chose is fine, "+
				"as 'embedding provider' or 'model provider'. Comments, identifiers and test files are not read by this guard.\n"+
				"    If it genuinely has to say it, add a providerExemption with a count and a reason.",
			rel, line, hit)
	}
	return fmt.Sprintf(
		"%s:%d names the compute provider in a string literal: %q\n"+
			"    Say what the PLATFORM does, not which vendor does it — "+
			"'the platform', 'the app', 'the machine'. The name is fine "+
			"in comments and identifiers; this guard only reads literals.\n"+
			"    If it genuinely has to name the provider, add a "+
			"providerExemption with a count and a reason.",
		rel, line, hit)
}

// providerViolations is THE GUARD: one message per hit in the files' string literals, past the exemptions'
// counted allowance, in file and line order. A function of the files it is given, so the test that plants a hit
// runs the code that reads the source, not a copy of it.
func providerViolations(files []string, exemptions []providerExemption) ([]string, error) {
	budget := map[string]int{}
	for _, e := range exemptions {
		budget[e.file+"\x00"+e.text] = e.count
	}
	seen := map[string]int{}

	var out []string
	for _, path := range files {
		// This guard's own patterns and negative controls are provider names by
		// necessity. Skipping the file by name is a structural exclusion, not an
		// allowance: nothing in it is ever printed.
		if strings.HasSuffix(path, "provider_name_guard_test.go") {
			continue
		}
		lits, err := stringLiterals(path)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		rel := filepath.ToSlash(path)
		lines := make([]int, 0, len(lits))
		for line := range lits {
			lines = append(lines, line)
		}
		sort.Ints(lines)
		for _, line := range lines {
			for _, v := range lits[line] {
				for _, hit := range literalHits(path, v) {
					key := rel + "\x00" + hit
					if allowed, ok := budget[key]; ok {
						seen[key]++
						if seen[key] <= allowed {
							continue
						}
					}
					out = append(out, providerMessage(rel, line, hit))
				}
			}
		}
	}
	return out, nil
}

func TestNoProviderNameInUserVisibleStrings(t *testing.T) {
	files := goFiles(t)

	// Anti-no-op: a walker pointed at the wrong directory finds nothing and
	// looks exactly like a clean corpus. ~40 files at the time of writing.
	if len(files) < 25 {
		t.Fatalf("only %d Go files scanned across %v — the walker is not reaching the source", len(files), scannedDirs)
	}
	// And the word rule reads only the files the binary is built from, so a walk that reached tests alone would
	// leave it vacuous.
	var built int
	reached := map[string]bool{}
	for _, path := range files {
		if !isTestFile(path) {
			built++
		}
		reached[filepath.ToSlash(path)] = true
	}
	// And each tree is reached BY NAME, one file in each: pkg/version words `afy version`, and a scan that
	// dropped a tree would read clean.
	for _, canary := range []string{"../cmd/deploy.go", "../internal/output/format.go", "../pkg/version/version.go"} {
		if !reached[canary] {
			t.Fatalf("%s was not scanned — the walker is not reaching its tree", canary)
		}
	}
	if built < 25 {
		t.Fatalf("only %d non-test Go files scanned across %v — the word rule is not reaching the binary's own text", built, scannedDirs)
	}

	violations, err := providerViolations(files, providerExemptions)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Error(v)
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

// THE WORD, both directions. Each row of the first list is a way to say "provider" of our infrastructure and must
// be caught, with the number of hits it holds; each of the second is English the rule must leave alone.
func TestProviderWordDetectorFiresOnOurInfrastructureAndLeavesTheCustomersOwn(t *testing.T) {
	for _, c := range []struct {
		text string
		hits int
	}{
		{"the provider is unavailable", 1},
		{"the provider's rate limit", 1},
		{"our providers are busy", 1},
		{"Provider error", 1},
		{"a provider-side limit", 1},
		{"the model provider and our provider", 1},
		{"a provider, another provider", 2},
	} {
		if got := providerWordHits(c.text); len(got) != c.hits {
			t.Errorf("%q: the detector found %v, want %d hit(s)", c.text, got, c.hits)
		}
	}

	for _, s := range []string{
		"their embedding provider",
		"any model provider",
		"Your Embedding Provider's key",
		"every model providers' limit",
		"the platform provides a machine",
		"as provided by you",
		"providing a key",
	} {
		if hits := providerWordHits(s); len(hits) > 0 {
			t.Errorf("the detector flagged %q, which is not about our infrastructure: %v", s, hits)
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

// THE GUARD BITES. The corpus test is silent when the source is clean, which is what a guard that reads nothing is
// too, so this plants hits in files of its own and runs the very function the corpus test runs: the word in a
// help string and in a struct tag is reported with its line, and so is a vendor name in a test file; the same
// word in a comment, in a customer's own provider, in "provides", and in a test file's literal is not.
func TestAPlantedProviderIsCaughtAndNothingElseIs(t *testing.T) {
	dir := t.TempDir()
	built := filepath.Join(dir, "planted.go")
	fixture := filepath.Join(dir, "planted_fixture_test.go")
	src := "package p\n" + //                                                        1
		"\n" + //                                                                  2
		"// The provider rejected it: a comment, not read.\n" + //                  3
		"var A = \"The provider rejected the request\"\n" + //                      4: hit
		"var B = \"their embedding provider's key\"\n" + //                        5: the customer's own
		"var C = \"any model provider\"\n" + //                                    6: the customer's own
		"var D = \"the platform provides it\"\n" + //                              7: another word
		"var E = \"Providers can be slow\"\n" + //                                 8: hit
		"type T struct {\n" + //                                                   9
		"\tName string `json:\"provider\"`\n" + //                               10: hit, a key the CLI prints
		"}\n"
	test := "package p\n\n" +
		"var F = \"the provider said no\"\n" + //                                  3: the word, in a test file
		"var G = \"see reporter.fly.dev\"\n" //                                    4: a vendor name, in a test file
	for path, text := range map[string]string{built: src, fixture: test} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := providerViolations([]string{built, fixture}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.ToSlash(built) + ":4 says \"provider\"",
		filepath.ToSlash(built) + ":8 says \"Providers\"",
		filepath.ToSlash(built) + ":10 says \"provider\"",
		filepath.ToSlash(fixture) + ":4 names the compute provider in a string literal: \"fly.dev\"",
	} {
		found := false
		for _, g := range got {
			found = found || strings.HasPrefix(g, want)
		}
		if !found {
			t.Errorf("the planted hit %q was not reported; got:\n%s", want, strings.Join(got, "\n"))
		}
	}
	if len(got) != 4 {
		t.Errorf("want exactly the 4 planted hits, got %d:\n%s", len(got), strings.Join(got, "\n"))
	}

	// A counted exemption takes exactly its count of one text in one file and nothing more.
	exempt := []providerExemption{{file: filepath.ToSlash(built), text: "provider", count: 2, why: "planted"}}
	got, err = providerViolations([]string{built}, exempt)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], filepath.ToSlash(built)+":8 says \"Providers\"") {
		t.Errorf("an exemption of 2 for \"provider\" leaves only the other spelling reported, got:\n%s", strings.Join(got, "\n"))
	}
}

// countMentions counts how many times `text` appears among the literals of the file at path, using the same
// matcher as the ban itself so the exemption and the rule can never disagree about what counts as a mention.
func countMentions(path string, lits map[int][]string, text string) int {
	n := 0
	for _, values := range lits {
		for _, v := range values {
			for _, hit := range literalHits(path, v) {
				if strings.EqualFold(hit, text) {
					n++
				}
			}
		}
	}
	return n
}

// TestTheExemptionCheckCanActuallyFail exists because the check it guards
// cannot fail on its own.
//
// providerExemptions is empty, and keeping it empty is the goal - no string
// literal in this binary names the provider. But an empty list makes
// TestProviderExemptionsAreLoadBearing VACUOUS: it ranges over nothing, its
// comparison never executes, and it reports PASS. That is the shape this
// whole workstream keeps finding - a check that reads as coverage while
// executing never - and it does not stop being that shape because the reason
// is a good one.
//
// So the comparison is exercised here on synthetic literals instead, in both
// directions: it must count what is present, and must not count what is not.
// One without the other is half a test - a counter stuck at zero passes the
// first check, and one stuck at the expected number passes the second.
func TestTheExemptionCheckCanActuallyFail(t *testing.T) {
	lits := map[int][]string{
		10: {`"deploying to fly.dev now"`},
		20: {`"see fly.dev for details"`},
		30: {`"the provider's limit"`},
	}

	if got := countMentions("x.go", lits, "fly.dev"); got != 2 {
		t.Errorf("counted %d mentions of a term that appears twice, want 2 - a "+
			"drifting exemption would go unnoticed", got)
	}
	if got := countMentions("x.go", lits, "flyctl"); got != 0 {
		t.Errorf("counted %d mentions of a term that never appears, want 0 - the "+
			"rot check would red on a healthy exemption", got)
	}
	// The word counts where the guard reads it, and not in a test file, where it does not.
	if got := countMentions("x.go", lits, "provider"); got != 1 {
		t.Errorf("counted %d mentions of the word in a built file, want 1", got)
	}
	if got := countMentions("x_test.go", lits, "provider"); got != 0 {
		t.Errorf("counted %d mentions of the word in a test file, want 0: the guard does not read it there", got)
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
		actual := countMentions(e.file, lits, e.text)
		if actual != e.count {
			t.Errorf("%s: %q exempted %dx, found %dx — a shrinking count means the exemption outlived its site. It claimed: %s",
				e.file, e.text, e.count, actual, e.why)
		}
	}
}
