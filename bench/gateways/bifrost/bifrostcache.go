package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// PromptCacheConfigured reports whether the provider's Bifrost config has a
// prompt_cache block (B2, docs/bifrost-features.md). Bifrost v2.5.1 honours
// the per-request x-bf-prompt-cache-auto-inject header only on such a
// provider: the header "cannot manufacture opt-in" (docs/features/
// prompt-caching.mdx), so an arm that asks for auto-inject on a provider
// without the block would silently test nothing, and the planner refuses it.
func (g *Gateway) PromptCacheConfigured(ctx context.Context, provider string) (bool, error) {
	raw, err := g.callRaw(ctx, http.MethodGet, providerPath(provider), nil)
	if err != nil {
		return false, err
	}
	var cur struct {
		PromptCache json.RawMessage `json:"prompt_cache"`
	}
	if err := json.Unmarshal(raw, &cur); err != nil {
		return false, err
	}
	b := bytes.TrimSpace(cur.PromptCache)
	return len(b) > 0 && !bytes.Equal(b, []byte("null")), nil
}

// ComplexityAnalyzerState is the state of Bifrost's complexity analyzer
// (GET /api/routing/complexity-analyzer-status on v2.5.1: "disabled" until a
// classifier is configured), and the state of its LLM fallback. A routing
// rule whose condition reads complexity_tier never matches while the
// analyzer is disabled, so the planner refuses such a rule (B2).
func (g *Gateway) ComplexityAnalyzerState(ctx context.Context) (string, error) {
	var st struct {
		State string `json:"state"`
		LLM   struct {
			State string `json:"state"`
		} `json:"llm"`
	}
	if err := g.call(ctx, http.MethodGet, "/api/routing/complexity-analyzer-status", nil, &st); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return "absent", nil
		}
		return "", err
	}
	if st.State == "disabled" && st.LLM.State != "" && st.LLM.State != "disabled" {
		return "llm_" + st.LLM.State, nil
	}
	return st.State, nil
}
