package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"

	"github.com/go-resty/resty/v2"
)

// What a 200 that does not decode MEANS, and why it is never retried.
//
// A retry can only help when the answer might differ the second time: a connection that failed, a
// timeout, a read cut off mid-body. A response whose whole body ARRIVED and then did not decode is
// the same bytes again, so the two retries (1 s apart) only delayed the error. The two shapes of it
// say different things to the person, so they are two errors:
//
//   - NotAPIResponseError: the body is not JSON at all (text/html from a Wi-Fi login page or a
//     proxy). The answer did not come from the Aetherfy API: check the network or proxy.
//   - UnexpectedShapeError: the body is JSON, of a shape this version of afy cannot read. The server
//     is newer than this binary: update afy.
//
// Both exit with the code of a failed operation (1), the CLI's documented class for it.

// NotAPIResponseError is a 2xx answer whose content type is not JSON.
type NotAPIResponseError struct {
	ContentType string
}

func (e *NotAPIResponseError) Error() string {
	ct := e.ContentType
	if ct == "" {
		ct = "no content type"
	}
	return fmt.Sprintf("the answer did not come from the Aetherfy API: it is %s, not JSON. "+
		"Check the network or proxy (a Wi-Fi login page or a proxy answers like this)", ct)
}

// UnexpectedShapeError is a JSON answer this version of afy cannot decode.
type UnexpectedShapeError struct {
	Err error
}

func (e *UnexpectedShapeError) Error() string {
	return fmt.Sprintf("the server answered in a format this version of afy does not understand (%v). "+
		"Update afy: run 'afy upgrade'", e.Err)
}

func (e *UnexpectedShapeError) Unwrap() error { return e.Err }

// isDecodeError reports whether err is the failure to decode a body that fully arrived.
func isDecodeError(err error) bool {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &typ)
}

// requestError is the error of a request that did not produce a response, or whose response body
// did not decode.
func requestError(err error) error {
	if isDecodeError(err) {
		return &UnexpectedShapeError{Err: err}
	}
	return fmt.Errorf("request failed: %w", err)
}

// notJSON returns the offending content type when a 2xx answer to a request that expected a decoded
// result is not JSON. resty decodes only JSON/XML content types and leaves the result untouched for
// any other, so without this a login page would read as an empty, successful answer.
func notJSON(resp *resty.Response) (string, bool) {
	if resp.IsError() || resp.Request == nil || resp.Request.Result == nil || len(resp.Body()) == 0 {
		return "", false
	}
	if resp.StatusCode() == 204 {
		return "", false
	}
	ct := resp.Header().Get("Content-Type")
	if resty.IsJSONType(ct) {
		return "", false
	}
	media, _, err := mime.ParseMediaType(ct)
	if err != nil {
		media = strings.TrimSpace(ct)
	}
	return media, true
}
