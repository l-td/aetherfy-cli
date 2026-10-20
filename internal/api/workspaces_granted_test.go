package api

import (
	"encoding/json"
	"testing"
	"time"
)

// GET /workspaces/{name}/granted-agents (CP c862ccc) serialises granted_at the
// way pydantic does a timezone-aware datetime: microseconds and an offset
// ("...123456+00:00"), not the "Z" a hand-written stand-in tends to use. Go's
// time.Time reads RFC 3339 only WITH a zone, so each form the route can answer
// with must decode, and to the same instant.
func TestGrantedAtDecodesEveryFormTheControlPlaneSends(t *testing.T) {
	want := time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC)
	for _, c := range []struct {
		at string
		d  time.Duration // extra, beyond want
	}{
		{"2026-10-13T09:30:00.123456+00:00", 123456 * time.Microsecond},
		{"2026-10-13T09:30:00+00:00", 0},
		{"2026-10-13T09:30:00Z", 0},
		{"2026-10-13T11:30:00+02:00", 0}, // the same instant, another offset
	} {
		var got []GrantedAgent
		body := `[{"id":"7d1c2f4e-0000-4000-8000-000000000001","name":"support-bot","granted_at":"` + c.at + `"}]`
		if err := json.Unmarshal([]byte(body), &got); err != nil || len(got) != 1 {
			t.Errorf("%s: %v %+v", c.at, err, got)
			continue
		}
		if !got[0].GrantedAt.Equal(want.Add(c.d)) {
			t.Errorf("%s decoded to %v, want %v", c.at, got[0].GrantedAt, want.Add(c.d))
		}
		if got[0].ID == "" || got[0].Name != "support-bot" {
			t.Errorf("%s: id/name lost: %+v", c.at, got[0])
		}
	}
}
