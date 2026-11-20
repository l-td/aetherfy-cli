package cpschemas

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// The rules, each a Problem kind. A struct is held to the schema of the answer
// it decodes:
//
//   - every json tag it decodes is a property the schema declares (KindNotSent):
//     a tag the server never sends is a zero value forever;
//   - every property the schema declares is decoded (KindNotDecoded), or waived
//     with the reason the CLI reads only a subset;
//   - a time.Time decodes a string declared `format: date-time` (KindType);
//   - a pointer decodes a property that is nullable or optional (KindPointer):
//     a pointer on a field that is always there is a nil check that never fires;
//   - every other Go kind decodes the JSON type the schema declares (KindType).
//
// A nested struct, slice or map is followed into the schema it decodes, through
// $ref, items, additionalProperties and a nullable anyOf.
const (
	KindNotSent    = "not-sent"
	KindNotDecoded = "not-decoded"
	KindType       = "type"
	KindPointer    = "pointer"
)

// Problem is one rule broken at one place.
type Problem struct {
	Kind   string
	Type   string // the Go type holding the field
	Schema string // the schema it is held to
	Field  string // the json name
	Detail string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s: %s.%s against %s: %s", p.Kind, p.Type, p.Field, p.Schema, p.Detail)
}

// Waiver excuses one kind of difference for the named fields of one Go type
// against one schema, with the reason. A waiver that excuses nothing is itself
// a failure (Unused), so a field that starts being decoded, or stops being
// sent, takes its waiver with it.
type Waiver struct {
	Kind   string
	Type   string
	Schema string
	Fields []string
	Reason string
}

// Checker holds a set of component schemas and the waivers, and records which
// waiver entries it used.
type Checker struct {
	Schemas map[string]interface{}
	Waivers []Waiver
	used    map[string]bool
}

func waiverKey(kind, typ, schema, field string) string {
	return kind + "|" + typ + "|" + schema + "|" + field
}

func (c *Checker) waived(p Problem) bool {
	for _, w := range c.Waivers {
		if w.Kind != p.Kind || w.Type != p.Type || w.Schema != p.Schema {
			continue
		}
		for _, f := range w.Fields {
			if f == p.Field {
				if c.used == nil {
					c.used = map[string]bool{}
				}
				c.used[waiverKey(w.Kind, w.Type, w.Schema, f)] = true
				return true
			}
		}
	}
	return false
}

// Unused lists every waived field that no check needed: an outdated waiver.
func (c *Checker) Unused() []string {
	var out []string
	for _, w := range c.Waivers {
		for _, f := range w.Fields {
			if !c.used[waiverKey(w.Kind, w.Type, w.Schema, f)] {
				out = append(out, fmt.Sprintf("%s %s.%s against %s", w.Kind, w.Type, f, w.Schema))
			}
		}
	}
	sort.Strings(out)
	return out
}

// node is a schema with its references resolved, and what was learned on the
// way: whether null is allowed, and the component name it came from.
type node struct {
	schema   map[string]interface{}
	name     string
	nullable bool
	union    []map[string]interface{} // more than one non-null member
}

func (c *Checker) resolve(raw interface{}, name string) (node, error) {
	n := node{name: name}
	s, ok := raw.(map[string]interface{})
	if !ok {
		return n, fmt.Errorf("not a schema object: %v", raw)
	}
	for depth := 0; depth < 32; depth++ {
		if ref, ok := s["$ref"].(string); ok {
			n.name = RefName(ref)
			next, found := c.Schemas[n.name].(map[string]interface{})
			if !found {
				return n, fmt.Errorf("reference to a schema the snapshot does not hold: %s", ref)
			}
			s = next
			continue
		}
		members, isUnion := s["anyOf"].([]interface{})
		if !isUnion {
			members, isUnion = s["oneOf"].([]interface{})
		}
		if isUnion {
			var rest []map[string]interface{}
			for _, m := range members {
				ms, _ := m.(map[string]interface{})
				if ms["type"] == "null" {
					n.nullable = true
					continue
				}
				rest = append(rest, ms)
			}
			if len(rest) == 1 {
				s = rest[0]
				continue
			}
			n.union = rest
			n.schema = s
			return n, nil
		}
		if t, ok := s["type"].([]interface{}); ok {
			// The 3.1 form `type: [X, "null"]`.
			var kept []interface{}
			for _, x := range t {
				if x == "null" {
					n.nullable = true
				} else {
					kept = append(kept, x)
				}
			}
			copy := map[string]interface{}{}
			for k, v := range s {
				copy[k] = v
			}
			if len(kept) == 1 {
				copy["type"] = kept[0]
			} else {
				copy["type"] = kept
			}
			s = copy
		}
		n.schema = s
		return n, nil
	}
	return n, fmt.Errorf("reference chain too deep at %s", name)
}

var (
	timeType = reflect.TypeOf(time.Time{})
	rawType  = reflect.TypeOf(json.RawMessage{})
)

// jsonFields lists the fields a struct decodes: json name -> field type,
// embedded structs flattened as encoding/json flattens them, `json:"-"` and
// untagged unexported fields left out.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Ptr {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				for k, v := range jsonFields(et) {
					if _, shadowed := out[k]; !shadowed {
						out[k] = v
					}
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

func typeName(t reflect.Type, at string) string {
	if t.Name() != "" {
		return t.Name()
	}
	return at
}

