package archive

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// `schedule:` is a BLOCK. The string form (`schedule: "0 9 * * *"`) the field
// had before is refused by the control plane at deploy (a push and an upload
// alike), and the CLI refuses it first, before anything is uploaded, with a
// sentence that shows the block. The sentence is the CLI's own, worded as the
// control plane words the same refusal ("Config validation failed: schedule: is a
// block: write ..." with the customer's own expression in it), so a deploy from
// this folder and a push say the same thing; it is NOT compared with a
// control-plane constant, because that sentence is built by a function there and
// this repo reads only string literals.
const (
	// ScheduleFormMessage has one `%s`: the cron expression the file wrote, or an
	// example one when the value gave none to reuse.
	ScheduleFormMessage = "Config validation failed: schedule: is a block: write\nschedule:\n  cron: \"%s\"\n" +
		"  deadline_minutes: 60   # optional: how late an occurrence may still start"
	// scheduleExampleCron is shown when the file's value gives no expression to reuse.
	scheduleExampleCron = "0 9 * * *"
)

// ScheduleFormError is a file whose `schedule:` is a value, not a block.
type ScheduleFormError struct{ text string }

func (e *ScheduleFormError) Error() string { return e.text }

// CheckScheduleForm refuses aetherfy.yaml bytes whose top-level `schedule:` is
// neither a block nor null (explicit `null`, like leaving it out, means no
// schedule). It only looks at the SHAPE of the value: the expression, and the
// deadline, are the server's to validate.
//
// Bytes that are not a YAML mapping pass through: the decode reports those.
func CheckScheduleForm(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	top := resolveAlias(doc.Content[0])
	if top == nil || top.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "schedule" {
			continue
		}
		val := resolveAlias(top.Content[i+1])
		if val == nil || val.Kind == yaml.MappingNode || (val.Kind == yaml.ScalarNode && val.Tag == "!!null") {
			return nil
		}
		expression := scheduleExampleCron
		if val.Kind == yaml.ScalarNode && val.Tag == "!!str" && strings.TrimSpace(val.Value) != "" {
			expression = val.Value
		}
		return &ScheduleFormError{text: fmt.Sprintf(ScheduleFormMessage, expression)}
	}
	return nil
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}
