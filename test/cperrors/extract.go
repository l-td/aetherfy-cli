// Package cperrors extracts the control plane's error-code registry and finds
// the code-shaped string literals this CLI pins against it.
//
// WHY THIS EXISTS. The CLI branches on control-plane error-code string
// literals — `deploy` keys on AGENT_NOT_FOUND to offer create-on-deploy, the
// lifecycle retry keys on AGENT_OPERATION_IN_PROGRESS and RESOURCE_BUSY,
// `agents run` keys on AGENT_RUN_REQUIRES_JOB_TYPE, and so on. If the control
// plane renames one, the branch silently stops firing: no crash, no red test,
// because the CLI's tests mock the server and agree with themselves about the
// spelling. That is not hypothetical — commit bf93cd1 fixed exactly this drift
// (AUTH_INVALID_API_KEY had become INVALID_API_KEY) and it was found by hand.
//
// The trust model is the one docs-site uses for cli-surface-snapshot.json,
// pointed the other way: a committed snapshot is the contract CI checks
// against, and where the sibling repo IS checked out the extraction re-runs
// and reds on drift, so a stale snapshot cannot survive a dev build.
//
// Nothing here is used by the `afy` binary. It is guard machinery, which is
// why it lives under test/.
package cperrors

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The two error-code tiers the control plane keeps deliberately separate
// (shared/error_codes.py, "SCOPE" note). Top-level codes appear as
// `detail.code`; violation-tier codes appear only inside `detail.violations[]`
// entries. The guard checks EXISTENCE, not tier — a client that pins either
// one is pinning a real string — but the snapshot records which is which so a
// reader can tell where a code can actually surface.
const (
	tierTopLevel  = "top-level"
	tierViolation = "violation"
)

// source is one control-plane file that declares error codes. `why` is
// documentation for the next reader of this list and deliberately stays in the
// source: it used to be copied into the committed snapshot, where a sentence
// nothing verifies is just drift bait in an artifact that claims to be extracted.
type source struct {
	path string // CP-repo-relative, slash-separated
	tier string
	why  string
}

// sources is the complete set of control-plane error-code registries, per the
// scope ruling recorded at the top of shared/error_codes.py: that file is the
// registry of every top-level `detail.code`, and the violation-tier codes stay
// module-local to the endpoint that owns them. Verified complete 2026-08-20 by
// sweeping the whole control plane for `^NAME = "NAME"$` outside these three —
// zero hits.
var sources = []source{
	{
		path: "shared/error_codes.py",
		tier: tierTopLevel,
		why:  "the registry of every code that can appear as detail.code",
	},
	{
		path: "api/routes/regions.py",
		tier: tierViolation,
		why:  "the five region-preflight codes, deliberately kept out of the top-level registry",
	},
	{
		path: "shared/plan_validator.py",
		tier: tierViolation,
		why:  "AGENT_COLLECTION_REGION_MISMATCH, raised through AgentScopeViolation",
	},
}

// Code is one extracted error code.
type Code struct {
	Source string `json:"source"`
	Tier   string `json:"tier"`
}

// SourceStat records what each source contributed, so an extraction that read
// a file and found nothing in it is visible rather than averaged away.
type SourceStat struct {
	Path  string `json:"path"`
	Tier  string `json:"tier"`
	Count int    `json:"count"`
}

// Registry is one extraction of the control plane's error codes.
//
// Literals holds the raw `NAME = "VALUE"` pairs the extraction accepted, keyed
// by name. It is never serialized: storing a value identical to its key would
// be noise in the artifact. It exists so ValidateExtraction can re-assert the
// self-naming rule the whole extractor rests on, rather than trusting that the
// one regexp implementing it was never loosened. A snapshot loaded from disk
// has no Literals, which is why the two validators are separate functions and
// not one function with a silent skip.
type Registry struct {
	Codes    map[string]Code   `json:"codes"`
	Sources  []SourceStat      `json:"sources"`
	Literals map[string]string `json:"-"`
}

