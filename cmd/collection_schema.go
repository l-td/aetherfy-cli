package cmd

// afy collections schema get/set/delete/analyze: a collection's payload schema,
// on the vectors API's …/collections/{name}/schema (contract sheet 1.5), as the
// dashboard's per-collection schema page manages it.
//
// The version travels with the file. `get --save` writes the schema with the
// etag it was read at; `set --file` sends that etag as If-Match, so an edit made
// against a version someone has since changed is refused (412) instead of
// overwriting theirs. A file without an etag is a first schema: set refuses it
// when the collection already has one.
//
// Conformance (POST …/schema/conformance) is not here: the dashboard does not
// offer it, and these commands are the dashboard's parity.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var collectionsSchemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Read, set, delete or analyze a collection's payload schema",
	Long: `A collection's payload schema: the fields its points' payloads carry, and
whether writes that break it are let through (off), logged (warn) or refused
(strict).

To change a schema, read it to a file, edit the file, and set it from that file:

  afy collections schema get articles --save schema.json
  afy collections schema set articles --file schema.json

The file carries the version it was read at (etag). If the schema changes in
between, set refuses and writes nothing; read it again and redo the edit.`,
}

// --- GET ---

var collectionsSchemaGetCmd = &cobra.Command{
	Use:   "get <collection>",
	Short: "Show a collection's schema and its version",
	Long: `Show a collection's schema, its enforcement mode, its description and its
version (etag).

--save <file> writes them to a JSON file instead, ready to edit and pass to
'afy collections schema set --file'. The file keeps the version, so the set is
refused if someone changes the schema in the meantime. An existing file is kept
unless --force.`,
	Example: `  # Show the schema
  afy collections schema get articles --workspace research

  # Save it to edit
  afy collections schema get articles --save schema.json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return schemaGet(r, args[0], schemaSavePath, schemaSaveForce) })
	},
}

var (
	schemaSavePath  string
	schemaSaveForce bool
)

func schemaGet(r *vecRun, collection, savePath string, force bool) int {
	if savePath != "" && !force && fileExists(savePath) {
		return r.fail(refuse("%s already exists. Use --force to overwrite. Nothing was sent.", savePath))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	s, err := client.GetSchema(collection)
	if err != nil {
		return r.fail(err)
	}
	if savePath != "" {
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return r.fail(err)
		}
		if err := os.WriteFile(savePath, append(data, '\n'), 0o644); err != nil {
			return r.fail(fmt.Errorf("writing %s: %w", savePath, err))
		}
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string `json:"collection"`
			*vectors.Schema
			SavedTo string `json:"saved_to,omitempty"`
		}{r.envelope(), collection, s, savePath})
	}
	if savePath != "" {
		fmt.Fprintf(r.stdout, "Schema of collection '%s' (version %s) saved to %s.\n", collection, s.Etag, savePath)
		fmt.Fprintf(r.stdout, "Edit it, then: afy collections schema set %s --file %s\n", collection, savePath)
		return 0
	}
	fmt.Fprintf(r.stdout, "Schema of collection '%s' in %s\n", collection, workspaceLabel(client.Workspace()))
	fmt.Fprintf(r.stdout, "Version:     %s\n", s.Etag)
	fmt.Fprintf(r.stdout, "Enforcement: %s\n", s.EnforcementMode)
	desc := "(none)"
	if s.Description != nil && *s.Description != "" {
		desc = *s.Description
	}
	fmt.Fprintf(r.stdout, "Description: %s\n", desc)
	fmt.Fprintln(r.stdout, indentedJSON(s.Schema))
	return 0
}

func indentedJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

// --- SET ---

var collectionsSchemaSetCmd = &cobra.Command{
	Use:   "set <collection>",
	Short: "Set a collection's schema from a file",
	Long: `Set a collection's schema from a JSON file:

  {"schema": {...}, "enforcement_mode": "off|warn|strict", "description": "..."}

To change an existing schema, read it with 'afy collections schema get
<collection> --save <file>', edit that file, and set it from it. The file's
etag is the version it was read at: if the schema has changed since, the set is
refused and nothing is written.

The whole schema is replaced, description included: a file without
"description" clears the stored one. get --save writes it into the file, so
the read-edit-set flow keeps it.

A file without an etag sets a FIRST schema, and is refused when the collection
already has one, so a hand-written file never overwrites someone's schema. Two
first schemas set at the same moment both succeed, and the later one stays.

enforcement_mode defaults to off.`,
	Example: `  # Change the schema
  afy collections schema get articles --save schema.json
  afy collections schema set articles --file schema.json

  # A first schema, written by hand
  afy collections schema set articles --file new-schema.json --workspace research`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return schemaSet(r, args[0], schemaFilePath) })
	},
}

var schemaFilePath string

// schemaFile is what set reads: the file get --save writes, or one by hand.
type schemaFile struct {
	Schema          json.RawMessage `json:"schema"`
	EnforcementMode string          `json:"enforcement_mode"`
	Description     *string         `json:"description"`
	Etag            string          `json:"etag"`
}

func readSchemaFile(path string) (*schemaFile, error) {
	if path == "" {
		return nil, refuse("--file is required: the JSON file with the schema (afy collections schema get <collection> --save <file> writes one)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, refuse("reading %s: %v. Nothing was sent.", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f schemaFile
	if err := dec.Decode(&f); err != nil {
		return nil, refuse("%s is not a schema file (%v): it holds {\"schema\": {...}, \"enforcement_mode\", \"description\", \"etag\"}. Nothing was sent.", path, err)
	}
	if t := bytes.TrimSpace(f.Schema); len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return nil, refuse("%s has no \"schema\". Nothing was sent.", path)
	}
	return &f, nil
}

func schemaSet(r *vecRun, collection, path string) int {
	f, err := readSchemaFile(path)
	if err != nil {
		return r.fail(err)
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	if f.Etag == "" {
		// No version: allowed only as the collection's first schema.
		current, err := client.GetSchema(collection)
		var apiErr *vectors.APIError
		switch {
		case err == nil:
			return r.fail(fmt.Errorf("collection '%s' already has a schema (version %s), and %s has no etag saying which version it edits, "+
				"so nothing was written: run `afy collections schema get %s --save <file>`, edit it, and set it from that file",
				collection, current.Etag, path, collection))
		case errors.As(err, &apiErr) && apiErr.Code == "SCHEMA_NOT_DEFINED":
		default:
			return r.fail(err)
		}
	}
	etag, err := client.PutSchema(collection, vectors.SchemaWrite{
		Schema: f.Schema, EnforcementMode: f.EnforcementMode, Description: f.Description,
	}, f.Etag)
	var apiErr *vectors.APIError
	if errors.As(err, &apiErr) && apiErr.Code == "SCHEMA_VERSION_MISMATCH" {
		changed := *apiErr
		changed.Message = fmt.Sprintf("the schema of collection '%s' changed since you read it (your file has version %s), so nothing was written: "+
			"run `afy collections schema get %s --save <file>` again, redo your edit, and set it from that file",
			collection, f.Etag, collection)
		return r.fail(&changed)
	}
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string `json:"collection"`
			Etag       string `json:"etag"`
		}{r.envelope(), collection, etag})
	}
	fmt.Fprintf(r.stdout, "Schema of collection '%s' set in %s (version %s).\n", collection, workspaceLabel(client.Workspace()), etag)
	return 0
}

// --- DELETE ---

var collectionsSchemaDeleteCmd = &cobra.Command{
	Use:   "delete <collection>",
	Short: "Delete a collection's schema",
	Long: `Delete a collection's schema. Its points are not touched; writes are no
longer checked against a schema.

Asks you to type the collection name; --yes skips the question (required
without a terminal).`,
	Example: `  # Delete the schema
  afy collections schema delete articles

  # From a script
  afy collections schema delete articles --yes --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		return runVec(cmd, func(r *vecRun) int {
			return schemaDelete(r, args[0], schemaDeleteYes, os.Stdin, interactive)
		})
	},
}

