package handlers

import "github.com/maximhq/bifrost/bench/keystore"

// ModelOption is one suggested model of a provider. The list is a starting
// point: the picker also accepts any model id the provider serves.
type ModelOption struct {
	ID       string   `json:"id"`
	Cheap    bool     `json:"cheap,omitempty"` // the provider's cheapest suggested model for its purposes
	Purposes []string `json:"purposes"`        // agent, judge, simuser, voice-stt, voice-tts
	Rank     int      `json:"rank"`            // lower is cheaper, across providers (rough list price)
}

// ModelProviderInfo describes a provider for the key form and the picker.
type ModelProviderInfo struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	DefaultBaseURL string        `json:"default_base_url,omitempty"`
	NeedsBaseURL   bool          `json:"needs_base_url"`
	KeyHint        string        `json:"key_hint"` // where to get a key
	KeyPrefix      string        `json:"key_prefix,omitempty"`
	Models         []ModelOption `json:"models"`
}

var chatPurposes = []string{"agent", "judge", "simuser"}

func modelChat(id string, rank int, cheap bool) ModelOption {
	return ModelOption{ID: id, Cheap: cheap, Purposes: chatPurposes, Rank: rank}
}

func modelVoice(id, purpose string, rank int) ModelOption {
	return ModelOption{ID: id, Purposes: []string{purpose}, Rank: rank}
}

// modelProviders is the static provider list. Ranks order models by rough
// input price so the default is the cheapest model a working key can call.
func modelProviders() []ModelProviderInfo {
	return []ModelProviderInfo{
		{ID: "openai", Name: "OpenAI", DefaultBaseURL: "https://api.openai.com/v1", KeyPrefix: "sk-",
			KeyHint: "Create a key at platform.openai.com/api-keys",
			Models: []ModelOption{modelChat("gpt-4o-mini", 20, true), modelChat("gpt-4.1-mini", 30, false), modelChat("gpt-5-mini", 35, false),
				modelChat("o4-mini", 60, false), modelChat("gpt-4.1", 70, false), modelChat("gpt-5", 75, false), modelChat("gpt-4o", 80, false),
				modelVoice("gpt-4o-mini-transcribe", "voice-stt", 20), modelVoice("whisper-1", "voice-stt", 30), modelVoice("gpt-4o-mini-tts", "voice-tts", 20)}},
		{ID: "anthropic", Name: "Anthropic", DefaultBaseURL: "https://api.anthropic.com/v1", KeyPrefix: "sk-ant-",
			KeyHint: "Create a key at console.anthropic.com/settings/keys",
			Models:  []ModelOption{modelChat("claude-haiku-4-5", 50, true), modelChat("claude-sonnet-4-5", 85, false), modelChat("claude-opus-4-5", 95, false)}},
		{ID: "gemini", Name: "Google Gemini", DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta",
			KeyHint: "Create a key at aistudio.google.com/apikey",
			Models:  []ModelOption{modelChat("gemini-2.5-flash-lite", 10, true), modelChat("gemini-2.5-flash", 40, false), modelChat("gemini-2.5-pro", 80, false)}},
		{ID: "openrouter", Name: "OpenRouter", DefaultBaseURL: "https://openrouter.ai/api/v1", KeyPrefix: "sk-or-",
			KeyHint: "Create a key at openrouter.ai/settings/keys; model ids look like vendor/model",
			Models: []ModelOption{modelChat("openai/gpt-4o-mini", 21, true), modelChat("google/gemini-2.5-flash", 41, false),
				modelChat("meta-llama/llama-3.3-70b-instruct", 45, false), modelChat("anthropic/claude-sonnet-4.5", 86, false)}},
		{ID: "groq", Name: "Groq", DefaultBaseURL: "https://api.groq.com/openai/v1", KeyPrefix: "gsk_",
			KeyHint: "Create a key at console.groq.com/keys",
			Models: []ModelOption{modelChat("llama-3.1-8b-instant", 5, true), modelChat("openai/gpt-oss-120b", 25, false), modelChat("llama-3.3-70b-versatile", 45, false),
				modelVoice("whisper-large-v3-turbo", "voice-stt", 10)}},
		{ID: "sarvam", Name: "Sarvam AI (speech)", DefaultBaseURL: "https://api.sarvam.ai",
			KeyHint: "Create a subscription key at dashboard.sarvam.ai; used for Indian-language speech to text and text to speech",
			Models:  []ModelOption{modelVoice("saarika:v2.5", "voice-stt", 15), modelVoice("bulbul:v2", "voice-tts", 15), modelChat("sarvam-m", 15, true)}},
		{ID: "openai-compatible", Name: "OpenAI-compatible endpoint", NeedsBaseURL: true,
			KeyHint: "Any server that speaks the OpenAI API (vLLM, Ollama, LiteLLM, Together, Fireworks, DeepSeek...): give its base URL ending in /v1",
			Models:  []ModelOption{}},
	}
}

// defaultModel is the cheapest suggested chat model a usable key can call:
// keys that tested ok come first, untested next; invalid or over-budget keys
// never count. A key with its own model list offers its first model.
func defaultModel(keys []ModelKeyView) *ModelRefView {
	providers := map[string]ModelProviderInfo{}
	for _, p := range modelProviders() {
		providers[p.ID] = p
	}
	var best *ModelRefView
	bestScore := 1 << 30
	for _, k := range keys {
		if k.OverBudget || k.Status == keystore.StatusInvalid || k.Status == keystore.StatusError {
			continue
		}
		penalty := 0
		if k.Status != keystore.StatusOK {
			penalty = 1000
		}
		consider := func(model string, rank int) {
			if s := rank + penalty; s < bestScore {
				bestScore, best = s, &ModelRefView{Provider: k.Provider, Model: model, KeyID: k.ID}
			}
		}
		if len(k.Models) > 0 {
			rank := 500
			for _, o := range providers[k.Provider].Models {
				if o.ID == k.Models[0] {
					rank = o.Rank
				}
			}
			consider(k.Models[0], rank)
			continue
		}
		for _, o := range providers[k.Provider].Models {
			if hasPurpose(o, "agent") {
				consider(o.ID, o.Rank)
			}
		}
	}
	return best
}

func hasPurpose(o ModelOption, p string) bool {
	for _, q := range o.Purposes {
		if q == p {
			return true
		}
	}
	return false
}
