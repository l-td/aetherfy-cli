package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// answering is a stand-in control plane that answers every request the same way.
func answering(t *testing.T, status int, contentType, body string, headers map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClientWithURL(srv.URL, "")
}

const errBody = `{"detail":{"code":"DEVICE_POLLING_TOO_FAST","message":"Slow down."}}`

func TestRetryAfterIsReadFromTheHeaderInSeconds(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"9", 9 * time.Second},
		{" 3 ", 3 * time.Second},
		{"", 0},
		{"0", 0},
		{"-4", 0},
		{"soon", 0},
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0},
	} {
		headers := map[string]string{}
		if tc.header != "" {
			headers["Retry-After"] = tc.header
		}
		c := answering(t, 429, "application/json", errBody, headers)
		_, err := c.PollDevice("dc")
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("Retry-After %q: want an *APIError, got %v", tc.header, err)
		}
		if apiErr.RetryAfter != tc.want {
			t.Errorf("Retry-After %q: RetryAfter = %v, want %v", tc.header, apiErr.RetryAfter, tc.want)
		}
		if apiErr.Code != "DEVICE_POLLING_TOO_FAST" || apiErr.StatusCode != 429 {
			t.Errorf("code/status = %q/%d", apiErr.Code, apiErr.StatusCode)
		}
	}
}

func TestPollDevicePendingIsAnAnswerNotAnError(t *testing.T) {
	tok, err := answering(t, 202, "application/json", `{"status":"pending","interval":7}`, nil).PollDevice("dc")
	if err != nil || tok.Status != DeviceStatusPending || tok.Interval != 7 {
		t.Errorf("tok %+v, err %v", tok, err)
	}
}

func TestPollDeviceApprovedCarriesTheKeyAndItsRow(t *testing.T) {
	body := `{"status":"approved","api_key":"afy_live_x","key":{"id":"k","name":"n","key_prefix":"afy_live_x...","created_at":"2026-11-12T10:00:00Z","last_used_at":null,"is_system_managed":false}}`
	tok, err := answering(t, 200, "application/json", body, nil).PollDevice("dc")
	if err != nil || tok.Status != DeviceStatusApproved || tok.APIKey != "afy_live_x" || tok.Key == nil || tok.Key.ID != "k" {
		t.Errorf("tok %+v, err %v", tok, err)
	}
}

func TestPollDeviceRefusesAnAnswerItCannotActOn(t *testing.T) {
	for name, body := range map[string]string{
		"an approval with no key": `{"status":"approved"}`,
		"an unknown status":       `{"status":"escalated"}`,
		"no status":               `{}`,
	} {
		_, err := answering(t, 200, "application/json", body, nil).PollDevice("dc")
		var invalid *ResponseInvalidError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: want a *ResponseInvalidError, got %v", name, err)
		}
	}
}

func TestStartDeviceSendsKeyNameOnlyWhenGiven(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		bodies = append(bodies, string(buf[:n]))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"d","user_code":"U","verification_uri":"v","expires_in":60,"interval":5}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClientWithURL(srv.URL, "")
	if _, err := c.StartDevice("laptop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartDevice("laptop", "ci"); err != nil {
		t.Fatal(err)
	}
	if bodies[0] != `{"device_name":"laptop"}` || bodies[1] != `{"device_name":"laptop","key_name":"ci"}` {
		t.Errorf("bodies = %q", bodies)
	}
}

func TestListKeysKeepsTheServersDocumentAndRefusesWhatIsNotOne(t *testing.T) {
	doc := `{"keys":[{"id":"k","name":"n","key_prefix":"afy_live_x...","created_at":"2026-11-12T10:00:00Z","last_used_at":null,"is_system_managed":false,"extra":1}]}`
	list, err := answering(t, 200, "application/json", doc, nil).ListKeys()
	if err != nil || len(list.Keys) != 1 || list.Keys[0].ID != "k" {
		t.Fatalf("list %+v, err %v", list, err)
	}
	if string(list.Raw) != doc {
		t.Errorf("Raw = %s, want the body as written", list.Raw)
	}

	// A login page answered with 200: not the API.
	_, err = answering(t, 200, "text/html", "<html>Sign in</html>", nil).ListKeys()
	var notAPI *NotAPIResponseError
	if !errors.As(err, &notAPI) {
		t.Errorf("an HTML 200: want *NotAPIResponseError, got %v", err)
	}
	// JSON of a shape this afy cannot read.
	_, err = answering(t, 200, "application/json", `["a"]`, nil).ListKeys()
	var shape *UnexpectedShapeError
	if !errors.As(err, &shape) {
		t.Errorf("a list that is an array: want *UnexpectedShapeError, got %v", err)
	}
}

func TestGetUsageKeepsTheServersDocumentAndRefusesWhatIsNotOne(t *testing.T) {
	doc := `{"hasActivePeriod":true,"tier":"starter","gaugePercent":40,"gaugeLabel":"40% of plan","future":true}`
	u, err := answering(t, 200, "application/json", doc, nil).GetUsage()
	if err != nil || !u.HasActivePeriod || u.GaugePercent == nil || *u.GaugePercent != 40 {
		t.Fatalf("usage %+v, err %v", u, err)
	}
	if string(u.Raw) != doc {
		t.Errorf("Raw = %s, want the body as written", u.Raw)
	}
	if _, err := answering(t, 200, "text/html", "<html>x</html>", nil).GetUsage(); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("an HTML 200: err = %v", err)
	}
}

func TestRevokeKeyEscapesTheIDAndRelaysTheRefusal(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"detail":{"code":"API_KEY_NOT_FOUND","message":"No such key."}}`))
	}))
	t.Cleanup(srv.Close)
	err := NewClientWithURL(srv.URL, "").RevokeKey("a/b c")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "API_KEY_NOT_FOUND" || apiErr.StatusCode != 404 {
		t.Errorf("err = %v", err)
	}
	if path != "/keys/a%2Fb%20c" {
		t.Errorf("path = %q, want the id escaped", path)
	}
}
