package faultproxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Scripted model constants. Usage is deterministic so synthetic runs price
// and join the same way every time.
const (
	scriptedOutputTokens = 32
	scriptedDefaultModel = "claude-sonnet-4-6"
	scriptedText         = "Scripted reply from the bench fault proxy."
)

// messagesRequest is the subset of an Anthropic Messages request the proxy
// reads.
type messagesRequest struct {
	Model  string          `json:"model"`
	Stream bool            `json:"stream"`
	Tools  []requestedTool `json:"tools"`
}

// requestedTool is one tool definition from a Messages request.
type requestedTool struct {
	Name        string          `json:"name"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// scriptedReply is one assistant turn of the scripted model, rendered as JSON
// or as SSE. It has exactly one content block: text or tool_use.
type scriptedReply struct {
	ID         string
	Model      string
	StopReason string
	Text       string
	ToolID     string
	ToolName   string
	ToolInput  map[string]any
	Input      int64
	Output     int64
}

// newScriptedReply builds the reply for one Messages request. toolTurn says
// whether this run still owes its first tool_use; it only matters when the
// request offers tools.
func newScriptedReply(body []byte, req messagesRequest, toolTurn bool) scriptedReply {
	model := req.Model
	if model == "" {
		model = scriptedDefaultModel
	}
	rp := scriptedReply{
		ID:         utils.NewID("msg", 24),
		Model:      model,
		StopReason: "end_turn",
		Text:       scriptedText,
		Input:      scriptedInputTokens(body),
		Output:     scriptedOutputTokens,
	}
	if toolTurn && len(req.Tools) > 0 {
		rp.StopReason = "tool_use"
		rp.Text = ""
		rp.ToolID = utils.NewID("toolu", 24)
		rp.ToolName = req.Tools[0].Name
		rp.ToolInput = scriptedToolInput(req.Tools[0].InputSchema)
	}
	return rp
}

// tokens returns the reply's usage in the ledger's buckets.
func (rp scriptedReply) tokens() *schemas.Tokens {
	return &schemas.Tokens{Fresh: rp.Input, Out: rp.Output}
}

func (rp scriptedReply) usage(output int64) map[string]any {
	return map[string]any{
		"input_tokens":                rp.Input,
		"output_tokens":               output,
		"cache_read_input_tokens":     0,
		"cache_creation_input_tokens": 0,
	}
}

func (rp scriptedReply) block() map[string]any {
	if rp.ToolID != "" {
		return map[string]any{"type": "tool_use", "id": rp.ToolID, "name": rp.ToolName, "input": rp.ToolInput}
	}
	return map[string]any{"type": "text", "text": rp.Text}
}

func (rp scriptedReply) message() map[string]any {
	return map[string]any{
		"id": rp.ID, "type": "message", "role": "assistant", "model": rp.Model,
		"content": []any{rp.block()}, "stop_reason": rp.StopReason, "stop_sequence": nil,
		"usage": rp.usage(rp.Output),
	}
}

// events renders the reply as the Anthropic SSE event sequence.
func (rp scriptedReply) events() []map[string]any {
	start := map[string]any{
		"id": rp.ID, "type": "message", "role": "assistant", "model": rp.Model,
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": rp.usage(0),
	}
	var startBlock, delta map[string]any
	if rp.ToolID != "" {
		input, err := json.Marshal(rp.ToolInput)
		if err != nil {
			input = []byte("{}")
		}
		startBlock = map[string]any{"type": "tool_use", "id": rp.ToolID, "name": rp.ToolName, "input": map[string]any{}}
		delta = map[string]any{"type": "input_json_delta", "partial_json": string(input)}
	} else {
		startBlock = map[string]any{"type": "text", "text": ""}
		delta = map[string]any{"type": "text_delta", "text": rp.Text}
	}
	return []map[string]any{
		{"type": "message_start", "message": start},
		{"type": "content_block_start", "index": 0, "content_block": startBlock},
		{"type": "content_block_delta", "index": 0, "delta": delta},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": rp.StopReason, "stop_sequence": nil}, "usage": rp.usage(rp.Output)},
		{"type": "message_stop"},
	}
}

// writeScriptedStream writes the reply as SSE, flushing after every event so
// the scripted path exercises the same streaming code in Bifrost as a real
// provider.
func writeScriptedStream(w http.ResponseWriter, rp scriptedReply) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	for _, ev := range rp.events() {
		data, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("encode sse event: %w", err)
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], data); err != nil {
			return fmt.Errorf("write sse event: %w", err)
		}
		if err := rc.Flush(); err != nil {
			return fmt.Errorf("flush sse event: %w", err)
		}
	}
	return nil
}

// scriptedInputTokens is the deterministic input count: body length / 4.
func scriptedInputTokens(body []byte) int64 {
	return int64(len(body) / 4)
}

// scriptedToolInput builds the smallest input that satisfies the tool's
// required properties: zero values of the declared types.
func scriptedToolInput(schema json.RawMessage) map[string]any {
	out := map[string]any{}
	var s struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil {
		return out
	}
	for _, name := range s.Required {
		out[name] = zeroForType(s.Properties[name].Type)
	}
	return out
}

func zeroForType(t any) any {
	name, _ := t.(string)
	if list, ok := t.([]any); ok && len(list) > 0 {
		name, _ = list[0].(string)
	}
	switch strings.ToLower(name) {
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	case "null":
		return nil
	default:
		return ""
	}
}

// scriptedModels is the model list the scripted upstream serves.
func scriptedModels() []map[string]any {
	return []map[string]any{
		scriptedModel("claude-sonnet-4-6", "Claude Sonnet 4.6"),
		scriptedModel("claude-haiku-4-5", "Claude Haiku 4.5"),
	}
}

func scriptedModel(id, display string) map[string]any {
	return map[string]any{"type": "model", "id": id, "display_name": display, "created_at": "2026-01-01T00:00:00Z"}
}

func scriptedModelList() map[string]any {
	models := scriptedModels()
	return map[string]any{
		"data": models, "has_more": false,
		"first_id": models[0]["id"], "last_id": models[len(models)-1]["id"],
	}
}
