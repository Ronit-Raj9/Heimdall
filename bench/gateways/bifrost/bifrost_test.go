package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	testToken = "test-setup-token"
	testModel = "claude-sonnet-4-6"
	testProxy = "http://faultproxy:9100"
	// testOpenAIModel is in the pinned datasheet (deploy/bifrost/datasheet)
	// and not deprecated there.
	testOpenAIModel = "gpt-5.4-mini"
)

// fakeBifrost is an in-memory stand-in for the v2.5.1 management API, shaped
// after the answers captured from the spike stack.
type fakeBifrost struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	provider  map[string]any   // the anthropic provider
	keys      []map[string]any // the anthropic provider's keys
	others    map[string]*fakeProvider
	keySeq    int
	vks       []map[string]any
	rows      []json.RawMessage // detail rows of every key
	hideLists int               // list calls answered with no rows, like an unflushed writer
	config    map[string]any
	calls     map[string]int // "METHOD /pattern" -> count
	bodies    map[string][]map[string]any

	// Hygiene state (hygiene.go): loaded plugins, plugin config rows, MCP
	// clients, routing rules and model routers (nil: the route answers 404).
	loaded     []string
	pluginRows []map[string]any
	mcpClients int
	rules      []map[string]any
	routers    []map[string]any
	down       map[string]bool // "METHOD /pattern" answered with 500
	dropped    map[string]any  // GET /api/logs/dropped answer; nil: dropped_requests 0
}

func newFakeBifrost(t *testing.T) *fakeBifrost {
	t.Helper()
	f := &fakeBifrost{t: t, calls: map[string]int{}, bodies: map[string][]map[string]any{}, config: goodConfig(),
		loaded: []string{"logging", "governance"}}
	mux := http.NewServeMux()
	routes := map[string]http.HandlerFunc{
		"GET /health":      f.health,
		"GET /api/version": func(w http.ResponseWriter, _ *http.Request) { writeJSONT(w, 200, "v2.5.1") },
		"GET /api/config":  f.getConfig,
		"GET /api/plugins/loaded": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONT(w, 200, map[string]any{"plugins": f.loaded})
		},
		"GET /api/plugins": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONT(w, 200, map[string]any{"plugins": orEmpty(f.pluginRows), "count": len(f.pluginRows)})
		},
		"GET /api/mcp/clients": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONT(w, 200, map[string]any{"clients": []any{}, "count": 0, "total_count": f.mcpClients, "limit": 25, "offset": 0})
		},
		"GET /api/routing/rules": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONT(w, 200, map[string]any{"rules": orEmpty(f.rules), "count": len(f.rules), "total_count": len(f.rules)})
		},
		"GET /api/routing/model-routers": func(w http.ResponseWriter, _ *http.Request) {
			if f.routers == nil {
				writeJSONT(w, 404, map[string]any{"error": map[string]any{"message": "Route not found: /api/routing/model-routers"}})
				return
			}
			writeJSONT(w, 200, map[string]any{"routers": f.routers, "count": len(f.routers)})
		},
		"GET /api/providers/{p}":                   f.getProvider,
		"POST /api/providers":                      f.postProvider,
		"PUT /api/providers/{p}":                   f.putProvider,
		"GET /api/providers/{p}/keys":              f.listKeys,
		"POST /api/providers/{p}/keys":             f.postKey,
		"PUT /api/providers/{p}/keys/{id}":         f.putKey,
		"GET /api/governance/virtual-keys":         f.listVKs,
		"POST /api/governance/virtual-keys":        f.postVK,
		"DELETE /api/governance/virtual-keys/{id}": f.deleteVK,
		"GET /api/logs":                            f.listLogs,
		"GET /api/logs/dropped": func(w http.ResponseWriter, _ *http.Request) {
			if f.dropped != nil {
				writeJSONT(w, 200, f.dropped)
				return
			}
			writeJSONT(w, 200, map[string]any{"dropped_requests": 0})
		},
		"GET /api/logs/{id}": f.getLog,
		// W9 routing policy under test (routing_test.go).
		"POST /api/routing/rules":        f.postRule,
		"DELETE /api/routing/rules/{id}": f.deleteRule,
	}
	for pattern, h := range routes {
		mux.HandleFunc(pattern, f.guard(pattern, h))
	}
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// orEmpty answers an empty JSON list instead of null.
func orEmpty(rows []map[string]any) []map[string]any {
	if rows == nil {
		return []map[string]any{}
	}
	return rows
}

