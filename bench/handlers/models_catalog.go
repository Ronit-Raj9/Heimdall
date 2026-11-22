package handlers

import (
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ModelOption is one suggested model of a provider. The list is a starting
// point: the picker also accepts any model id the provider serves.
type ModelOption struct {
	ID       string   `json:"id"`
	Cheap    bool     `json:"cheap,omitempty"` // the provider's cheapest suggested model for its purposes
	Purposes []string `json:"purposes"`        // agent, judge, simuser, voice-stt, voice-tts
	Rank     int      `json:"rank"`            // lower is cheaper, across providers (rough list price)
	// OpenWeights marks a model whose weights are published (anyone can
	// self-host it); Family groups it ("deepseek", "glm", "kimi", "qwen"...).
	OpenWeights bool   `json:"open_weights,omitempty"`
	Family      string `json:"family,omitempty"`
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
	// BifrostProvider is the Bifrost core provider that serves this entry
	// ("openai" for an OpenAI-compatible vendor reached through a custom
	// base URL). CustomBaseURL is true when Bifrost reaches it that way.
	BifrostProvider string `json:"bifrost_provider"`
	CustomBaseURL   bool   `json:"custom_base_url"`
}

var chatPurposes = []string{"agent", "judge", "simuser"}

func modelChat(id string, rank int, cheap bool) ModelOption {
	return ModelOption{ID: id, Cheap: cheap, Purposes: chatPurposes, Rank: rank}
}

// modelOpen is a chat model with published weights.
func modelOpen(id, family string, rank int, cheap bool) ModelOption {
	o := modelChat(id, rank, cheap)
	o.OpenWeights, o.Family = true, family
	return o
}

func modelVoice(id, purpose string, rank int) ModelOption {
	return ModelOption{ID: id, Purposes: []string{purpose}, Rank: rank}
}

// modelProviders is the provider list (data in models_catalog_data.go).
// Ranks order models by rough input price so the default is the cheapest
// model a working key can call. BifrostProvider defaults to the entry id.
func modelProviders() []ModelProviderInfo {
	ps := catalogProviders()
	// Every Bifrost provider (schemas/bifrostproviders.go) is offered, even
	// one the curated list has no models for yet: the picker accepts any
	// model id the provider serves.
	have := map[string]bool{}
	for _, p := range ps {
		have[p.ID] = true
	}
	for _, id := range schemas.BifrostProviderIDs() {
		if have[id] {
			continue
		}
		spec, _ := schemas.LookupBifrostProvider(id)
		hint := "Type any model id this provider serves"
		if spec.Docs != "" {
			hint = "Create a key at " + spec.Docs
		}
		ps = append(ps, ModelProviderInfo{ID: id, Name: spec.Name, DefaultBaseURL: spec.TestBaseURL, NeedsBaseURL: spec.NeedsBaseURL, KeyHint: hint})
	}
	for i := range ps {
		if ps[i].BifrostProvider == "" {
			ps[i].BifrostProvider = ps[i].ID
		}
		if ps[i].Models == nil {
			ps[i].Models = []ModelOption{}
		}
	}
	return ps
}

// defaultModel is the cheapest suggested chat model a usable key can call:
// keys that tested ok come first, untested next; invalid or over-budget keys
// never count. A key with its own model list offers its first model. A key
// that tested ok and reported the models it serves (models_available) beats
// any static suggestion: its pick is a model the provider really serves.
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
		if k.Status == keystore.StatusOK && len(k.ModelsAvailable) > 0 {
			// A key that reported no chat model falls back to the catalog suggestions.
			if model, rank := reportedModel(providers[k.Provider], k.ModelsAvailable); model != "" {
				consider(model, rank-reportedBonus)
				continue
			}
		}
		// A custom endpoint's static list is only examples (it serves
		// whatever its server serves), so it never sets the default.
		if p := providers[k.Provider]; !p.NeedsBaseURL {
			for _, o := range p.Models {
				if hasPurpose(o, "agent") {
					consider(o.ID, o.Rank)
				}
			}
		}
	}
	return best
}

// reportedBonus puts a model a working key reported ahead of every static suggestion.
const reportedBonus = 1 << 20

// preferredReported is picked first when a key reports it.
const preferredReported = "gpt-6.2-sol"

// nonChatModelRE matches model ids that cannot serve a chat loop: speech,
// transcription, image, embedding, moderation, realtime and search models.
// The UI's isChatModelId (ui/components/models/modelPicker.tsx) uses the same
// pattern; keep the two in step.
var nonChatModelRE = regexp.MustCompile(`(?i)(^|[-_/.:])(tts|transcribe|whisper|embed|embedding|image|dall-e|moderation|realtime|audio|speech|sora|search-preview)([-_/.:]|$)`)

// chatModelID reports whether model id of provider p can serve a chat loop
// (agent, judge or simulated user). A model the catalog lists counts when
// one of its purposes is a chat purpose; any other id counts unless its name
// says it is a speech, image, embedding or similar model.
func chatModelID(p ModelProviderInfo, id string) bool {
	for _, o := range p.Models {
		if o.ID == id {
			for _, c := range chatPurposes {
				if hasPurpose(o, c) {
					return true
				}
			}
			return false
		}
	}
	return !nonChatModelRE.MatchString(id) && !strings.HasPrefix(strings.ToLower(id), "chatgpt-image")
}

// reportedModel picks from the models a key reported: the preferred model when
// present, else the cheapest suggested agent model it serves, else the first
// one that can serve a chat loop, else "" (the caller then uses the catalog).
func reportedModel(p ModelProviderInfo, avail []string) (string, int) {
	have := map[string]bool{}
	for _, m := range avail {
		have[m] = true
	}
	if have[preferredReported] {
		return preferredReported, 0
	}
	best, bestRank := "", 1<<30
	for _, o := range p.Models {
		if hasPurpose(o, "agent") && have[o.ID] && o.Rank < bestRank {
			best, bestRank = o.ID, o.Rank
		}
	}
	if best != "" {
		return best, bestRank
	}
	for _, m := range avail {
		if chatModelID(p, m) {
			return m, 500
		}
	}
	return "", 500
}

func hasPurpose(o ModelOption, p string) bool {
	for _, q := range o.Purposes {
		if q == p {
			return true
		}
	}
	return false
}
