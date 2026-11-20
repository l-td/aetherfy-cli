package cpschemas

// The checker's own rules, each with the case that must pass and the case that
// must be reported, on synthetic schemas, so a rule that stops firing is red
// here before it is silently green on the real snapshot.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func schemas(t *testing.T, doc string) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return out
}

const fixture = `{
  "Thing": {"type": "object", "required": ["id", "count", "made_at", "link"],
    "properties": {
      "id": {"type": "string"},
      "count": {"type": "integer"},
      "made_at": {"type": "string", "format": "date-time"},
      "note": {"anyOf": [{"type": "string"}, {"type": "null"}]},
      "link": {"$ref": "#/components/schemas/Link"},
      "tags": {"type": "array", "items": {"type": "string"}},
      "extra": {"type": "object", "additionalProperties": {"type": "integer"}}
    }},
  "Link": {"type": "object", "required": ["linked"],
    "properties": {"linked": {"type": "boolean"}, "since": {"anyOf": [{"type": "string", "format": "date-time"}, {"type": "null"}]}}},
  "A": {"type": "object", "required": ["kind"], "properties": {"kind": {"type": "string"}, "a": {"type": "integer"}}},
  "B": {"type": "object", "required": ["kind"], "properties": {"kind": {"type": "string"}, "b": {"type": "integer"}}}
}`

type link struct {
	Linked bool       `json:"linked"`
	Since  *time.Time `json:"since"`
}

type thing struct {
	ID     string         `json:"id"`
	Count  int            `json:"count"`
	MadeAt time.Time      `json:"made_at"`
	Note   *string        `json:"note"`
	Link   link           `json:"link"`
	Tags   []string       `json:"tags,omitempty"`
	Extra  map[string]int `json:"extra"`
	Local  string         `json:"-"`
}

func check(t *testing.T, v interface{}, waivers ...Waiver) ([]Problem, *Checker) {
	t.Helper()
	c := &Checker{Schemas: schemas(t, fixture), Waivers: waivers}
	ref := map[string]interface{}{"$ref": "#/components/schemas/Thing"}
	return c.Check(reflect.TypeOf(v), ref, "", "call"), c
}

func kinds(ps []Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Kind+":"+p.Field)
	}
	return strings.Join(out, " ")
}

// The positive control: a struct that decodes exactly the schema, through a
// $ref, a nullable anyOf, a slice, a map, and with `json:"-"` left out.
func TestAnExactStructHasNoProblem(t *testing.T) {
	if ps, _ := check(t, thing{}); len(ps) != 0 {
		t.Fatalf("an exact struct was reported: %v", ps)
	}
}

func TestATagTheSchemaLacksIsNotSent(t *testing.T) {
	type renamed struct {
		thing
		Gone string `json:"gone"`
	}
	ps, _ := check(t, renamed{})
	if kinds(ps) != KindNotSent+":gone" {
		t.Fatalf("want not-sent:gone, got %q", kinds(ps))
	}
}

func TestAPropertyTheStructLacksIsNotDecoded(t *testing.T) {
	type partial struct {
		ID     string         `json:"id"`
		Count  int            `json:"count"`
		MadeAt time.Time      `json:"made_at"`
		Link   link           `json:"link"`
		Tags   []string       `json:"tags"`
		Extra  map[string]int `json:"extra"`
	}
	ps, _ := check(t, partial{})
	if kinds(ps) != KindNotDecoded+":note" {
		t.Fatalf("want not-decoded:note, got %q", kinds(ps))
	}
}

func TestAWaiverExcusesItsFieldsAndAnUnusedOneIsOutdated(t *testing.T) {
	type partial struct {
		ID     string         `json:"id"`
		Count  int            `json:"count"`
		MadeAt time.Time      `json:"made_at"`
		Link   link           `json:"link"`
		Tags   []string       `json:"tags"`
		Extra  map[string]int `json:"extra"`
	}
	w := Waiver{Kind: KindNotDecoded, Type: "partial", Schema: "Thing", Fields: []string{"note", "count"}, Reason: "test"}
	ps, c := check(t, partial{}, w)
	if len(ps) != 0 {
		t.Fatalf("the waived field was still reported: %v", ps)
	}
	unused := c.Unused()
	if len(unused) != 1 || !strings.Contains(unused[0], "partial.count") {
		t.Fatalf("a waived field the struct decodes must be outdated, got %v", unused)
	}
}

