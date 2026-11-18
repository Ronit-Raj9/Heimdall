package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeMCPBifrost answers the B2 routes the way Bifrost v2.5.1 did on the
// hd-b2 stack: MCP clients (create, list, delete), a key's mcp_configs, the
// MCP log and a provider's prompt_cache block.
type fakeMCPBifrost struct {
	mu        sync.Mutex
	clients   []map[string]any
	grants    []any
	keys      []map[string]any
	logs      []map[string]any
	prompt    any
	codeFlip  bool // report the opposite is_code_mode_client
	noTools   bool
	deletes   []string
	logsQuery string
}

func (f *fakeMCPBifrost) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/mcp/client", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		name, _ := b["name"].(string)
		if strings.Contains(name, "-") {
			http.Error(w, `{"error":{"message":"Invalid client name: name cannot contain hyphens"}}`, http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		code, _ := b["is_code_mode_client"].(bool)
		if f.codeFlip {
			code = !code
		}
		tools := []any{map[string]any{"name": "get_ticket"}, map[string]any{"name": "issue_refund"}}
		if f.noTools {
			tools = nil
		}
		f.clients = append(f.clients, map[string]any{
			"config": map[string]any{"client_id": "cid-" + name, "name": name, "endpoint_slug": strings.ReplaceAll(name, "_", "-"),
				"is_code_mode_client": code, "connection_string": map[string]any{"value": b["connection_string"]}},
			"tools": tools, "state": "healthy",
		})
		_, _ = io.WriteString(w, `{"message":"MCP client connected successfully","status":"success"}`)
	})
	mux.HandleFunc("GET /api/mcp/clients", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"clients": f.clients, "total_count": len(f.clients)})
	})
	mux.HandleFunc("DELETE /api/mcp/client/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.PathValue("id")
		f.deletes = append(f.deletes, id)
		kept := f.clients[:0]
		for _, c := range f.clients {
			if c["config"].(map[string]any)["client_id"] != id {
				kept = append(kept, c)
			}
		}
		f.clients = kept
		_, _ = io.WriteString(w, `{"status":"success"}`)
	})
	mux.HandleFunc("POST /api/governance/virtual-keys", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["provider_configs"] != nil {
			t.Errorf("mcp key body = %v: it must not be able to make model calls", b)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.grants = b["mcp_configs"].([]any)
		f.keys = append(f.keys, map[string]any{"id": "vk-mcp", "name": b["name"], "value": "sk-bf-mcp"})
		_ = json.NewEncoder(w).Encode(map[string]any{"virtual_key": f.keys[len(f.keys)-1]})
	})
	mux.HandleFunc("GET /api/governance/virtual-keys", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"virtual_keys": f.keys})
	})
	mux.HandleFunc("DELETE /api/governance/virtual-keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.PathValue("id")
		f.deletes = append(f.deletes, "key:"+id)
		kept := f.keys[:0]
		for _, k := range f.keys {
			if k["id"] != id {
				kept = append(kept, k)
			}
		}
		f.keys = kept
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("GET /api/mcp-logs", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.logsQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": f.logs, "pagination": map[string]any{"total_count": len(f.logs)}})
	})
	mux.HandleFunc("GET /api/providers/{name}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": r.PathValue("name"), "prompt_cache": f.prompt})
	})
	return mux
}

func newFakeMCPGateway(t *testing.T, f *fakeMCPBifrost) *Gateway {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func mcpRC() *schemas.RunContext {
	return &schemas.RunContext{Run: &schemas.Run{RunID: "r_abc123"}, Key: &schemas.RunKey{ID: "vk-1", Value: "sk-bf-x"}}
}

func TestMCPClientNameAndSlug(t *testing.T) {
	for _, tt := range []struct{ svc, run, name, slug string }{
		{"desk", "r_abc123", "desk_r_abc123", "desk-r-abc123"},
		{"web-search", "r_Q1", "web_search_r_q1", "web-search-r-q1"},
		{"--", "r_1", "mcp_r_1", "mcp-r-1"},
	} {
		if got := MCPClientName(tt.svc, tt.run); got != tt.name {
			t.Errorf("MCPClientName(%q, %q) = %q, want %q", tt.svc, tt.run, got, tt.name)
		}
		if got := MCPSlug(MCPClientName(tt.svc, tt.run)); got != tt.slug {
			t.Errorf("slug = %q, want %q", got, tt.slug)
		}
	}
}

func TestServeMCPCreatesGrantsAndRemoves(t *testing.T) {
	f := &fakeMCPBifrost{}
	g := newFakeMCPGateway(t, f)
	ups := []schemas.MCPUpstream{{Service: "desk", URL: "http://tp-r_abc123:9200/s/desk/mcp"}}
	clients, key, err := g.ServeMCP(context.Background(), mcpRC(), ups, true)
	if err != nil {
		t.Fatal(err)
	}
	if key == nil || key.ID != "vk-mcp" || key.Value != "sk-bf-mcp" || f.keys[0]["name"] != "hm-mcp-r_abc123" {
		t.Fatalf("mcp key = %+v, keys %v", key, f.keys)
	}
	if len(clients) != 1 || clients[0].Name != "desk_r_abc123" || clients[0].Slug != "desk-r-abc123" || clients[0].Tools != 2 {
		t.Fatalf("clients = %+v", clients)
	}
	if len(f.grants) != 1 || f.grants[0].(map[string]any)["mcp_client_name"] != "desk_r_abc123" {
		t.Fatalf("grants = %v", f.grants)
	}
	f.clients = append(f.clients, map[string]any{"config": map[string]any{"client_id": "cid-other", "name": "desk_r_other"}})
	if err := g.RemoveMCP(context.Background(), "r_abc123"); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 2 || f.deletes[0] != "key:vk-mcp" || f.deletes[1] != "cid-desk_r_abc123" {
		t.Fatalf("deletes = %v, want the run's mcp key and only the run's client", f.deletes)
	}
}

func TestServeMCPRefusesAndCleansUp(t *testing.T) {
	ups := []schemas.MCPUpstream{{Service: "desk", URL: "http://tp-r_abc123:9200/s/desk/mcp"}}
	for name, f := range map[string]*fakeMCPBifrost{"code mode not applied": {codeFlip: true}, "no tools discovered": {noTools: true}} {
		t.Run(name, func(t *testing.T) {
			g := newFakeMCPGateway(t, f)
			if _, _, err := g.ServeMCP(context.Background(), mcpRC(), ups, true); err == nil {
				t.Fatal("want an error")
			}
			if len(f.clients) != 0 {
				t.Fatalf("clients left behind: %v", f.clients)
			}
		})
	}
	if _, _, err := newFakeMCPGateway(t, &fakeMCPBifrost{}).ServeMCP(context.Background(), &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}}, ups, false); err == nil {
		t.Fatal("want an error without a key")
	}
}

