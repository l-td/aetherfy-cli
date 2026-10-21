package cmd

import "fmt"

// The words of the access levels, written once in the design doc (aetherfy-
// control-plane docs/AGENT_ACCESS_LEVELS.md, "The canonical sentences") and
// used word for word by the dashboard's confirmations, the docs and this CLI.
// Messages say "read access", never the raw read_write; the JSON keeps the raw
// values (they are the contract).

// levelWords is a level as people say it.
var levelWords = map[string]string{
	"read":       "read access",
	"read_write": "read and write access",
	"full":       "full access",
}

// levelSentences is what each level can do.
var levelSentences = map[string]string{
	"read":       "Read access: can search, query, retrieve and count points, and read a collection's information and schema.",
	"read_write": "Read and write access: everything read access can do, plus add, change and delete points, and create collections.",
	"full":       "Full access: everything read and write access can do, plus delete collections and change their structure (schema, indexes, aliases), which changes them for everyone who uses them.",
}

// wordsOf is the level as people say it (an unknown level is shown as it is).
func wordsOf(level string) string {
	if w, ok := levelWords[level]; ok {
		return w
	}
	return level
}

// raisedSentence is what raising an agent's access to level in workspace means.
func raisedSentence(workspace, level string) string {
	return fmt.Sprintf("This lets the agent's code do more in %s. %s", workspace, levelSentences[level])
}

// narrowedNote is the note under a lowered level or a revoked grant.
const narrowedNote = "Takes effect within " + accessChangeWindow + "; requests above the new level are refused."

// unprotectSentence is what turning a collection's protection off means.
func unprotectSentence(collection string) string {
	return fmt.Sprintf("Unprotecting %s lets agents with read and write access delete its points, and agents with full access delete the collection.", collection)
}

// usedByAgent is one line of the warning before a collection is deleted.
func usedByAgent(name string, days int) string {
	return fmt.Sprintf("Used by agent '%s' in the last %d days.", name, days)
}
