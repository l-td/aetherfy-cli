package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// pagedServer answers the deployment list from pages keyed by the before_id it
// is asked for ("" = the first request), and records every before_id asked. It
// gives up (an empty last page) after maxAsks requests so that a client that
// would follow a cursor forever fails the test instead of hanging it.
type pagedServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	asked []string // the before_id of each request, in order
	limit []string // the limit of each request, in order
}

const pagedServerMaxAsks = 10

func newPagedServer(t *testing.T, pages map[string]string) *pagedServer {
	t.Helper()
	p := &pagedServer{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.asked = append(p.asked, r.URL.Query().Get("before_id"))
		p.limit = append(p.limit, r.URL.Query().Get("limit"))
		n := len(p.asked)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n > pagedServerMaxAsks {
			_, _ = w.Write([]byte(`{"deployments":[],"next_before_id":null}`))
			return
		}
		body, ok := pages[r.URL.Query().Get("before_id")]
		if !ok {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":{"code":"DEPLOYMENTS_INVALID_BEFORE_ID","message":"no such id"}}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pagedServer) requests() ([]string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...), append([]string(nil), p.limit...)
}

const (
	pageOne = `{"deployments":[` +
		`{"id":"d5","agent_id":"a1","version":5,"state":"active","created_at":"2026-10-05T10:00:00Z"},` +
		`{"id":"d4","agent_id":"a1","version":4,"state":"superseded","created_at":"2026-10-04T10:00:00Z"}],` +
		`"next_before_id":"d4"}`
	pageTwo = `{"deployments":[` +
		`{"id":"d3","agent_id":"a1","version":3,"state":"superseded","created_at":"2026-10-03T10:00:00Z"},` +
		`{"id":"d2","agent_id":"a1","version":2,"state":"failed","created_at":"2026-10-02T10:00:00Z"}],` +
		`"next_before_id":"d2"}`
	pageThree = `{"deployments":[` +
		`{"id":"d1","agent_id":"a1","version":1,"state":"superseded","created_at":"2026-10-01T10:00:00Z"}],` +
		`"next_before_id":null}`
)

func TestListDeploymentsFollowsEveryPageAndReturnsTheRowsInOrder(t *testing.T) {
	p := newPagedServer(t, map[string]string{"": pageOne, "d4": pageTwo, "d2": pageThree})
	rows, err := NewClientWithURL(p.srv.URL, "afy_test_key").ListDeployments("bot")
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d deployments, want all 5 from the three pages", len(rows))
	}
	for i, want := range []int{5, 4, 3, 2, 1} {
		if rows[i].Version != want {
			t.Errorf("row %d is v%d, want v%d (newest first across the pages)", i, rows[i].Version, want)
		}
	}
	asked, limits := p.requests()
	if len(asked) != 3 || asked[0] != "" || asked[1] != "d4" || asked[2] != "d2" {
		t.Errorf("before_id of each request = %q, want [\"\" \"d4\" \"d2\"]: each page continues after the last cursor", asked)
	}
	for i, l := range limits {
		if l != "100" {
			t.Errorf("request %d asked limit=%q, want the largest page, 100", i, l)
		}
	}
}

func TestListDeploymentsRefusesACursorItHasAlreadyFollowed(t *testing.T) {
	// The server keeps answering "d4" as the next page, also on the page d4
	// itself opens: followed blindly it never ends.
	loop := `{"deployments":[` +
		`{"id":"d3","agent_id":"a1","version":3,"state":"superseded","created_at":"2026-10-03T10:00:00Z"}],` +
		`"next_before_id":"d4"}`
	p := newPagedServer(t, map[string]string{"": pageOne, "d4": loop})
	rows, err := NewClientWithURL(p.srv.URL, "afy_test_key").ListDeployments("bot")
	var invalid *ResponseInvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("a repeated cursor must be a ResponseInvalidError, got rows=%d err=%v", len(rows), err)
	}
	if asked, _ := p.requests(); len(asked) != 2 {
		t.Errorf("the client made %d requests (%q), want 2: it must stop at the repeat", len(asked), asked)
	}
}

func TestListDeploymentsRefusesAnEmptyNextCursor(t *testing.T) {
	// "" is the first page's own position: following it starts over.
	empty := `{"deployments":[` +
		`{"id":"d5","agent_id":"a1","version":5,"state":"active","created_at":"2026-10-05T10:00:00Z"}],` +
		`"next_before_id":""}`
	p := newPagedServer(t, map[string]string{"": empty})
	_, err := NewClientWithURL(p.srv.URL, "afy_test_key").ListDeployments("bot")
	var invalid *ResponseInvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("an empty next cursor must be a ResponseInvalidError, got %v", err)
	}
	if asked, _ := p.requests(); len(asked) != 1 {
		t.Errorf("the client made %d requests (%q), want 1", len(asked), asked)
	}
}

func TestListDeploymentsPageSendsEveryQueryParameterAndOmitsTheEmptyOnes(t *testing.T) {
	cases := []struct {
		name  string
		query DeploymentsQuery
		want  map[string]string
	}{
		{"all four", DeploymentsQuery{Since: "7d", Status: "failed", Limit: 5, BeforeID: "d4"},
			map[string]string{"since": "7d", "status": "failed", "limit": "5", "before_id": "d4"}},
		{"none", DeploymentsQuery{}, map[string]string{}},
		{"limit only", DeploymentsQuery{Limit: 20}, map[string]string{"limit": "20"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = map[string]string{}
				for k, v := range r.URL.Query() {
					got[k] = v[0]
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"deployments":[],"next_before_id":null}`))
			}))
			defer srv.Close()
			if _, err := NewClientWithURL(srv.URL, "afy_test_key").ListDeploymentsPage("bot", tc.query); err != nil {
				t.Fatalf("ListDeploymentsPage: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Errorf("query = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("query %s = %q, want %q (all: %v)", k, got[k], v, got)
				}
			}
		})
	}
}