// Snapshot is the on-disk form of a Registry.
type Snapshot struct {
	Comment       string          `json:"$comment"`
	Generator     string          `json:"generator"`
	SchemaVersion int             `json:"schemaVersion"`
	Sources       []SourceStat    `json:"sources"`
	Codes         map[string]Code `json:"codes"`
}

// SchemaVersion is the snapshot format this package reads and writes.
const SchemaVersion = 1

// SnapshotPath is the committed snapshot, relative to the CLI repo root.
const SnapshotPath = "test/cp-error-codes-snapshot.json"

// GeneratorPath is what regenerates it.
const GeneratorPath = "scripts/cp-error-codes-snapshot"

// RootEnv overrides where the control-plane checkout is looked for. The guard's
// drift check reads it, which is also how the mutation proofs point the guard at
// a doctored copy of the registry.
const RootEnv = "AETHERFY_CP_ROOT"

// A module-level, self-named string constant: `NAME = "NAME"`.
//
// Self-naming is the whole rule, and it is what keeps this honest without a
// hand-maintained include list. plan_validator.py holds one error code beside
// UPGRADE_URL = "/billing/upgrade", SUPPORT_CONTACT = "mailto:..." and two
// FREEZE_REASON_* values; requiring name == value takes the code and leaves the
// four others, by form rather than by enumeration. Anchored at column 0 so
// class attributes and locals are never mistaken for a registry entry.
var pyConst = regexp.MustCompile(`^([A-Z][A-Z0-9_]*) = "([^"]*)"\s*(?:#.*)?$`)

// codeShape is what an error code looks like: SCREAMING_SNAKE with at least
// one underscore.
//
// The underscore is load-bearing for the Go-side scan — without it every
// "SERVICE", "ERROR", "STATUS" and "ARCHIVED" in the CLI becomes a candidate
// and the guard drowns. Every code the control plane has ever published is
// multi-word, and Validate re-checks that against the live registry, so if a
// single-word code is ever added the guard reds and says to widen this.
var codeShape = regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+$`)

// Root answers where the control-plane checkout should be, given the CLI repo
// root. Siblings by default; RootEnv wins.
func Root(repoRoot string) string {
	if v := os.Getenv(RootEnv); v != "" {
		return v
	}
	return filepath.Join(repoRoot, "..", "aetherfy-control-plane")
}

// RootExists reports whether there is a control-plane checkout at cpRoot at all.
//
// This is a SEPARATE question from whether its registries are where we look for
// them, and conflating the two is how a guard stops guarding in silence: one
// function answering "present?" for both meant a checkout whose registry had
// merely MOVED reported "no control-plane checkout", skipped, and went green.
// The control plane is refactored often and shared/error_codes.py is exactly the
// kind of file that gets folded into a package. See MissingSources.
func RootExists(cpRoot string) bool {
	info, err := os.Stat(cpRoot)
	return err == nil && info.IsDir()
}

// MissingSources lists the configured registries that are not readable under
// cpRoot. Callers that found a checkout must treat a non-empty result as a
// FAILURE naming the paths, never as absence: extracting from two registries of
// three and diffing against a snapshot built from all three would report the
// third's codes as deletions, and skipping instead would silently drop drift
// detection on every machine that has the sibling.
func MissingSources(cpRoot string) []string {
	var missing []string
	for _, s := range sources {
		if _, err := os.Stat(filepath.Join(cpRoot, filepath.FromSlash(s.path))); err != nil {
			missing = append(missing, s.path)
		}
	}
	return missing
}

// Extract reads every source under cpRoot and returns the codes they declare.
func Extract(cpRoot string) (*Registry, error) {
	reg := &Registry{Codes: map[string]Code{}, Literals: map[string]string{}}

	for _, s := range sources {
		full := filepath.Join(cpRoot, filepath.FromSlash(s.path))
		body, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", s.path, err)
		}

		count := 0
		for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
			m := pyConst.FindStringSubmatch(line)
			if m == nil || m[1] != m[2] {
				continue
			}
			count++
			if _, dup := reg.Codes[m[1]]; dup {
				continue // first source wins; the count still records the sighting
			}
			reg.Codes[m[1]] = Code{Source: s.path, Tier: s.tier}
			reg.Literals[m[1]] = m[2]
		}

		reg.Sources = append(reg.Sources, SourceStat{Path: s.path, Tier: s.tier, Count: count})
	}

	return reg, nil
}

// minTotalCodes is the anti-no-op floor. The registry held 123 codes when this
// guard was written and is append-only by policy, so falling under 80 means the
// extractor has rotted, not that the control plane shrank.
//
// It is a PARSER-ROT TRIPWIRE and nothing more. It deliberately does not try to
// notice a mass deletion — 40 codes could vanish and this would still pass — and
// it does not need to: the drift check re-extracts wherever the sibling exists
// and reds on every single missing code by name, and regeneration puts the diff
// in front of a human. Raising the floor toward the real count would only turn
// every legitimate addition into a second edit.
const minTotalCodes = 80

// Validate is the refuse-to-trust check for a registry's STRUCTURE: source
// coverage, non-empty extraction per source, the floor, and the shape of every
// code name. It is what can be asked of a snapshot loaded from disk.
//
// An extraction that found nothing must never read as "everything matches",
// because it would — by having nothing to disagree with. A wrong extracted
// value is worse than no snapshot at all, so every failure here is fatal.
func Validate(reg *Registry) error {
	if reg == nil {
		return fmt.Errorf("no registry at all")
	}
	if len(reg.Sources) != len(sources) {
		return fmt.Errorf("covers %d source(s), want %d — the source list changed; "+
			"regenerate the snapshot", len(reg.Sources), len(sources))
	}
	for _, s := range reg.Sources {
		if s.Count == 0 {
			return fmt.Errorf("%s yielded 0 codes — the `NAME = \"NAME\"` parser has rotted, "+
				"or the file moved. Refusing to treat an empty extraction as agreement", s.Path)
		}
	}
	if len(reg.Codes) < minTotalCodes {
		return fmt.Errorf("extracted %d code(s), floor is %d — too few to be the real registry",
			len(reg.Codes), minTotalCodes)
	}
	for name := range reg.Codes {
		if !codeShape.MatchString(name) {
			return fmt.Errorf("code %q does not match the shape the Go-side scan looks for (%s). "+
				"A code the scan cannot see is a code the guard cannot check: widen codeShape "+
				"in test/cperrors/extract.go and re-run", name, codeShape)
		}
	}
	return nil
}