// Check holds Go type t to the schema raw, which came from the component named
// name ("" for an inline schema). at names the place for messages.
func (c *Checker) Check(t reflect.Type, raw interface{}, name, at string) []Problem {
	n, err := c.resolve(raw, name)
	if err != nil {
		return []Problem{{Kind: KindType, Type: at, Schema: name, Detail: err.Error()}}
	}
	return c.checkNode(t, n, at)
}

// CheckMember holds t to one named member of a union answer, for a route whose
// one status answers with more than one model (the CLI's call decodes the one
// it asked for).
func (c *Checker) CheckMember(t reflect.Type, raw interface{}, member, at string) []Problem {
	n, err := c.resolve(raw, "")
	if err != nil {
		return []Problem{{Kind: KindType, Type: at, Detail: err.Error()}}
	}
	if n.name == member && n.union == nil {
		return c.checkNode(t, n, at)
	}
	for _, m := range n.union {
		if ref, _ := m["$ref"].(string); RefName(ref) == member {
			return c.Check(t, m, member, at)
		}
	}
	return []Problem{{Kind: KindType, Type: at, Schema: member,
		Detail: fmt.Sprintf("the answer has no member %s", member)}}
}

func isAny(s map[string]interface{}) bool {
	for k := range s {
		switch k {
		case "type", "properties", "items", "additionalProperties", "anyOf", "oneOf", "allOf", "enum", "const", "format":
			return false
		}
	}
	return true
}

func (c *Checker) checkNode(t reflect.Type, n node, at string) []Problem {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Interface || t == rawType {
		return nil // decodes anything
	}
	if n.union != nil {
		return []Problem{{Kind: KindType, Type: at, Schema: n.name,
			Detail: fmt.Sprintf("the schema is a union of %d models; name the member the CLI decodes", len(n.union))}}
	}
	s := n.schema
	if isAny(s) {
		return nil // the schema allows any JSON; nothing to hold the type to
	}
	jt, _ := s["type"].(string)
	if _, isList := s["type"].([]interface{}); isList {
		return []Problem{{Kind: KindType, Type: at, Schema: n.name, Detail: fmt.Sprintf("several JSON types %v", s["type"])}}
	}
	mismatch := func(want string) []Problem {
		return []Problem{{Kind: KindType, Type: at, Schema: n.name,
			Detail: fmt.Sprintf("Go %s decodes JSON %s, the schema declares %q", t, want, jt)}}
	}
	switch {
	case t == timeType:
		if jt != "string" || s["format"] != "date-time" {
			return []Problem{{Kind: KindType, Type: at, Schema: n.name,
				Detail: fmt.Sprintf("time.Time needs a string with format date-time, the schema declares %q format %v", jt, s["format"])}}
		}
		return nil
	case t.Kind() == reflect.String:
		if jt != "string" {
			return mismatch("string")
		}
		return nil
	case t.Kind() == reflect.Bool:
		if jt != "boolean" {
			return mismatch("boolean")
		}
		return nil
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Uint64:
		if jt != "integer" {
			return mismatch("integer")
		}
		return nil
	case t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64:
		if jt != "number" && jt != "integer" {
			return mismatch("number")
		}
		return nil
	case t.Kind() == reflect.Slice:
		if jt != "array" {
			return mismatch("array")
		}
		items, ok := s["items"]
		if !ok {
			return nil
		}
		return c.Check(t.Elem(), items, "", at+"[]")
	case t.Kind() == reflect.Map:
		if jt != "object" {
			return mismatch("object")
		}
		if ap, ok := s["additionalProperties"].(map[string]interface{}); ok {
			return c.Check(t.Elem(), ap, "", at+"{}")
		}
		return nil
	case t.Kind() == reflect.Struct:
		if jt != "object" && jt != "" {
			return mismatch("object")
		}
		return c.checkStruct(t, n, at)
	}
	return []Problem{{Kind: KindType, Type: at, Schema: n.name, Detail: fmt.Sprintf("Go %s is not a decodable kind", t)}}
}

func (c *Checker) checkStruct(t reflect.Type, n node, at string) []Problem {
	owner := typeName(t, at)
	schema := n.name
	if schema == "" {
		schema = at
	}
	props, _ := n.schema["properties"].(map[string]interface{})
	required := map[string]bool{}
	if req, ok := n.schema["required"].([]interface{}); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	var out []Problem
	add := func(p Problem) {
		if !c.waived(p) {
			out = append(out, p)
		}
	}
	fields := jsonFields(t)
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ft := fields[name]
		raw, sent := props[name]
		if !sent {
			add(Problem{Kind: KindNotSent, Type: owner, Schema: schema, Field: name,
				Detail: "the struct decodes a field the schema does not declare, so it is a zero value forever"})
			continue
		}
		child, err := c.resolve(raw, "")
		if err != nil {
			out = append(out, Problem{Kind: KindType, Type: owner, Schema: schema, Field: name, Detail: err.Error()})
			continue
		}
		if ft.Kind() == reflect.Ptr && required[name] && !child.nullable {
			add(Problem{Kind: KindPointer, Type: owner, Schema: schema, Field: name,
				Detail: "a pointer on a field that is always sent and never null"})
		}
		for _, p := range c.checkNode(ft, child, owner+"."+name) {
			if p.Field == "" {
				p.Type, p.Schema, p.Field = owner, schema, name
			}
			add(p)
		}
	}
	var undecoded []string
	for name := range props {
		if _, ok := fields[name]; !ok {
			undecoded = append(undecoded, name)
		}
	}
	sort.Strings(undecoded)
	for _, name := range undecoded {
		add(Problem{Kind: KindNotDecoded, Type: owner, Schema: schema, Field: name,
			Detail: "the schema declares a field the struct does not decode"})
	}
	return out
}
