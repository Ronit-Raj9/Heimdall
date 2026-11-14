package bifrost

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// openAIItem is one Responses output item: a function_call (JSON arguments),
// a custom_tool_call (free-form input, Codex's apply_patch) or a
// local_shell_call (an action object).
type openAIItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     *string         `json:"input"`
	Action    json.RawMessage `json:"action"`
}

// openAIToolCall is one Chat Completions tool call, whole or as a stream
// delta (index set, fields partial).
type openAIToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAIToolCalls reads tool calls from an OpenAI response as Bifrost stores
// it in raw_response: a chat.completion or response object, or a stream of
// chat.completion.chunk or Responses event payloads. ok is false when raw is
// not in an OpenAI shape, so the caller tries the Anthropic one. Call ids are
// the ids tool results refer to: tool_calls[].id and call_id. ArgsHash is
// utils.HashJSON of the parsed arguments. It is pure.
func openAIToolCalls(raw string, stream bool) ([]schemas.ToolCallRef, bool) {
	if !stream {
		var body struct {
			Object  string       `json:"object"`
			Output  []openAIItem `json:"output"`
			Choices []struct {
				Message struct {
					ToolCalls []openAIToolCall `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(raw), &body) != nil {
			return nil, false
		}
		switch body.Object {
		case "response":
			return itemCalls(body.Output), true
		case "chat.completion":
			var out []schemas.ToolCallRef
			for _, c := range body.Choices {
				for _, tc := range c.Message.ToolCalls {
					out = append(out, toolCallRef(tc.ID, tc.Function.Name, argsJSON(tc.Function.Arguments)))
				}
			}
			return out, true
		}
		return nil, false
	}
	return openAIStreamToolCalls(streamPayloads(raw))
}

// openAIStreamToolCalls reads a Responses event stream (the done output
// items, else the completed response's output) or a chat completion chunk
// stream (tool call deltas joined by index).
func openAIStreamToolCalls(payloads []string) ([]schemas.ToolCallRef, bool) {
	var done, final []openAIItem
	responses, chat := false, false
	type pending struct {
		id, name string
		args     strings.Builder
	}
	byIndex := map[int]*pending{}
	for _, payload := range payloads {
		var ev struct {
			Type     string      `json:"type"`
			Object   string      `json:"object"`
			Item     *openAIItem `json:"item"`
			Response *struct {
				Output []openAIItem `json:"output"`
			} `json:"response"`
			Choices []struct {
				Delta struct {
					ToolCalls []openAIToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &ev) != nil {
			continue
		}
		switch {
		case strings.HasPrefix(ev.Type, "response."):
			responses = true
			if ev.Type == "response.output_item.done" && ev.Item != nil {
				done = append(done, *ev.Item)
			}
			if ev.Type == "response.completed" && ev.Response != nil {
				final = ev.Response.Output
			}
		case ev.Object == "chat.completion.chunk":
			chat = true
			for _, c := range ev.Choices {
				for _, tc := range c.Delta.ToolCalls {
					p, ok := byIndex[tc.Index]
					if !ok {
						p = &pending{}
						byIndex[tc.Index] = p
					}
					if tc.ID != "" {
						p.id = tc.ID
					}
					if tc.Function.Name != "" {
						p.name = tc.Function.Name
					}
					p.args.WriteString(tc.Function.Arguments)
				}
			}
		}
	}
	switch {
	case responses && len(done) > 0:
		return itemCalls(done), true
	case responses:
		return itemCalls(final), true
	case chat:
		idx := make([]int, 0, len(byIndex))
		for i := range byIndex {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		var out []schemas.ToolCallRef
		for _, i := range idx {
			p := byIndex[i]
			out = append(out, toolCallRef(p.id, p.name, argsJSON(p.args.String())))
		}
		return out, true
	}
	return nil, false
}

// itemCalls converts the tool call items of a Responses output.
func itemCalls(items []openAIItem) []schemas.ToolCallRef {
	var out []schemas.ToolCallRef
	for _, it := range items {
		id := it.CallID
		if id == "" {
			id = it.ID
		}
		switch it.Type {
		case "function_call":
			out = append(out, toolCallRef(id, it.Name, argsJSON(it.Arguments)))
		case "custom_tool_call":
			input := ""
			if it.Input != nil {
				input = *it.Input
			}
			b, err := json.Marshal(input)
			if err != nil {
				b = []byte(`""`)
			}
			out = append(out, toolCallRef(id, it.Name, b))
		case "local_shell_call":
			out = append(out, toolCallRef(id, "local_shell", it.Action))
		}
	}
	return out
}

// argsJSON is an OpenAI arguments string as JSON; empty means no arguments.
func argsJSON(s string) json.RawMessage {
	if strings.TrimSpace(s) == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}
