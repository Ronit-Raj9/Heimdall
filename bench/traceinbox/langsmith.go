package traceinbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// The LangSmith source reads the API as its OpenAPI description states it
// (https://api.smith.langchain.com/openapi.json, version 0.1.0, read 18 Nov
// 2026) and the filter syntax page
// (https://docs.langchain.com/langsmith/trace-query-syntax):
//
//   - POST /api/v2/runs/query with project_ids, is_root, has_error, filter
//     (e.g. and(eq(feedback_key, "user_score"), lt(feedback_score, 0.5))),
//     min_start_time, max_start_time, min_feedback_modified_at, page_size
//     (max 1000), selects (ID, TRACE_ID, NAME, START_TIME, STATUS, ERROR,
//     ...), cursor; answer {items: [...], next_cursor}, newest first.
//   - GET /api/v2/traces/{trace_id}/runs with project_id, min_start_time,
//     max_start_time and repeated selects; answer {items} in start order.
//     Self-hosted LangSmith needs v0.16 or later for both.
//   - Auth: the X-API-Key header; X-Tenant-Id selects a workspace.
//
// Run inputs and outputs are free JSON whose shape depends on the framework
// that traced them; the conversion below reads the common shapes (messages
// lists, LangChain message objects, input or question strings, tool runs)
// and is best effort. No real LangSmith has been called; the tests use a
// local fake of these endpoints.

const (
	langsmithPage       = 100
	langsmithMaxPages   = 50
	langsmithFeedbackLB = 7 * 24 * time.Hour // how far back a run with new feedback may have started
)

var langsmithRootSelects = []string{"ID", "TRACE_ID", "NAME", "START_TIME", "STATUS", "ERROR"}

var langsmithTraceSelects = []string{"ID", "NAME", "RUN_TYPE", "STATUS", "START_TIME", "END_TIME", "ERROR", "INPUTS", "OUTPUTS", "PARENT_RUN_IDS", "IS_ROOT"}

type langsmithFetcher struct {
	c      *apiClient
	getenv func(string) string
}

// langsmithRun is the part of a run the source reads.
type langsmithRun struct {
	ID        string         `json:"id"`
	TraceID   string         `json:"trace_id"`
	Name      string         `json:"name"`
	RunType   string         `json:"run_type"`
	Status    string         `json:"status"`
	StartTime string         `json:"start_time"`
	Error     string         `json:"error"`
	Inputs    map[string]any `json:"inputs"`
	Outputs   map[string]any `json:"outputs"`
	IsRoot    bool           `json:"is_root"`
	Parents   []string       `json:"parent_run_ids"`
}

// Fetch implements Fetcher.
func (f *langsmithFetcher) Fetch(ctx context.Context, src *Source, cur Cursor, now time.Time) (*Batch, error) {
	key := f.getenv(src.APIKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("langsmith source %s: set %s", src.Name, src.APIKeyEnv)
	}
	hdr := http.Header{"X-Api-Key": {key}}
	if src.Workspace != "" {
		hdr.Set("X-Tenant-Id", src.Workspace)
	}
	host := strings.TrimRight(src.Host, "/")
	from := windowStart(src, cur, now)
	cs := newCandidates(src.maxPerPull(), cur)
	roots := map[string]time.Time{}
	b := &Batch{Cursor: cur}
	b.Cursor.Since = now
	if src.Filters.Errors {
		body := map[string]any{"project_ids": []string{src.Project}, "is_root": true, "has_error": true,
			"min_start_time": from.Format(time.RFC3339Nano), "max_start_time": now.Format(time.RFC3339Nano)}
		if err := f.query(ctx, host, hdr, body, cs, roots, FlagError, func(r langsmithRun) string {
			return "run errored: " + oneLine(firstNonEmpty(r.Error, "status "+r.Status), 200)
		}); err != nil {
			return nil, err
		}
	}
	if fb := src.Filters.Feedback; fb != nil && fb.Below != nil {
		filter := fmt.Sprintf("and(eq(feedback_key, %s), lt(feedback_score, %s))", strconv.Quote(fb.Name), strconv.FormatFloat(*fb.Below, 'f', -1, 64))
		body := map[string]any{"project_ids": []string{src.Project}, "is_root": true, "filter": filter,
			"min_start_time": from.Add(-langsmithFeedbackLB).Format(time.RFC3339Nano), "max_start_time": now.Format(time.RFC3339Nano),
			"min_feedback_modified_at": from.Format(time.RFC3339Nano)}
		if err := f.query(ctx, host, hdr, body, cs, roots, FlagFeedback+":"+fb.Name, func(langsmithRun) string {
			return fmt.Sprintf("negative user feedback: %s below %s", fb.Name, strconv.FormatFloat(*fb.Below, 'f', -1, 64))
		}); err != nil {
			return nil, err
		}
	}
	if b.Truncated = cs.full; b.Truncated {
		b.holdAt(from.Add(src.overlap())) // read this window again; taken traces are skipped
	}
	for _, c := range cs.list() {
		s, err := f.trace(ctx, host, hdr, src.Project, c.id, roots[c.id])
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			b.Warnings = append(b.Warnings, fmt.Sprintf("trace %s: %v", c.id, err))
			b.holdAt(c.at.Add(src.overlap())) // its flag time may be old; tried again by the next pull
			continue
		}
		b.Sessions = append(b.Sessions, Flagged{Session: s, TraceID: c.id, At: c.at, Reasons: c.reasons, Kinds: c.kinds})
	}
	return b, nil
}

