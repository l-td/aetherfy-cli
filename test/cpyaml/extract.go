// Package cpyaml extracts the control plane's aetherfy.yaml field set -- the
// fields of its AetherfyConfig pydantic model and the models nested in it --
// and the wording it refuses an unknown field with, from the sibling
// aetherfy-control-plane checkout.
//
// WHY IT EXISTS. The server refuses a key its model does not declare, and the
// CLI refuses the same keys before it uploads, from archive.AetherfyConfig's
// yaml tags. Two declarations of one field set drift: a field added to the
// model alone would be refused by the CLI although the server accepts it, and
// every test on both sides would stay green, each checking its own list. So
// the two sets are compared wherever the checkout exists -- a dev box with the
// sibling, and the e2e nightly, which sets cperrors.RequireEnv.
//
// NO COMMITTED SNAPSHOT, like cpreadiness and for the same reason: nothing in
// this repo needs the server's set where the control plane is absent, and a
// live read answers what a snapshot would pin. This repo's CI checks out no
// sibling and so does not run the comparison; the nightly does.
//
// READ FROM SOURCE, not by importing the model: the CLI's tests must not need a
// Python with the control plane's dependencies. The reader is deliberately
// narrow and refuses what it does not understand (see Extract) rather than
// returning a smaller set, because a smaller set would still be a set, and
// comparing against it would only report the CLI as having extra fields.
package cpyaml

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SourcePath is where the models live, CP-repo-relative.
const SourcePath = "shared/config_parser.py"

// RootModel is the model an aetherfy.yaml document is validated against.
const RootModel = "AetherfyConfig"

// Model is one pydantic model: its fields, each mapped to the nested model its
// annotation names ("" for a plain value), and whether it forbids extras.
type Model struct {
	Fields map[string]string
	Forbid bool
}

// Source is what Extract reads: every BaseModel class in SourcePath, and the
// three unknown-field constants.
type Source struct {
	Models           map[string]*Model
	Message          string  // UNKNOWN_FIELD_MESSAGE
	Suggestion       string  // UNKNOWN_FIELD_SUGGESTION
	SuggestionCutoff float64 // UNKNOWN_FIELD_SUGGESTION_CUTOFF
}

var (
	classDecl = regexp.MustCompile(`^class ([A-Za-z_][A-Za-z0-9_]*)\(([A-Za-z_.]*BaseModel)\):`)
	// A class-body field: exactly four spaces, a public name, a colon, an
	// annotation, an optional default. Methods (`    def`), decorators,
	// `model_config = ...` and private names (`_X = ...`) do not match.
	fieldDecl = regexp.MustCompile(`^    ([A-Za-z][A-Za-z0-9_]*)\s*:\s*(.+?)\s*(?:=.*)?$`)
	forbid    = regexp.MustCompile(`^    model_config\s*=\s*ConfigDict\((.*)\)\s*$`)
	ident     = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	strConst  = regexp.MustCompile(`^(UNKNOWN_FIELD_MESSAGE|UNKNOWN_FIELD_SUGGESTION) = "([^"]*)"\s*(?:#.*)?$`)
	numConst  = regexp.MustCompile(`^UNKNOWN_FIELD_SUGGESTION_CUTOFF = ([0-9.]+)\s*(?:#.*)?$`)
)

// Names an annotation may use without naming a model.
var plainTypes = map[string]bool{
	"Optional": true, "List": true, "Dict": true, "Any": true, "Union": true,
	"str": true, "int": true, "bool": true, "float": true, "None": true,
	"list": true, "dict": true, "typing": true,
}

