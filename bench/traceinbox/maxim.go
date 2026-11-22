package traceinbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// The Maxim source reads Maxim's public API as its OpenAPI description
// states it (https://www.getmaxim.ai/docs/public-apis/openapi/log-repositories.json,
// read 22 Nov 2026):
//
//   - POST /v1/log-repositories/logs/search with workspaceId, id (the log
//     repository), type "trace", timestamp {gte, lte}, traceFilters and
//     evaluatorFilters ({combinator, filters: [{evaluatorName, operator
//     "is", value "fail"}]} or [{columnName "isError", operator "=", value
//     "true"}]), page, limit (at most 50) and sorting; answer {data: {logs:
//     [...]}, pagination: {total, page, limit}}.
//   - GET /v1/log-repositories/logs/traces?id= reads one trace: input,
//     output, timeline (spans with model, messages, completionResult) and
//     evaluationData.
//   - Auth: the x-maxim-api-key header.
//
// The span shapes inside timeline are only loosely specified; the
// conversion below reads OpenAI-style messages and is best effort. No real
// Maxim has been called; the tests use a local fake of these endpoints.

// DefaultMaximHost is the Maxim API a source without a host reads.
const DefaultMaximHost = "https://api.getmaxim.ai"

const (
	maximPage     = 50
	maximMaxPages = 40
)

type maximFetcher struct {
	c      *apiClient
	getenv func(string) string
}

// maximLog is one search result.
type maximLog struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	StartTimestamp string         `json:"startTimestamp"`
	SessionID      string         `json:"sessionId"`
	Tags           map[string]any `json:"tags"`
	EvaluationData []maximEval    `json:"evaluationData"`
}

type maximEval struct {
	EvaluatorName string `json:"evaluatorName"`
	Pass          any    `json:"pass"`
	Score         any    `json:"score"`
	Reasoning     string `json:"reasoning"`
}

// maximTrace is GET /v1/log-repositories/logs/traces.
type maximTrace struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Input          any             `json:"input"`
	Output         any             `json:"output"`
	Timeline       []maximSpan     `json:"timeline"`
	EvaluationData []maximEval     `json:"evaluationData"`
	Tags           json.RawMessage `json:"tags"`
}

type maximSpan struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	Model            string          `json:"model"`
	Messages         []any           `json:"messages"`
	CompletionResult json.RawMessage `json:"completionResult"`
	Status           string          `json:"status"`
	Error            any             `json:"error"`
	Input            any             `json:"input"`
	Output           any             `json:"output"`
	Args             any             `json:"args"`
	Result           any             `json:"result"`
	Spans            []maximSpan     `json:"spans"`
	Children         []maximSpan     `json:"children"`
}

// Fetch implements Fetcher.
func (f *maximFetcher) Fetch(ctx context.Context, src *Source, cur Cursor, now time.Time) (*Batch, error) {
	key := f.getenv(src.APIKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("maxim source %s: set %s", src.Name, src.APIKeyEnv)
	}
	hdr := http.Header{"X-Maxim-Api-Key": {key}}
	host := strings.TrimRight(firstNonEmpty(src.Host, DefaultMaximHost), "/")
	from := windowStart(src, cur, now)
	cs := newCandidates(src.maxPerPull(), cur)
	b := &Batch{Cursor: cur}
	b.Cursor.Since = now
	base := map[string]any{"workspaceId": src.Workspace, "id": src.Project, "type": "trace",
		"timestamp": map[string]any{"gte": from.UTC().Format(time.RFC3339), "lte": now.UTC().Format(time.RFC3339)},
		"sorting":   map[string]any{"columnName": "startTimestamp", "desc": true}}
	if src.Filters.Errors {
		body := clone(base)
		body["traceFilters"] = map[string]any{"combinator": "all", "filters": []any{
			map[string]any{"columnName": "isError", "operator": "=", "value": "true"}}}
		if err := f.search(ctx, host, hdr, body, cs, FlagError, func(maximLog) string { return "trace errored in Maxim" }); err != nil {
			return nil, err
		}
	}
	for _, name := range src.Filters.Evaluators {
		body := clone(base)
		body["evaluatorFilters"] = map[string]any{"combinator": "all", "filters": []any{
			map[string]any{"evaluatorName": name, "operator": "is", "value": "fail"}}}
		if err := f.search(ctx, host, hdr, body, cs, FlagEvaluator+":"+name, func(l maximLog) string {
			reason := "Maxim evaluator " + name + " failed"
			for _, e := range l.EvaluationData {
				if e.EvaluatorName == name && e.Reasoning != "" {
					reason += ": " + oneLine(e.Reasoning, 160)
				}
			}
			return reason
		}); err != nil {
			return nil, err
		}
	}
	if b.Truncated = cs.full; b.Truncated {
		b.holdAt(from.Add(src.overlap()))
	}
	for _, c := range cs.list() {
		s, err := f.trace(ctx, host, hdr, c.id)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			b.Warnings = append(b.Warnings, fmt.Sprintf("trace %s: %v", c.id, err))
			b.holdAt(c.at.Add(src.overlap()))
			continue
		}
		b.Sessions = append(b.Sessions, Flagged{Session: s, TraceID: c.id, At: c.at, Reasons: c.reasons, Kinds: c.kinds,
			Extra: map[string]string{"workspace": src.Workspace}})
	}
	return b, nil
}

