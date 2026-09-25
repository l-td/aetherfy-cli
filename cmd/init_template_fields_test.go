package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/archive"
	"gopkg.in/yaml.v3"
)

// Commented-out settings: "# spawn:", "#   enabled: true", "#     - sub-agent-1",
// "# entrypoint: main.py  # TODO...". Prose comments ("# --- ADVANCED
// SETTINGS ---") are left alone -- uncommented they would not be YAML, and
// the check below passes an unparseable file through to the decode.
var commentedSetting = regexp.MustCompile(`(?m)^# ?(\s*(?:[a-z_]+:|- ).*)$`)

// `afy init` writes the file most customers start from, including commented-out
// settings they are invited to uncomment. Every one of those, uncommented, must
// be a field the server accepts: the spawn example carried `workspace:` under
// `spawn:` until unknown fields were refused, and uncommenting it would have
// been the customer's first refusal.
func TestInitTemplateUncommentedIsAccepted(t *testing.T) {
	for _, schedule := range []string{"", "0 3 * * *"} {
		for _, entrypoint := range []string{"", "main.py"} {
			out := buildAetherfyYAML("demo", "python3.11", "service", "us-east-1", 256, true, true, entrypoint, schedule)
			uncommented := commentedSetting.ReplaceAllString(out, "$1")

			// Positive controls: the spawn block was uncommented, and the
			// result is YAML -- otherwise the check has nothing to refuse.
			if !strings.Contains(uncommented, "\nspawn:\n") {
				t.Fatalf("the spawn example was not uncommented:\n%s", uncommented)
			}
			var doc map[string]any
			if err := yaml.Unmarshal([]byte(uncommented), &doc); err != nil {
				t.Fatalf("uncommented template is not YAML: %v\n%s", err, uncommented)
			}

			if err := archive.CheckUnknownFields([]byte(uncommented)); err != nil {
				t.Errorf("afy init's file, uncommented, is refused: %v\n%s", err, uncommented)
			}
		}
	}
}
