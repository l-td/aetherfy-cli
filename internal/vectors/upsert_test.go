package vectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pointWithDim is a point whose vector has dim elements and whose estimate is
// therefore UpsertPointFramingBytes + dim*UpsertFloatJSONBytes.
func pointWithDim(id, dim int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%d,"vector":[%s]}`, id, strings.TrimSuffix(strings.Repeat("0,", dim), ",")))
}

func TestPointWireBytesIsTheSDKsEstimateAndNeverUnderVectordbs(t *testing.T) {
	cases := map[string]struct {
		point string
		want  int
	}{
		"no payload":   {`{"id":1,"vector":[0.5,0.25,1]}`, 100 + 3*18},
		"null payload": {`{"id":1,"vector":[1],"payload":null}`, 100 + 18},
		// Compacted, as JSON.stringify writes it: the spaces do not count.
		"payload": {`{"id":1,"vector":[1],"payload": {"lang": "en"}}`, 100 + 18 + len(`{"lang":"en"}`)},
		// "1e20" is 4 bytes typed and 21 as JSON.stringify writes it, so it is
		// counted at the longest a number can be written (24).
		"exponent number": {`{"id":1,"vector":[1],"payload":{"n":1e20}}`, 100 + 18 + len(`{"n":}`) + 24},
		// A vector that is not an array is counted at its own length.
		"named vectors": {`{"id":1,"vector":{"a":[1,2]}}`, 100 + len(`{"a":[1,2]}`)},
	}
	for name, tc := range cases {
		if got := PointWireBytes(json.RawMessage(tc.point)); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
	// What JSON.stringify writes for the exponent case is within the bound.
	if js := len(`{"n":100000000000000000000}`); 100+18+js > PointWireBytes(json.RawMessage(`{"id":1,"vector":[1],"payload":{"n":1e20}}`)) {
		t.Error("the estimate is under vectordb's measure of a payload with an exponent number")
	}
}

func TestUpsertBatchesSplitAtTheSDKsByteLimit(t *testing.T) {
	// 60 000 elements: 1 080 100 bytes a point, so 23 fit in 24 MiB and the
	// 24th would not.
	const dim = 60000
	size := UpsertPointFramingBytes + dim*UpsertFloatJSONBytes
	perBatch := UpsertMaxRequestBytes / size
	if perBatch != 23 {
		t.Fatalf("test arithmetic: %d points per batch", perBatch)
	}
	points := make([]json.RawMessage, 30)
	for i := range points {
		points[i] = pointWithDim(i, dim)
	}
	batches := UpsertBatches(points)
	if len(batches) != 2 || len(batches[0].Points) != 23 || len(batches[1].Points) != 7 {
		t.Fatalf("batches of %v, want 23 and 7", batchSizes(batches))
	}
	if batches[0].Bytes != 23*size || batches[0].Bytes > UpsertMaxRequestBytes {
		t.Errorf("first batch estimated at %d bytes", batches[0].Bytes)
	}
	if string(batches[1].Points[0]) != string(points[23]) {
		t.Error("the split reordered the points")
	}
}

func TestUpsertBatchesSplitAtThePointCapAndSendAnOversizePointAlone(t *testing.T) {
	small := make([]json.RawMessage, UpsertPointsMax+1)
	for i := range small {
		small[i] = pointWithDim(i, 1)
	}
	if got := batchSizes(UpsertBatches(small)); fmt.Sprint(got) != fmt.Sprint([]int{UpsertPointsMax, 1}) {
		t.Errorf("batches of %v", got)
	}
	// One point over the byte limit on its own: alone, never dropped.
	huge := UpsertMaxRequestBytes/UpsertFloatJSONBytes + 1
	mixed := []json.RawMessage{pointWithDim(1, 1), pointWithDim(2, huge), pointWithDim(3, 1)}
	if got := batchSizes(UpsertBatches(mixed)); fmt.Sprint(got) != "[1 1 1]" {
		t.Errorf("batches of %v, want the oversize point alone", got)
	}
}

func batchSizes(batches []UpsertBatch) []int {
	out := make([]int, len(batches))
	for i, b := range batches {
		out[i] = len(b.Points)
	}
	return out
}

func TestUpsertWaitsLongerThanVectordbMayTakeOverARequest(t *testing.T) {
	if UpsertTimeout != 100*time.Second || UpsertTimeout <= UpsertServerRequestTimeout {
		t.Errorf("UpsertTimeout %s, vectordb's request timeout %s", UpsertTimeout, UpsertServerRequestTimeout)
	}
	// Every request, small or a full 24 MiB, gets it; nothing else does.
	f := &fakeUpsert{}
	if _, err := clientWith(f).Upsert("articles", append(manyPoints(30, 60000), manyPoints(1, 1)...)); err != nil {
		t.Fatal(err)
	}
	for i, got := range f.timeouts {
		if got != UpsertTimeout {
			t.Errorf("request %d waited %s, want %s", i+1, got, UpsertTimeout)
		}
	}
	if New("http://x", "k", "").timeout != DefaultTimeout {
		t.Error("the other calls' timeout changed")
	}
}

// fakeUpsert answers the nth request (1-based) with answer, and records each
// request's points and timeout.
type fakeUpsert struct {
	answers  map[int]func() (int, []byte, error)
	points   []int
	timeouts []time.Duration
}

func (f *fakeUpsert) send(method, url string, body []byte, _ http.Header, timeout time.Duration) (int, []byte, error) {
	var b struct{ Points []json.RawMessage }
	_ = json.Unmarshal(body, &b)
	f.points = append(f.points, len(b.Points))
	f.timeouts = append(f.timeouts, timeout)
	if a, ok := f.answers[len(f.points)]; ok {
		return a()
	}
	return 200, []byte(`{"result":{"status":"completed"},"status":"ok"}`), nil
}

func clientWith(f *fakeUpsert) *Client {
	c := New("http://vectors.test", "k", "")
	c.send = f.send
	return c
}

func manyPoints(n, dim int) []json.RawMessage {
	out := make([]json.RawMessage, n)
	for i := range out {
		out[i] = pointWithDim(i, dim)
	}
	return out
}

func TestUpsertReportsTheOutcomeOfTheFailedRequest(t *testing.T) {
	refuse := func(status int) func() (int, []byte, error) {
		return func() (int, []byte, error) {
			return status, []byte(`{"error":{"code":"NOT_FOUND","message":"refused"}}`), nil
		}
	}
	lost := func() (int, []byte, error) { return 0, nil, fmt.Errorf("read: connection reset by peer") }
	// 46 points of 60 000 elements: two requests of 23, each about 24 MB,
	// over one vectordb chunk (12 MiB). 1-element points: two requests of the point
	// cap and 1, each far under it.
	cases := map[string]struct {
		points      []json.RawMessage
		answer      func() (int, []byte, error)
		written     int
		unconfirmed int
	}{
		"lost connection":              {manyPoints(UpsertPointsMax+1, 1), lost, UpsertPointsMax, 1},
		"server error":                 {manyPoints(UpsertPointsMax+1, 1), refuse(502), UpsertPointsMax, 1},
		"refusal of one chunk or less": {manyPoints(UpsertPointsMax+1, 1), refuse(400), UpsertPointsMax, 0},
		"refusal of a larger request":  {manyPoints(46, 60000), refuse(400), 23, 23},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeUpsert{answers: map[int]func() (int, []byte, error){2: tc.answer}}
			progress, err := clientWith(f).Upsert("articles", tc.points)
			if err == nil {
				t.Fatal("no error")
			}
			if progress.Written != tc.written || progress.Unconfirmed != tc.unconfirmed {
				t.Errorf("progress %+v, want written %d, unconfirmed %d", progress, tc.written, tc.unconfirmed)
			}
			if len(f.points) != 2 {
				t.Errorf("%d requests; nothing is sent after a failure", len(f.points))
			}
		})
	}
}

// A real timeout, over HTTP: the second request gets no answer in time. The
// first is written, the second's outcome is unknown.
func TestUpsertTimeoutOnTheSecondRequestIsUnconfirmed(t *testing.T) {
	var n int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 2 {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"status":"completed"},"status":"ok"}`))
	}))
	defer srv.Close()
	defer close(release)
	c := New(srv.URL, "k", "")
	c.upsertTimeout = 200 * time.Millisecond
	progress, err := c.Upsert("articles", manyPoints(UpsertPointsMax+5, 1))
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if progress != (UpsertProgress{Written: UpsertPointsMax, Unconfirmed: 5}) {
		t.Errorf("progress %+v", progress)
	}
}