// query pages POST /api/v2/runs/query and flags each root run's trace.
func (f *langsmithFetcher) query(ctx context.Context, host string, hdr http.Header, body map[string]any, cs *candidates, roots map[string]time.Time, kind string, reason func(langsmithRun) string) error {
	body["page_size"] = langsmithPage
	body["selects"] = langsmithRootSelects
	for page := 0; page < langsmithMaxPages; page++ {
		var ans struct {
			Items      []langsmithRun `json:"items"`
			NextCursor string         `json:"next_cursor"`
		}
		if err := f.c.doJSON(ctx, http.MethodPost, host+"/api/v2/runs/query", hdr, body, &ans); err != nil {
			return fmt.Errorf("langsmith runs query: %w", err)
		}
		for _, r := range ans.Items {
			trace := firstNonEmpty(r.TraceID, r.ID)
			at := parseRFC3339(r.StartTime)
			if _, ok := roots[trace]; !ok {
				roots[trace] = at
			}
			if !cs.add(trace, at, reason(r), kind) {
				return nil
			}
		}
		if ans.NextCursor == "" {
			return nil
		}
		body["cursor"] = ans.NextCursor
	}
	cs.full = true
	return nil
}

// trace reads every run of one trace and converts it to a chat transcript
// that traceimport reads.
func (f *langsmithFetcher) trace(ctx context.Context, host string, hdr http.Header, project, traceID string, start time.Time) (*traceimport.Session, error) {
	if start.IsZero() {
		start = time.Now().UTC().Add(-24 * time.Hour)
	}
	q := url.Values{"project_id": {project}, "min_start_time": {start.Add(-time.Hour).Format(time.RFC3339Nano)},
		"max_start_time": {start.Add(24 * time.Hour).Format(time.RFC3339Nano)}, "selects": langsmithTraceSelects}
	var ans struct {
		Items []langsmithRun `json:"items"`
	}
	if err := f.c.doJSON(ctx, http.MethodGet, host+"/api/v2/traces/"+url.PathEscape(traceID)+"/runs?"+q.Encode(), hdr, nil, &ans); err != nil {
		return nil, err
	}
	if len(ans.Items) == 0 {
		return nil, fmt.Errorf("no runs in trace %s", traceID)
	}
	conv := langsmithTranscript(traceID, ans.Items)
	data, err := json.Marshal(conv)
	if err != nil {
		return nil, err
	}
	tr, err := traceimport.Parse(data, traceimport.FormatChat)
	if err != nil {
		return nil, err
	}
	return tr.Sessions[0], nil
}

