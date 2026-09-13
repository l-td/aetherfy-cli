// Package cplink extracts the FIELD NAMES of the control plane's link-status
// response, so the CLI's decode of it cannot drift in silence.
//
// WHY THIS EXISTS. internal/api.GitHubLinkStatus decodes
// GET /agents/{id}/github by json tag. Rename a field on the control-plane side
// and nothing here fails: encoding/json leaves the Go field at its zero value,
// so `afy status` prints an empty branch, or calls an agent's directory the
// repository root, or silently stops warning that a link is inert. No error, no
// red test, because this repo's tests mock the server and agree with themselves
// about the spellings. It is the same failure mode cperrors was built for, one
// layer down: there the pin is an error-code literal, here it is a json tag.
//
// The trust model is cperrors': a committed snapshot is what CI checks against,
// and where the sibling control plane IS checked out the extraction re-runs and
// reds on drift, so a stale snapshot cannot survive a dev build.
//
// PUSH-GATED, AND SCOPED TO ONE FILE. The snapshot describes pushed code by
// construction: the generator refuses to write while the model's own file is
// dirty or sits in unpushed commits, and the drift check declines to compare
// rather than reporting a developer's own work as staleness. The gate asks about
// api/routes/github.py specifically, not the whole control-plane checkout, and
// that is deliberate: this extraction reads one file, so unrelated unpushed work
// elsewhere in that repo is no reason to stop checking this one. (docs-site's
// cp-surface gate is repo-wide because it imports the whole FastAPI app.)
//
// Nothing here is used by the `afy` binary. It is guard machinery, which is why
// it lives under test/.
package cplink

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SourcePath is the control-plane file declaring the model, CP-repo-relative.
const SourcePath = "api/routes/github.py"

// ModelName is the pydantic model whose fields are the response's field names.
const ModelName = "GitHubLinkStatusResponse"

// SnapshotPath is the committed snapshot, relative to the CLI repo root.
const SnapshotPath = "test/cp-link-status-snapshot.json"

// GeneratorPath is what regenerates it.
const GeneratorPath = "scripts/cp-link-status-snapshot"

// SchemaVersion is the snapshot format this package reads and writes.
const SchemaVersion = 1

// minFields is the anti-no-op floor. The model held seven fields when this guard
// was written, and a response model loses fields only by a deliberate contract
// break. Under five means the parser has rotted, not that the model shrank.
//
// It is a PARSER-ROT TRIPWIRE and nothing more: it cannot notice one field going
// missing, and does not need to — the drift check names every difference, and
// regeneration puts the diff in front of a human.
const minFields = 5

// Source records where an extraction came from and whether that state is
// shareable. It is the push gate's evidence, carried in the snapshot so a reader
// can see which commit of which file the field list describes.
type Source struct {
	Path   string `json:"path"`
	Model  string `json:"model"`
	Branch string `json:"branch"`
	// Dirty is true when the source file has uncommitted changes.
	Dirty bool `json:"dirty"`
	// Unpushed is true when commits touching the source file are not on the
	// branch's upstream. Nil when the branch has no upstream at all, which is
	// its own kind of unshareable.
	Unpushed *bool `json:"unpushed"`
}

// Fields is one extraction: the model's field names, and where they came from.
type Fields struct {
	Names  []string `json:"fields"`
	Source *Source  `json:"source"`
}

// Snapshot is the on-disk form.
type Snapshot struct {
	Comment       string   `json:"$comment"`
	Generator     string   `json:"generator"`
	SchemaVersion int      `json:"schemaVersion"`
	Source        *Source  `json:"source"`
	Fields        []string `json:"fields"`
}

// RootEnv, Root and RootExists are cperrors' — one answer to "where is the
// control plane", not two. Kept as wrappers so callers here need not import a
// package about error codes to ask a question about a response model.

// fieldDecl is a pydantic field declaration at class-body indentation:
// `    name: type ...`. Anchored at exactly four spaces so a nested class body,
// a method local, or a continuation line cannot pass for a field.
//
// Names are lowercase with underscores, which every field on this model is and
// every json key the CLI decodes must be. A field declared any other way is not
// something GitHubLinkStatus can be reading, so leaving it out of the snapshot
// would be safer than guessing — but it would also be a silent narrowing, which
// is why Validate re-checks the count floor rather than trusting this regexp.
var fieldDecl = regexp.MustCompile(`^ {4}([a-z][a-z0-9_]*)\s*:\s*\S`)

// classStart matches the model's own class statement.
func classStart(model string) *regexp.Regexp {
	return regexp.MustCompile(`^class ` + regexp.QuoteMeta(model) + `\s*\(`)
}

// dedent is any line that ends a class body: column-0 content.
var dedent = regexp.MustCompile(`^\S`)

// ParseModel returns the field names declared by one pydantic model in src.
//
// The seam that makes this testable without a control-plane checkout: the
// drift check and the generator both reach the real file through Extract, and
// the unit tests drive this with synthetic source, including the negative
// controls (a model that is not there, a body with no fields).
func ParseModel(src, model string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	start := classStart(model)

	inClass := false
	var names []string
	for _, line := range lines {
		if !inClass {
			if start.MatchString(line) {
				inClass = true
			}
			continue
		}
		// A docstring or comment line is neither a field nor a dedent.
		if m := fieldDecl.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
			continue
		}
		if dedent.MatchString(line) {
			break // the class body ended
		}
	}

	if !inClass {
		return nil, fmt.Errorf("no `class %s(...)` in the source — the model was renamed or "+
			"moved. Extracting nothing here would read as agreement with everything", model)
	}
	sort.Strings(names)
	return names, nil
}

