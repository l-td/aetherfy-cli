package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/vectors"
)

// `afy points list` and `afy points delete`, against the fake vectors API in
// vectors_commands_test.go. The rules each test pins are the brief's: paging
// follows next_page_offset, the filter goes through as given, ids and --filter
// exclude each other, the count is shown before anything is deleted, and the
// three refusals (no terminal without --yes, an empty filter, both or neither
// selector) send nothing.

// pagedCollection answers scrolls over points 1..total in id order, the way
// Qdrant does: a page of up to limit points from offset, and the id after the
// last one as next_page_offset (null when there is none).
func pagedCollection(total int) func(r vecRequest) (int, string) {
	return func(r vecRequest) (int, string) {
		start := 1
		if off, ok := r.Body["offset"].(float64); ok {
			start = int(off)
		}
		limit := int(r.Body["limit"].(float64))
		points := []string{}
		id := start
		for ; id <= total && len(points) < limit; id++ {
			p := fmt.Sprintf(`{"id":%d,"payload":{"n":%d}`, id, id)
			if r.Body["with_vector"] == true {
				p += `,"vector":[0.5,0.25]`
			}
			points = append(points, p+"}")
		}
		next := "null"
		if id <= total {
			next = fmt.Sprint(id)
		}
		return 200, `{"result":{"points":[` + strings.Join(points, ",") + `],"next_page_offset":` + next + `},"status":"ok"}`
	}
}

func TestPointsListPagesThroughByFollowingNextPageOffset(t *testing.T) {
	s := newVecServer(t, pagedCollection(25))
	var ids []float64
	offset := ""
	pages := 0
	for {
		r, stdout, _, _ := s.run("research", true)
		if code := pointsList(r, "articles", 10, offset, langFilter, false); code != 0 {
			t.Fatalf("page %d: exit %d", pages+1, code)
		}
		pages++
		out := decode(t, stdout.String())
		if out["collection"] != "articles" || out["workspace"] != "research" || out["endpoint"] != s.srv.URL {
			t.Errorf("envelope = %v", out)
		}
		for _, p := range out["points"].([]interface{}) {
			point := p.(map[string]interface{})
			if _, has := point["vector"]; has || len(point) != 2 {
				t.Errorf("point = %v; want id and payload only without --with-vectors", point)
			}
			ids = append(ids, point["id"].(float64))
		}
		next, present := out["next_page_offset"]
		if !present {
			t.Fatal("--json has no next_page_offset key; the last page must say null")
		}
		if next == nil {
			break
		}
		offset = fmt.Sprint(next)
		if pages > 5 {
			t.Fatal("RUNAWAY: next_page_offset never ended")
		}
	}
	if pages != 3 || len(ids) != 25 || ids[0] != 1 || ids[24] != 25 {
		t.Errorf("read %d point(s) over %d page(s), first %v last %v; want 1..25 over 3", len(ids), pages, ids[0], ids[len(ids)-1])
	}

	reqs := s.got()
	for i, req := range reqs {
		if req.Method != "POST" || req.Path != "/api/v1/workspaces/research/collections/articles/points/scroll" {
			t.Errorf("request %d: %s %s", i, req.Method, req.Path)
		}
		if req.Body["limit"] != 10.0 || req.Body["with_payload"] != true || req.Body["with_vector"] != false {
			t.Errorf("request %d body = %v", i, req.Body)
		}
		// The filter goes through as given, on every page.
		f, ok := req.Body["filter"].(map[string]interface{})
		if !ok || f["must"] == nil {
			t.Errorf("request %d: filter = %v, want the JSON object as given", i, req.Body["filter"])
		}
	}
	if _, sent := reqs[0].Body["offset"]; sent {
		t.Error("the first page sent an offset; none was given")
	}
	// A digits-only offset is a numeric id, like `points get`'s ids.
	if reqs[1].Body["offset"] != 11.0 || reqs[2].Body["offset"] != 21.0 {
		t.Errorf("offsets sent = %v, %v; want 11, 21 (the previous pages' next_page_offset)", reqs[1].Body["offset"], reqs[2].Body["offset"])
	}
}

func TestPointsListOffsetThatIsAUUIDIsSentAsAString(t *testing.T) {
	const uuid = "5c56c793-69f3-4fbf-87e6-c4bf54c28c26"
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, `{"result":{"points":[],"next_page_offset":null}}`
	})
	r, _, _, _ := s.run("", true)
	if code := pointsList(r, "articles", 5, uuid, "", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := only(t, s.got()).Body["offset"]; got != uuid {
		t.Errorf("offset = %v, want the UUID as a string", got)
	}
}

