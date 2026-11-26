package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func mcpToolsRC(tools ...string) *schemas.RunContext {
	rc := mcpRC()
	rc.Agent = &schemas.AgentConfig{}
	rc.Agent.Gateway.Bifrost = &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{Tools: tools}}
	return rc
}

func TestToolGrantDefaultsToAll(t *testing.T) {
	for name, rc := range map[string]*schemas.RunContext{"nil": nil, "no agent": mcpRC(), "no tools": mcpToolsRC()} {
		if got := toolGrant(rc, "desk"); !reflect.DeepEqual(got, []string{"*"}) {
			t.Errorf("%s: toolGrant = %v, want [*]", name, got)
		}
	}
}

func TestToolGrantPerService(t *testing.T) {
	rc := mcpToolsRC("desk/get_ticket", "shop/get_order", "desk/issue_refund")
	if got := toolGrant(rc, "desk"); !reflect.DeepEqual(got, []string{"get_ticket", "issue_refund"}) {
		t.Fatalf("desk grant = %v", got)
	}
	if got := toolGrant(rc, "shop"); !reflect.DeepEqual(got, []string{"get_order"}) {
		t.Fatalf("shop grant = %v", got)
	}
}

func TestToolGrantServiceStarAndEmpty(t *testing.T) {
	rc := mcpToolsRC("desk/*", "desk/get_ticket")
	if got := toolGrant(rc, "desk"); !reflect.DeepEqual(got, []string{"*"}) {
		t.Fatalf("desk grant = %v, want [*]", got)
	}
	got := toolGrant(rc, "shop")
	if got == nil || len(got) != 0 {
		t.Fatalf("shop grant = %#v, want an empty, non nil list", got)
	}
	b, _ := json.Marshal(got)
	if string(b) != "[]" {
		t.Fatalf("shop grant JSON = %s, want []", b)
	}
}

func TestServeMCPGrantsListedTools(t *testing.T) {
	f := &fakeMCPBifrost{}
	g := newFakeMCPGateway(t, f)
	ups := []schemas.MCPUpstream{{Service: "desk", URL: "http://tp-r_abc123:9200/s/desk/mcp"}}
	if _, _, err := g.ServeMCP(context.Background(), mcpToolsRC("desk/get_ticket"), ups, false); err != nil {
		t.Fatal(err)
	}
	if len(f.grants) != 1 {
		t.Fatalf("grants = %v", f.grants)
	}
	got, _ := json.Marshal(f.grants[0].(map[string]any)["tools_to_execute"])
	if string(got) != `["get_ticket"]` {
		t.Fatalf("key grant tools_to_execute = %s, want [\"get_ticket\"]", got)
	}
}

// fakeMCPEndpoint is Bifrost's per client MCP endpoint: initialize,
// tools/list with the given tools, and tools/call answered by call.
func fakeMCPEndpoint(t *testing.T, tools []string, call func(w http.ResponseWriter, id any)) (*httptest.Server, *[]string) {
	t.Helper()
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.URL.Path+" "+r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		var m struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
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
			var list []any
			for _, n := range tools {
				list = append(list, map[string]any{"name": n, "inputSchema": map[string]any{"type": "object"}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"tools": list}})
		case "tools/call":
			call(w, m.ID)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

func TestCheckForbiddenToolRefused(t *testing.T) {
	srv, auth := fakeMCPEndpoint(t, []string{"get_ticket", "issue_refund"}, func(w http.ResponseWriter, id any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": "tool not allowed"}})
	})
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	c := g.CheckForbiddenTool(context.Background(), "desk-r-abc123", "sk-bf-mcp", "issue_refund", "r_abc123")
	if !c.Listed || !c.Refused || c.Tool != "issue_refund" || c.Marker != "r_abc123" {
		t.Fatalf("check = %+v, want listed and refused", c)
	}
	for _, a := range *auth {
		if a != "/mcp/desk-r-abc123 Bearer sk-bf-mcp" {
			t.Fatalf("request %q, want the slug endpoint with the MCP key", a)
		}
	}
}

func TestCheckForbiddenToolReachedWhenResult(t *testing.T) {
	srv, _ := fakeMCPEndpoint(t, []string{"get_ticket"}, func(w http.ResponseWriter, id any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}})
	})
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	c := g.CheckForbiddenTool(context.Background(), "desk-r-abc123", "sk-bf-mcp", "issue_refund", "r_abc123")
	if c.Listed || c.Refused {
		t.Fatalf("check = %+v, want not listed and not refused", c)
	}
	srv2, _ := fakeMCPEndpoint(t, nil, func(w http.ResponseWriter, id any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"isError": true, "content": []any{}}})
	})
	g2, _ := New(testConfig(srv2.URL))
	if c := g2.CheckForbiddenTool(context.Background(), "s", "k", "issue_refund", "m"); !c.Refused || c.Listed {
		t.Fatalf("isError result: check = %+v, want refused", c)
	}
}