// Extract reads the model's fields from a control-plane checkout, with the
// provenance the push gate needs.
func Extract(cpRoot string) (*Fields, error) {
	full := filepath.Join(cpRoot, filepath.FromSlash(SourcePath))
	body, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", SourcePath, err)
	}
	names, err := ParseModel(string(body), ModelName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SourcePath, err)
	}
	return &Fields{Names: names, Source: describeSource(cpRoot)}, nil
}

// SourceMissing reports whether the source file is absent from a checkout that
// exists. A caller that found a checkout must treat this as a FAILURE naming the
// path, never as absence: a moved file that reads as "no checkout" is how a
// guard stops guarding without saying so (cperrors.MissingSources, same lesson).
func SourceMissing(cpRoot string) bool {
	_, err := os.Stat(filepath.Join(cpRoot, filepath.FromSlash(SourcePath)))
	return err != nil
}

// describeSource asks git about the source file. Every failure degrades to "not
// shareable" rather than to "fine": an unknown state must never read as pushed.
func describeSource(cpRoot string) *Source {
	src := &Source{Path: SourcePath, Model: ModelName, Dirty: true}

	git := func(args ...string) (string, bool) {
		cmd := exec.Command("git", args...)
		cmd.Dir = cpRoot
		out, err := cmd.Output()
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}

	branch, ok := git("rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return src // no git answer at all: leave it dirty, which blocks
	}
	src.Branch = branch

	status, ok := git("status", "--porcelain", "--", SourcePath)
	if !ok {
		return src
	}
	src.Dirty = status != ""

	// No upstream is its own unshareable state, recorded as a nil Unpushed
	// rather than as false, because "nothing to compare against" is not
	// "nothing to push".
	if _, ok := git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); !ok {
		return src
	}
	changed, ok := git("diff", "--name-only", "@{upstream}..HEAD", "--", SourcePath)
	if !ok {
		return src
	}
	unpushed := changed != ""
	src.Unpushed = &unpushed
	return src
}

// Unshareable says why an extraction cannot be compared or committed, or "" when
// it can. The wording mirrors docs-site's unshareableSource, because it is the
// same gate for the same reason.
func Unshareable(src *Source) string {
	if src == nil {
		return "the extraction carries no provenance, so there is no way to tell whether it " +
			"describes pushed code"
	}
	if src.Dirty {
		return fmt.Sprintf("%s has uncommitted changes in the control-plane checkout", src.Path)
	}
	if src.Unpushed == nil {
		return fmt.Sprintf("branch %q has no upstream, so its commits are on no shared branch",
			src.Branch)
	}
	if *src.Unpushed {
		return fmt.Sprintf("%s is changed by commits that are not on %q's upstream, so the "+
			"surface it describes is not pushed", src.Path, src.Branch)
	}
	return ""
}

// Validate is the refuse-to-trust check for a field list's structure. It is what
// can be asked of a snapshot loaded from disk as well as of a fresh extraction.
//
// An extraction that found nothing must never read as "everything matches",
// because it would — by having nothing to disagree with.
func Validate(f *Fields) error {
	if f == nil {
		return fmt.Errorf("no field list at all")
	}
	if len(f.Names) == 0 {
		return fmt.Errorf("the field list is EMPTY — the class-body parser has rotted, or the "+
			"model moved out of %s. Refusing to treat nothing as agreement", SourcePath)
	}
	if len(f.Names) < minFields {
		return fmt.Errorf("extracted %d field(s), floor is %d — too few to be %s",
			len(f.Names), minFields, ModelName)
	}
	seen := map[string]bool{}
	for _, n := range f.Names {
		if seen[n] {
			return fmt.Errorf("field %q appears twice — the parser is reading something other "+
				"than one class body", n)
		}
		seen[n] = true
		if !fieldDecl.MatchString("    " + n + ": x") {
			return fmt.Errorf("field %q is not the shape a json tag can be (%s)", n, fieldDecl)
		}
	}
	if !sort.StringsAreSorted(f.Names) {
		return fmt.Errorf("the field list is not sorted, so a regeneration would churn the diff")
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

// Fields view of a loaded snapshot, so both sides of a comparison are one type.
func (s *Snapshot) FieldList() *Fields {
	if s == nil {
		return nil
	}
	return &Fields{Names: s.Fields, Source: s.Source}
}

// Marshal renders a field list as the committed snapshot's bytes.
func Marshal(f *Fields) ([]byte, error) {
	snap := Snapshot{
		Comment: "The field names of the control plane's " + ModelName + ", extracted from the " +
			"sibling aetherfy-control-plane repo — do not edit by hand. Consumed by " +
			"test/cp_link_status_test.go so internal/api.GitHubLinkStatus cannot decode a " +
			"field name the control plane does not send. Regenerate with `go run ./" +
			GeneratorPath + "`.",
		Generator:     GeneratorPath,
		SchemaVersion: SchemaVersion,
		Source:        f.Source,
		Fields:        f.Names,
	}
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}
