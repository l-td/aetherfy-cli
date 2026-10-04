package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var pointsCmd = &cobra.Command{
	Use:   "points",
	Short: "Count, browse, read, search and delete the points in a collection",
	Long: `Count, browse, read, search and delete the points in a collection.
Writing points is the SDKs' job, with the chunking and retries a bulk load
needs; deleting them is here, for cleanup.

afy points list <collection> pages through them;
afy points get <collection> <id> reads points by id;
afy points search <collection> finds the nearest ones to a vector;
afy points delete <collection> <id> removes points by id or by --filter.

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
	pointsListLimit    int
	pointsListOffset   string
	pointsListVectors  bool
	pointsDeleteYes    bool
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

// pointJSON is a point in --json output. score is set on search hits only,
// vector only by afy points list --with-vectors.
type pointJSON struct {
	ID      json.RawMessage `json:"id"`
	Score   *float64        `json:"score,omitempty"`
	Payload json.RawMessage `json:"payload"`
	Vector  json.RawMessage `json:"vector,omitempty"`
}

func toPointsJSON(points []vectors.Point, withVectors bool) []pointJSON {
	out := make([]pointJSON, 0, len(points))
	for _, p := range points {
		payload := p.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		pj := pointJSON{ID: p.ID, Score: p.Score, Payload: payload}
		if withVectors {
			pj.Vector = p.Vector
			if len(pj.Vector) == 0 {
				pj.Vector = json.RawMessage("null")
			}
		}
		out = append(out, pj)
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
		}{r.envelope(), collection, toPointsJSON(points, false)})
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

// --- LIST ---

var pointsListCmd = &cobra.Command{
	Use:   "list <collection>",
	Short: "Page through the points in a collection, with their payloads",
	Long: fmt.Sprintf(`Page through the points in a collection, in id order, with their payloads.

Each page holds up to --limit points (default %d, at most %d, the most the
vectors API returns in one read). When there are more, the page ends with the
id the next one starts at: pass it as --offset to read the next page, with
the same --filter. --json prints it as next_page_offset, null on the last page.

--with-vectors adds each point's vector.`, vectors.ScrollLimitDefault, vectors.ScrollLimitMax),
	Example: `  # The first ten points
  afy points list articles

  # The next page, from the id the previous one ended with
  afy points list articles --offset 42

  # The points a filter matches, 100 at a time, as JSON
  afy points list articles --limit 100 --filter '{"must":[{"key":"lang","match":{"value":"en"}}]}' --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int {
			return pointsList(r, args[0], pointsListLimit, pointsListOffset, pointsFilter, pointsListVectors)
		})
	},
}

// previewWidth is how much of a payload or a vector a row of afy points list
// shows.
const previewWidth = 60

// reservedPayloadPrefix starts the payload keys vectordb writes itself (the
// attested __aetherfy_agent_id and __aetherfy_deployment_id, returned on every
// read).
const reservedPayloadPrefix = "__aetherfy_"

// preview shortens raw JSON to width characters for a table cell. A payload
// object is printed with its own keys first, sorted, and the reserved keys
// after them: measured against the API, every point carries both attested keys
// (null for a human write) in no fixed order, and they filled the cell before
// any of the customer's fields. Only the order changes; --json is untouched.
func preview(raw json.RawMessage, width int) string {
	text := string(raw)
	if len(text) == 0 {
		text = "null"
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil && obj != nil {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			ri, rj := strings.HasPrefix(keys[i], reservedPayloadPrefix), strings.HasPrefix(keys[j], reservedPayloadPrefix)
			if ri != rj {
				return rj
			}
			return keys[i] < keys[j]
		})
		var b strings.Builder
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			name, _ := json.Marshal(k)
			b.Write(name)
			b.WriteString(":")
			b.Write(obj[k])
		}
		b.WriteString("}")
		text = b.String()
	}
	if r := []rune(text); len(r) > width {
		return string(r[:width-1]) + "…"
	}
	return text
}

func pointsList(r *vecRun, collection string, limit int, offsetRaw, filterRaw string, withVectors bool) int {
	if limit < 1 || limit > vectors.ScrollLimitMax {
		return r.fail(refuse("--limit must be a whole number from 1 to %d, got %d", vectors.ScrollLimitMax, limit))
	}
	filter, err := parseFilter(filterRaw)
	if err != nil {
		return r.fail(err)
	}
	var offset interface{}
	if trimmed := strings.TrimSpace(offsetRaw); trimmed != "" {
		offset = pointID(trimmed)
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	page, err := client.Scroll(collection, limit, offset, filter, withVectors)
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection     string          `json:"collection"`
			Points         []pointJSON     `json:"points"`
			NextPageOffset json.RawMessage `json:"next_page_offset"`
		}{r.envelope(), collection, toPointsJSON(page.Points, withVectors), page.NextPageOffset})
	}
	if len(page.Points) == 0 {
		fmt.Fprintln(r.stdout, "No points.")
		return 0
	}
	headers := []string{"ID", "Payload"}
	if withVectors {
		headers = append(headers, "Vector")
	}
	rows := make([][]string, 0, len(page.Points))
	for _, p := range page.Points {
		row := []string{string(p.ID), preview(p.Payload, previewWidth)}
		if withVectors {
			row = append(row, preview(p.Vector, previewWidth))
		}
		rows = append(rows, row)
	}
	output.RenderTable(r.stdout, headers, rows)
	if next := string(page.NextPageOffset); next != "null" {
		// A string id (a UUID) is printed without its JSON quotes, the way
		// --offset takes it.
		var asString string
		if json.Unmarshal(page.NextPageOffset, &asString) == nil {
			next = asString
		}
		fmt.Fprintf(r.stdout, "\nMore points: pass --offset %s for the next page.\n", next)
	}
	return 0
}

// --- DELETE ---

var pointsDeleteCmd = &cobra.Command{
	Use:   "delete <collection> [<id>...]",
	Short: "Delete points by id, or the points a filter matches",
	Long: `Delete points by id, or every point a filter matches: give ids or --filter,
not both.

It counts what will go first and prints the count (by id: the ids that exist;
by filter: the points it matches now), then asks you to type the collection's
name. --yes skips the question; it is required when stdin is not a terminal,
so a script cannot delete by accident. When nothing matches, nothing is
deleted.

An empty filter ({}, or one with no conditions) is refused: it matches every
point, and deleting every point is afy collections delete <name>.`,
	Example: `  # Two points by id, confirming by name
  afy points delete articles 17 42

  # Every point a filter matches, from a script
  afy points delete articles --filter '{"must":[{"key":"lang","match":{"value":"de"}}]}' --yes --json`,
	Args: refuseArgs(cobra.MinimumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		return runVec(cmd, func(r *vecRun) int {
			return pointsDelete(r, args[0], args[1:], pointsFilter, pointsDeleteYes, os.Stdin, interactive)
		})
	},
}

// filterHasConditions reports whether a filter object narrows anything: some
// key whose value is neither null nor an empty list. {} and {"must":[]} match
// every point.
func filterHasConditions(filter json.RawMessage) bool {
	var obj map[string]interface{}
	if err := json.Unmarshal(filter, &obj); err != nil {
		return true // parseFilter has already refused anything but an object
	}
	for _, v := range obj {
		if list, isList := v.([]interface{}); v == nil || (isList && len(list) == 0) {
			continue
		}
		return true
	}
	return false
}

func pointsDelete(r *vecRun, collection string, rawIDs []string, filterRaw string, yes bool, stdin io.Reader, interactive bool) int {
	filter, err := parseFilter(filterRaw)
	if err != nil {
		return r.fail(err)
	}
	switch {
	case len(rawIDs) > 0 && filter != nil:
		return r.fail(refuse("give point ids or --filter, not both"))
	case len(rawIDs) == 0 && filter == nil:
		return r.fail(refuse("give the ids of the points to delete, or --filter"))
	case filter != nil && !filterHasConditions(filter):
		return r.fail(refuse("not deleting: an empty --filter matches every point in '%s'. "+
			"To delete the whole collection, run afy collections delete %s.", collection, collection))
	}
	if !yes && !interactive {
		return r.fail(refuse("not deleting points from '%s': stdin is not a terminal, so nobody "+
			"can confirm. Pass --yes to delete without the question.", collection))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}

	// Count what will go, and say it, before anything is deleted. By id, only
	// the ids that exist are sent on, so the count is what the delete removes.
	var ids []json.RawMessage
	var n int64
	if filter != nil {
		if n, err = client.CountPoints(collection, filter); err != nil {
			return r.fail(err)
		}
	} else {
		asked := make([]interface{}, 0, len(rawIDs))
		for _, raw := range rawIDs {
			asked = append(asked, pointID(raw))
		}
		if ids, err = client.ExistingPointIDs(collection, asked); err != nil {
			return r.fail(err)
		}
		n = int64(len(ids))
	}
	// On stderr, like the question, so --json keeps stdout to one object.
	fmt.Fprintf(r.stderr, "%d point(s) in collection '%s' will be deleted.\n", n, collection)

	if n > 0 {
		if !yes {
			fmt.Fprint(r.stderr, "Type the collection name to confirm: ")
			line, _ := bufio.NewReader(stdin).ReadString('\n')
			if strings.TrimSpace(line) != collection {
				return r.fail(refuse("deletion cancelled: the name typed did not match"))
			}
		}
		if err := client.DeletePoints(collection, ids, filter); err != nil {
			return r.fail(err)
		}
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string `json:"collection"`
			Deleted    int64  `json:"deleted"`
		}{r.envelope(), collection, n})
	}
	if n == 0 {
		fmt.Fprintln(r.stdout, "Nothing matched; nothing deleted.")
		return 0
	}
	fmt.Fprintf(r.stdout, "Deleted %d point(s) from collection '%s'.\n", n, collection)
	return 0
}

func init() {
	pointsCountCmd.Flags().StringVar(&pointsFilter, "filter", "", "Only count the points this filter (a JSON object) matches")

	pointsSearchCmd.Flags().StringVar(&pointsSearchVector, "vector", "", "The query vector: a JSON array of numbers, or @path to one; required")
	pointsSearchCmd.Flags().IntVar(&pointsSearchLimit, "limit", 10, "How many points to return")
	pointsSearchCmd.Flags().StringVar(&pointsFilter, "filter", "", "Only return points this filter (a JSON object) matches")

	pointsListCmd.Flags().IntVar(&pointsListLimit, "limit", vectors.ScrollLimitDefault, "How many points a page holds, at most "+strconv.Itoa(vectors.ScrollLimitMax))
	pointsListCmd.Flags().StringVar(&pointsListOffset, "offset", "", "The id to start at: the previous page's next_page_offset")
	pointsListCmd.Flags().StringVar(&pointsFilter, "filter", "", "Only list the points this filter (a JSON object) matches")
	pointsListCmd.Flags().BoolVar(&pointsListVectors, "with-vectors", false, "Include each point's vector")

	pointsDeleteCmd.Flags().StringVar(&pointsFilter, "filter", "", "Delete the points this filter (a JSON object) matches, instead of ids")
	pointsDeleteCmd.Flags().BoolVarP(&pointsDeleteYes, "yes", "y", false, "Delete without asking")

	pointsCmd.AddCommand(pointsCountCmd)
	pointsCmd.AddCommand(pointsListCmd)
	pointsCmd.AddCommand(pointsGetCmd)
	pointsCmd.AddCommand(pointsSearchCmd)
	pointsCmd.AddCommand(pointsDeleteCmd)
}
