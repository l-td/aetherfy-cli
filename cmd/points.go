package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

var pointsCmd = &cobra.Command{
	Use:   "points",
	Short: "Count, read and search the points in a collection",
	Long: `Count, read and search the points in a collection. Read-only: writing
points is the SDKs' job, with the chunking and retries a bulk load needs.

afy points get <collection> <id> reads points by id; afy points search
<collection> finds the nearest ones to a vector.

--filter takes a filter object as JSON, in the shape the SDKs send, e.g.
'{"must":[{"key":"lang","match":{"value":"en"}}]}'.`,
}

// --- COUNT ---

var pointsCountCmd = &cobra.Command{
	Use:   "count <collection>",
	Short: "Count the points in a collection, or those a filter matches",
	Example: `  # Every point
  afy points count articles

  # The points a filter matches
  afy points count articles --filter '{"must":[{"key":"lang","match":{"value":"en"}}]}' --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return pointsCount(r, args[0], pointsFilter) })
	},
}

var (
	pointsFilter       string
	pointsSearchVector string
	pointsSearchLimit  int
)

// parseFilter reads --filter: "" for none, else a JSON object sent as given.
func parseFilter(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
		return nil, refuse("--filter must be a JSON object, got '%s'", raw)
	}
	return json.RawMessage(raw), nil
}

func pointsCount(r *vecRun, collection, filterRaw string) int {
	filter, err := parseFilter(filterRaw)
	if err != nil {
		return r.fail(err)
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	n, err := client.CountPoints(collection, filter)
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string `json:"collection"`
			Count      int64  `json:"count"`
		}{r.envelope(), collection, n})
	}
	fmt.Fprintln(r.stdout, n)
	return 0
}

// --- GET ---

var pointsGetCmd = &cobra.Command{
	Use:   "get <collection> <id>...",
	Short: "Read points by id, with their payloads",
	Long: `Read points by id, with their payloads (not their vectors).

An id made only of digits is sent as a number, anything else as a string (a
UUID). An id that does not exist is left out of the answer, not an error.`,
	Example: `  # Two points by numeric id
  afy points get articles 17 42

  # One point by UUID, as JSON
  afy points get articles 5c56c793-69f3-4fbf-87e6-c4bf54c28c26 --json`,
	Args: refuseArgs(cobra.MinimumNArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return pointsGet(r, args[0], args[1:]) })
	},
}

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

// pointID is an id as typed: digits are a numeric id, anything else a string.
func pointID(raw string) interface{} {
	if digitsOnly.MatchString(raw) {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return n
		}
	}
	return raw
}

// pointJSON is a point in --json output. score is set on search hits only.
type pointJSON struct {
	ID      json.RawMessage `json:"id"`
	Score   *float64        `json:"score,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

func toPointsJSON(points []vectors.Point) []pointJSON {
	out := make([]pointJSON, 0, len(points))
	for _, p := range points {
		payload := p.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		out = append(out, pointJSON{ID: p.ID, Score: p.Score, Payload: payload})
	}
	return out
}

func pointsGet(r *vecRun, collection string, rawIDs []string) int {
	ids := make([]interface{}, 0, len(rawIDs))
	for _, raw := range rawIDs {
		ids = append(ids, pointID(raw))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	points, err := client.GetPoints(collection, ids)
	if err != nil {
		return r.fail(err)
	}
	return r.printPoints(collection, points, false)
}

// printPoints prints points as JSON, or one row per point.
func (r *vecRun) printPoints(collection string, points []vectors.Point, scored bool) int {
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string      `json:"collection"`
			Points     []pointJSON `json:"points"`
		}{r.envelope(), collection, toPointsJSON(points)})
	}
	if len(points) == 0 {
		fmt.Fprintln(r.stdout, "No points.")
		return 0
	}
	headers := []string{"ID", "Payload"}
	if scored {
		headers = []string{"ID", "Score", "Payload"}
	}
	rows := make([][]string, 0, len(points))
	for _, p := range points {
		row := []string{string(p.ID)}
		if scored && p.Score != nil {
			row = append(row, strconv.FormatFloat(*p.Score, 'f', 4, 64))
		}
		rows = append(rows, append(row, string(p.Payload)))
	}
	output.RenderTable(r.stdout, headers, rows)
	return 0
}

// --- SEARCH ---

