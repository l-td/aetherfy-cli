// Package cpschemas holds the control plane's RESPONSE SCHEMAS, so that every
// struct the CLI decodes a control-plane answer into is checked against the
// model the control plane actually answers with.
//
// WHY THIS EXISTS. internal/api decodes each answer by json tag. When the
// control plane renames, drops or retypes a field, encoding/json does not
// complain: the Go field keeps its zero value, and `afy` prints an empty
// column, a wrong state or a missing warning. This repo's own tests serve
// hand-written bodies, so they agree with the CLI about every spelling, which is
// how `afy messages send --json` printed fields the control plane never sent.
// cplink pins ONE model's field names; this pins every answer the CLI reads, both
// ways, with types (test/cp_response_schemas_test.go).
//
// THE SOURCE IS THE APP ITSELF. The extraction imports the control plane's
// FastAPI app and reads `app.openapi()` (dump.py), because only the app knows
// which model each route answers with, and a source parse cannot read unions,
// Optional, Literal or an alias. That needs the control plane's Python with its
// requirements installed: PythonEnv names it, and the e2e nightly passes the
// venv it builds from the control plane's own requirements.
//
// The trust model is cperrors': a committed snapshot is what CI checks against
// (CI checks out no control plane), the generator refuses a checkout with
// uncommitted or unpushed Python (cperrors.DescribeCheckout / Unshareable, the
// one rule for every snapshot this repo takes from the control plane), and where
// the sibling IS checked out the extraction re-runs and reds on drift.
//
// Nothing here is used by the `afy` binary. It is guard machinery, which is why
// it lives under test/.
package cpschemas

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/l-td/aetherfy-cli/test/cperrors"
)

// SnapshotPath is the committed snapshot, relative to the CLI repo root.
const SnapshotPath = "test/cp-response-schemas-snapshot.json"

// GeneratorPath is what regenerates it.
const GeneratorPath = "scripts/cp-response-schemas-snapshot"

// SchemaVersion is the snapshot format this package reads and writes.
const SchemaVersion = 1

// PythonEnv names the Python that can import the control plane's app. Unset, it
// is `python` on PATH.
const PythonEnv = "CONTROL_PLANE_PYTHON"

// The anti-no-op floors. The app answered 83 2xx operations and declared 79
// component schemas when this guard was written (2026-11-20); a control plane
// loses routes only by a deliberate break. Fewer means the extraction rotted (a
// router no longer included, an import that half-failed), not that the API
// shrank.
const (
	minOperations = 50
	minSchemas    = 40
)

//go:embed dump.py
var dumpScript string

// Extraction is one reading of the app: every 2xx answer and the component
// schemas they reference. Both are kept as decoded JSON, so the drift check is
// a plain deep comparison and the snapshot needs no schema of its own.
type Extraction struct {
	Operations map[string]interface{} `json:"operations"`
	Schemas    map[string]interface{} `json:"schemas"`
}

// Source records which control plane a snapshot describes.
type Source struct {
	Commit string `json:"commit"`
	Branch string `json:"branch"`
}

// Snapshot is the on-disk form.
type Snapshot struct {
	Comment       string                 `json:"$comment"`
	Generator     string                 `json:"generator"`
	SchemaVersion int                    `json:"schemaVersion"`
	Source        *Source                `json:"source"`
	Operations    map[string]interface{} `json:"operations"`
	Schemas       map[string]interface{} `json:"schemas"`
}

// Python is the interpreter the extraction runs.
func Python() string {
	if v := os.Getenv(PythonEnv); v != "" {
		return v
	}
	return "python"
}

// Extract imports the app of the checkout at cpRoot and reads its schemas.
//
// An error here is "this machine cannot import that app" (no interpreter, no
// requirements) or a dump that printed something other than the document; the
// caller decides whether that is a skip or a failure.
func Extract(cpRoot string) (*Extraction, error) {
	cmd := exec.Command(Python(), "-c", dumpScript)
	cmd.Dir = cpRoot
	cmd.Env = append(os.Environ(), "ENVIRONMENT=test", "PYTHONUTF8=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 2000 {
			tail = "..." + tail[len(tail)-2000:]
		}
		return nil, fmt.Errorf("%s could not import the control plane's app in %s (%v). Set %s to "+
			"a Python with the control plane's requirements installed.\n%s", Python(), cpRoot, err, PythonEnv, tail)
	}
	var ext Extraction
	if err := json.Unmarshal(stdout.Bytes(), &ext); err != nil {
		return nil, fmt.Errorf("the dump printed something that is not the schema document: %v", err)
	}
	return &ext, nil
}

