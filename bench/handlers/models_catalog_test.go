package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// bifrostCoreProviders are the providers section 5 of the agent-first
// contract requires in the catalog.
var bifrostCoreProviders = []string{"openai", "anthropic", "azure", "bedrock", "vertex", "gemini", "mistral", "groq", "cohere",
	"deepseek", "xai", "cerebras", "fireworks", "openrouter", "perplexity", "huggingface", "nebius", "parasail", "baseten",
	"databricks", "cloudflare", "sagemaker", "replicate", "ollama", "vllm", "sgl", "minimax", "byteplus", "sarvam",
	"elevenlabs", "github-copilot", "openai-compatible"}

func TestCatalogListsEveryBifrostProvider(t *testing.T) {
	byID := map[string]ModelProviderInfo{}
	for _, p := range modelProviders() {
		if _, dup := byID[p.ID]; dup {
			t.Errorf("duplicate provider %s", p.ID)
		}
		byID[p.ID] = p
		if p.KeyHint == "" || p.Name == "" || p.BifrostProvider == "" || p.Models == nil {
			t.Errorf("provider %s incomplete: %+v", p.ID, p)
		}
		// A built-in provider holds a key by name; a vendor reached through a
		// custom base URL holds one as a custom provider (its default base URL
		// is filled in when the key is added without one).
		if !keystore.KnownProvider(p.ID) && !(p.CustomBaseURL && schemas.ValidCustomProviderID(p.ID) && p.DefaultBaseURL != "") {
			t.Errorf("provider %s cannot hold a key", p.ID)
		}
		if p.CustomBaseURL && p.BifrostProvider != "openai" {
			t.Errorf("custom provider %s must route through openai, got %s", p.ID, p.BifrostProvider)
		}
		if !p.NeedsBaseURL && p.CustomBaseURL && p.DefaultBaseURL == "" {
			t.Errorf("custom provider %s has no default base URL", p.ID)
		}
		seen := map[string]bool{}
		for _, m := range p.Models {
			if seen[m.ID] {
				t.Errorf("%s: duplicate model %s", p.ID, m.ID)
			}
			seen[m.ID] = true
			if m.OpenWeights != (m.Family != "") {
				t.Errorf("%s/%s: open_weights and family must go together", p.ID, m.ID)
			}
		}
	}
	for _, id := range bifrostCoreProviders {
		if _, ok := byID[id]; !ok {
			t.Errorf("catalog misses Bifrost provider %s", id)
		}
	}
	for _, id := range keystore.Providers {
		if _, ok := byID[id]; !ok {
			t.Errorf("key store allows %s but the catalog has no entry", id)
		}
	}
}

func TestCatalogOpenWeightFamilies(t *testing.T) {
	families := map[string]bool{}
	for _, p := range modelProviders() {
		for _, m := range p.Models {
			if m.OpenWeights {
				families[m.Family] = true
			}
		}
	}
	for _, f := range []string{"deepseek", "glm", "kimi", "mimo", "minimax", "mistral", "qwen", "llama", "gpt-oss", "inkling"} {
		if !families[f] {
			t.Errorf("no open-weight model of family %s", f)
		}
	}
	for _, p := range modelProviders() {
		if p.ID == "zai" || p.ID == "moonshot" || p.ID == "xiaomi" || p.ID == "qwen" {
			if !p.CustomBaseURL || p.NeedsBaseURL || !strings.HasPrefix(p.DefaultBaseURL, "https://") {
				t.Errorf("%s should be a custom base URL provider with a default: %+v", p.ID, p)
			}
		}
	}
}

// The xcodebest proxy models are listed and priced (gpt-6.2-sol and
// gpt-6.2-astra added to the snapshot 2026-11-22), and never become the
// default for a key that has not reported them.
func TestCatalogProxyModelsUnpriced(t *testing.T) {
	var compat ModelProviderInfo
	for _, p := range modelProviders() {
		if p.ID == "openai-compatible" {
			compat = p
		}
	}
	want := []string{"gpt-6.2-sol", "gpt-6.2-astra", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-sol", "gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra"}
	have := map[string]bool{}
	for _, m := range compat.Models {
		have[m.ID] = true
	}
	for _, id := range want {
		if !have[id] {
			t.Errorf("openai-compatible misses %s", id)
		}
	}
	prices := loadRepoPrices(t)
	for _, id := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		if _, ok := prices.Models[id]; !ok {
			t.Errorf("%s has no price in the snapshot", id)
		}
	}
	keys := []ModelKeyView{{ID: "c", Provider: "openai-compatible", Status: "ok", Models: []string{}}}
	if d := defaultModel(keys); d != nil {
		t.Fatalf("static proxy suggestion became the default: %+v", d)
	}
}

func loadRepoPrices(t *testing.T) *evidence.PriceTable {
	t.Helper()
	pt, err := evidence.LoadPrices(filepath.Join("..", "deploy", "pricing-snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	return pt
}

// Every added price row names its source page and date; the rows the
// catalog's open-weight natives rely on are present.
func TestPricingSnapshotSources(t *testing.T) {
	pt := loadRepoPrices(t)
	raw, err := os.ReadFile(filepath.Join("..", "deploy", "pricing-snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		ModelSources map[string]struct {
			URL  string `json:"url"`
			Date string `json:"date"`
		} `json:"model_sources"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for id, s := range file.ModelSources {
		if _, ok := pt.Models[id]; !ok {
			t.Errorf("source for unpriced model %s", id)
		}
		if !strings.HasPrefix(s.URL, "https://") || len(s.Date) != 10 {
			t.Errorf("%s: bad source %+v", id, s)
		}
	}
	for _, id := range []string{"deepseek-v4-pro", "glm-5.3", "mimo-v2.6-pro", "MiniMax-M3", "mistral-large-4", "qwen3.8-27b",
		"openai/gpt-oss-120b", "accounts/fireworks/models/kimi-k3", "moonshotai/kimi-k3.1-pro", "thinkingmachines/inkling"} {
		if _, ok := file.ModelSources[id]; !ok {
			t.Errorf("no priced, sourced row for %s", id)
		}
	}
}

// The /models catalog and the Bifrost provider table (schemas/bifrostproviders.go)
// must agree: a catalog provider is a Bifrost core provider (reached natively),
// the generic openai-compatible id, or a vendor served as a custom
// OpenAI-compatible provider with its public endpoint. And every provider the
// key store accepts by name is in the catalog, so the keys page can offer it.
func TestCatalogAgreesWithBifrostProviders(t *testing.T) {
	inCatalog := map[string]bool{}
	for _, p := range modelProviders() {
		inCatalog[p.ID] = true
		_, builtin := schemas.LookupBifrostProvider(p.ID)
		switch {
		case p.ID == schemas.CustomProviderID:
		case builtin:
			if p.CustomBaseURL {
				t.Errorf("%s is a Bifrost core provider but the catalog reaches it through a custom base URL", p.ID)
			}
		default:
			if !schemas.ValidCustomProviderID(p.ID) || !p.CustomBaseURL || p.DefaultBaseURL == "" {
				t.Errorf("%s is not a Bifrost provider, so it must be a custom OpenAI-compatible vendor with a default base URL: %+v", p.ID, p)
			}
			if got := catalogDefaultBaseURL(p.ID); got != p.DefaultBaseURL {
				t.Errorf("%s: a key without a base URL gets %q, want %q", p.ID, got, p.DefaultBaseURL)
			}
		}
	}
	var missing []string
	for _, id := range schemas.BifrostProviderIDs() {
		if !inCatalog[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		t.Errorf("Bifrost providers missing from the /models catalog: %v", missing)
	}
}
