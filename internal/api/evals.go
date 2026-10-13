package api

// Agent evals (the control plane's api/routes/evals.py): datasets and their
// versions, evals of an agent's current release, their cases, cancel and the
// comparison of two evals. Every route answers 503 EVALS_NOT_ENABLED while
// evals are switched off on the control plane.

import (
	"fmt"
	"net/url"
	"strconv"
)

func evalsPath(agent string, format string, args ...interface{}) string {
	return fmt.Sprintf("/agents/%s", url.PathEscape(agent)) + fmt.Sprintf(format, args...)
}

// ListEvalDatasets is GET /agents/{a}/eval-datasets.
func (c *Client) ListEvalDatasets(agent string) ([]EvalDataset, error) {
	var out []EvalDataset
	return out, c.Get(evalsPath(agent, "/eval-datasets"), &out)
}

// GetEvalDataset is GET /agents/{a}/eval-datasets/{name}: the dataset and its
// version summaries. 404 EVAL_DATASET_NOT_FOUND.
func (c *Client) GetEvalDataset(agent, name string) (*EvalDataset, error) {
	var out EvalDataset
	if err := c.Get(evalsPath(agent, "/eval-datasets/%s", url.PathEscape(name)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateEvalDataset is POST /agents/{a}/eval-datasets. 409 EVAL_DATASET_EXISTS.
func (c *Client) CreateEvalDataset(agent, name, description string) (*EvalDataset, error) {
	body := map[string]interface{}{"name": name}
	if description != "" {
		body["description"] = description
	}
	var out EvalDataset
	if err := c.Post(evalsPath(agent, "/eval-datasets"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteEvalDataset is DELETE /agents/{a}/eval-datasets/{name}. 409
// EVAL_DATASET_IN_USE while an eval of it is active.
func (c *Client) DeleteEvalDataset(agent, name string) error {
	return c.Delete(evalsPath(agent, "/eval-datasets/%s", url.PathEscape(name)))
}

// PushEvalDatasetVersion is POST /agents/{a}/eval-datasets/{name}/versions.
// 413 EVAL_DATASET_TOO_LARGE, 422 EVAL_DATASET_INVALID (with violations),
// EVAL_GRADER_INVALID and EVAL_JUDGE_HOST_NOT_ALLOWED.
func (c *Client) PushEvalDatasetVersion(agent, name string, body *EvalDatasetVersionCreate) (*EvalDatasetVersion, error) {
	var out EvalDatasetVersion
	if err := c.Post(evalsPath(agent, "/eval-datasets/%s/versions", url.PathEscape(name)), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetEvalDatasetVersion is GET .../versions/{n}: the version, its graders and
// one page of cases after afterOrdinal.
func (c *Client) GetEvalDatasetVersion(agent, name string, version, limit, afterOrdinal int) (*EvalDatasetVersion, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("after_ordinal", strconv.Itoa(afterOrdinal))
	var out EvalDatasetVersion
	path := evalsPath(agent, "/eval-datasets/%s/versions/%d?%s", url.PathEscape(name), version, q.Encode())
	if err := c.Get(path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartEval is POST /agents/{a}/evals (202).
func (c *Client) StartEval(agent string, body *EvalRunCreate) (*EvalRun, error) {
	var out EvalRun
	if err := c.Post(evalsPath(agent, "/evals"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EvalsQuery narrows ListEvals. Zero values are omitted.
type EvalsQuery struct {
	Dataset        string
	ReleaseVersion int
	Status         string
	Limit          int
}

// ListEvals is GET /agents/{a}/evals, newest first.
func (c *Client) ListEvals(agent string, q EvalsQuery) ([]EvalRun, error) {
	params := url.Values{}
	if q.Dataset != "" {
		params.Set("dataset", q.Dataset)
	}
	if q.ReleaseVersion > 0 {
		params.Set("release_version", strconv.Itoa(q.ReleaseVersion))
	}
	if q.Status != "" {
		params.Set("status", q.Status)
	}
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	path := evalsPath(agent, "/evals")
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out []EvalRun
	return out, c.Get(path, &out)
}

// GetEval is GET /agents/{a}/evals/{id}: progress and a live or final summary.
func (c *Client) GetEval(agent, evalID string) (*EvalRun, error) {
	var out EvalRun
	if err := c.Get(evalsPath(agent, "/evals/%s", url.PathEscape(evalID)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListEvalCases is GET /agents/{a}/evals/{id}/cases; verdict "" is every case.
func (c *Client) ListEvalCases(agent, evalID, verdict string, limit, afterOrdinal int) (*EvalCasePage, error) {
	q := url.Values{}
	if verdict != "" {
		q.Set("verdict", verdict)
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("after_ordinal", strconv.Itoa(afterOrdinal))
	var out EvalCasePage
	path := evalsPath(agent, "/evals/%s/cases?%s", url.PathEscape(evalID), q.Encode())
	if err := c.Get(path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelEval is POST /agents/{a}/evals/{id}/cancel. 409 EVAL_NOT_CANCELLABLE.
func (c *Client) CancelEval(agent, evalID string) (*EvalRun, error) {
	var out EvalRun
	if err := c.Post(evalsPath(agent, "/evals/%s/cancel", url.PathEscape(evalID)), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CompareEvals is GET /agents/{a}/eval-comparisons. 409 EVAL_COMPARE_INCOMPATIBLE.
func (c *Client) CompareEvals(agent, base, head string) (*EvalComparison, error) {
	q := url.Values{}
	q.Set("base", base)
	q.Set("head", head)
	var out EvalComparison
	if err := c.Get(evalsPath(agent, "/eval-comparisons?%s", q.Encode()), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
