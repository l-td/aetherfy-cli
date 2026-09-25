package archive

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// An aetherfy.yaml key the server's model does not declare is refused by the
// server with this sentence, and the CLI says the same thing before it uploads.
// The control plane owns the wording: these are its UNKNOWN_FIELD_MESSAGE,
// UNKNOWN_FIELD_SUGGESTION and UNKNOWN_FIELD_SUGGESTION_CUTOFF
// (shared/config_parser.py), with `{path}`/`{suggestion}` spelt `%s`, and
// TestUnknownFieldWordingEqualsTheControlPlanes compares them wherever that
// checkout exists.
const (
	UnknownFieldMessage          = "unknown field `%s`"
	UnknownFieldSuggestion       = ", did you mean `%s`?"
	UnknownFieldSuggestionCutoff = 0.6
)

// FieldSet is the shape of the accepted keys: each name maps to the keys its
// value may hold when that value is itself a block (spawn), or to nil.
type FieldSet map[string]FieldSet

// KnownFields is the CLI's field set, read from AetherfyConfig's yaml tags.
// There is no second list: a field added to the struct is accepted, suggested
// and paired with the control plane from that one declaration.
func KnownFields() FieldSet {
	return fieldsOf(reflect.TypeOf(AetherfyConfig{}))
}

func fieldsOf(t reflect.Type) FieldSet {
	out := FieldSet{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			// An untagged field would be matched by yaml.v3 under its
			// lowercased Go name — a field name nobody chose. Refuse to build
			// a set from it rather than guess.
			panic(fmt.Sprintf("archive.%s.%s has no yaml tag; every config field must name its key", t.Name(), f.Name))
		}
		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch {
		case ft.Kind() == reflect.Struct:
			out[tag] = fieldsOf(ft)
		case (ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map) && elemIsStruct(ft):
			// A list or map of blocks puts an index or a key in the path. No
			// field has that shape today; teach CheckUnknownFields the path
			// before adding one, rather than check it as a leaf.
			panic(fmt.Sprintf("archive.%s.%s holds blocks in a %s; the unknown-field walk does not descend into those", t.Name(), f.Name, ft.Kind()))
		default:
			out[tag] = nil
		}
	}
	return out
}

func elemIsStruct(t reflect.Type) bool {
	e := t.Elem()
	if e.Kind() == reflect.Ptr {
		e = e.Elem()
	}
	return e.Kind() == reflect.Struct
}

// Paths flattens the set to sorted dotted paths: "spawn", "spawn.enabled", ...
func (fs FieldSet) Paths() []string {
	var out []string
	var walk func(prefix string, set FieldSet)
	walk = func(prefix string, set FieldSet) {
		for name, sub := range set {
			p := prefix + name
			out = append(out, p)
			if sub != nil {
				walk(p+".", sub)
			}
		}
	}
	walk("", fs)
	sort.Strings(out)
	return out
}

// UnknownFieldsError is a file that names keys the server would refuse. Its
// text is one clause per key, joined as the server joins them.
type UnknownFieldsError struct {
	Clauses []string
}

func (e *UnknownFieldsError) Error() string {
	return "aetherfy.yaml: " + strings.Join(e.Clauses, "; ")
}

// CheckUnknownFields refuses aetherfy.yaml bytes that name a key outside
// KnownFields, at any depth, with the server's wording and suggestion. Only
// NAMES are checked: values, and fields left out, are the server's business
// (a field absent from the file keeps the value set elsewhere).
//
// Bytes that are not a YAML mapping pass through: the decode reports those.
func CheckUnknownFields(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	var clauses []string
	walkUnknown(doc.Content[0], KnownFields(), nil, &clauses)
	if len(clauses) > 0 {
		return &UnknownFieldsError{Clauses: clauses}
	}
	return nil
}

