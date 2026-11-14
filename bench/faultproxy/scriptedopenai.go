package faultproxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// scriptedOpenAIModel is the model the OpenAI scripted upstream reports when a
// request names none.
const scriptedOpenAIModel = "gpt-5-mini"

// openAIRequest is the subset of a Chat Completions or Responses request the
// proxy reads. Tools keep both shapes: chat nests name and parameters under
// "function", Responses puts them on the tool.
type openAIRequest struct {
	Model         string       `json:"model"`
	Stream        bool         `json:"stream"`
	Tools         []openAITool `json:"tools"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

type openAITool struct {
	Type       string          `json:"type"`
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
	Function   *struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// firstFunction returns the first function tool's name and JSON schema.
func (r openAIRequest) firstFunction() (string, json.RawMessage, bool) {
	for _, t := range r.Tools {
		if t.Type != "function" {
			continue
		}
		if t.Function != nil && t.Function.Name != "" {
			return t.Function.Name, t.Function.Parameters, true
		}
		if t.Name != "" {
			return t.Name, t.Parameters, true
		}
	}
	return "", nil, false
}

// openAIReply is one scripted assistant turn in OpenAI terms: text, or one
// function call. Usage is deterministic: half the prompt is reported cached,
// so the cache bucket is exercised end to end.
type openAIReply struct {
	ID       string
	ItemID   string // Responses output item id (msg_ or fc_)
	Model    string
	Created  int64
	Text     string
	CallID   string
	ToolName string
	ToolArgs string // JSON text, as OpenAI sends it
	Prompt   int64
	Cached   int64
	Output   int64
}

// newOpenAIReply builds the reply for one request. responses selects the
// Responses API id prefix.
func newOpenAIReply(body []byte, req openAIRequest, responses, toolTurn bool) openAIReply {
	model := req.Model
	if model == "" {
		model = scriptedOpenAIModel
	}
	prompt := scriptedInputTokens(body)
	rp := openAIReply{
		ID: "chatcmpl-" + utils.NewID("", 24)[1:], Model: model, Created: time.Now().Unix(),
		Text: scriptedText, Prompt: prompt, Cached: prompt / 2, Output: scriptedOutputTokens,
		ItemID: utils.NewID("msg", 24),
	}
	if responses {
		rp.ID = utils.NewID("resp", 24)
	}
	if name, schema, ok := req.firstFunction(); ok && toolTurn {
		args, err := json.Marshal(scriptedToolInput(schema))
		if err != nil {
			args = []byte("{}")
		}
		rp.Text, rp.CallID, rp.ToolName, rp.ToolArgs = "", utils.NewID("call", 24), name, string(args)
		rp.ItemID = utils.NewID("fc", 24)
	}
	return rp
}

// tokens returns the reply's usage in the ledger's buckets.
func (rp openAIReply) tokens() *schemas.Tokens {
	return &schemas.Tokens{Fresh: rp.Prompt - rp.Cached, CacheRead: rp.Cached, Out: rp.Output}
}

func (rp openAIReply) chatUsage() map[string]any {
	return map[string]any{
		"prompt_tokens": rp.Prompt, "completion_tokens": rp.Output, "total_tokens": rp.Prompt + rp.Output,
		"prompt_tokens_details":     map[string]any{"cached_tokens": rp.Cached, "audio_tokens": 0},
		"completion_tokens_details": map[string]any{"reasoning_tokens": 0, "audio_tokens": 0, "accepted_prediction_tokens": 0, "rejected_prediction_tokens": 0},
	}
}

// chatCompletion renders the reply as a Chat Completions JSON body.
func (rp openAIReply) chatCompletion() map[string]any {
	msg := map[string]any{"role": "assistant", "content": rp.Text, "refusal": nil}
	finish := "stop"
	if rp.CallID != "" {
		msg["content"] = nil
		msg["tool_calls"] = []any{map[string]any{
			"id": rp.CallID, "type": "function", "function": map[string]any{"name": rp.ToolName, "arguments": rp.ToolArgs},
		}}
		finish = "tool_calls"
	}
	return map[string]any{
		"id": rp.ID, "object": "chat.completion", "created": rp.Created, "model": rp.Model,
		"choices":            []any{map[string]any{"index": 0, "message": msg, "logprobs": nil, "finish_reason": finish}},
		"usage":              rp.chatUsage(),
		"service_tier":       "default",
		"system_fingerprint": nil,
	}
}

// chatChunks renders the reply as Chat Completions stream chunks: role, one
// content or tool call delta, the finish reason, then usage when asked.
func (rp openAIReply) chatChunks(includeUsage bool) []map[string]any {
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": rp.ID, "object": "chat.completion.chunk", "created": rp.Created, "model": rp.Model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "logprobs": nil, "finish_reason": finish}},
		}
	}
	var out []map[string]any
	if rp.CallID != "" {
		out = append(out,
			chunk(map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
				"index": 0, "id": rp.CallID, "type": "function", "function": map[string]any{"name": rp.ToolName, "arguments": ""},
			}}}, nil),
			chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": rp.ToolArgs}}}}, nil),
			chunk(map[string]any{}, "tool_calls"))
	} else {
		out = append(out,
			chunk(map[string]any{"role": "assistant", "content": "", "refusal": nil}, nil),
			chunk(map[string]any{"content": rp.Text}, nil),
			chunk(map[string]any{}, "stop"))
	}
	if includeUsage {
		u := chunk(nil, nil)
		u["choices"] = []any{}
		u["usage"] = rp.chatUsage()
		out = append(out, u)
	}
	return out
}

// outputItem is the Responses output item, in progress or done.
func (rp openAIReply) outputItem(done bool) map[string]any {
	status := "in_progress"
	if done {
		status = "completed"
	}
	if rp.CallID != "" {
		args := ""
		if done {
			args = rp.ToolArgs
		}
		return map[string]any{"type": "function_call", "id": rp.ItemID, "call_id": rp.CallID, "name": rp.ToolName, "arguments": args, "status": status}
	}
	content := []any{}
	if done {
		content = append(content, rp.textPart(rp.Text))
	}
	return map[string]any{"type": "message", "id": rp.ItemID, "status": status, "role": "assistant", "content": content}
}

func (rp openAIReply) textPart(text string) map[string]any {
	return map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}
}

// response renders the Responses object, in progress (no output, no usage)
// or completed.
func (rp openAIReply) response(done bool) map[string]any {
	r := map[string]any{
		"id": rp.ID, "object": "response", "created_at": rp.Created, "status": "in_progress", "model": rp.Model,
		"output": []any{}, "usage": nil, "error": nil, "incomplete_details": nil,
		"parallel_tool_calls": true, "tool_choice": "auto", "tools": []any{}, "store": false,
	}
	if done {
		r["status"] = "completed"
		r["output"] = []any{rp.outputItem(true)}
		r["usage"] = map[string]any{
			"input_tokens": rp.Prompt, "input_tokens_details": map[string]any{"cached_tokens": rp.Cached},
			"output_tokens": rp.Output, "output_tokens_details": map[string]any{"reasoning_tokens": 0},
			"total_tokens": rp.Prompt + rp.Output,
		}
	}
	return r
}

// responseEvents renders the Responses stream event sequence.
func (rp openAIReply) responseEvents() []map[string]any {
	evs := []map[string]any{
		{"type": "response.created", "response": rp.response(false)},
		{"type": "response.in_progress", "response": rp.response(false)},
		{"type": "response.output_item.added", "output_index": 0, "item": rp.outputItem(false)},
	}
	if rp.CallID != "" {
		evs = append(evs,
			map[string]any{"type": "response.function_call_arguments.delta", "item_id": rp.ItemID, "output_index": 0, "delta": rp.ToolArgs},
			map[string]any{"type": "response.function_call_arguments.done", "item_id": rp.ItemID, "output_index": 0, "arguments": rp.ToolArgs})
	} else {
		at := map[string]any{"item_id": rp.ItemID, "output_index": 0, "content_index": 0}
		with := func(typ string, kv ...any) map[string]any {
			ev := map[string]any{"type": typ}
			for k, v := range at {
				ev[k] = v
			}
			for i := 0; i+1 < len(kv); i += 2 {
				ev[kv[i].(string)] = kv[i+1]
			}
			return ev
		}
		evs = append(evs,
			with("response.content_part.added", "part", rp.textPart("")),
			with("response.output_text.delta", "delta", rp.Text, "logprobs", []any{}),
			with("response.output_text.done", "text", rp.Text, "logprobs", []any{}),
			with("response.content_part.done", "part", rp.textPart(rp.Text)))
	}
	evs = append(evs,
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": rp.outputItem(true)},
		map[string]any{"type": "response.completed", "response": rp.response(true)})
	for i, ev := range evs {
		ev["sequence_number"] = i
	}
	return evs
}

// serveScriptedOpenAI answers one OpenAI-format request from the scripted
// model and fills the ledger entry's response id and tokens.
func (s *Server) serveScriptedOpenAI(w http.ResponseWriter, r *http.Request, kind, slot string, body []byte, e *schemas.LedgerEntry) {
	w.Header().Set("x-request-id", utils.NewID("req", 24))
	switch kind {
	case KindMessage:
		var req openAIRequest
		if err := json.Unmarshal(body, &req); err != nil {
			req = openAIRequest{}
		}
		responses := strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/responses")
		_, _, hasTool := req.firstFunction()
		rp := newOpenAIReply(body, req, responses, hasTool && s.plans.takeToolTurn(slot))
		e.ResponseID, e.Tokens = rp.ID, rp.tokens()
		switch {
		case responses && req.Stream:
			s.streamEvents(w, slot, rp.responseEvents(), true, false)
		case responses:
			s.writeJSON(w, http.StatusOK, rp.response(true))
		case req.Stream:
			s.streamEvents(w, slot, rp.chatChunks(req.StreamOptions != nil && req.StreamOptions.IncludeUsage), false, true)
		default:
			s.writeJSON(w, http.StatusOK, rp.chatCompletion())
		}
	case KindCountTokens:
		s.writeJSON(w, http.StatusOK, map[string]any{"object": "response.input_tokens", "input_tokens": scriptedInputTokens(body)})
	case KindModels:
		if id := strings.TrimPrefix(r.URL.Path, "/v1/models/"); id != r.URL.Path && id != "" {
			s.writeJSON(w, http.StatusOK, scriptedOpenAIModelEntry(id))
			return
		}
		data := []any{}
		for _, id := range []string{scriptedOpenAIModel, "gpt-5.4-mini", "gpt-6.1-terra"} {
			data = append(data, scriptedOpenAIModelEntry(id))
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
	default:
		s.writeError(w, FormatOpenAI, http.StatusNotFound, errNotFound, "no scripted route for "+r.Method+" "+r.URL.Path)
	}
}

// streamEvents writes SSE frames, flushing after each. named adds an
// "event:" line (Responses API); done appends "data: [DONE]" (Chat
// Completions).
func (s *Server) streamEvents(w http.ResponseWriter, slot string, evs []map[string]any, named, done bool) {
	if err := writeSSE(w, evs, named, done); err != nil {
		s.logger.Warn("scripted stream interrupted", "slot", slot, "error", err.Error())
	}
}

func writeSSE(w http.ResponseWriter, evs []map[string]any, named, done bool) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	for _, ev := range evs {
		data, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("encode sse event: %w", err)
		}
		frame := "data: " + string(data) + "\n\n"
		if named {
			frame = fmt.Sprintf("event: %s\n%s", ev["type"], frame)
		}
		if _, err := fmt.Fprint(w, frame); err != nil {
			return fmt.Errorf("write sse event: %w", err)
		}
		if err := rc.Flush(); err != nil {
			return fmt.Errorf("flush sse event: %w", err)
		}
	}
	if done {
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			return fmt.Errorf("write sse done: %w", err)
		}
		if err := rc.Flush(); err != nil {
			return fmt.Errorf("flush sse done: %w", err)
		}
	}
	return nil
}

func scriptedOpenAIModelEntry(id string) map[string]any {
	return map[string]any{"id": id, "object": "model", "created": 1767225600, "owned_by": "openai"}
}
