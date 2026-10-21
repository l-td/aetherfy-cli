package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The canonical sentences of the access levels are written once, in the control
// plane's design doc (docs/AGENT_ACCESS_LEVELS.md, "The canonical sentences"), and
// the CLI says them word for word (cmd/access_words.go), as the dashboard and the
// docs do. A sibling that is not checked out is a SKIP, said aloud: a skip is not
// a pass.

func designDoc(t *testing.T) string {
	t.Helper()
	for _, rel := range []string{
		filepath.Join("..", "..", "aetherfy-control-plane", "docs", "AGENT_ACCESS_LEVELS.md"),
		filepath.Join("..", "..", "..", "aetherfy-control-plane", "docs", "AGENT_ACCESS_LEVELS.md"),
	} {
		if data, err := os.ReadFile(rel); err == nil {
			return strings.Join(strings.Fields(strings.ReplaceAll(string(data), "**", "")), " ")
		}
	}
	t.Skip("SKIPPED: the design doc (aetherfy-control-plane is not checked out beside this repo); " +
		"a skip is not a pass, so the canonical sentences were not compared")
	return ""
}

func TestTheCLISaysTheCanonicalSentencesWordForWord(t *testing.T) {
	doc := designDoc(t)
	for level, sentence := range levelSentences {
		if !strings.Contains(doc, sentence) {
			t.Errorf("%s: %q is not in the design doc", level, sentence)
		}
	}
	if !strings.Contains(doc, narrowedNote) {
		t.Errorf("the lowered note %q is not in the design doc", narrowedNote)
	}
	if !strings.Contains(doc, unprotectSentence("<collection>")) {
		t.Errorf("the unprotect sentence is not the design doc's")
	}
	if !strings.Contains(doc, strings.Replace(raisedSentence("<workspace>", "read"), " Read access: can search, query, retrieve and count points, and read a collection's information and schema.", "", 1)) {
		t.Errorf("the raise sentence is not the design doc's")
	}
}

func TestLevelsAreSaidInPlainWordsAndTheRawValueIsKeptForTheMachine(t *testing.T) {
	for level, words := range map[string]string{"read": "read access", "read_write": "read and write access", "full": "full access"} {
		if got := wordsOf(level); got != words {
			t.Errorf("%s: %q, want %q", level, got, words)
		}
		if strings.Contains(levelSentences[level], "_") {
			t.Errorf("%s: the sentence carries a raw value: %q", level, levelSentences[level])
		}
	}
	if wordsOf("unknown") != "unknown" {
		t.Error("an unknown level is shown as it is, never hidden")
	}
}
