package maxim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Log repository reads for bulk trace import (lane Z06), as Maxim's public
// OpenAPI description states them (read 22 Nov 2026,
// https://www.getmaxim.ai/docs/public-apis/openapi/log-repositories.json):
//
//   - POST /v1/log-repositories/logs/search {workspaceId, id, type "trace",
//     timestamp {gte, lte}, traceFilters, evaluatorFilters, page, limit (at
//     most 50), sorting} answers {data: {logs: [...]}, pagination: {total}};
//   - GET /v1/log-repositories/logs/traces?id= answers {data: trace}.
//
// No real Maxim has been called; the tests use a local fake.

const (
	searchPath = "/v1/log-repositories/logs/search"
	tracePath  = "/v1/log-repositories/logs/traces"
	// SearchPageSize is the largest page Maxim's search returns.
	SearchPageSize = 50
	maxTraceAnswer = 16 << 20
)

// LogSearch is one search of a log repository. Exactly one of Errors and
// Evaluator narrows it; neither lists every trace in the window.
type LogSearch struct {
	WorkspaceID string
	RepoID      string
	From, To    string // RFC 3339
	Errors      bool   // only traces Maxim marks isError
	Evaluator   string // only traces this evaluator failed
	Page        int
}

// Evaluation is one evaluator's result on a trace.
type Evaluation struct {
	EvaluatorName string `json:"evaluatorName"`
	Pass          any    `json:"pass"`
	Score         any    `json:"score"`
	Reasoning     string `json:"reasoning"`
}

// Failed reports whether the evaluator failed the trace.
func (e Evaluation) Failed() bool {
	p := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Pass)))
	return p == "false" || p == "fail" || p == "failed"
}

// ScoreValue is the numeric score, and false when the evaluator gave none.
func (e Evaluation) ScoreValue() (float64, bool) {
	switch v := e.Score.(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// LogEntry is one search result.
type LogEntry struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	StartTimestamp string       `json:"startTimestamp"`
	SessionID      string       `json:"sessionId"`
	IsError        any          `json:"isError"`
	EvaluationData []Evaluation `json:"evaluationData"`
}

// SearchPage is one page of a search.
type SearchPage struct {
	Logs  []LogEntry
	Total int
}

// SearchLogs reads one page of a log repository search.
func (c Client) SearchLogs(ctx context.Context, apiKey string, q LogSearch) (*SearchPage, error) {
	body := map[string]any{"workspaceId": q.WorkspaceID, "id": q.RepoID, "type": "trace",
		"timestamp": map[string]any{"gte": q.From, "lte": q.To},
		"sorting":   map[string]any{"columnName": "startTimestamp", "desc": true},
		"page":      q.Page, "limit": SearchPageSize}
	switch {
	case q.Errors:
		body["traceFilters"] = map[string]any{"combinator": "all", "filters": []any{
			map[string]any{"columnName": "isError", "operator": "=", "value": "true"}}}
	case q.Evaluator != "":
		body["evaluatorFilters"] = map[string]any{"combinator": "all", "filters": []any{
			map[string]any{"evaluatorName": q.Evaluator, "operator": "is", "value": "fail"}}}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	data, err := c.call(ctx, apiKey, http.MethodPost, c.base()+searchPath, raw, maxAnswer)
	if err != nil {
		return nil, fmt.Errorf("maxim logs search: %w", err)
	}
	var ans struct {
		Data struct {
			Logs []LogEntry `json:"logs"`
		} `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(data, &ans); err != nil {
		return nil, fmt.Errorf("maxim logs search: answer is not a log page: %w", err)
	}
	return &SearchPage{Logs: ans.Data.Logs, Total: ans.Pagination.Total}, nil
}

// Trace reads one trace and returns its data object as Maxim sent it.
func (c Client) Trace(ctx context.Context, apiKey, traceID string) (json.RawMessage, error) {
	data, err := c.call(ctx, apiKey, http.MethodGet, c.base()+tracePath+"?"+url.Values{"id": {traceID}}.Encode(), nil, maxTraceAnswer)
	if err != nil {
		return nil, fmt.Errorf("maxim trace %s: %w", traceID, err)
	}
	var ans struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &ans); err != nil || len(ans.Data) == 0 || string(ans.Data) == "null" {
		return nil, fmt.Errorf("maxim trace %s: answer holds no trace", traceID)
	}
	return ans.Data, nil
}

func (c Client) call(ctx context.Context, apiKey, method, u string, body []byte, limit int64) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-maxim-api-key", apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s", redactKey(err.Error(), apiKey))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read answer: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("answer is over %d bytes", limit)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized
	case resp.StatusCode/100 != 2:
		return nil, fmt.Errorf("maxim answered %d: %s", resp.StatusCode, redactKey(trimBody(data), apiKey))
	}
	return data, nil
}