// ValidateExtraction is Validate plus the check only a FRESH extraction can be
// asked for: that every accepted constant was self-named.
//
// Validate alone shape-checks the KEY and never looks at the value, so the
// self-naming rule lived in exactly one place — the `m[1] != m[2]` test inside
// Extract — with nothing asserting it. Deleting that test made the generator
// write UPGRADE_URL = "/billing/upgrade", SUPPORT_CONTACT and both FREEZE_REASON_*
// values into the snapshot AS ERROR CODES, and every check passed: the keys are
// perfectly code-shaped. The count went 123 → 127 while error_codes.py still
// reported its usual 117, so the number a reader would look at did not move.
// From there the guard would have blessed a CLI branch on "UPGRADE_URL".
//
// The generator and the live-drift check both call this. A loaded snapshot has
// no Literals and gets Validate instead — two functions rather than one with a
// conditional, so neither can silently degrade into the other.
func ValidateExtraction(reg *Registry) error {
	if err := Validate(reg); err != nil {
		return err
	}
	if len(reg.Literals) != len(reg.Codes) {
		return fmt.Errorf("carries %d literal(s) for %d code(s) — an extraction must record "+
			"what it read, or the self-naming check below proves nothing",
			len(reg.Literals), len(reg.Codes))
	}
	for name, literal := range reg.Literals {
		if literal != name {
			return fmt.Errorf("%s is bound to %q, not to itself — this extraction accepted a "+
				"constant that is not self-named, so it is picking up things that are not error "+
				"codes (URLs, reason strings). The `NAME = \"NAME\"` rule in Extract is the only "+
				"thing separating the two", name, literal)
		}
	}
	return nil
}

// Load reads a committed snapshot.
func Load(path string) (*Snapshot, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &snap, nil
}

