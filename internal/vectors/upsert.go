package vectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Upsert: the SDKs' upsert (PUT .../points with {"points": [...]}), split into
// requests the way the SDKs split them, each waiting longer than vectordb may
// take over it, and reporting what is known to be written when one fails.
//
// Each number below is a copy, pinned to its source by aetherfy-e2e-tests
// tests/pyunit/test_index_timeouts_pair.py (the upsert gates), which reads
// this file as literal `const` lines.

// UpsertPointsMax is the most points one upsert request may carry: vectordb's
// DEFAULT_MAX_POINTS (backend/middleware/streamingPointsParser.js), which
// refuses a larger body 400 TOO_MANY_POINTS.
const UpsertPointsMax = 10000

// UpsertMaxRequestBytes is the most one upsert request carries, by
// PointWireBytes: the SDKs' MAX_REQUEST_BYTES (python aetherfy_vectors/
// chunking.py, js src/utils/chunking.ts).
const UpsertMaxRequestBytes = 24 * 1024 * 1024

// The per-point size estimate, the SDKs' point_wire_bytes and vectordb's
// pointWireBytes (backend/services/chunking.js): framing, plus 18 bytes per
// vector element, plus the payload's JSON.
const UpsertPointFramingBytes = 100
const UpsertFloatJSONBytes = 18

// UpsertServerChunkBytes is vectordb's CHUNK_TARGET_BYTES (backend/services/
// chunking.js): vectordb writes an upsert to Qdrant in chunks of at most this
// many bytes by the same estimate, flushing each one while it still reads the
// request. A refusal that comes after a flush leaves the flushed chunks
// written (backend/services/streamingUpsertHandler.js: the pre-flush at the
// `chunkBytes + pointBytes > CHUNK_TARGET_BYTES` check, then the per-point
// refusals after it, "already-flushed chunks stay"). So a 4xx proves nothing
// was written only for a request no larger than one chunk.
const UpsertServerChunkBytes = 12 * 1024 * 1024

// UpsertServerRequestTimeout is vectordb's server.requestTimeout
// (backend/server.js): the longest vectordb lets any one request run before
// it ends it. Every upsert request waits that long plus UpsertTimeoutMargin
// (the client's own hop), so vectordb always answers or ends the request
// before the CLI gives up: an upsert the server completes is never reported
// as unconfirmed for want of waiting. A dropped connection or a server error
// still is.
const UpsertServerRequestTimeout = 90 * time.Second
const UpsertTimeoutMargin = 10 * time.Second

// UpsertTimeout is each upsert request's timeout.
const UpsertTimeout = UpsertServerRequestTimeout + UpsertTimeoutMargin

// jsNumberMaxLen is the longest JSON.stringify writes a number in
// ("-1.7976931348623157e+308"). vectordb measures a payload as
// JSON.stringify re-writes it, and only a number in exponent form can come
// out longer than it was typed ("1e20" is written as 21 digits).
const jsNumberMaxLen = 24

// PointWireBytes is a point's size by the SDKs' and vectordb's estimate, and
// never less than vectordb's: the payload is measured compacted, as given,
// with every exponent-form number counted at the longest JSON.stringify could
// write it. (vectordb also drops the attested keys before measuring, which
// only makes its figure smaller.) Anything the estimate does not cover (a
// vector that is not an array) is counted at its own length.
func PointWireBytes(point json.RawMessage) int {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(point, &obj); err != nil {
		return len(point)
	}
	n := UpsertPointFramingBytes
	if v, ok := obj["vector"]; ok {
		var elems []json.RawMessage
		if json.Unmarshal(v, &elems) == nil {
			n += len(elems) * UpsertFloatJSONBytes
		} else {
			n += len(v)
		}
	}
	if p, ok := obj["payload"]; ok && string(p) != "null" {
		n += jsonUpperBound(p)
	}
	return n
}

// jsonUpperBound is raw's compacted length, with each exponent-form number
// counted at jsNumberMaxLen.
func jsonUpperBound(raw json.RawMessage) int {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return len(raw)
	}
	n := buf.Len()
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if err != nil {
			return n
		}
		if num, ok := tok.(json.Number); ok && strings.ContainsAny(string(num), "eE") && len(num) < jsNumberMaxLen {
			n += jsNumberMaxLen - len(num)
		}
	}
}

// UpsertBatch is one request's points and their estimated size.
type UpsertBatch struct {
	Points []json.RawMessage
	Bytes  int
}

// UpsertBatches splits points into requests in order, the SDKs'
// chunk_points_by_bytes plus vectordb's point cap: a request ends before a
// point that would take it past UpsertMaxRequestBytes or UpsertPointsMax. A
// single point larger than the byte limit goes alone (the server decides).
func UpsertBatches(points []json.RawMessage) []UpsertBatch {
	var out []UpsertBatch
	var cur UpsertBatch
	for _, p := range points {
		size := PointWireBytes(p)
		if len(cur.Points) > 0 && (cur.Bytes+size > UpsertMaxRequestBytes || len(cur.Points) == UpsertPointsMax) {
			out = append(out, cur)
			cur = UpsertBatch{}
		}
		cur.Points = append(cur.Points, p)
		cur.Bytes += size
	}
	if len(cur.Points) > 0 {
		out = append(out, cur)
	}
	return out
}

// UpsertProgress is what an upsert is known to have done. Written points were
// confirmed by the server. Unconfirmed points were in the request that failed
// with an outcome nobody can know from here: some, all or none of them may be
// written.
type UpsertProgress struct {
	Written     int
	Unconfirmed int
}

// outcomeUnknown reports whether a failed request of size bytes may have
// written some of its points: no answer (a timeout, a lost connection), a
// server error, or a refusal of a request larger than one vectordb chunk.
func outcomeUnknown(err error, size int) bool {
	var te *TimeoutError
	var ne *NetworkError
	if errors.As(err, &te) || errors.As(err, &ne) {
		return true
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return false // never sent (the body did not encode)
	}
	if ae.Status >= 500 {
		return true
	}
	return size > UpsertServerChunkBytes
}

// Upsert adds or replaces points by id, in UpsertBatches order, one request
// at a time, not retried, stopping at the first failure. Every rule on a
// point's contents is the server's.
func (c *Client) Upsert(collection string, points []json.RawMessage) (UpsertProgress, error) {
	var progress UpsertProgress
	for _, batch := range UpsertBatches(points) {
		body := map[string]interface{}{"points": batch.Points}
		err := c.do(http.MethodPut, c.collectionPath(collection, "/points"), body, c.upsertTimeout, nil)
		if err != nil {
			if outcomeUnknown(err, batch.Bytes) {
				progress.Unconfirmed = len(batch.Points)
			}
			return progress, err
		}
		progress.Written += len(batch.Points)
	}
	return progress, nil
}