func TestATimeNeedsADateTimeString(t *testing.T) {
	type wrong struct {
		thing
		Count time.Time `json:"count"`
	}
	ps, _ := check(t, wrong{})
	if kinds(ps) != KindType+":count" {
		t.Fatalf("want type:count, got %q", kinds(ps))
	}
}

func TestAPointerOnAnAlwaysSentFieldIsReported(t *testing.T) {
	type pointed struct {
		thing
		ID *string `json:"id"`
	}
	ps, _ := check(t, pointed{})
	if kinds(ps) != KindPointer+":id" {
		t.Fatalf("want pointer:id, got %q", kinds(ps))
	}
}

func TestAPointerOnAnOptionalFieldIsNot(t *testing.T) {
	type optional struct {
		thing
		Tags *[]string `json:"tags"`
	}
	if ps, _ := check(t, optional{}); len(ps) != 0 {
		t.Fatalf("tags is optional, so a pointer is right: %v", ps)
	}
}

func TestAWrongKindIsReportedInsideANestedModel(t *testing.T) {
	type badLink struct {
		Linked string     `json:"linked"`
		Since  *time.Time `json:"since"`
	}
	type nested struct {
		thing
		Link badLink `json:"link"`
	}
	ps, _ := check(t, nested{})
	if len(ps) != 1 || ps[0].Kind != KindType || ps[0].Type != "badLink" || ps[0].Field != "linked" {
		t.Fatalf("want type badLink.linked, got %+v", ps)
	}
}

func TestAMapIsHeldToItsValues(t *testing.T) {
	type badMap struct {
		thing
		Extra map[string]string `json:"extra"`
	}
	ps, _ := check(t, badMap{})
	if kinds(ps) != KindType+":extra" {
		t.Fatalf("want type:extra, got %q", kinds(ps))
	}
}

func TestAUnionNeedsItsMemberNamed(t *testing.T) {
	c := &Checker{Schemas: schemas(t, fixture)}
	union := map[string]interface{}{"anyOf": []interface{}{
		map[string]interface{}{"$ref": "#/components/schemas/A"},
		map[string]interface{}{"$ref": "#/components/schemas/B"},
	}}
	type a struct {
		Kind string `json:"kind"`
		A    int    `json:"a"`
	}
	if ps := c.Check(reflect.TypeOf(a{}), union, "", "call"); len(ps) != 1 || ps[0].Kind != KindType {
		t.Fatalf("a union with no member named must be reported, got %v", ps)
	}
	if ps := c.CheckMember(reflect.TypeOf(a{}), union, "A", "call"); len(ps) != 0 {
		t.Fatalf("member A fits, got %v", ps)
	}
	if ps := c.CheckMember(reflect.TypeOf(a{}), union, "B", "call"); kinds(ps) != KindNotSent+":a "+KindNotDecoded+":b" {
		t.Fatalf("member B does not fit, got %q", kinds(ps))
	}
	if ps := c.CheckMember(reflect.TypeOf(a{}), union, "C", "call"); len(ps) != 1 {
		t.Fatalf("a member the union lacks must be reported, got %v", ps)
	}
}

func TestValidateRefusesAThinOrDanglingExtraction(t *testing.T) {
	ops := map[string]interface{}{}
	for i := 0; i < minOperations; i++ {
		ops[strings.Repeat("x", i+1)] = map[string]interface{}{"$ref": "#/components/schemas/Thing"}
	}
	full := map[string]interface{}{}
	for i := 0; i < minSchemas; i++ {
		full[strings.Repeat("S", i+1)] = map[string]interface{}{}
	}
	full["Thing"] = map[string]interface{}{}
	if err := Validate(&Extraction{Operations: ops, Schemas: full}); err != nil {
		t.Fatalf("a full extraction was refused: %v", err)
	}
	if err := Validate(&Extraction{Operations: map[string]interface{}{"a": nil}, Schemas: full}); err == nil {
		t.Fatal("too few operations must be refused")
	}
	delete(full, "Thing")
	if err := Validate(&Extraction{Operations: ops, Schemas: full}); err == nil || !strings.Contains(err.Error(), "Thing") {
		t.Fatalf("a dangling reference must be refused, got %v", err)
	}
}
