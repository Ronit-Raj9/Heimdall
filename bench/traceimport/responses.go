package traceimport

import "encoding/json"

// ResponsesToChat turns an OpenAI Responses API input history and output
// (Bifrost's responses_input_history and responses_output log fields) into
// chat messages the chat format reads: a message item keeps its role (the
// parser reads developer as system), a function or custom tool call becomes
// an assistant message holding that call, a call's output becomes a tool
// message, and reasoning and other items are dropped. Without it a Responses
// row (Codex CLI, the OpenAI Agents SDK) came through only as its content
// summary, the system instructions, labelled as the user turn, with every
// tool call lost (live lane l1).
func ResponsesToChat(input, output []json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	for _, list := range [][]json.RawMessage{input, output} {
		for _, raw := range list {
			var item map[string]any
			if json.Unmarshal(raw, &item) != nil || item == nil {
				continue
			}
			var msg map[string]any
			switch typ := asString(item["type"]); {
			case asString(item["role"]) != "" && (typ == "" || typ == "message"):
				out = append(out, raw)
				continue
			case typ == "function_call" || typ == "custom_tool_call":
				msg = map[string]any{"role": "assistant", "content": []any{map[string]any{
					"type": "function_call", "call_id": item["call_id"], "name": item["name"], "arguments": firstOf(item, "arguments", "input"),
				}}}
			case typ == "function_call_output" || typ == "custom_tool_call_output":
				msg = map[string]any{"role": "tool", "tool_call_id": item["call_id"], "content": []any{map[string]any{
					"type": "function_call_output", "call_id": item["call_id"], "output": item["output"],
				}}}
			default:
				continue
			}
			if b, err := json.Marshal(msg); err == nil {
				out = append(out, b)
			}
		}
	}
	return out
}
