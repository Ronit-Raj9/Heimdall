package faultproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestOrgSlotsEveryProxiedProvider checks that the proxy accepts an
// organization slot for every proxied Bifrost provider (lane A09), and that
// direct providers are not in its table.
func TestOrgSlotsEveryProxiedProvider(t *testing.T) {
	env := newTestEnv(t, "", "")
	ctx := context.Background()
	for _, spec := range schemas.BifrostProviders() {
		_, inTable := env.srv.providers.byName[spec.ID]
		if spec.Route == schemas.RouteDirect {
			if inTable {
				t.Errorf("direct provider %s is in the proxy table", spec.ID)
			}
			continue
		}
		if !inTable {
			t.Errorf("proxied provider %s is not in the proxy table", spec.ID)
			continue
		}
		name := "org_a_" + strings.ReplaceAll(spec.ID, "-", "") + "_1"
		sl := OrgSlot{Name: name, Provider: spec.ID, Auth: name, Key: "k-" + spec.ID}
		if spec.NeedsBaseURL {
			sl.BaseURL = "https://" + spec.ID + ".example.com/v1"
		}
		if err := env.client.RegisterOrgSlots(ctx, []OrgSlot{sl}); err != nil {
			t.Errorf("%s: %v", spec.ID, err)
		}
		if env.srv.providers.byName[spec.ID].BaseURL == "" {
			sl.Name, sl.Auth, sl.BaseURL = name+"x", name+"x", ""
			if err := env.client.RegisterOrgSlots(ctx, []OrgSlot{sl}); err == nil {
				t.Errorf("%s: slot without a base URL accepted", spec.ID)
			}
		}
	}
}

// TestOrgSlotsUpstreamShapes checks the upstream request per provider
// shape: a custom OpenAI-compatible base that is not .../v1 replaces the
// request's /v1, Azure gets its api-key header, and a keyless vLLM server
// gets no key.
func TestOrgSlotsUpstreamShapes(t *testing.T) {
	type hit struct{ path, auth, apiKey string }
	var mu sync.Mutex
	var hits []hit
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, hit{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("api-key")})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer up.Close()
	env := newTestEnv(t, "", "")
	env.srv.client = up.Client()
	ctx := context.Background()
	slots := []OrgSlot{
		{Name: "org_a_zhipu_1", Provider: ProviderOpenAI, Auth: "ph-zhipu", Key: "zk", BaseURL: up.URL + "/api/paas/v4"},
		{Name: "org_a_moonshot_1", Provider: ProviderOpenAI, Auth: "ph-moon", Key: "mk", BaseURL: up.URL + "/v1"},
		{Name: "org_a_azure_1", Provider: "azure", Auth: "ph-azure", Key: "az", BaseURL: up.URL},
		{Name: "org_a_vllm_1", Provider: "vllm", Auth: "ph-vllm", BaseURL: up.URL + "/v1"},
	}
	if err := env.client.RegisterOrgSlots(ctx, slots); err != nil {
		t.Fatal(err)
	}
	send := func(path, header, value string) {
		req, _ := http.NewRequest(http.MethodPost, env.data.URL+path, strings.NewReader(`{"model":"m","messages":[]}`))
		req.Header.Set(header, value)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, b)
		}
	}
	send("/v1/chat/completions", "Authorization", "Bearer ph-zhipu")
	send("/v1/chat/completions", "Authorization", "Bearer ph-moon")
	send("/openai/v1/chat/completions", "api-key", "ph-azure")
	send("/v1/chat/completions", "Authorization", "Bearer ph-vllm")
	mu.Lock()
	defer mu.Unlock()
	want := []hit{
		{"/api/paas/v4/chat/completions", "Bearer zk", ""},
		{"/v1/chat/completions", "Bearer mk", ""},
		{"/openai/v1/chat/completions", "", "az"},
		{"/v1/chat/completions", "", ""},
	}
	if len(hits) != len(want) {
		t.Fatalf("hits %+v", hits)
	}
	for i := range want {
		if hits[i] != want[i] {
			t.Errorf("call %d: got %+v want %+v", i, hits[i], want[i])
		}
	}
	for _, e := range env.entries(t) {
		if e.Kind != KindMessage {
			t.Errorf("ledger line %s kind %s, want message", e.Slot, e.Kind)
		}
	}
}