func goodConfig() map[string]any {
	return map[string]any{"client_config": map[string]any{
		"enable_logging": true, "disable_content_logging": false, "enforce_auth_on_inference": true,
		"mcp_disable_auto_tool_inject": true, "allow_direct_keys": false,
	}}
}

func (f *fakeBifrost) guard(pattern string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls[pattern]++
		if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				f.bodies[pattern] = append(f.bodies[pattern], body)
			}
		}
		f.mu.Unlock()
		if pattern != "GET /health" && r.Header.Get(setupTokenHeader) != testToken {
			writeJSONT(w, 401, map[string]any{"error": map[string]any{"message": "setup token required"}})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.down[pattern] {
			writeJSONT(w, 500, map[string]any{"error": map[string]any{"message": "down"}})
			return
		}
		h(w, r)
	}
}

func (f *fakeBifrost) count(pattern string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[pattern]
}

func (f *fakeBifrost) lastBody(pattern string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bodies[pattern]
	if len(b) == 0 {
		f.t.Fatalf("no body recorded for %s", pattern)
	}
	return b[len(b)-1]
}

func (f *fakeBifrost) health(w http.ResponseWriter, _ *http.Request) {
	writeJSONT(w, 200, map[string]any{"status": "ok"})
}

func (f *fakeBifrost) getConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSONT(w, 200, f.config)
}

// fakeProvider is a provider other than anthropic in the fake.
type fakeProvider struct {
	cfg  map[string]any
	keys []map[string]any
}

// state returns the named provider's config and keys, anthropic's being
// f.provider and f.keys.
func (f *fakeBifrost) state(name string) (*map[string]any, *[]map[string]any) {
	if name == "anthropic" {
		return &f.provider, &f.keys
	}
	if f.others == nil {
		f.others = map[string]*fakeProvider{}
	}
	p, ok := f.others[name]
	if !ok {
		p = &fakeProvider{}
		f.others[name] = p
	}
	return &p.cfg, &p.keys
}

func (f *fakeBifrost) getProvider(w http.ResponseWriter, r *http.Request) {
	cfg, _ := f.state(r.PathValue("p"))
	if *cfg == nil {
		writeJSONT(w, 404, map[string]any{"error": map[string]any{"message": "Provider not found: not found"}})
		return
	}
	writeJSONT(w, 200, *cfg)
}

func (f *fakeBifrost) postProvider(w http.ResponseWriter, _ *http.Request) {
	body := f.bodies["POST /api/providers"][len(f.bodies["POST /api/providers"])-1]
	cfg, _ := f.state(fmt.Sprint(body["provider"]))
	*cfg = body
	writeJSONT(w, 200, body)
}

func (f *fakeBifrost) putProvider(w http.ResponseWriter, r *http.Request) {
	cfg, _ := f.state(r.PathValue("p"))
	*cfg = f.bodies["PUT /api/providers/{p}"][len(f.bodies["PUT /api/providers/{p}"])-1]
	writeJSONT(w, 200, *cfg)
}

func (f *fakeBifrost) listKeys(w http.ResponseWriter, r *http.Request) {
	cfg, keys := f.state(r.PathValue("p"))
	if *cfg == nil {
		writeJSONT(w, 404, map[string]any{})
		return
	}
	writeJSONT(w, 200, map[string]any{"keys": *keys, "total": len(*keys)})
}

func (f *fakeBifrost) postKey(w http.ResponseWriter, r *http.Request) {
	body := f.bodies["POST /api/providers/{p}/keys"][len(f.bodies["POST /api/providers/{p}/keys"])-1]
	for _, name := range append([]string{"anthropic"}, f.otherNames()...) {
		_, keys := f.state(name)
		for _, k := range *keys {
			if k["name"] == body["name"] {
				writeJSONT(w, 409, map[string]any{"error": map[string]any{"message": "API key names must be unique across providers"}})
				return
			}
		}
	}
	_, keys := f.state(r.PathValue("p"))
	f.keySeq++
	body["id"] = fmt.Sprintf("key-%d", f.keySeq)
	*keys = append(*keys, body)
	writeJSONT(w, 200, body)
}

