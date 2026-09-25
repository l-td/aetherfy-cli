package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Create and drop payload indexes on a collection",
	Long: `Create and drop payload indexes: an index on one payload field makes
filters on it fast, and is required to order a scroll by it.`,
}

// --- CREATE ---

var indexCreateCmd = &cobra.Command{
	Use:   "create <collection> <field>",
	Short: "Index one payload field, and wait until the index is usable",
	Long: `Index one payload field, and return only once the index is built, so a
filter or an ordered scroll on the field works as soon as this exits 0.

The server waits up to 25 s for a build. A longer build is answered
"acknowledged", and the create is sent again, which waits for the running
build, until the answer is "completed". The wait ends at --timeout seconds
(default 600) with a "still building" error; the build carries on, and
running afy index create <collection> <field> again waits for it. Creating an
index that already exists returns at once.

--type is one of keyword, integer, float, bool, geo, datetime, uuid, text,
or a JSON object for a parameterised index. It is sent as given.`,
	Example: `  # Index a keyword field
  afy index create articles thread_id --type keyword

  # Index a timestamp to order by it, waiting at most five minutes
  afy index create articles ts --type integer --timeout 300 --json

  # A full-text index with its parameters
  afy index create articles body --type '{"type":"text","tokenizer":"word","lowercase":true}'`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = checkAuth()
		var timeout *string
		if cmd.Flags().Changed("timeout") {
			timeout = &indexTimeout
		}
		return exitWith(indexCreate(newVecRun(cmd), args[0], args[1], indexType, timeout))
	},
}

var (
	indexType    string
	indexTimeout string
)

// fieldSchema reads --type: a JSON object is a parameterised schema, anything
// else a type name. Both are sent as given.
func fieldSchema(raw string) (interface{}, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, refuse("--type is required: keyword, integer, float, bool, geo, datetime, uuid, text, or a JSON object")
	}
	if !strings.HasPrefix(trimmed, "{") {
		return trimmed, nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return nil, refuse("--type looks like a JSON object but is not one: %v", err)
	}
	return obj, nil
}

// indexCreate runs `afy index create`. timeoutRaw is --timeout as typed, nil
// when it was not given (an explicit empty value is refused, not defaulted).
func indexCreate(r *vecRun, collection, field, typ string, timeoutRaw *string) int {
	schema, err := fieldSchema(typ)
	if err != nil {
		return r.fail(err)
	}
	var timeout time.Duration
	if timeoutRaw != nil {
		if timeout, err = vectors.ParseIndexTimeout(*timeoutRaw); err != nil {
			return r.fail(&inputError{msg: err.Error()})
		}
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	if err := client.CreateFieldIndex(collection, field, schema, timeout); err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string      `json:"collection"`
			Field      string      `json:"field"`
			Type       interface{} `json:"type"`
			Status     string      `json:"status"`
		}{r.envelope(), collection, field, schema, "completed"})
	}
	fmt.Fprintf(r.stdout, "Index on '%s' in collection '%s' is built.\n", field, collection)
	return 0
}

// --- DELETE ---

var indexDeleteCmd = &cobra.Command{
	Use:   "delete <collection> <field>",
	Short: "Drop the payload index on one field",
	Long: `Drop the payload index on one field. Dropping an index the field does not
have succeeds; a collection that does not exist is an error.`,
	Example: `  # Drop an index
  afy index delete articles thread_id`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = checkAuth()
		return exitWith(indexDelete(newVecRun(cmd), args[0], args[1]))
	},
}

func indexDelete(r *vecRun, collection, field string) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	if err := client.DeleteFieldIndex(collection, field); err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string `json:"collection"`
			Field      string `json:"field"`
			Deleted    bool   `json:"deleted"`
		}{r.envelope(), collection, field, true})
	}
	fmt.Fprintf(r.stdout, "Index on '%s' in collection '%s' dropped.\n", field, collection)
	return 0
}

func init() {
	indexCreateCmd.Flags().StringVar(&indexType, "type", "", "Index type: keyword, integer, float, bool, geo, datetime, uuid, text, or a JSON object; required")
	indexCreateCmd.Flags().StringVar(&indexTimeout, "timeout", "", "Seconds to wait for the build before giving up (default 600)")
	indexCreateCmd.Flags().BoolVar(&vecJSON, "json", false, vecJSONHelp)
	indexCreateCmd.Flags().StringVar(&vecVectorsURL, "vectors-url", "", vecVectorsURLHelp)
	indexCreateCmd.Flags().StringVar(&vecAPIRegion, "api-region", "", vecAPIRegionHelp)
	indexCreateCmd.Flags().StringVar(&vecWorkspace, "workspace", "", vecWorkspaceHelp)

	indexDeleteCmd.Flags().BoolVar(&vecJSON, "json", false, vecJSONHelp)
	indexDeleteCmd.Flags().StringVar(&vecVectorsURL, "vectors-url", "", vecVectorsURLHelp)
	indexDeleteCmd.Flags().StringVar(&vecAPIRegion, "api-region", "", vecAPIRegionHelp)
	indexDeleteCmd.Flags().StringVar(&vecWorkspace, "workspace", "", vecWorkspaceHelp)

	indexCmd.AddCommand(indexCreateCmd)
	indexCmd.AddCommand(indexDeleteCmd)
}
