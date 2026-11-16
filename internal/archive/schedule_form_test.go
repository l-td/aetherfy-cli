package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `schedule:` is a block. The string form it had before is refused before any
// upload, with a sentence that shows the block to write.

func scheduleFormRefusal(t *testing.T, yamlText string) *ScheduleFormError {
	t.Helper()
	err := CheckConfigKeys([]byte(yamlText))
	require.Error(t, err, "expected a refusal for:\n%s", yamlText)
	var sf *ScheduleFormError
	require.True(t, errors.As(err, &sf), "not a ScheduleFormError: %T %v", err, err)
	return sf
}

func TestAStringScheduleIsRefusedWithTheBlockToWriteInsteadUsingItsOwnExpression(t *testing.T) {
	const want = "Config validation failed: schedule: is a block: write\nschedule:\n  cron: \"%s\"\n" +
		"  deadline_minutes: 60   # optional: how late an occurrence may still start"
	for name, c := range map[string]struct{ yaml, cron string }{
		"a quoted string":   {`schedule: "0 9 * * 1-5"`, `0 9 * * 1-5`},
		"a single-quoted":   {`schedule: '0 3 * * *'`, `0 3 * * *`},
		"a plain scalar":    {`schedule: 0 9 * * *`, `0 9 * * *`},
		"a number":          {`schedule: 5`, `0 9 * * *`},
		"a list":            {"schedule:\n  - '0 3 * * *'", `0 9 * * *`},
		"a blank string":    {`schedule: "  "`, `0 9 * * *`},
		"after other keys":  {"memory_mb: 256\nschedule: '15 * * * *'", `15 * * * *`},
		"an aliased string": {"entrypoint: &s '0 4 * * *'\nschedule: *s", `0 4 * * *`},
	} {
		t.Run(name, func(t *testing.T) {
			sf := scheduleFormRefusal(t, base+c.yaml+"\n")
			assert.Equal(t, fmt.Sprintf(want, c.cron), sf.Error())
		})
	}
}

// THE CONTROL: what is a block, or no schedule, is not refused.
func TestABlockANullAndNoScheduleAreNotRefused(t *testing.T) {
	for name, text := range map[string]string{
		"a block":                               "schedule:\n  cron: '0 3 * * *'\n",
		"a block with a deadline":               "schedule:\n  cron: '0 3 * * *'\n  deadline_minutes: 30\n",
		"a block as a flow map":                 "schedule: {cron: '0 3 * * *', deadline_minutes: 30}\n",
		"an aliased block":                      "entrypoint: &s {cron: '0 3 * * *'}\nschedule: *s\n",
		"an explicit null":                      "schedule: null\n",
		"nothing after the colon":               "schedule:\n",
		"no schedule at all":                    "memory_mb: 256\n",
		"not a mapping (the decode reports it)": "- a\n- b\n",
	} {
		assert.NoError(t, CheckConfigKeys([]byte(base+text)), name)
	}
	assert.NoError(t, CheckScheduleForm([]byte("- not\n- a mapping\n")))
	assert.NoError(t, CheckScheduleForm([]byte("")))
	assert.NoError(t, CheckScheduleForm([]byte("a: [unclosed")))
}

// A key the server does not declare is reported first, as the control plane
// reports names before values.
func TestAnUnknownFieldIsReportedBeforeTheScheduleForm(t *testing.T) {
	err := CheckConfigKeys([]byte(base + "schedul: x\nschedule: '0 3 * * *'\n"))
	var uf *UnknownFieldsError
	assert.True(t, errors.As(err, &uf), "%T %v", err, err)
}

// What `afy deploy` builds the upload with must not produce bytes for the file,
// and the refusal must be the block sentence, not the typed decode's error about
// Go types.
func TestParseAetherfyConfigRefusesAStringScheduleBeforeTheDecodeAndAnyUpload(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte(base+"schedule: '0 9 * * *'\n"), 0o644))

	_, err := ParseAetherfyConfig(dir)
	var sf *ScheduleFormError
	require.True(t, errors.As(err, &sf), "want the schedule form's own refusal, not a decode error about types: %T %v", err, err)

	data, err := CreateTarballWithValidation(dir)
	assert.Nil(t, data, "a tarball was built for a refused file")
	assert.True(t, errors.As(err, &sf), "%v", err)
}

// A block decodes into the typed config: the expression, and the deadline as the
// file wrote it.
func TestParseAetherfyConfigReadsTheScheduleBlock(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "aetherfy.yaml"),
		[]byte(base+"schedule:\n  cron: '0 9 * * 1-5'\n  deadline_minutes: 60\n"), 0o644))
	cfg, err := ParseAetherfyConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.Schedule)
	assert.Equal(t, "0 9 * * 1-5", cfg.Schedule.Cron)
	assert.Equal(t, 60, cfg.Schedule.DeadlineMinutes)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte(base+"schedule:\n  cron: '0 9 * * 1-5'\n"), 0o644))
	cfg, err = ParseAetherfyConfig(dir)
	require.NoError(t, err)
	assert.Nil(t, cfg.Schedule.DeadlineMinutes, "a deadline left out stays unset")

	// A file that is not YAML at all keeps the decode's own error.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte("runtime: [unclosed\n"), 0o644))
	_, err = ParseAetherfyConfig(dir)
	require.Error(t, err)
	var again *ScheduleFormError
	assert.False(t, errors.As(err, &again), "%v", err)
}