func (f *fakeBifrost) otherNames() []string {
	var out []string
	for name := range f.others {
		out = append(out, name)
	}
	return out
}

func (f *fakeBifrost) putKey(w http.ResponseWriter, r *http.Request) {
	body := f.bodies["PUT /api/providers/{p}/keys/{id}"][len(f.bodies["PUT /api/providers/{p}/keys/{id}"])-1]
	_, keys := f.state(r.PathValue("p"))
	for i, k := range *keys {
		if k["id"] == r.PathValue("id") {
			body["id"] = k["id"]
			(*keys)[i] = body
			writeJSONT(w, 200, body)
			return
		}
	}
	writeJSONT(w, 404, map[string]any{})
}

func (f *fakeBifrost) listVKs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	page := f.vks[min(offset, len(f.vks)):]
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	writeJSONT(w, 200, map[string]any{"virtual_keys": page, "count": len(page)})
}

func (f *fakeBifrost) postVK(w http.ResponseWriter, _ *http.Request) {
	body := f.bodies["POST /api/governance/virtual-keys"][len(f.bodies["POST /api/governance/virtual-keys"])-1]
	vk := map[string]any{"id": fmt.Sprintf("vk-%d", len(f.vks)+1+f.calls["DELETE /api/governance/virtual-keys/{id}"]), "name": body["name"], "value": "sk-bf-" + fmt.Sprint(len(f.vks))}
	f.vks = append(f.vks, vk)
	writeJSONT(w, 200, map[string]any{"message": "Virtual key created successfully", "virtual_key": vk})
}

func (f *fakeBifrost) deleteVK(w http.ResponseWriter, r *http.Request) {
	for i, vk := range f.vks {
		if vk["id"] == r.PathValue("id") {
			f.vks = append(f.vks[:i:i], f.vks[i+1:]...)
			writeJSONT(w, 200, map[string]any{"message": "deleted"})
			return
		}
	}
	writeJSONT(w, 404, map[string]any{"error": map[string]any{"message": "virtual key not found"}})
}

func (f *fakeBifrost) listLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("start_time") == "" || q.Get("virtual_key_ids") == "" {
		writeJSONT(w, 400, map[string]any{"error": "start_time and virtual_key_ids expected"})
		return
	}
	rows := f.rows
	if f.hideLists > 0 {
		f.hideLists--
		rows = nil
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	page := rows[min(offset, len(rows)):]
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	writeJSONT(w, 200, map[string]any{"logs": page, "has_logs": len(rows) > 0})
}

func (f *fakeBifrost) getLog(w http.ResponseWriter, r *http.Request) {
	for _, row := range f.rows {
		var head struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(row, &head) == nil && head.ID == r.PathValue("id") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(row)
			return
		}
	}
	writeJSONT(w, 404, map[string]any{})
}