// search pages POST /v1/log-repositories/logs/search and flags each trace.
func (f *maximFetcher) search(ctx context.Context, host string, hdr http.Header, body map[string]any, cs *candidates, kind string, reason func(maximLog) string) error {
	body["limit"] = maximPage
	for page := 0; page < maximMaxPages; page++ {
		body["page"] = page
		var ans struct {
			Data struct {
				Logs []maximLog `json:"logs"`
			} `json:"data"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		}
		if err := f.c.doJSON(ctx, http.MethodPost, host+"/v1/log-repositories/logs/search", hdr, body, &ans); err != nil {
			return fmt.Errorf("maxim logs search: %w", err)
		}
		for _, l := range ans.Data.Logs {
			if !cs.add(l.ID, parseRFC3339(l.StartTimestamp), reason(l), kind) {
				return nil
			}
		}
		if len(ans.Data.Logs) < maximPage || (page+1)*maximPage >= ans.Pagination.Total {
			return nil
		}
	}
	cs.full = true
	return nil
}

// trace reads one trace and converts it to a chat transcript that
// traceimport reads.
func (f *maximFetcher) trace(ctx context.Context, host string, hdr http.Header, traceID string) (*traceimport.Session, error) {
	var ans struct {
		Data maximTrace `json:"data"`
	}
	if err := f.c.doJSON(ctx, http.MethodGet, host+"/v1/log-repositories/logs/traces?"+url.Values{"id": {traceID}}.Encode(), hdr, nil, &ans); err != nil {
		return nil, err
	}
	if ans.Data.ID == "" {
		return nil, fmt.Errorf("maxim has no trace %s", traceID)
	}
	data, err := json.Marshal(maximTranscript(traceID, &ans.Data))
	if err != nil {
		return nil, err
	}
	tr, err := traceimport.Parse(data, traceimport.FormatChat)
	if err != nil {
		return nil, err
	}
	return tr.Sessions[0], nil
}

// maximTranscript builds an OpenAI-style conversation from a trace: the
// first generation's messages (or the trace input as the user's request),
// each tool span as a call and its result, and the trace output as the final
// answer.
func maximTranscript(traceID string, t *maximTrace) map[string]any {
	var msgs []any
	model := ""
	var spans []maximSpan
	var walk func([]maximSpan)
	walk = func(ss []maximSpan) {
		for _, s := range ss {
			spans = append(spans, s)
			walk(s.Spans)
			walk(s.Children)
		}
	}
	walk(t.Timeline)
	for _, s := range spans {
		if strings.EqualFold(s.Type, "generation") && len(s.Messages) > 0 {
			for _, m := range s.Messages {
				if msg := chatMessage(m); msg != nil {
					msgs = append(msgs, msg)
				}
			}
			model = s.Model
			break
		}
	}
	if len(msgs) == 0 {
		if in := textValue(t.Input); in != "" {
			msgs = append(msgs, map[string]any{"role": "user", "content": in})
		}
	}
	errText := ""
	for _, s := range spans {
		if !strings.Contains(strings.ToLower(s.Type), "tool") {
			continue
		}
		args, _ := json.Marshal(firstNonNil(s.Args, s.Input))
		if args == nil || string(args) == "null" {
			args = []byte("{}")
		}
		msgs = append(msgs, map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
			map[string]any{"id": s.ID, "type": "function", "function": map[string]any{"name": s.Name, "arguments": string(args)}}}})
		result := textValue(firstNonNil(s.Result, s.Output))
		isErr := strings.EqualFold(s.Status, "error") || s.Error != nil
		if isErr {
			result = "Error: " + firstNonEmpty(textValue(s.Error), result, "tool call failed")
			errText = firstNonEmpty(errText, result)
		}
		msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": s.ID, "name": s.Name, "content": result, "is_error": isErr})
	}
	if out := textValue(t.Output); out != "" {
		msgs = append(msgs, map[string]any{"role": "assistant", "content": out})
	}
	conv := map[string]any{"id": traceID, "name": t.Name, "model": model, "messages": msgs}
	for _, e := range t.EvaluationData {
		if fmt.Sprint(e.Pass) == "false" || strings.EqualFold(fmt.Sprint(e.Pass), "fail") {
			conv["status"] = "error"
			conv["error"] = firstNonEmpty(errText, "Maxim evaluator "+e.EvaluatorName+" failed")
			break
		}
	}
	if errText != "" && conv["status"] == nil {
		conv["status"], conv["error"] = "error", errText
	}
	return conv
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// MaximSession converts one Maxim trace (the data object of GET
// /v1/log-repositories/logs/traces) into a session, as a pull does. The
// session is not yet redacted; Take redacts it before it is stored.
func MaximSession(traceID string, data json.RawMessage) (*traceimport.Session, error) {
	var t maximTrace
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("maxim trace %s: %w", traceID, err)
	}
	raw, err := json.Marshal(maximTranscript(traceID, &t))
	if err != nil {
		return nil, err
	}
	tr, err := traceimport.Parse(raw, traceimport.FormatChat)
	if err != nil {
		return nil, fmt.Errorf("maxim trace %s: %w", traceID, err)
	}
	return tr.Sessions[0], nil
}
