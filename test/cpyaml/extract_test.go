package cpyaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeCP(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, filepath.FromSlash(SourcePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	return root
}

// The shapes config_parser.py has: a nested model through Optional[...], a
// one-line and a multi-line docstring (whose prose looks like a field), a
// multi-line Field(...) default, validators, private class attributes, a
// comment block, and the three constants. CRLF, as a Windows checkout has it.
const realShape = "UNKNOWN_FIELD_MESSAGE = \"unknown field `{path}`\"\r\n" +
	"UNKNOWN_FIELD_SUGGESTION = \", did you mean `{suggestion}`?\"\r\n" +
	"UNKNOWN_FIELD_SUGGESTION_CUTOFF = 0.6\r\n" +
	"\r\n" +
	"class SpawnConfig(BaseModel):\r\n" +
	"    \"\"\"Spawn configuration section.\"\"\"\r\n" +
	"    model_config = ConfigDict(extra=\"forbid\")\r\n" +
	"\r\n" +
	"    enabled: bool = Field(default=False, description=\"x: y\")\r\n" +
	"    workers: Optional[List[str]] = Field(default=None)\r\n" +
	"\r\n" +
	"\r\n" +
	"class AetherfyConfig(BaseModel):\r\n" +
	"    \"\"\"\r\n" +
	"    Agent configuration.\r\n" +
	"    note: this line is prose, not a field\r\n" +
	"    \"\"\"\r\n" +
	"    model_config = ConfigDict(extra=\"forbid\")\r\n" +
	"\r\n" +
	"    name: str = Field(..., description=\"Agent name\")\r\n" +
	"    description: Optional[str] = Field(\r\n" +
	"        default=None,\r\n" +
	"        description=(\r\n" +
	"            \"schedule: not a field either\"\r\n" +
	"        ),\r\n" +
	"    )\r\n" +
	"    spawn: Optional[SpawnConfig] = Field(default=None)\r\n" +
	"    memory_mb: Optional[int] = Field(default=256, gt=0)\r\n" +
	"\r\n" +
	"    @field_validator('name')\r\n" +
	"    @classmethod\r\n" +
	"    def validate_name(cls, v: str) -> str:\r\n" +
	"        return v\r\n" +
	"\r\n" +
	"    # a comment: at class indent\r\n" +
	"    _NON_NULLABLE_FIELDS = frozenset({'memory_mb'})\r\n" +
	"    _sanitize = field_validator('description')(f)\r\n" +
	"\r\n" +
	"\r\n" +
	"class ConfigParseError(Exception):\r\n" +
	"    code: str = 'not a model field'\r\n"

func TestExtractReadsTheModelsTheirNestingAndTheWording(t *testing.T) {
	src, err := Extract(fakeCP(t, realShape))
	require.NoError(t, err)
	require.NoError(t, Validate(src))
	assert.Equal(t, []string{"description", "memory_mb", "name", "spawn", "spawn.enabled", "spawn.workers"}, Paths(src),
		"docstring and Field(...) prose, validators, private attributes and a non-model class must not be read as fields")
	assert.Empty(t, NotForbidding(src))
	assert.Equal(t, "unknown field `{path}`", src.Message)
	assert.Equal(t, ", did you mean `{suggestion}`?", src.Suggestion)
	assert.Equal(t, 0.6, src.SuggestionCutoff)
}

// The mutation the pair exists for, at the extractor: a field added to the
// model alone shows up in the set the CLI is compared with.
func TestAFieldAddedToTheModelIsInTheSet(t *testing.T) {
	src, err := Extract(fakeCP(t, realShape))
	require.NoError(t, err)
	before := Paths(src)

	mutated := replaceOnce(t, realShape, "    memory_mb: Optional[int] = Field(default=256, gt=0)\r\n",
		"    memory_mb: Optional[int] = Field(default=256, gt=0)\r\n    max_runtime_seconds: Optional[int] = None\r\n")
	src, err = Extract(fakeCP(t, mutated))
	require.NoError(t, err)
	assert.Len(t, Paths(src), len(before)+1)
	assert.Contains(t, Paths(src), "max_runtime_seconds")
}

func TestAModelThatIgnoresExtrasIsReported(t *testing.T) {
	body := realShape
	for _, cfg := range []string{`ConfigDict(extra="ignore")`, `ConfigDict(extra=EXTRA)`, `ConfigDict()`} {
		src, err := Extract(fakeCP(t, replaceOnce(t, body, "class SpawnConfig(BaseModel):\r\n    \"\"\"Spawn configuration section.\"\"\"\r\n    model_config = ConfigDict(extra=\"forbid\")",
			"class SpawnConfig(BaseModel):\r\n    \"\"\"Spawn configuration section.\"\"\"\r\n    model_config = "+cfg)))
		require.NoError(t, err)
		assert.Equal(t, []string{"SpawnConfig"}, NotForbidding(src), cfg)
	}
}

func TestAnAnnotationItCannotResolveIsRefusedNotDropped(t *testing.T) {
	for _, ann := range []string{"Optional[ImportedModel]", "ClassVar[int]", `Literal["a"]`} {
		_, err := Extract(fakeCP(t, replaceOnce(t, realShape, "    memory_mb: Optional[int]", "    memory_mb: "+ann)))
		require.Error(t, err, ann)
		assert.Contains(t, err.Error(), "AetherfyConfig.memory_mb", ann)
	}
}

func TestValidateRefusesWhatCouldAgreeWithAnything(t *testing.T) {
	src, err := Extract(fakeCP(t, "class Other(BaseModel):\n    a: int\n"))
	require.NoError(t, err)
	assert.Error(t, Validate(src), "no root model")

	src, err = Extract(fakeCP(t, replaceOnce(t, realShape, "UNKNOWN_FIELD_SUGGESTION_CUTOFF = 0.6\r\n", "")))
	require.NoError(t, err)
	assert.Error(t, Validate(src), "a missing constant")

	src, err = Extract(fakeCP(t, replaceOnce(t, realShape,
		"    enabled: bool = Field(default=False, description=\"x: y\")\r\n    workers: Optional[List[str]] = Field(default=None)\r\n", "    pass\r\n")))
	require.NoError(t, err)
	assert.Error(t, Validate(src), "a reachable model with no fields")

	_, err = Extract(fakeCP(t, realShape+"UNKNOWN_FIELD_MESSAGE = \"other\"\r\n"))
	assert.Error(t, err, "a constant assigned twice")
}

func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	require.Contains(t, s, old, "fixture does not contain the text to replace")
	return strings.Replace(s, old, new, 1)
}