func writeJSONT(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func testConfig(url string) Config {
	return Config{
		URL: url, SetupToken: testToken, FaultProxyURL: testProxy, Model: testModel,
		Slots: []Slot{{Name: "fp-slot-1", Value: "slot-auth-1"}, {Name: "fp-slot-2", Value: "slot-auth-2"}},
	}
}

func newTestGateway(t *testing.T, f *fakeBifrost) *Gateway {
	t.Helper()
	g, err := New(testConfig(f.srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestNew(t *testing.T) {
	base := testConfig("http://127.0.0.1:18080/")
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "valid with defaults", mutate: func(*Config) {}},
		{name: "no url", mutate: func(c *Config) { c.URL = "" }, wantErr: "url is required"},
		{name: "no token", mutate: func(c *Config) { c.SetupToken = "" }, wantErr: "setup token is required"},
		{name: "no proxy", mutate: func(c *Config) { c.FaultProxyURL = "" }, wantErr: "fault proxy url is required"},
		{name: "no model", mutate: func(c *Config) { c.Model = "" }, wantErr: "model is required"},
		{name: "negative retries", mutate: func(c *Config) { c.MaxRetries = -1 }, wantErr: "negative"},
		{name: "no slots", mutate: func(c *Config) { c.Slots = nil }, wantErr: "at least one key slot"},
		{name: "duplicate slot", mutate: func(c *Config) { c.Slots = append(c.Slots, c.Slots[0]) }, wantErr: "listed twice"},
		{name: "slot without value", mutate: func(c *Config) { c.Slots = []Slot{{Name: "a"}} }, wantErr: "needs a name and a value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.Slots = append([]Slot{}, base.Slots...)
			tt.mutate(&cfg)
			g, err := New(cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if g.GetName() != "bifrost" || g.GetVersion() != "1.0.0" {
				t.Errorf("part = %s %s", g.GetName(), g.GetVersion())
			}
			if len(g.providers) != 1 || g.providers[0].Name != "anthropic" || g.providers[0].Mode != ModeProxied || g.cfg.URL != "http://127.0.0.1:18080" {
				t.Errorf("defaults: providers %+v url %q", g.providers, g.cfg.URL)
			}
		})
	}
}

func TestSetup(t *testing.T) {
	tests := []struct {
		name         string
		provider     map[string]any
		keys         []map[string]any
		wantPosts    int
		wantPuts     int
		wantKeyPosts int
		wantKeyPuts  int
	}{
		{name: "fresh gateway", wantPosts: 1, wantKeyPosts: 2},
		{
			name: "drifted provider and one existing key", provider: map[string]any{"network_config": map[string]any{"base_url": "https://api.anthropic.com", "max_retries": 3}},
			keys:     []map[string]any{{"id": "key-9", "name": "fp-slot-2", "models": []any{"claude-haiku-5-5"}}},
			wantPuts: 1, wantKeyPosts: 1, wantKeyPuts: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeBifrost(t)
			f.provider, f.keys = tt.provider, tt.keys
			g := newTestGateway(t, f)
			if err := g.Setup(context.Background()); err != nil {
				t.Fatalf("Setup: %v", err)
			}
			if got := f.count("POST /api/providers"); got != tt.wantPosts {
				t.Errorf("provider posts = %d, want %d", got, tt.wantPosts)
			}
			if got := f.count("PUT /api/providers/{p}"); got != tt.wantPuts {
				t.Errorf("provider puts = %d, want %d", got, tt.wantPuts)
			}
			if got := f.count("POST /api/providers/{p}/keys"); got != tt.wantKeyPosts {
				t.Errorf("key posts = %d, want %d", got, tt.wantKeyPosts)
			}
			if got := f.count("PUT /api/providers/{p}/keys/{id}"); got != tt.wantKeyPuts {
				t.Errorf("key puts = %d, want %d", got, tt.wantKeyPuts)
			}
			nc, _ := f.provider["network_config"].(map[string]any)
			if nc["base_url"] != testProxy || nc["allow_private_network"] != true || f.provider["store_raw_request_response"] != true {
				t.Errorf("provider = %v", f.provider)
			}
			if len(g.keyIDs) != 2 || g.keyIDs["fp-slot-1"] == "" || g.keyIDs["fp-slot-2"] == "" {
				t.Errorf("key ids = %v", g.keyIDs)
			}
			for _, k := range f.keys {
				if k["weight"] != float64(1) || k["enabled"] != true {
					t.Errorf("key %v lacks weight 1 or enabled", k)
				}
			}
			// Second run changes nothing on the provider and creates no key.
			if err := g.Setup(context.Background()); err != nil {
				t.Fatalf("second Setup: %v", err)
			}
			if f.count("POST /api/providers") != tt.wantPosts || f.count("PUT /api/providers/{p}") != tt.wantPuts {
				t.Errorf("second Setup touched the provider")
			}
			if f.count("POST /api/providers/{p}/keys") != tt.wantKeyPosts {
				t.Errorf("second Setup created keys")
			}
		})
	}
}

func TestMergeModels(t *testing.T) {
	tests := []struct {
		have []string
		want string
		out  string
	}{
		{nil, "a", "a"},
		{[]string{"a"}, "a", "a"},
		{[]string{"b", "a"}, "c", "b,a,c"},
		{[]string{"", "b"}, "", "b"},
	}
	for _, tt := range tests {
		t.Run(tt.out, func(t *testing.T) {
			if got := strings.Join(mergeModels(tt.have, tt.want), ","); got != tt.out {
				t.Errorf("mergeModels = %q, want %q", got, tt.out)
			}
		})
	}
}

// runContext returns a minimal run context for runID.
func runContext(runID, model string) *schemas.RunContext {
	return &schemas.RunContext{Run: &schemas.Run{RunID: runID}, Agent: &schemas.AgentConfig{Model: model}}
}

// multiConfig is a gateway with a proxied anthropic provider and a direct
// openai provider whose key comes from a fake environment.
func multiConfig(url string) Config {
	return Config{
		URL: url, SetupToken: testToken, FaultProxyURL: testProxy, MaxRetries: 1,
		Getenv: func(k string) string { return map[string]string{"OPENAI_API_KEY": "sk-real-openai"}[k] },
		Providers: []ProviderSetup{
			{Name: "anthropic", Model: testModel, Slots: []Slot{{Name: "fp-slot-1", Value: "fp-slot-1"}}},
			{Name: "openai", Mode: ModeDirect, KeyEnv: "OPENAI_API_KEY", Model: testOpenAIModel,
				Slots: []Slot{{Name: "oa-slot-1"}, {Name: "oa-slot-2"}}},
		},
	}
}

func TestSetupProviders(t *testing.T) {
	f := newFakeBifrost(t)
	g, err := New(multiConfig(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.ProviderMode("anthropic") != ModeProxied || g.ProviderMode("openai") != ModeDirect || g.ProviderMode("gemini") != "" {
		t.Errorf("modes %q %q %q", g.ProviderMode("anthropic"), g.ProviderMode("openai"), g.ProviderMode("gemini"))
	}
	anc, _ := f.provider["network_config"].(map[string]any)
	if anc["base_url"] != testProxy || anc["allow_private_network"] != true {
		t.Errorf("anthropic provider = %v", f.provider)
	}
	oa := f.others["openai"]
	onc, _ := oa.cfg["network_config"].(map[string]any)
	if onc["base_url"] != "" || onc["allow_private_network"] != false || oa.cfg["store_raw_request_response"] != true || onc["max_retries"] != float64(1) {
		t.Errorf("openai provider = %v", oa.cfg)
	}
	if len(oa.keys) != 2 || oa.keys[0]["value"] != "sk-real-openai" || oa.keys[1]["value"] != "sk-real-openai" || f.keys[0]["value"] != "fp-slot-1" {
		t.Errorf("keys anthropic %v openai %v", f.keys, oa.keys)
	}
	// A second Setup changes nothing on the providers.
	puts := f.count("PUT /api/providers/{p}")
	if err := g.Setup(context.Background()); err != nil || f.count("PUT /api/providers/{p}") != puts {
		t.Errorf("second setup: %v, provider PUTs %d -> %d", err, puts, f.count("PUT /api/providers/{p}"))
	}
	// A direct provider without its key refuses to set up.
	cfg := multiConfig(f.srv.URL)
	cfg.Getenv = func(string) string { return "" }
	g2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := g2.Setup(context.Background()); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY is not set") {
		t.Errorf("setup without key: %v", err)
	}
}

func TestNewProviders(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "duplicate provider", mutate: func(c *Config) { c.Providers = append(c.Providers, c.Providers[0]) }, wantErr: "listed twice"},
		{name: "slot shared across providers", mutate: func(c *Config) { c.Providers[1].Slots = []Slot{{Name: "fp-slot-1"}} }, wantErr: "slot fp-slot-1 is listed twice"},
		{name: "bad mode", mutate: func(c *Config) { c.Providers[0].Mode = "mirror" }, wantErr: "mode must be proxied or direct"},
		{name: "direct without key env", mutate: func(c *Config) { c.Providers[1].KeyEnv = "" }, wantErr: "key env is required"},
		{name: "proxied without proxy", mutate: func(c *Config) { c.FaultProxyURL = "" }, wantErr: "fault proxy url is required"},
		{name: "direct only needs no proxy", mutate: func(c *Config) { c.FaultProxyURL, c.Providers = "", c.Providers[1:] }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := multiConfig("http://x")
			cfg.Providers = append([]ProviderSetup{}, cfg.Providers...)
			tt.mutate(&cfg)
			_, err := New(cfg)
			if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
