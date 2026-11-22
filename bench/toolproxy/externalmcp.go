package toolproxy

import (
	"bufio"
	"bytes"
	"mime"
	"strings"
)

// External MCP servers (ExternalRoute.MCP, docs/mcp-server-testing.md). The
// route is an external route in every other way (allowlist, credential,
// caps, ledger); these helpers only make its ledger rows read like MCP rows:
// the tool a tools/call named, or the JSON-RPC method, and an error flag
// that also covers a JSON-RPC error and an MCP result with isError.

// mcpRequestLabel names the call in body: the tool of a tools/call, else the
// JSON-RPC method ("initialize", "tools/list"), "batch" for a batch and
// "notification" for a body with no id. A body that is not JSON-RPC keeps
// fallback. It is pure.
func mcpRequestLabel(body []byte, fallback string) string {
	items, batch, ok := parseRPC(body)
	if !ok || len(items) == 0 {
		return fallback
	}
	if batch {
		return "batch"
	}
	m := items[0].msg
	if m.Method == methodToolsCall {
		if name, _ := readToolParams(m.Params); name != "" {
			return name
		}
		return methodToolsCall
	}
	if m.Method != "" {
		return m.Method
	}
	return fallback
}

// mcpAnswerIsError reports whether an MCP answer (plain JSON or an SSE
// stream of JSON-RPC messages) holds a JSON-RPC error or a tool result with
// isError. An answer it cannot read is not called an error here; the HTTP
// status still is. It is pure.
func mcpAnswerIsError(contentType string, raw []byte) bool {
	for _, payload := range mcpPayloads(contentType, raw) {
		items, _, ok := parseRPC(payload)
		if !ok {
			continue
		}
		for _, it := range items {
			if len(bytes.TrimSpace(it.msg.Error)) > 0 && string(bytes.TrimSpace(it.msg.Error)) != "null" {
				return true
			}
			if len(it.msg.Result) > 0 && resultIsError(it.msg.Result) {
				return true
			}
		}
	}
	return false
}

// mcpPayloads splits an answer into its JSON-RPC payloads: the data of each
// SSE event for text/event-stream, else the whole body.
func mcpPayloads(contentType string, raw []byte) [][]byte {
	mt, _, _ := mime.ParseMediaType(contentType)
	if mt != "text/event-stream" {
		return [][]byte{raw}
	}
	var out [][]byte
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, []byte(strings.Join(cur, "\n")))
			cur = nil
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64<<10), maxStdioLine)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "data:"):
			cur = append(cur, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return out
}
