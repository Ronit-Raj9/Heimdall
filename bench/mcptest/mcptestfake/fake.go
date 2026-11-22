// Package mcptestfake is a small streamable HTTP MCP server for tests of
// the MCP server mode (bench/mcptest and its handlers). It is never used by
// the product itself.
package mcptestfake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Token is the bearer token the server wants when Server.RequireToken is set.
const Token = "fake-mcp-token"

// Server answers initialize, tools/list, tools/call and ping.
//
// Tools: search_orders (read-only, well described, needs a string query),
// do (no description, untyped parameter) and delete_order (destructive).
type Server struct {
	RequireToken bool
	// SSE answers tools/call as an event stream.
	SSE bool

	mu       sync.Mutex
	sessions map[string]bool
	n        atomic.Int64
	// Calls counts tools/call per tool.
	Calls map[string]int
}

// New returns a server.
func New() *Server { return &Server{sessions: map[string]bool{}, Calls: map[string]int{}} }

// ToolsJSON is the tools/list result.
const ToolsJSON = `{"tools":[
{"name":"search_orders","description":"Search the shop's orders by customer name or order number. Returns the matching orders with their id and status.",
 "inputSchema":{"type":"object","properties":{"query":{"type":"string","description":"Customer name or order number, for example A-1001"},"limit":{"type":"integer","description":"Most orders to return, 1 to 50"}},"required":["query"]},
 "annotations":{"readOnlyHint":true}},
{"name":"do","inputSchema":{"type":"object","properties":{"data":{}}}},
{"name":"delete_order","description":"Delete an order for good. Returns nothing.","inputSchema":{"type":"object","properties":{"id":{"type":"string","description":"Order id"}},"required":["id"]},
 "annotations":{"destructiveHint":true}}
]}`

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.RequireToken && r.Header.Get("Authorization") != "Bearer "+Token {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.sessions, r.Header.Get("Mcp-Session-Id"))
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error: the body is not JSON"}}`)
		return
	}
	sid := r.Header.Get("Mcp-Session-Id")
	if msg.Method != "initialize" {
		s.mu.Lock()
		known := s.sessions[sid]
		s.mu.Unlock()
		if !known {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
	}
	if len(msg.ID) == 0 { // a notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result, rpcErr string
	switch msg.Method {
	case "initialize":
		sid = fmt.Sprintf("sess-%d", s.n.Add(1))
		s.mu.Lock()
		s.sessions[sid] = true
		s.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", sid)
		result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake-shop","version":"1.2.0"}}`
	case "tools/list":
		result = ToolsJSON
	case "ping":
		result = `{}`
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		s.mu.Lock()
		s.Calls[p.Name]++
		s.mu.Unlock()
		switch p.Name {
		case "search_orders":
			q, ok := p.Arguments["query"].(string)
			if !ok || strings.TrimSpace(q) == "" {
				result = `{"content":[{"type":"text","text":"query is required and must be a string, for example A-1001"}],"isError":true}`
			} else {
				result = fmt.Sprintf(`{"content":[{"type":"text","text":"1 order: A-1001 for %s, status shipped"}]}`, q)
			}
		case "do":
			result = `{"content":[{"type":"text","text":"done"}]}`
		case "delete_order":
			result = `{"content":[{"type":"text","text":"deleted"}]}`
		default:
			rpcErr = `{"code":-32602,"message":"Unknown tool: ` + p.Name + `"}`
		}
	default:
		rpcErr = `{"code":-32601,"message":"Method not found"}`
	}
	out := `{"jsonrpc":"2.0","id":` + string(msg.ID) + `,`
	if rpcErr != "" {
		out += `"error":` + rpcErr + `}`
	} else {
		out += `"result":` + result + `}`
	}
	if s.SSE && msg.Method == "tools/call" {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", out)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// CallsOf returns how many times tool was called.
func (s *Server) CallsOf(tool string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Calls[tool]
}
