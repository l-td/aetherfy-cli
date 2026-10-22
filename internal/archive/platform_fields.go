package archive

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// PlatformField is a key that LEFT aetherfy.yaml for the platform, and where
// that property is set now.
type PlatformField struct {
	Key   string
	Where string
}

// PlatformFields are the keys the control plane refuses in aetherfy.yaml: the
// agent's name, description and workspace are the platform's, set with
// `afy update` or the dashboard. In the control plane's ORDER, because the
// refusal lists them in that order (shared/config_parser.py PLATFORM_FIELDS).
// Held to the control plane's by TestPlatformFieldsEqualTheControlPlanes, which
// compares the keys, the `where` sentences and their order.
var PlatformFields = []PlatformField{
	{"name", "the agent's name is set on the platform: rename it with `afy update <agent> --name <new>` or in the dashboard"},
	{"description", "the agent's description is set on the platform: `afy update <agent> --description <text>` or the dashboard"},
	{"workspace", "the workspace an agent lives in is set on the platform: move it with `afy update <agent> --workspace <name>` or the dashboard's Move"},
}

// The sentence the control plane refuses such a file with, and the CLI says
// before it uploads anything. The control plane owns the wording: these are its
// PLATFORM_FIELDS_REFUSAL, PLATFORM_FIELD_CLAUSE and PLATFORM_FIELD_JOINER
// (shared/config_parser.py), with `{fields}`, `{field}` and `{where}` spelt
// `%s`; TestPlatformFieldWordingEqualsTheControlPlanes compares them wherever
// that checkout exists.
const (
	PlatformFieldsRefusal = "aetherfy.yaml says how the agent runs, not what it is called or where it lives. Remove %s."
	PlatformFieldClause   = "`%s:` (%s)"
	PlatformFieldJoiner   = "; "
)

// PlatformFieldsError is a file that declares a key the platform owns. Its text
// is the control plane's sentence for it, word for word.
type PlatformFieldsError struct {
	Keys []string
	text string
}

func (e *PlatformFieldsError) Error() string { return e.text }

// CheckPlatformFields refuses aetherfy.yaml bytes that declare `name:`,
// `description:` or `workspace:` at the top level, with the control plane's
// sentence. Like the control plane it looks at the top level only, and it names
// every declared key, in PLATFORM_FIELDS' order.
//
// Bytes that are not a YAML mapping pass through: the decode reports those.
func CheckPlatformFields(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	top := doc.Content[0]
	if top.Kind == yaml.AliasNode {
		top = top.Alias
	}
	if top == nil || top.Kind != yaml.MappingNode {
		return nil
	}
	present := map[string]bool{}
	for i := 0; i+1 < len(top.Content); i += 2 {
		present[top.Content[i].Value] = true
	}
	var keys, clauses []string
	for _, f := range PlatformFields {
		if present[f.Key] {
			keys = append(keys, f.Key)
			clauses = append(clauses, fmt.Sprintf(PlatformFieldClause, f.Key, f.Where))
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return &PlatformFieldsError{
		Keys: keys,
		text: fmt.Sprintf(PlatformFieldsRefusal, strings.Join(clauses, PlatformFieldJoiner)),
	}
}

// CheckConfigKeys is every key check a deploy would fail on, in the control
// plane's order: a platform-owned key first (it refuses those before it
// validates the model), then the names its model does not declare.
func CheckConfigKeys(data []byte) error {
	if err := CheckPlatformFields(data); err != nil {
		return err
	}
	return CheckUnknownFields(data)
}