// Extract reads SourcePath under cpRoot.
//
// It REFUSES, rather than skips: an annotation naming something that is
// neither a plain type nor a model in this file (a model imported from
// elsewhere, a ClassVar, a Literal of names), a model_config it cannot read,
// or a constant assigned twice. Each would otherwise shrink or distort the set.
func Extract(cpRoot string) (*Source, error) {
	raw, err := os.ReadFile(filepath.Join(cpRoot, filepath.FromSlash(SourcePath)))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	src := &Source{Models: map[string]*Model{}}
	annotations := map[string]map[string]string{} // model -> field -> annotation
	seenConst := map[string]int{}

	var cur string
	inDoc := false
	for _, l := range lines {
		if m := strConst.FindStringSubmatch(l); m != nil {
			seenConst[m[1]]++
			if m[1] == "UNKNOWN_FIELD_MESSAGE" {
				src.Message = m[2]
			} else {
				src.Suggestion = m[2]
			}
		}
		if m := numConst.FindStringSubmatch(l); m != nil {
			seenConst["UNKNOWN_FIELD_SUGGESTION_CUTOFF"]++
			if src.SuggestionCutoff, err = strconv.ParseFloat(m[1], 64); err != nil {
				return nil, fmt.Errorf("UNKNOWN_FIELD_SUGGESTION_CUTOFF = %s is not a number", m[1])
			}
		}

		if m := classDecl.FindStringSubmatch(l); m != nil {
			cur = m[1]
			src.Models[cur] = &Model{Fields: map[string]string{}}
			annotations[cur] = map[string]string{}
			inDoc = false
			continue
		}
		if cur == "" {
			continue
		}
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "#") {
			cur = "" // column 0: the class body has ended
			continue
		}
		// Docstrings hold prose that can look like `    name: text`.
		if n := strings.Count(l, `"""`); n > 0 {
			if n == 1 {
				inDoc = !inDoc
			}
			continue
		}
		if inDoc {
			continue
		}
		if m := forbid.FindStringSubmatch(l); m != nil {
			// Anything but a literal extra="forbid" -- "ignore", "allow", a
			// name, a ConfigDict split over lines -- reads as NOT forbidding,
			// which reds the pair rather than passing it.
			src.Models[cur].Forbid = strings.Contains(m[1], `extra="forbid"`) ||
				strings.Contains(m[1], `extra='forbid'`)
			continue
		}
		if m := fieldDecl.FindStringSubmatch(l); m != nil {
			if m[1] == "model_config" {
				return nil, fmt.Errorf("%s.model_config is annotated, which this reader does not parse", cur)
			}
			annotations[cur][m[1]] = m[2]
		}
	}

	for name, c := range seenConst {
		if c > 1 {
			return nil, fmt.Errorf("%s is assigned %d times in %s", name, c, SourcePath)
		}
	}

	for model, fields := range annotations {
		for field, ann := range fields {
			nested := ""
			for _, id := range ident.FindAllString(ann, -1) {
				if plainTypes[id] {
					continue
				}
				if _, ok := src.Models[id]; !ok {
					return nil, fmt.Errorf("%s.%s is annotated `%s`, and `%s` is neither a plain type nor a "+
						"model in %s -- teach this reader the shape rather than drop the field", model, field, ann, id, SourcePath)
				}
				if nested != "" && nested != id {
					return nil, fmt.Errorf("%s.%s is annotated `%s`, which names two models", model, field, ann)
				}
				nested = id
			}
			src.Models[model].Fields[field] = nested
		}
	}
	return src, nil
}

// Validate refuses an extraction that could agree with anything.
func Validate(src *Source) error {
	root, ok := src.Models[RootModel]
	if !ok {
		return fmt.Errorf("no `class %s(BaseModel)` in %s -- the model moved or the reader has rotted", RootModel, SourcePath)
	}
	if len(root.Fields) < 2 {
		return fmt.Errorf("found %d field(s) on %s in %s -- refusing to treat that as agreement", len(root.Fields), RootModel, SourcePath)
	}
	for _, name := range reachable(src) {
		if len(src.Models[name].Fields) == 0 {
			return fmt.Errorf("model %s, reachable from %s, has no fields this reader could see", name, RootModel)
		}
	}
	if src.Message == "" || src.Suggestion == "" || src.SuggestionCutoff == 0 {
		return fmt.Errorf("UNKNOWN_FIELD_MESSAGE, UNKNOWN_FIELD_SUGGESTION and UNKNOWN_FIELD_SUGGESTION_CUTOFF must all be "+
			"module-level literals in %s (found %q, %q, %v)", SourcePath, src.Message, src.Suggestion, src.SuggestionCutoff)
	}
	return nil
}

// reachable lists RootModel and every model nested under it, sorted.
func reachable(src *Source) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(m string) {
		if seen[m] {
			return
		}
		seen[m] = true
		for _, nested := range src.Models[m].Fields {
			if nested != "" {
				walk(nested)
			}
		}
	}
	walk(RootModel)
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Paths is the field set as sorted dotted paths, the shape
// archive.FieldSet.Paths produces: "spawn", "spawn.enabled", ...
func Paths(src *Source) []string {
	var out []string
	var walk func(prefix, model string)
	walk = func(prefix, model string) {
		for field, nested := range src.Models[model].Fields {
			out = append(out, prefix+field)
			if nested != "" {
				walk(prefix+field+".", nested)
			}
		}
	}
	walk("", RootModel)
	sort.Strings(out)
	return out
}

// NotForbidding lists the reachable models that do not declare
// extra="forbid", which means the server would accept a key the CLI refuses.
func NotForbidding(src *Source) []string {
	var out []string
	for _, m := range reachable(src) {
		if !src.Models[m].Forbid {
			out = append(out, m)
		}
	}
	return out
}