var schemaDeleteYes bool

func schemaDelete(r *vecRun, collection string, yes bool, stdin io.Reader, interactive bool) int {
	if err := confirmDestructiveOn(yes, destructiveAsk{
		action:   fmt.Sprintf("deletes the schema of collection '%s' (its points stay)", collection),
		warn:     []string{fmt.Sprintf("This deletes the schema of collection '%s'. Its points stay.", collection)},
		prompt:   "Type the collection name to confirm: ",
		accept:   typedName(collection),
		mismatch: "the name typed did not match",
	}, interactive, vecPrompter(r, stdin)); err != nil {
		return r.fail(err)
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	if err := client.DeleteSchema(collection); err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection    string `json:"collection"`
			SchemaDeleted bool   `json:"schema_deleted"`
		}{r.envelope(), collection, true})
	}
	fmt.Fprintf(r.stdout, "Schema of collection '%s' deleted in %s.\n", collection, workspaceLabel(client.Workspace()))
	return 0
}

// --- ANALYZE ---

var collectionsSchemaAnalyzeCmd = &cobra.Command{
	Use:   "analyze <collection>",
	Short: "Sample a collection's points and suggest a schema",
	Long: `Sample a collection's points and report each payload field: how many points
carry it, its types, and warnings (mixed types, rarely present). Ends with a
suggested schema. Nothing is changed.`,
	Example: `  # Analyze with the default sample
  afy collections schema analyze articles

  # A larger sample, as JSON
  afy collections schema analyze articles --sample-size 5000 --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return schemaAnalyze(r, args[0], schemaSampleSize) })
	},
}

var schemaSampleSize int

func schemaAnalyze(r *vecRun, collection string, sampleSize int) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	a, err := client.AnalyzeSchema(collection, sampleSize)
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string `json:"collection"`
			*vectors.SchemaAnalysis
		}{r.envelope(), collection, a})
	}
	fmt.Fprintf(r.stdout, "Analyzed %d of %d points of collection '%s' in %s.\n",
		a.SampleSize, a.TotalPoints, collection, workspaceLabel(client.Workspace()))
	names := make([]string, 0, len(a.Fields))
	for name := range a.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 0 {
		fmt.Fprintln(r.stdout)
		tw := tabwriter.NewWriter(r.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "FIELD\tPRESENCE\tTYPES\tWARNINGS")
		for _, name := range names {
			f := a.Fields[name]
			types := shares(f.Types)
			if len(f.ElementTypes) > 0 {
				types += " (elements: " + shares(f.ElementTypes) + ")"
			}
			fmt.Fprintf(tw, "%s\t%.1f%%\t%s\t%s\n", name, f.Presence*100, types, strings.Join(f.Warnings, ", "))
		}
		_ = tw.Flush()
	}
	fmt.Fprintln(r.stdout)
	fmt.Fprintln(r.stdout, "Suggested schema:")
	fmt.Fprintln(r.stdout, indentedJSON(a.SuggestedSchema))
	return 0
}

// shares prints {"string": 0.75, "integer": 0.25} as "string 75.0%, integer 25.0%",
// largest first.
func shares(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %.1f%%", k, m[k]*100)
	}
	return strings.Join(parts, ", ")
}

func init() {
	collectionsSchemaGetCmd.Flags().StringVar(&schemaSavePath, "save", "", "Write the schema and its version to this JSON file, to edit and pass to set --file")
	collectionsSchemaGetCmd.Flags().BoolVarP(&schemaSaveForce, "force", "f", false, "With --save, overwrite an existing file")
	collectionsSchemaSetCmd.Flags().StringVar(&schemaFilePath, "file", "", "JSON file with the schema (from get --save, or written by hand for a first schema), required")
	collectionsSchemaDeleteCmd.Flags().BoolVarP(&schemaDeleteYes, "yes", "y", false, "Delete without asking")
	collectionsSchemaAnalyzeCmd.Flags().IntVar(&schemaSampleSize, "sample-size", 0, "Points to sample (default: the server's)")

	collectionsSchemaCmd.AddCommand(collectionsSchemaGetCmd)
	collectionsSchemaCmd.AddCommand(collectionsSchemaSetCmd)
	collectionsSchemaCmd.AddCommand(collectionsSchemaDeleteCmd)
	collectionsSchemaCmd.AddCommand(collectionsSchemaAnalyzeCmd)
	collectionsCmd.AddCommand(collectionsSchemaCmd)
}
