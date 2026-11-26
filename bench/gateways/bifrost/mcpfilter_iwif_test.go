package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestCheckForbiddenToolCallsOnlyTheNamedTool: the filter check makes one
// tools/call, for the forbidden tool it was given, with the run's marker as
// its only argument, even when the client lists other tools (group F
// security review).
func TestCheckForbiddenToolCallsOnlyTheNamedTool(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.Unmarshal(body, &m)
		w.Header().Set("Content-Type", "application/json")
		switch m.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "bf", "version": "1"}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			list := []any{}
			for _, n := range []string{"get_ticket", "issue_refund", "delete_account"} {
				list = append(list, map[string]any{"name": n, "inputSchema": map[string]any{"type": "object"}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"tools": list}})
		case "tools/call":
			mu.Lock()
			calls = append(calls, m.Params)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32000, "message": "tool not allowed"}})
		}
	}))
	t.Cleanup(srv.Close)
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	c := g.CheckForbiddenTool(context.Background(), "desk-r-abc123", "sk-bf-mcp", "issue_refund", "r_abc123")
	if !c.Refused {
		t.Fatalf("check = %+v", c)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0]["name"] != "issue_refund" {
		t.Fatalf("tools/call = %v, want one call of issue_refund", calls)
	}
	args, _ := calls[0]["arguments"].(map[string]any)
	if len(args) != 1 || args["heimdall_filter_check"] != "r_abc123" {
		t.Fatalf("arguments = %v, want only the run marker", calls[0]["arguments"])
	}
}