func walkUnknown(n *yaml.Node, known FieldSet, path []string, clauses *[]string) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if key.Tag == "!!merge" {
			// `<<: *anchor` splices another mapping in; its keys are checked
			// where the server sees them, after the merge. Rare enough in a
			// config file that the server remains the only check for them.
			continue
		}
		name := key.Value
		sub, ok := known[name]
		if !ok {
			*clauses = append(*clauses, unknownFieldClause(path, name, known))
			continue
		}
		if sub != nil {
			walkUnknown(val, sub, append(append([]string{}, path...), name), clauses)
		}
	}
}

func unknownFieldClause(parent []string, name string, siblings FieldSet) string {
	full := strings.Join(append(append([]string{}, parent...), name), ".")
	clause := fmt.Sprintf(UnknownFieldMessage, full)
	candidates := make([]string, 0, len(siblings))
	for s := range siblings {
		candidates = append(candidates, s)
	}
	if close, ok := CloseMatch(name, candidates, UnknownFieldSuggestionCutoff); ok {
		clause += fmt.Sprintf(UnknownFieldSuggestion, strings.Join(append(append([]string{}, parent...), close), "."))
	}
	return clause
}

// CloseMatch is Python's difflib.get_close_matches(word, possibilities, n=1,
// cutoff) — the call the control plane makes — so both sides suggest the same
// field. Ported rather than approximated: a different similarity measure would
// agree on the obvious typos and part ways on the rest.
//
// What the port relies on:
//   - get_close_matches filters on real_quick_ratio, quick_ratio and ratio,
//     the first two being upper bounds of the third, so ratio >= cutoff alone
//     decides membership.
//   - It keeps the largest (score, candidate) tuple, so a tie in score goes to
//     the larger string.
//   - SequenceMatcher(None, candidate, word): no junk function, and autojunk
//     only engages when `word` has 200 or more characters. Such a key gets no
//     suggestion here, where the server might offer one.
//   - Strings are compared as code points (runes), as Python compares str.
func CloseMatch(word string, possibilities []string, cutoff float64) (string, bool) {
	b := []rune(word)
	if len(b) >= 200 {
		return "", false
	}
	best, bestScore, found := "", 0.0, false
	for _, x := range possibilities {
		a := []rune(x)
		score := ratio(a, b)
		if score < cutoff {
			continue
		}
		if !found || score > bestScore || (score == bestScore && x > best) {
			best, bestScore, found = x, score, true
		}
	}
	return best, found
}

// ratio is SequenceMatcher.ratio(): 2*M / (len(a)+len(b)), M the total size of
// the matching blocks.
func ratio(a, b []rune) float64 {
	total := len(a) + len(b)
	if total == 0 {
		return 1.0
	}
	return 2.0 * float64(matchingTotal(a, b)) / float64(total)
}

// matchingTotal is sum(size for _, _, size in get_matching_blocks()): the
// longest match, then the same recursively on either side of it.
func matchingTotal(a, b []rune) int {
	b2j := map[rune][]int{}
	for j, r := range b {
		b2j[r] = append(b2j[r], j)
	}
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, len(a), 0, len(b)}}
	total := 0
	for len(queue) > 0 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := longestMatch(a, b2j, s.alo, s.ahi, s.blo, s.bhi)
		if k == 0 {
			continue
		}
		total += k
		if s.alo < i && s.blo < j {
			queue = append(queue, span{s.alo, i, s.blo, j})
		}
		if i+k < s.ahi && j+k < s.bhi {
			queue = append(queue, span{i + k, s.ahi, j + k, s.bhi})
		}
	}
	return total
}

// longestMatch is find_longest_match without junk: the longest common block of
// a[alo:ahi] and b[blo:bhi], earliest in a, then earliest in b. The junk
// extensions that follow it in Python are no-ops when nothing is junk.
func longestMatch(a []rune, b2j map[rune][]int, alo, ahi, blo, bhi int) (int, int, int) {
	besti, bestj, bestsize := alo, blo, 0
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range b2j[a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}
	return besti, bestj, bestsize
}
