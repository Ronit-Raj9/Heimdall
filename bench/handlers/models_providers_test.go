package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestKeyTestRequestEveryProvider checks the key test call each Bifrost
// provider gets (lane A09): its host, path and auth header carry the key.
func TestKeyTestRequestEveryProvider(t *testing.T) {
	want := map[string]struct{ url, header, value string }{
		"openai":         {"https://api.openai.com/v1/models", "Authorization", "Bearer K"},
		"anthropic":      {"https://api.anthropic.com/v1/models?limit=100", "x-api-key", "K"},
		"mistral":        {"https://api.mistral.ai/v1/models", "Authorization", "Bearer K"},
		"deepseek":       {"https://api.deepseek.com/models", "Authorization", "Bearer K"},
		"cohere":         {"https://api.cohere.ai/v1/models", "Authorization", "Bearer K"},
		"elevenlabs":     {"https://api.elevenlabs.io/v1/models", "xi-api-key", "K"},
		"replicate":      {"https://api.replicate.com/v1/account", "Authorization", "Bearer K"},
		"huggingface":    {"https://huggingface.co/api/whoami-v2", "Authorization", "Bearer K"},
		"azure":          {"https://r.openai.azure.com/openai/v1/models", "api-key", "K"},
		"databricks":     {"https://w.cloud.databricks.com/api/2.0/serving-endpoints", "Authorization", "Bearer K"},
		"github-copilot": {"https://api.github.com/copilot_internal/v2/token", "Authorization", "token K"},
		"cloudflare":     {"https://api.cloudflare.com/client/v4/user/tokens/verify", "Authorization", "Bearer K"},
		"vllm":           {"https://v.example.com/v1/models", "Authorization", "Bearer K"},
		"zhipu":          {"https://open.bigmodel.cn/api/paas/v4/models", "Authorization", "Bearer K"},
	}
	base := map[string]string{"azure": "https://r.openai.azure.com", "databricks": "https://w.cloud.databricks.com",
		"vllm": "https://v.example.com/v1", "zhipu": "https://open.bigmodel.cn/api/paas/v4"}
	for id, w := range want {
		value := "K"
		if id == "cloudflare" {
			value = `{"api_key":"K","account_id":"a"}`
		}
		req, err := testRequest(context.Background(), &keystore.Key{Provider: id, BaseURL: base[id]}, value)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		if req.URL.String() != w.url || req.Header.Get(w.header) != w.value {
			t.Errorf("%s: %s %s=%q", id, req.URL, w.header, req.Header.Get(w.header))
		}
	}
	// Every non-direct provider gets a test request.
	for _, p := range schemas.BifrostProviders() {
		if p.Test.None {
			continue
		}
		k := &keystore.Key{Provider: p.ID}
		if p.NeedsBaseURL {
			k.BaseURL = "https://" + p.ID + ".example.com/v1"
		}
		if _, err := testRequest(context.Background(), k, "K"); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
}

// TestKeyTestDirectProviders checks that SigV4 and OAuth credentials are
// checked for complete fields without a network call.
func TestKeyTestDirectProviders(t *testing.T) {
	res := testProviderKey(context.Background(), false, &keystore.Key{Provider: "bedrock"}, `{"access_key":"A","secret_key":"S","region":"us-east-1"}`)
	if res.Status != keystore.StatusUntested || res.OK {
		t.Fatalf("bedrock: %+v", res)
	}
	res = testProviderKey(context.Background(), false, &keystore.Key{Provider: "vertex"}, `{"project_id":"p"}`)
	if res.Status != keystore.StatusInvalid || strings.Contains(res.Message, `"p"`) {
		t.Fatalf("vertex half filled: %+v", res)
	}
}

func TestBifrostProvidersView(t *testing.T) {
	v := bifrostProvidersView()
	b, _ := json.Marshal(v)
	for _, id := range []string{`"bedrock"`, `"vertex"`, `"github-copilot"`} {
		if !strings.Contains(string(b), id) {
			t.Errorf("view lacks %s", id)
		}
	}
	if !v.Custom.Custom || !v.Custom.NeedsBaseURL {
		t.Errorf("custom form %+v", v.Custom)
	}
}