// langsmithTranscript builds an OpenAI-style conversation from a trace's
// runs: the root's input as the user's request (or the first LLM run's
// messages), every tool run as a call and its result, the root's output as
// the final answer, and the root's error as the outcome.
func langsmithTranscript(traceID string, runs []langsmithRun) map[string]any {
	root := &runs[0]
	for i := range runs {
		if runs[i].IsRoot || runs[i].ID == traceID {
			root = &runs[i]
			break
		}
		if len(runs[i].Parents) == 0 && root == &runs[0] && len(runs[0].Parents) > 0 {
			root = &runs[i]
		}
	}
	var msgs []any
	msgs = append(msgs, inputMessages(root.Inputs)...)
	if len(msgs) == 0 {
		for _, r := range runs {
			if strings.EqualFold(r.RunType, "llm") {
				msgs = append(msgs, inputMessages(r.Inputs)...)
				break
			}
		}
	}
	model := ""
	for _, r := range runs {
		switch strings.ToLower(r.RunType) {
		case "tool":
			args, _ := json.Marshal(firstValue(r.Inputs, "input", "args", "tool_input"))
			if args == nil || string(args) == "null" {
				args, _ = json.Marshal(r.Inputs)
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{"id": r.ID, "type": "function", "function": map[string]any{"name": r.Name, "arguments": string(args)}}}})
			result := textValue(firstValue(r.Outputs, "output", "result", "content"))
			if result == "" && len(r.Outputs) > 0 {
				b, _ := json.Marshal(r.Outputs)
				result = string(b)
			}
			isErr := strings.EqualFold(r.Status, "error") || r.Error != ""
			if isErr {
				result = "Error: " + firstNonEmpty(r.Error, result, "tool run failed")
			}
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": r.ID, "name": r.Name, "content": result, "is_error": isErr})
		case "llm":
			if model == "" {
				model = r.Name
			}
		}
	}
	if out := textValue(firstValue(root.Outputs, "output", "answer", "response", "content", "text", "result")); out != "" {
		msgs = append(msgs, map[string]any{"role": "assistant", "content": out})
	} else if m := outputMessage(root.Outputs); m != nil {
		msgs = append(msgs, m)
	}
	conv := map[string]any{"id": traceID, "name": root.Name, "model": model, "messages": msgs}
	if strings.EqualFold(root.Status, "error") || root.Error != "" {
		conv["status"] = "error"
		conv["error"] = firstNonEmpty(root.Error, "root run status error")
	}
	return conv
}

// inputMessages reads a run's inputs as user-side messages: a messages list
// (OpenAI dicts, LangChain message objects, or a list of such lists), else
// one user message from input, question, query or prompt.
func inputMessages(in map[string]any) []any {
	if in == nil {
		return nil
	}
	if list, ok := in["messages"].([]any); ok {
		if len(list) > 0 {
			if inner, ok := list[0].([]any); ok {
				list = inner
			}
		}
		var out []any
		for _, m := range list {
			if msg := chatMessage(m); msg != nil {
				out = append(out, msg)
			}
		}
		return out
	}
	if t := textValue(firstValue(in, "input", "question", "query", "prompt", "text")); t != "" {
		return []any{map[string]any{"role": "user", "content": t}}
	}
	return nil
}

// outputMessage reads an LLM-shaped output: generations or a message.
func outputMessage(out map[string]any) map[string]any {
	if out == nil {
		return nil
	}
	if gens, ok := out["generations"].([]any); ok && len(gens) > 0 {
		g := gens[0]
		if inner, ok := g.([]any); ok && len(inner) > 0 {
			g = inner[0]
		}
		if gm, ok := g.(map[string]any); ok {
			if m := chatMessage(gm["message"]); m != nil {
				return m
			}
			if t, ok := gm["text"].(string); ok && t != "" {
				return map[string]any{"role": "assistant", "content": t}
			}
		}
	}
	if m := chatMessage(out["message"]); m != nil {
		return m
	}
	return nil
}

// chatMessage turns one message in OpenAI or LangChain form into an
// OpenAI-style message.
func chatMessage(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if _, ok := m["role"]; ok {
		return m
	}
	kw, _ := m["kwargs"].(map[string]any)
	typ, _ := m["type"].(string)
	if kw == nil {
		if data, ok := m["data"].(map[string]any); ok {
			kw = data
		}
	}
	if kw == nil {
		return nil
	}
	if typ == "" || typ == "constructor" {
		if ids, ok := m["id"].([]any); ok && len(ids) > 0 {
			typ, _ = ids[len(ids)-1].(string)
		}
		if t, ok := kw["type"].(string); ok && t != "" {
			typ = t
		}
	}
	role := "user"
	switch strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(typ, "Message"), "MessageChunk")) {
	case "ai", "assistant":
		role = "assistant"
	case "system":
		role = "system"
	case "tool", "function":
		role = "tool"
	}
	out := map[string]any{"role": role, "content": kw["content"]}
	if tc, ok := kw["tool_calls"]; ok {
		out["tool_calls"] = tc
	}
	if id, ok := kw["tool_call_id"]; ok {
		out["tool_call_id"] = id
	}
	return out
}

func firstValue(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// textValue renders a string, or a JSON value as compact JSON; nil is "".
func textValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
