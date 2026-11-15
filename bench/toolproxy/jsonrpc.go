package toolproxy

import (
	"bytes"
	"encoding/json"
)

// MCP methods the proxy records.
const (
	methodToolsCall = "tools/call"
	methodToolsList = "tools/list"
)

// rpcMessage is one JSON-RPC 2.0 message, request or response. Fields stay
// raw so a message that is passed on unchanged is passed on byte for byte.
type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// rpcItem is one message of a body with its original bytes.
type rpcItem struct {
	raw json.RawMessage
	msg rpcMessage
}

// parseRPC decodes a JSON-RPC body: one message or a batch (a JSON array).
// ok is false when the body is not JSON-RPC at all; elements of a batch that
// are not objects are kept with an empty message so they are still passed
// on. It is pure.
func parseRPC(body []byte) (items []rpcItem, batch bool, ok bool) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, false, false
	}
	if body[0] == '[' {
		var raws []json.RawMessage
		if err := json.Unmarshal(body, &raws); err != nil {
			return nil, true, false
		}
		for _, r := range raws {
			var m rpcMessage
			if err := json.Unmarshal(r, &m); err != nil {
				m = rpcMessage{}
			}
			items = append(items, rpcItem{raw: r, msg: m})
		}
		return items, true, true
	}
	var m rpcMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, false, false
	}
	return []rpcItem{{raw: json.RawMessage(body), msg: m}}, false, true
}

// idKey is the key a request id is matched under: its compact JSON, so the
// number 7 and the string "7" stay apart. An absent or null id is "".
func idKey(id json.RawMessage) string {
	id = bytes.TrimSpace(id)
	if len(id) == 0 || string(id) == "null" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, id); err != nil {
		return string(id)
	}
	return buf.String()
}

// toolParams is what the proxy reads from tools/call params.
type toolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// readToolParams returns the tool name and the raw arguments of a tools/call.
// Malformed params give an empty name and the params themselves as the
// arguments, so the row still records what was sent.
func readToolParams(params json.RawMessage) (string, []byte) {
	var p toolParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", params
	}
	return p.Name, p.Arguments
}

// encodeBatch encodes raw messages as a JSON array.
func encodeBatch(raws []json.RawMessage) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, r := range raws {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(r)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// toolErrorResponse is the JSON-RPC response an injected error or timeout
// answers with: an MCP tool result with isError true, the way a tool server
// reports a failed tool, not a protocol error.
func toolErrorResponse(id json.RawMessage, text string) json.RawMessage {
	return responseWithResult(id, toolErrorResult(text))
}

// toolErrorResult is the MCP tool result of an injected error or timeout.
func toolErrorResult(text string) json.RawMessage {
	res, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"isError": true,
	})
	return res
}

// responseWithResult builds a JSON-RPC 2.0 success response.
func responseWithResult(id json.RawMessage, result json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(id)) == 0 {
		id = json.RawMessage("null")
	}
	b, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{"2.0", id, result})
	if err != nil {
		return json.RawMessage(`{"jsonrpc":"2.0","id":null,"result":{"content":[],"isError":true}}`)
	}
	return b
}

// resultIsError reports whether an MCP tool result says isError.
func resultIsError(result json.RawMessage) bool {
	var r struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return false
	}
	return r.IsError
}

// poisonResult appends text to the last text item of an MCP tool result, or
// adds a text item when there is none. ok is false when result is not a JSON
// object, in which case nothing is changed.
func poisonResult(result json.RawMessage, text string) (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(result, &obj); err != nil || obj == nil {
		return result, false
	}
	var content []json.RawMessage
	if c, ok := obj["content"]; ok {
		if err := json.Unmarshal(c, &content); err != nil {
			content = nil
		}
	}
	appended := false
	for i := len(content) - 1; i >= 0 && !appended; i-- {
		var item map[string]any
		if err := json.Unmarshal(content[i], &item); err != nil || item["type"] != "text" {
			continue
		}
		s, _ := item["text"].(string)
		item["text"] = s + "\n\n" + text
		b, err := json.Marshal(item)
		if err != nil {
			continue
		}
		content[i] = b
		appended = true
	}
	if !appended {
		b, _ := json.Marshal(map[string]string{"type": "text", "text": text})
		content = append(content, b)
	}
	c, err := json.Marshal(content)
	if err != nil {
		return result, false
	}
	obj["content"] = c
	out, err := json.Marshal(obj)
	if err != nil {
		return result, false
	}
	return out, true
}

// replaceResult returns the message raw with its result replaced, keeping
// every other member.
func replaceResult(raw json.RawMessage, result json.RawMessage) (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return raw, false
	}
	obj["result"] = result
	out, err := json.Marshal(obj)
	if err != nil {
		return raw, false
	}
	return out, true
}