func TestMCPCallsReadsOnlyTheRunKey(t *testing.T) {
	vk, other := "vk-1", "vk-2"
	f := &fakeMCPBifrost{logs: []map[string]any{
		{"id": "l1", "tool_name": "get_ticket", "server_label": "desk_r_abc123", "virtual_key_id": vk, "status": "success", "latency": 40, "arguments": `{"ticket_id":"T-1001"}`},
		{"id": "l2", "tool_name": "get_ticket", "server_label": "desk_r_abc123", "virtual_key_id": other, "status": "success"},
	}}
	g := newFakeMCPGateway(t, f)
	calls, complete, reason := g.MCPCalls(context.Background(), vk, []schemas.BifrostMCPClient{{Service: "desk", Name: "desk_r_abc123"}})
	if !complete || reason != "" || len(calls) != 1 {
		t.Fatalf("calls = %+v complete %t reason %q", calls, complete, reason)
	}
	c := calls[0]
	if c.Service != "desk" || c.Tool != "get_ticket" || c.LatencyMS != 40 || c.ArgsSHA256 != utils.CanonicalArgsSHA256(map[string]any{"ticket_id": "T-1001"}) {
		t.Fatalf("call = %+v", c)
	}
	if !strings.Contains(f.logsQuery, "virtual_key_ids=vk-1") {
		t.Fatalf("query %q does not filter on the run key", f.logsQuery)
	}
}

func TestPromptCacheConfigured(t *testing.T) {
	for _, tt := range []struct {
		block any
		want  bool
	}{{nil, false}, {map[string]any{"auto_inject": false}, true}, {map[string]any{}, true}} {
		g := newFakeMCPGateway(t, &fakeMCPBifrost{prompt: tt.block})
		got, err := g.PromptCacheConfigured(context.Background(), "anthropic")
		if err != nil || got != tt.want {
			t.Errorf("block %v: got %t %v, want %t", tt.block, got, err, tt.want)
		}
	}
}

func TestFeatureFieldsFromDetailRow(t *testing.T) {
	raw := []byte(`{"id":"r1","object":"chat_completion","provider":"anthropic","model":"m","status":"success","latency":45.3,"upstream_latency":44.5,"overhead_latency":0.77,"complexity_tier":"COMPLEX","cache_debug":{"cache_hit":true,"hit_type":"direct"}}`)
	p, err := parseDetail(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := p.Req
	if r.OverheadMS == nil || *r.OverheadMS != 0.77 || r.UpstreamMS == nil || *r.UpstreamMS != 44.5 || r.ComplexityTier != "COMPLEX" || !r.CacheHit || r.CacheHitType != "direct" {
		t.Fatalf("row = %+v", r)
	}
	p, err = parseDetail([]byte(`{"id":"r2","status":"success"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Req.OverheadMS != nil || p.Req.UpstreamMS != nil || p.Req.CacheHitType != "" {
		t.Fatalf("a row without the fields got %+v", p.Req)
	}
}

func TestRemoveStaleMCP(t *testing.T) {
	f := &fakeMCPBifrost{
		clients: []map[string]any{
			{"config": map[string]any{"client_id": "c-live", "name": "desk_r_live1", "connection_string": map[string]any{"value": "http://tp-r_live1:9200/s/desk/mcp"}}},
			{"config": map[string]any{"client_id": "c-dead", "name": "desk_r_dead1", "connection_string": map[string]any{"value": "http://tp-r_dead1:9200/s/desk/mcp"}}},
			{"config": map[string]any{"client_id": "c-team", "name": "billing_r_2024", "connection_string": map[string]any{"value": "http://billing.internal/mcp"}}},
		},
		keys: []map[string]any{
			{"id": "k-live", "name": "hm-mcp-r_live1"}, {"id": "k-dead", "name": "hm-mcp-r_dead1"}, {"id": "k-team", "name": "hm-mcp-team"},
		},
	}
	g := newFakeMCPGateway(t, f)
	n, err := g.RemoveStaleMCP(context.Background(), map[string]bool{"r_live1": true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(f.deletes) != 2 || f.deletes[0] != "key:k-dead" || f.deletes[1] != "c-dead" {
		t.Fatalf("deleted %d: %v; want only the dead run's key and client", n, f.deletes)
	}
}