// Registry view of a loaded snapshot, so both sides of a drift comparison are
// the same type.
func (s *Snapshot) Registry() *Registry {
	if s == nil {
		return nil
	}
	return &Registry{Codes: s.Codes, Sources: s.Sources}
}

// Marshal renders a registry as the committed snapshot's bytes. Map keys are
// sorted by encoding/json, so regenerating without a change is a no-op diff.
func Marshal(reg *Registry) ([]byte, error) {
	snap := Snapshot{
		Comment: "The control plane's error-code registry, extracted from the sibling " +
			"aetherfy-control-plane repo — do not edit by hand. Consumed by " +
			"test/cp_error_codes_test.go so the CLI cannot branch on an error code the " +
			"control plane does not publish. Regenerate with `go run ./" + GeneratorPath + "`.",
		Generator:     GeneratorPath,
		SchemaVersion: SchemaVersion,
		Sources:       reg.Sources,
		Codes:         reg.Codes,
	}
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// Literal is one code-shaped string literal found in Go source.
type Literal struct {
	Value string
	File  string // repo-relative, slash-separated
	Line  int
}

// envelopeCode matches a code sitting INSIDE a larger string: the `code` field
// of a control-plane error envelope, as the mocked servers in this repo write
// it — `{"detail":{"code":"AGENT_NOT_FOUND","message":"..."}}`.
//
// Without this, half the fixtures are invisible. The drift bf93cd1 fixed lived
// in both forms on the same test case: a `wantCode: "AUTH_INVALID_API_KEY"`
// expectation (a whole literal) and the response body it was compared against
// (a code embedded in JSON). A guard that saw only the first would have caught
// that one, but a fixture whose body alone goes stale is the same bug — the
// mock answers with a string the server stopped sending.
//
// The pattern is the envelope's own shape rather than "any capitals inside any
// string", which would flag every env-var name printed in help text.
var envelopeCode = regexp.MustCompile(`"code"\s*:\s*"([A-Z][A-Z0-9_]*)"`)

// ScanGoSource returns every control-plane error code pinned by one Go file:
// code-shaped string literals, plus codes embedded in an error envelope.
//
// Parsed as Go rather than grepped, because the distinction that matters is
// exactly the one a regex cannot make: internal/api/errors.go's doc comment
// spells out the envelope with "STABLE_CODE" and names AGENT_NOT_FOUND and
// WORKSPACE_NAME_TAKEN as examples. Those are prose about the contract, not
// pins on it. Comments are not in the AST, so they are not candidates.
//
// Excluding comments is a decision, not an oversight. That same comment records
// the 2026-08-20 auth rename by naming both sides of each pair — AUTH_REQUIRED →
// MISSING_API_KEY and three more. Those old spellings are the history, and a
// guard that scanned comments would demand their deletion forever. Prose about a
// contract is not a use of it; only code that branches can go silently dead.
func ScanGoSource(name, src string) ([]Literal, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", name, err)
	}

	var out []Literal
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		line := fset.Position(lit.Pos()).Line

		if codeShape.MatchString(v) {
			out = append(out, Literal{Value: v, File: name, Line: line})
			return true
		}
		for _, m := range envelopeCode.FindAllStringSubmatch(v, -1) {
			out = append(out, Literal{Value: m[1], File: name, Line: line})
		}
		return true
	})
	return out, nil
}

// skippedDirs are not the CLI's source.
var skippedDirs = map[string]bool{
	".git": true, "build": true, "dist": true, "vendor": true, "node_modules": true,
}

// ScanTree returns every code-shaped string literal in every .go file under
// root, except files whose repo-relative path is in skip.
//
// Test files are scanned on purpose: a mocked server that answers with a code
// the control plane no longer emits is drift that makes the suite pass while
// the product is broken, which is the failure mode this whole guard exists for.
func ScanTree(root string, skip map[string]bool) ([]Literal, error) {
	var out []Literal

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && skippedDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skip[rel] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lits, err := ScanGoSource(rel, string(body))
		if err != nil {
			return err
		}
		out = append(out, lits...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}