func TestPointsListWithVectorsAndTheHumanTable(t *testing.T) {
	s := newVecServer(t, pagedCollection(3))
	r, stdout, _, _ := s.run("", true)
	if code := pointsList(r, "articles", 2, "", "", true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if req := only(t, s.got()); req.Body["with_vector"] != true {
		t.Errorf("--with-vectors sent with_vector %v", req.Body["with_vector"])
	}
	out := decode(t, stdout.String())
	p := out["points"].([]interface{})[0].(map[string]interface{})
	if v, ok := p["vector"].([]interface{}); !ok || len(v) != 2 || len(p) != 3 {
		t.Errorf("point = %v; want id, payload, vector", p)
	}
	if out["next_page_offset"] != 3.0 {
		t.Errorf("next_page_offset = %v", out["next_page_offset"])
	}

	// Human output: one row per point with a short payload, and how to get the
	// next page.
	s = newVecServer(t, func(vecRequest) (int, string) {
		long := strings.Repeat("x", 200)
		return 200, `{"result":{"points":[{"id":1,"payload":{"__aetherfy_deployment_id":null,"title":"a","__aetherfy_agent_id":null,"lang":"en"}},` +
			`{"id":"5c56c793-69f3-4fbf-87e6-c4bf54c28c26","payload":{"body":"` + long + `"}}],` +
			`"next_page_offset":"7d2b0c55-0a8b-4bd7-8d5e-6f6bd1a3e0a1"}}`
	})
	r, stdout, _, _ = s.run("", false)
	if code := pointsList(r, "articles", 2, "", "", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	text := stdout.String()
	// The customer's own keys first, sorted; the attested keys every point
	// carries come after them, so they do not fill the cell.
	if !strings.Contains(text, `{"lang":"en","title":"a","__aetherfy_agent_id":null,`) || strings.Contains(text, strings.Repeat("x", previewWidth)) {
		t.Errorf("rows should carry a payload preview cut at %d characters:\n%s", previewWidth, text)
	}
	if !strings.Contains(text, "More points: pass --offset 7d2b0c55-0a8b-4bd7-8d5e-6f6bd1a3e0a1 for the next page.") {
		t.Errorf("the next page's offset is not printed bare:\n%s", text)
	}

	// The last page says nothing about a next one.
	s = newVecServer(t, pagedCollection(1))
	r, stdout, _, _ = s.run("", false)
	if code := pointsList(r, "articles", 10, "", "", false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stdout.String(), "More points") {
		t.Errorf("the last page offered a next one:\n%s", stdout.String())
	}
}

func TestPointsListLimitIsBoundedByTheAPIsReadLimit(t *testing.T) {
	if vectors.ScrollLimitMax != 1000 || vectors.ScrollLimitDefault != 10 {
		t.Fatalf("ScrollLimitMax %d, ScrollLimitDefault %d: vectordb's READ_LIMIT_MAX is 1000 and the SDKs' scroll default 10",
			vectors.ScrollLimitMax, vectors.ScrollLimitDefault)
	}
	if f := pointsListCmd.Flag("limit"); f.DefValue != fmt.Sprint(vectors.ScrollLimitDefault) {
		t.Errorf("--limit defaults to %s, not ScrollLimitDefault", f.DefValue)
	}
	s := newVecServer(t, pagedCollection(0))
	r, _, _, _ := s.run("", true)
	if code := pointsList(r, "articles", vectors.ScrollLimitMax, "", "", false); code != 0 {
		t.Errorf("--limit %d (the maximum) exit %d", vectors.ScrollLimitMax, code)
	}
	for _, limit := range []int{0, -1, vectors.ScrollLimitMax + 1} {
		s := newVecServer(t, pagedCollection(0))
		r, _, stderr, connects := s.run("", true)
		if code := pointsList(r, "articles", limit, "", "", false); code != exitInputRefused {
			t.Errorf("--limit %d: exit %d, want %d", limit, code, exitInputRefused)
		}
		if len(s.got()) != 0 || *connects != 0 {
			t.Errorf("--limit %d still resolved or sent", limit)
		}
		e := decode(t, stderr.String())["error"].(map[string]interface{})
		if e["message"] != fmt.Sprintf("--limit must be a whole number from 1 to 1000, got %d", limit) {
			t.Errorf("--limit %d: error = %v", limit, e)
		}
	}
}

// --- delete ---

// deleteServer answers the count probes and the delete. retrieve lists the
// ids that exist; count is what a filter matches.
func deleteServer(t *testing.T, existing []string, count int) *vecServer {
	return newVecServer(t, func(r vecRequest) (int, string) {
		switch {
		case strings.HasSuffix(r.Path, "/points/retrieve"):
			points := []string{}
			for _, id := range existing {
				points = append(points, `{"id":`+id+`,"payload":null}`)
			}
			return 200, `{"result":[` + strings.Join(points, ",") + `]}`
		case strings.HasSuffix(r.Path, "/points/count"):
			return 200, fmt.Sprintf(`{"result":{"count":%d}}`, count)
		case strings.HasSuffix(r.Path, "/points/delete"):
			return 200, `{"result":{"operation_id":9,"status":"completed"},"status":"ok"}`
		}
		t.Errorf("unexpected request %s %s", r.Method, r.Path)
		return 500, `{}`
	})
}

func paths(reqs []vecRequest) string {
	var out []string
	for _, r := range reqs {
		out = append(out, r.Method+" "+r.Path[strings.LastIndex(r.Path, "/points"):])
	}
	return strings.Join(out, ", ")
}

func TestPointsDeleteByIDsCountsTheOnesThatExistAndDeletesThose(t *testing.T) {
	s := deleteServer(t, []string{"17", `"5c56c793-69f3-4fbf-87e6-c4bf54c28c26"`}, 0)
	r, stdout, stderr, _ := s.run("", true)
	code := pointsDelete(r, "articles", []string{"17", "99", "5c56c793-69f3-4fbf-87e6-c4bf54c28c26"}, "", true, strings.NewReader(""), false)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr.String())
	}
	reqs := s.got()
	if got := paths(reqs); got != "POST /points/retrieve, POST /points/delete" {
		t.Fatalf("requests = %s; want the count (a retrieve), then the delete", got)
	}
	asked := reqs[0].Body["ids"].([]interface{})
	if len(asked) != 3 || asked[0] != 17.0 || asked[1] != 99.0 || reqs[0].Body["with_payload"] != false {
		t.Errorf("retrieve body = %v", reqs[0].Body)
	}
	// Only the ids that exist are deleted, so the count shown is the count
	// removed.
	sent := reqs[1].Body["points"].([]interface{})
	if len(sent) != 2 || sent[0] != 17.0 || sent[1] != "5c56c793-69f3-4fbf-87e6-c4bf54c28c26" {
		t.Errorf("delete body = %v", reqs[1].Body)
	}
	if _, has := reqs[1].Body["filter"]; has {
		t.Errorf("a delete by ids sent a filter: %v", reqs[1].Body)
	}
	if !strings.Contains(stderr.String(), "2 point(s) in collection 'articles' will be deleted.") {
		t.Errorf("the count was not shown before the delete: %q", stderr.String())
	}
	out := decode(t, stdout.String())
	if out["deleted"] != 2.0 || out["collection"] != "articles" || len(out) != 4 {
		t.Errorf("--json = %v; want endpoint, workspace, collection, deleted", out)
	}
}

