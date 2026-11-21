package replay

import (
	"encoding/json"
	"errors"
)

// Request shapes AppendUserMessage and NewToolResults read.
const (
	ShapeAnthropic = "anthropic" // Messages: messages[] with content blocks
	ShapeChat      = "chat"      // Chat Completions: messages[] with tool messages
	ShapeResponses = "responses" // Responses: input[] items
	ShapeGemini    = "gemini"    // generateContent: contents[] with parts
)

// ErrUnknownShape is returned for a request whose shape is not one of the
// above.
var ErrUnknownShape = errors.New("request shape not recognized")

// AppendUserMessage returns body with a user message holding text added at
// the end of the conversation, in the request's own shape. A Messages
// request whose last turn is already the user's (tool results) gets a text
// block on that turn, since two user turns in a row are refused there.
func AppendUserMessage(body []byte, shape, text string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	switch shape {
	case ShapeAnthropic:
		msgs, _ := req["messages"].([]any)
		if n := len(msgs); n > 0 {
			if last, ok := msgs[n-1].(map[string]any); ok && last["role"] == "user" {
				var blocks []any
				switch c := last["content"].(type) {
				case string:
					blocks = []any{map[string]any{"type": "text", "text": c}}
				case []any:
					blocks = c
				}
				last["content"] = append(blocks, map[string]any{"type": "text", "text": text})
				return json.Marshal(req)
			}
		}
		req["messages"] = append(msgs, map[string]any{"role": "user", "content": text})
	case ShapeChat:
		msgs, _ := req["messages"].([]any)
		req["messages"] = append(msgs, map[string]any{"role": "user", "content": text})
	case ShapeResponses:
		var items []any
		switch in := req["input"].(type) {
		case string:
			items = []any{map[string]any{"role": "user", "content": in}}
		case []any:
			items = in
		}
		req["input"] = append(items, map[string]any{"role": "user", "content": text})
	case ShapeGemini:
		contents, _ := req["contents"].([]any)
		req["contents"] = append(contents, map[string]any{"role": "user", "parts": []any{map[string]any{"text": text}}})
	default:
		return nil, ErrUnknownShape
	}
	return json.Marshal(req)
}

// NewToolResults returns the names of the tools whose results the newest
// turn of a request carries: the request the model gets right after those
// tools ran. A tool whose call cannot be found in the history is left out.
func NewToolResults(body []byte, shape string) []string {
	var req struct {
		Messages []json.RawMessage `json:"messages"`
		Input    json.RawMessage   `json:"input"`
	}
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	switch shape {
	case ShapeAnthropic:
		return anthropicToolResults(req.Messages)
	case ShapeChat:
		return chatToolResults(req.Messages)
	case ShapeResponses:
		var items []json.RawMessage
		if json.Unmarshal(req.Input, &items) != nil {
			return nil
		}
		return responsesToolResults(items)
	}
	return nil
}

type anthropicMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
}

func anthropicToolResults(raw []json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	names := map[string]string{}
	var last []anthropicBlock
	for i, r := range raw {
		var m anthropicMsg
		if json.Unmarshal(r, &m) != nil {
			continue
		}
		var blocks []anthropicBlock
		_ = json.Unmarshal(m.Content, &blocks)
		for _, b := range blocks {
			if b.Type == "tool_use" {
				names[b.ID] = b.Name
			}
		}
		if i == len(raw)-1 && m.Role == "user" {
			last = blocks
		}
	}
	var out []string
	for _, b := range last {
		if b.Type == "tool_result" && names[b.ToolUseID] != "" {
			out = append(out, names[b.ToolUseID])
		}
	}
	return out
}

func chatToolResults(raw []json.RawMessage) []string {
	type msg struct {
		Role       string `json:"role"`
		ToolCallID string `json:"tool_call_id"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	msgs := make([]msg, 0, len(raw))
	names := map[string]string{}
	for _, r := range raw {
		var m msg
		_ = json.Unmarshal(r, &m)
		for _, c := range m.ToolCalls {
			names[c.ID] = c.Function.Name
		}
		msgs = append(msgs, m)
	}
	var out []string
	for i := len(msgs) - 1; i >= 0 && msgs[i].Role == "tool"; i-- {
		if n := names[msgs[i].ToolCallID]; n != "" {
			out = append([]string{n}, out...)
		}
	}
	return out
}

func responsesToolResults(raw []json.RawMessage) []string {
	type item struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
	}
	items := make([]item, 0, len(raw))
	names := map[string]string{}
	for _, r := range raw {
		var it item
		_ = json.Unmarshal(r, &it)
		if it.Type == "function_call" || it.Type == "custom_tool_call" {
			names[it.CallID] = it.Name
		}
		items = append(items, it)
	}
	var out []string
	for i := len(items) - 1; i >= 0 && (items[i].Type == "function_call_output" || items[i].Type == "custom_tool_call_output"); i-- {
		if n := names[items[i].CallID]; n != "" {
			out = append([]string{n}, out...)
		}
	}
	return out
}