// DescribeSource names the commit and branch of a checkout.
func DescribeSource(cpRoot string) *Source {
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", cpRoot}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return &Source{Commit: git("rev-parse", "HEAD"), Branch: git("rev-parse", "--abbrev-ref", "HEAD")}
}

// SourceMissing reports whether a checkout that exists lacks the app's module.
// The caller must treat that as a FAILURE, never as absence.
func SourceMissing(cpRoot string) bool {
	_, err := os.Stat(filepath.Join(cpRoot, "main.py"))
	return err != nil
}

// Validate refuses an extraction that could read as agreement with everything:
// too few operations or schemas, or a reference to a schema it does not hold.
func Validate(e *Extraction) error {
	if e == nil {
		return fmt.Errorf("no extraction at all")
	}
	if len(e.Operations) < minOperations {
		return fmt.Errorf("%d operation(s), floor is %d: the extraction lost routes, refusing to treat that as the API",
			len(e.Operations), minOperations)
	}
	if len(e.Schemas) < minSchemas {
		return fmt.Errorf("%d component schema(s), floor is %d: the extraction lost models", len(e.Schemas), minSchemas)
	}
	var dangling []string
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch x := v.(type) {
		case map[string]interface{}:
			if ref, ok := x["$ref"].(string); ok {
				if _, found := e.Schemas[RefName(ref)]; !found {
					dangling = append(dangling, ref)
				}
			}
			for _, sub := range x {
				walk(sub)
			}
		case []interface{}:
			for _, sub := range x {
				walk(sub)
			}
		}
	}
	walk(e.Operations)
	walk(e.Schemas)
	if len(dangling) > 0 {
		sort.Strings(dangling)
		return fmt.Errorf("reference(s) to schemas the extraction does not hold: %v", dangling)
	}
	return nil
}

// RefName is the component name a `#/components/schemas/<name>` reference names.
func RefName(ref string) string {
	return strings.TrimPrefix(ref, "#/components/schemas/")
}

// Same reports whether two extractions describe the same answers.
func Same(a, b *Extraction) bool {
	return reflect.DeepEqual(a.Operations, b.Operations) && reflect.DeepEqual(a.Schemas, b.Schemas)
}

// Diff names, briefly, what differs between a committed and a live extraction.
func Diff(committed, live *Extraction) []string {
	var out []string
	side := func(label string, a, b map[string]interface{}) {
		for k, v := range b {
			if old, ok := a[k]; !ok {
				out = append(out, fmt.Sprintf("%s %q: new", label, k))
			} else if !reflect.DeepEqual(old, v) {
				out = append(out, fmt.Sprintf("%s %q: changed", label, k))
			}
		}
		for k := range a {
			if _, ok := b[k]; !ok {
				out = append(out, fmt.Sprintf("%s %q: gone", label, k))
			}
		}
	}
	side("operation", committed.Operations, live.Operations)
	side("schema", committed.Schemas, live.Schemas)
	sort.Strings(out)
	return out
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

// Extraction view of a loaded snapshot.
func (s *Snapshot) Extraction() *Extraction {
	return &Extraction{Operations: s.Operations, Schemas: s.Schemas}
}

// Marshal renders an extraction as the committed snapshot's bytes.
func Marshal(e *Extraction, src *Source) ([]byte, error) {
	snap := Snapshot{
		Comment: "The control plane's response schemas: every 2xx answer of its app and the component " +
			"schemas they reference, read from app.openapi() of the sibling aetherfy-control-plane repo. " +
			"Do not edit by hand. Consumed by test/cp_response_schemas_test.go, which holds every " +
			"struct internal/api decodes an answer into to its route's schema. Regenerate with `go run ./" +
			GeneratorPath + "`.",
		Generator:     GeneratorPath,
		SchemaVersion: SchemaVersion,
		Source:        src,
		Operations:    e.Operations,
		Schemas:       e.Schemas,
	}
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// Unshareable is cperrors' rule, re-exported so the generator and the drift
// check read as one decision.
func Unshareable(cpRoot string) string {
	return cperrors.Unshareable(cperrors.DescribeCheckout(cpRoot))
}