var pointsSearchCmd = &cobra.Command{
	Use:   "search <collection>",
	Short: "Find the points nearest to a vector",
	Long: `Find the points nearest to a vector, with their scores and payloads.

--vector is a JSON array of numbers, or @path to read one from a file. It
must have the collection's size.`,
	Example: `  # The five nearest points to a vector
  afy points search articles --vector '[0.12, -0.03, 0.88]' --limit 5

  # A vector from a file, filtered, as JSON
  afy points search articles --vector @query.json --filter '{"must":[{"key":"lang","match":{"value":"en"}}]}' --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int {
			return pointsSearch(r, args[0], pointsSearchVector, pointsSearchLimit, pointsFilter)
		})
	},
}

// parseVector reads --vector: a JSON array of numbers, or @path to one.
func parseVector(raw string) ([]float64, error) {
	text := raw
	if strings.HasPrefix(raw, "@") {
		data, err := os.ReadFile(raw[1:])
		if err != nil {
			return nil, refuse("--vector %s: %v", raw, err)
		}
		text = string(data)
	}
	if strings.TrimSpace(text) == "" {
		return nil, refuse("--vector is required: a JSON array of numbers, or @path to one")
	}
	var v []float64
	if err := json.Unmarshal([]byte(text), &v); err != nil || len(v) == 0 {
		return nil, refuse("--vector must be a non-empty JSON array of numbers: %s", describeJSONError(err))
	}
	return v, nil
}

func describeJSONError(err error) string {
	if err == nil {
		return "got an empty array"
	}
	return err.Error()
}

func pointsSearch(r *vecRun, collection, vectorRaw string, limit int, filterRaw string) int {
	vector, err := parseVector(vectorRaw)
	if err != nil {
		return r.fail(err)
	}
	if limit <= 0 {
		return r.fail(refuse("--limit must be a whole number above 0, got %d", limit))
	}
	filter, err := parseFilter(filterRaw)
	if err != nil {
		return r.fail(err)
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	hits, err := client.Search(collection, vector, limit, filter)
	if err != nil {
		return r.fail(err)
	}
	return r.printPoints(collection, hits, true)
}

func init() {
	pointsCountCmd.Flags().StringVar(&pointsFilter, "filter", "", "Only count the points this filter (a JSON object) matches")
	pointsCountCmd.Flags().BoolVar(&vecJSON, "json", false, vecJSONHelp)
	pointsCountCmd.Flags().StringVar(&vecVectorsURL, "vectors-url", "", vecVectorsURLHelp)
	pointsCountCmd.Flags().StringVar(&vecAPIRegion, "api-region", "", vecAPIRegionHelp)
	pointsCountCmd.Flags().StringVar(&vecWorkspace, "workspace", "", vecWorkspaceHelp)

	pointsGetCmd.Flags().BoolVar(&vecJSON, "json", false, vecJSONHelp)
	pointsGetCmd.Flags().StringVar(&vecVectorsURL, "vectors-url", "", vecVectorsURLHelp)
	pointsGetCmd.Flags().StringVar(&vecAPIRegion, "api-region", "", vecAPIRegionHelp)
	pointsGetCmd.Flags().StringVar(&vecWorkspace, "workspace", "", vecWorkspaceHelp)

	pointsSearchCmd.Flags().StringVar(&pointsSearchVector, "vector", "", "The query vector: a JSON array of numbers, or @path to one; required")
	pointsSearchCmd.Flags().IntVar(&pointsSearchLimit, "limit", 10, "How many points to return")
	pointsSearchCmd.Flags().StringVar(&pointsFilter, "filter", "", "Only return points this filter (a JSON object) matches")
	pointsSearchCmd.Flags().BoolVar(&vecJSON, "json", false, vecJSONHelp)
	pointsSearchCmd.Flags().StringVar(&vecVectorsURL, "vectors-url", "", vecVectorsURLHelp)
	pointsSearchCmd.Flags().StringVar(&vecAPIRegion, "api-region", "", vecAPIRegionHelp)
	pointsSearchCmd.Flags().StringVar(&vecWorkspace, "workspace", "", vecWorkspaceHelp)

	pointsCmd.AddCommand(pointsCountCmd)
	pointsCmd.AddCommand(pointsGetCmd)
	pointsCmd.AddCommand(pointsSearchCmd)
}