func TestPointsDeleteByFilterCountsWithTheSameFilterFirst(t *testing.T) {
	s := deleteServer(t, nil, 5)
	r, stdout, stderr, _ := s.run("research", false)
	if code := pointsDelete(r, "articles", nil, langFilter, true, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr.String())
	}
	reqs := s.got()
	if got := paths(reqs); got != "POST /points/count, POST /points/delete" {
		t.Fatalf("requests = %s", got)
	}
	for _, req := range reqs {
		if !strings.HasPrefix(req.Path, "/api/v1/workspaces/research/collections/articles/") {
			t.Errorf("sent to %s", req.Path)
		}
		f, ok := req.Body["filter"].(map[string]interface{})
		if !ok || f["must"] == nil {
			t.Errorf("%s filter = %v, want the JSON object as given", req.Path, req.Body["filter"])
		}
	}
	if reqs[0].Body["exact"] != true {
		t.Errorf("the count is not exact: %v", reqs[0].Body)
	}
	if _, has := reqs[1].Body["points"]; has {
		t.Errorf("a delete by filter sent ids: %v", reqs[1].Body)
	}
	if !strings.Contains(stderr.String(), "5 point(s) in collection 'articles' will be deleted.") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if stdout.String() != "Deleted 5 point(s) from collection 'articles'.\n" {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestPointsDeleteAsksUnlessYes(t *testing.T) {
	t.Run("typing the name deletes", func(t *testing.T) {
		s := deleteServer(t, nil, 3)
		r, _, stderr, _ := s.run("", false)
		if code := pointsDelete(r, "articles", nil, langFilter, false, strings.NewReader("articles\n"), true); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if got := paths(s.got()); got != "POST /points/count, POST /points/delete" {
			t.Errorf("requests = %s", got)
		}
		// The count comes before the question.
		text := stderr.String()
		if c, q := strings.Index(text, "3 point(s)"), strings.Index(text, "Type the collection name"); c < 0 || q < c {
			t.Errorf("stderr = %q; want the count, then the question", text)
		}
	})
	t.Run("typing another name deletes nothing", func(t *testing.T) {
		s := deleteServer(t, nil, 3)
		r, _, _, _ := s.run("", false)
		if code := pointsDelete(r, "articles", nil, langFilter, false, strings.NewReader("article\n"), true); code != exitInputRefused {
			t.Errorf("exit %d", code)
		}
		if got := paths(s.got()); got != "POST /points/count" {
			t.Errorf("requests = %s; a mismatched name must not delete", got)
		}
	})
	t.Run("no terminal and no --yes is refused before any request", func(t *testing.T) {
		s := deleteServer(t, []string{"1"}, 3)
		r, stdout, stderr, connects := s.run("", true)
		if code := pointsDelete(r, "articles", []string{"1"}, "", false, strings.NewReader("articles\n"), false); code != exitInputRefused {
			t.Errorf("exit %d, want %d", code, exitInputRefused)
		}
		if len(s.got()) != 0 || *connects != 0 || stdout.Len() != 0 {
			t.Error("a refused delete resolved, sent or printed something")
		}
		e := decode(t, stderr.String())["error"].(map[string]interface{})
		if msg, _ := e["message"].(string); !strings.Contains(msg, "stdin is not a terminal") || !strings.Contains(msg, "--yes") {
			t.Errorf("the refusal should say why and how to proceed: %v", e)
		}
	})
	t.Run("nothing matching deletes nothing and asks nothing", func(t *testing.T) {
		s := deleteServer(t, nil, 0)
		r, stdout, stderr, _ := s.run("", true)
		if code := pointsDelete(r, "articles", []string{"99"}, "", false, strings.NewReader(""), true); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if got := paths(s.got()); got != "POST /points/retrieve" {
			t.Errorf("requests = %s; nothing to delete must send no delete", got)
		}
		if strings.Contains(stderr.String(), "Type the collection name") {
			t.Error("asked to confirm deleting nothing")
		}
		if out := decode(t, stdout.String()); out["deleted"] != 0.0 {
			t.Errorf("--json = %v", out)
		}
	})
}

func TestPointsDeleteRefusesBadSelectorsBeforeAnyRequest(t *testing.T) {
	cases := map[string]struct {
		ids    []string
		filter string
		want   string
	}{
		"ids and a filter":     {[]string{"1"}, langFilter, "give point ids or --filter, not both"},
		"neither":              {nil, "", "give the ids of the points to delete, or --filter"},
		"empty filter":         {nil, `{}`, "not deleting: an empty --filter matches every point in 'articles'. To delete the whole collection, run afy collections delete articles."},
		"no conditions":        {nil, `{"must":[],"should":null}`, "not deleting: an empty --filter matches every point in 'articles'. To delete the whole collection, run afy collections delete articles."},
		"filter not an object": {nil, `[1]`, "--filter must be a JSON object, got '[1]'"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := deleteServer(t, []string{"1"}, 1)
			r, _, stderr, connects := s.run("", true)
			if code := pointsDelete(r, "articles", tc.ids, tc.filter, true, strings.NewReader(""), true); code != exitInputRefused {
				t.Errorf("exit %d, want %d", code, exitInputRefused)
			}
			if len(s.got()) != 0 || *connects != 0 {
				t.Errorf("a refused delete still resolved (%d) or sent (%d)", *connects, len(s.got()))
			}
			if e := decode(t, stderr.String())["error"].(map[string]interface{}); e["message"] != tc.want {
				t.Errorf("error = %v\nwant %q", e["message"], tc.want)
			}
		})
	}
	// A filter with one real condition among empty ones is not empty.
	if !filterHasConditions(json.RawMessage(`{"must":[],"must_not":[{"key":"a","match":{"value":1}}]}`)) {
		t.Error("a filter with a must_not condition was read as empty")
	}
}

// The API's error on the delete itself, after the count went through (here the
// collection was deleted in between): exit 1, the code on stderr, nothing on
// stdout.
func TestPointsDeleteReportsTheDeletesOwnError(t *testing.T) {
	s := newVecServer(t, func(r vecRequest) (int, string) {
		if strings.HasSuffix(r.Path, "/points/count") {
			return 200, `{"result":{"count":4}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"Collection 'articles' not found"}}`
	})
	r, stdout, stderr, _ := s.run("", false)
	if code := pointsDelete(r, "articles", nil, langFilter, true, strings.NewReader(""), false); code != exitRequestFailed {
		t.Errorf("exit %d", code)
	}
	if stdout.Len() != 0 || !strings.HasSuffix(stderr.String(), "Error: Collection 'articles' not found (NOT_FOUND)\n") {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}
