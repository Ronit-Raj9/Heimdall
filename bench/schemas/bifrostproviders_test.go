package schemas

import (
	"strings"
	"testing"
)

// coreProviders are Bifrost core's chat and speech providers
// (core/schemas/bifrost.go ModelProvider), all of which Heimdall must offer.
var coreProviders = []string{"openai", "azure", "anthropic", "bedrock", "bedrock_mantle", "sagemaker", "cohere", "vertex", "mistral",
	"ollama", "opencode-go", "opencode-zen", "groq", "sgl", "parasail", "perplexity", "cerebras", "deepseek", "gemini", "openrouter",
	"elevenlabs", "huggingface", "nebius", "xai", "replicate", "vllm", "fireworks", "sarvam", "wafer", "github-copilot", "databricks",
	"cloudflare", "typesafe", "minimax", "byteplus", "baseten", "kimchi"}

func TestBifrostProvidersCoverCore(t *testing.T) {
	for _, id := range coreProviders {
		p, ok := LookupBifrostProvider(id)
		if !ok {
			t.Errorf("provider %s missing", id)
			continue
		}
		if p.Route != RouteProxied && p.Route != RouteDirect {
			t.Errorf("%s route %q", id, p.Route)
		}
		if p.Route == RouteProxied && (p.Format == "" || len(p.Message) == 0 || (p.AuthHeader == "") == (p.AuthQuery == "")) {
			t.Errorf("%s: proxied provider needs a format, message routes and one auth style: %+v", id, p)
		}
		if p.BaseURL == "" && !p.NeedsBaseURL && p.Route == RouteProxied {
			t.Errorf("%s: no base URL and none asked of the key", id)
		}
		if !p.Test.None && p.TestBaseURL == "" && !p.NeedsBaseURL {
			t.Errorf("%s: key test has no base URL", id)
		}
	}
	seen := map[string]bool{}
	for _, p := range BifrostProviders() {
		if seen[p.ID] {
			t.Errorf("%s listed twice", p.ID)
		}
		seen[p.ID] = true
	}
}

func TestResolveAndSplit(t *testing.T) {
	for in, want := range map[string]string{"openai-compatible": "openai", "google": "gemini", "Groq": "groq", "zhipu": "zhipu"} {
		if got, _, _ := ResolveBifrostProvider(in); got != want {
			t.Errorf("resolve %s = %s", in, got)
		}
	}
	if _, spec, custom := ResolveBifrostProvider("moonshot"); !custom || !spec.NeedsBaseURL || spec.Format != "openai" {
		t.Errorf("custom spec %+v", spec)
	}
	for _, id := range []string{"openai", "openai-compatible", "google", "runway", "X", "a", "has space"} {
		if ValidCustomProviderID(id) {
			t.Errorf("%q accepted as a custom id", id)
		}
	}
	if !ValidCustomProviderID("zhipu") || !ValidCustomProviderID("xiaomi-mimo") {
		t.Error("custom ids refused")
	}
	cases := []struct{ in, prov, model string }{
		{"deepseek/deepseek-chat", "deepseek", "deepseek-chat"},
		{"openrouter/moonshotai/kimi-k2", "openrouter", "moonshotai/kimi-k2"},
		{"meta-llama/llama-3.3-70b-instruct", "", "meta-llama/llama-3.3-70b-instruct"},
		{"gpt-4o-mini", "", "gpt-4o-mini"},
		{"zhipu/glm-4.6", "zhipu", "glm-4.6"},
		{"github-copilot/gpt-4.1", "github-copilot", "gpt-4.1"},
	}
	for _, c := range cases {
		if p, m := SplitProviderModel(c.in, "zhipu"); p != c.prov || m != c.model {
			t.Errorf("split %s = %s, %s", c.in, p, m)
		}
	}
}

func TestCredentialShapes(t *testing.T) {
	spec := func(id string) BifrostProviderSpec { p, _ := LookupBifrostProvider(id); return p }
	ok := []struct{ id, value, base string }{
		{"openai", "sk-1", ""},
		{"bedrock", `{"access_key":"A","secret_key":"S","region":"us-east-1"}`, ""},
		{"bedrock", `{"api_key":"bk","region":"us-west-2"}`, ""},
		{"sagemaker", `{"access_key":"A","secret_key":"S","region":"us-east-1"}`, ""},
		{"vertex", `{"project_id":"p","region":"global","auth_credentials":{"type":"service_account"}}`, ""},
		{"cloudflare", `{"api_key":"t","account_id":"a"}`, ""},
		{"azure", "az", "https://r.openai.azure.com"},
		{"databricks", "dapi1", "https://w.cloud.databricks.com"},
		{"vllm", "", "https://v.example.com/v1"},
		{"github-copilot", "ghu_x", ""},
	}
	for _, c := range ok {
		if err := CheckCredential(spec(c.id), c.value, c.base); err != nil {
			t.Errorf("%s %s: %v", c.id, c.value, err)
		}
	}
	bad := []struct{ id, value, base string }{
		{"openai", "", ""},
		{"bedrock", `{"region":"us-east-1"}`, ""},
		{"bedrock", `{"access_key":"A","region":"us-east-1"}`, ""},
		{"bedrock", `{"access_key":"A","secret_key":"S"}`, ""},
		{"sagemaker", `{"api_key":"x","region":"us-east-1"}`, ""},
		{"vertex", `{"project_id":"p","region":"r"}`, ""},
		{"cloudflare", "plain-token", ""},
		{"azure", "az", ""},
		{"vllm", "", ""},
	}
	for _, c := range bad {
		err := CheckCredential(spec(c.id), c.value, c.base)
		if err == nil {
			t.Errorf("%s %s accepted", c.id, c.value)
		} else if strings.Contains(err.Error(), "secret") && strings.Contains(c.value, `"S"`) && strings.Contains(err.Error(), `"S"`) {
			t.Errorf("message echoes the value: %v", err)
		}
	}
	block, cfg, value := BifrostKeyConfig(spec("bedrock"), CredentialFields(spec("bedrock"), `{"api_key":"bk","region":"eu-west-1"}`), "", "")
	if block != "bedrock_key_config" || cfg["region"] != "eu-west-1" || value != "bk" || cfg["access_key"] != nil {
		t.Errorf("bedrock api key block %s %v %q", block, cfg, value)
	}
	block, cfg, _ = BifrostKeyConfig(spec("azure"), CredentialFields(spec("azure"), "az"), "https://r.openai.azure.com/", "http://fp:9100")
	if block != "azure_key_config" || cfg["endpoint"] != "http://fp:9100" {
		t.Errorf("azure block %s %v", block, cfg)
	}
	if block, _, value := BifrostKeyConfig(spec("groq"), CredentialFields(spec("groq"), "gsk"), "", ""); block != "" || value != "gsk" {
		t.Errorf("groq %s %s", block, value)
	}
}
