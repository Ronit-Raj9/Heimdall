package faultproxy

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// Wire formats. A provider's format picks its error envelope, how the proxy
// reads the response id and usage, and whether a scripted model exists.
const (
	FormatAnthropic = "anthropic"
	FormatOpenAI    = "openai"
	FormatGemini    = "gemini"
)

// Built-in provider names (Bifrost provider names).
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderGemini    = "gemini"
)

// defaultSlotsPerProvider is how many slots a provider gets when Config.Slots
// names none for it.
const defaultSlotsPerProvider = 4

// ProviderSpec is one upstream provider as the proxy sees it. Bifrost's
// provider of the same name points its base_url at the proxy, so the proxy
// receives the provider's own request paths and forwards them to BaseURL
// unchanged. Patterns are "METHOD /path" with path.Match globs ("*" never
// crosses a "/"); method "*" matches any method.
type ProviderSpec struct {
	Name        string   `json:"name" yaml:"name"`
	BaseURL     string   `json:"base_url" yaml:"base_url"` // Bifrost's default base URL for the provider
	KeyEnv      string   `json:"key_env" yaml:"key_env"`   // environment variable with the real key
	Keyless     bool     `json:"keyless,omitempty" yaml:"keyless"`
	Scripted    bool     `json:"scripted,omitempty" yaml:"scripted"` // answer from the scripted model when the key is empty
	Auth        Auth     `json:"auth" yaml:"auth"`
	Format      string   `json:"format" yaml:"format"` // anthropic | openai | gemini
	Message     []string `json:"message" yaml:"message"`
	CountTokens []string `json:"count_tokens,omitempty" yaml:"count_tokens"`
	Models      []string `json:"models,omitempty" yaml:"models"`
	Audio       []string `json:"audio,omitempty" yaml:"audio"`             // X8 voice agents: speech and transcription routes (KindAudio)
	SlotPrefix  string   `json:"slot_prefix,omitempty" yaml:"slot_prefix"` // default slots are <prefix>-slot-1..4
	// SarvamAudio sends the audio requests for Sarvam models to Sarvam AI
	// (sarvam.go); nil: none.
	SarvamAudio *SarvamAudio `json:"sarvam_audio,omitempty" yaml:"sarvam_audio"`
	// PerSlotBase: the provider has no fixed API host (Azure endpoint,
	// self-hosted vLLM or SGLang); each organization slot brings its base
	// URL, and BaseURL may be empty (lane A09).
	PerSlotBase bool `json:"per_slot_base,omitempty" yaml:"per_slot_base"`
	// OrgOnly: no default shared slots; the provider serves organization
	// keys (PUT /orgslots) unless the config lists slots for it (lane A09).
	OrgOnly bool `json:"org_only,omitempty" yaml:"org_only"`
}

// Auth is where a provider carries its key: a header with an optional
// prefix ("Authorization: Bearer <key>") or a query parameter ("?key=").
type Auth struct {
	Header string `json:"header,omitempty" yaml:"header"`
	Prefix string `json:"prefix,omitempty" yaml:"prefix"`
	Query  string `json:"query,omitempty" yaml:"query"`
}

// Built-in auth styles.
var (
	AuthXAPIKey = Auth{Header: "x-api-key"}
	AuthBearer  = Auth{Header: "Authorization", Prefix: "Bearer "}
	AuthGoogKey = Auth{Header: "x-goog-api-key"}
	AuthQuery   = Auth{Query: "key"}
)

// openAIChatPaths are the conversation routes of Bifrost's OpenAI-compatible
// providers (core/providers/<name>/<name>.go on transports/v2.5.1).
var openAIChatPaths = []string{"POST /v1/chat/completions", "POST /v1/completions", "POST /v1/responses"}

// DefaultProviders returns the built-in provider table. Base URLs and request
// paths are Bifrost v2.5.1's defaults for each provider. A new slice is
// returned on every call. No entry falls back to the scripted model when its
// key is empty (X01): a keyless request fails with "no key configured", so a
// run can never look real while a script answered it. Config.ScriptedFallback
// turns the fallback back on for anthropic and openai (local development).
func DefaultProviders() []ProviderSpec {
	compat := func(name, base, env string, paths ...string) ProviderSpec {
		if len(paths) == 0 {
			paths = openAIChatPaths
		}
		return ProviderSpec{
			Name: name, BaseURL: base, KeyEnv: env, Auth: AuthBearer, Format: FormatOpenAI,
			Message: paths, Models: []string{"GET /v1/models", "GET /v1/models/*"},
		}
	}
	out := []ProviderSpec{
		{
			Name: ProviderAnthropic, BaseURL: "https://api.anthropic.com", KeyEnv: "ANTHROPIC_API_KEY",
			Auth: AuthXAPIKey, Format: FormatAnthropic, SlotPrefix: "fp",
			Message:     []string{"POST /v1/messages"},
			CountTokens: []string{"POST /v1/messages/count_tokens"},
			Models:      []string{"GET /v1/models", "GET /v1/models/*"},
		},
		{
			Name: ProviderOpenAI, BaseURL: "https://api.openai.com", KeyEnv: "OPENAI_API_KEY",
			Auth: AuthBearer, Format: FormatOpenAI, SlotPrefix: "oa",
			Message:     []string{"POST /v1/chat/completions", "POST /v1/responses"},
			CountTokens: []string{"POST /v1/responses/input_tokens"},
			Models:      []string{"GET /v1/models", "GET /v1/models/*"},
			Audio:       []string{"POST /v1/audio/transcriptions", "POST /v1/audio/speech", "GET /v1/realtime"}, // X8 voice agents
			SarvamAudio: &SarvamAudio{},                                                                         // saaras/bulbul models to Sarvam AI when SARVAM_API_KEY is set
		},
		{
			Name: ProviderGemini, BaseURL: "https://generativelanguage.googleapis.com/v1beta", KeyEnv: "GEMINI_API_KEY",
			Auth: AuthGoogKey, Format: FormatGemini, SlotPrefix: "gm",
			Message:     []string{"POST /models/*:generateContent", "POST /models/*:streamGenerateContent"},
			CountTokens: []string{"POST /models/*:countTokens"},
			Models:      []string{"GET /models", "GET /models/*"},
		},
		compat("groq", "https://api.groq.com/openai", "GROQ_API_KEY"),
		compat("mistral", "https://api.mistral.ai", "MISTRAL_API_KEY"),
		compat("xai", "https://api.x.ai", "XAI_API_KEY"),
		compat("openrouter", "https://openrouter.ai/api", "OPENROUTER_API_KEY"),
		compat("fireworks", "https://api.fireworks.ai/inference", "FIREWORKS_API_KEY",
			"POST /v1/chat/completions", "POST /v1/completions", "POST /v1/responses", "POST /v1/messages"),
		compat("deepseek", "https://api.deepseek.com", "DEEPSEEK_API_KEY",
			"POST /chat/completions", "POST /beta/completions"),
		compat("cerebras", "https://api.cerebras.ai", "CEREBRAS_API_KEY"),
		compat("perplexity", "https://api.perplexity.ai", "PERPLEXITY_API_KEY",
			"POST /chat/completions", "POST /v1/responses"),
		func() ProviderSpec {
			p := compat("ollama", "http://ollama:11434", "OLLAMA_API_KEY")
			p.Keyless = true
			return p
		}(),
	}
	return append(out, catalogProviders(out)...)
}

// catalogProviders are the other proxied providers of Bifrost core
// (schemas.BifrostProviders, lane A09) that the built-in list above does
// not name. They serve organization keys only (OrgOnly) so a default proxy
// keeps its slot set; a provider table file can give them shared slots.
// Direct providers (Bedrock, Vertex, SageMaker, Databricks, GitHub
// Copilot) sign their own requests and never pass the proxy.
func catalogProviders(have []ProviderSpec) []ProviderSpec {
	seen := map[string]bool{}
	for _, p := range have {
		seen[p.Name] = true
	}
	var out []ProviderSpec
	for _, c := range schemas.BifrostProviders() {
		if c.Route != schemas.RouteProxied || seen[c.ID] {
			continue
		}
		p := ProviderSpec{Name: c.ID, BaseURL: c.BaseURL, KeyEnv: c.KeyEnv, Keyless: c.Keyless, Format: c.Format,
			Auth: Auth{Header: c.AuthHeader, Prefix: c.AuthPrefix, Query: c.AuthQuery}, Message: append([]string{}, c.Message...),
			Models: append([]string{}, c.Models...), Audio: append([]string{}, c.Audio...), PerSlotBase: c.BaseURL == "", OrgOnly: true}
		out = append(out, p)
	}
	return out
}

// LoadProviders reads a provider table file (YAML or JSON, a list of
// ProviderSpec or {"providers": [...]}) and merges it over the built-in
// table: an entry replaces the built-in provider of the same name, a new name
// is added. The result is validated.
func LoadProviders(file string) ([]ProviderSpec, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read provider table %s: %w", file, err)
	}
	var list []ProviderSpec
	if err := yaml.Unmarshal(raw, &list); err != nil {
		var wrapped struct {
			Providers []ProviderSpec `yaml:"providers"`
		}
		if werr := yaml.Unmarshal(raw, &wrapped); werr != nil {
			return nil, fmt.Errorf("parse provider table %s: %w", file, err)
		}
		list = wrapped.Providers
	}
	out := MergeProviders(DefaultProviders(), list)
	if _, err := indexProviders(out); err != nil {
		return nil, fmt.Errorf("provider table %s: %w", file, err)
	}
	return out, nil
}

// MergeProviders returns base with every entry of over applied: same name
// replaces, a new name is appended. Neither input is modified.
func MergeProviders(base, over []ProviderSpec) []ProviderSpec {
	out := append([]ProviderSpec{}, base...)
	for _, p := range over {
		replaced := false
		for i := range out {
			if out[i].Name == p.Name {
				out[i], replaced = p, true
				break
			}
		}
		if !replaced {
			out = append(out, p)
		}
	}
	return out
}

// Validate checks one provider entry.
func (p ProviderSpec) Validate() error {
	var errs []error
	if p.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if p.BaseURL != "" || !p.PerSlotBase {
		if _, err := parseBaseURL(p.BaseURL); err != nil {
			errs = append(errs, err)
		}
	}
	switch p.Format {
	case FormatAnthropic, FormatOpenAI, FormatGemini:
	default:
		errs = append(errs, fmt.Errorf("format must be anthropic, openai or gemini, got %q", p.Format))
	}
	if (p.Auth.Header == "") == (p.Auth.Query == "") {
		errs = append(errs, errors.New("auth needs exactly one of header or query"))
	}
	if p.KeyEnv == "" && !p.Keyless {
		errs = append(errs, errors.New("key_env is required unless keyless"))
	}
	if len(p.Message) == 0 {
		errs = append(errs, errors.New("at least one message pattern is required"))
	}
	for _, pat := range append(append(append([]string{}, p.Message...), p.CountTokens...), p.Models...) {
		if _, _, err := splitPattern(pat); err != nil {
			errs = append(errs, err)
		}
	}
	if p.SarvamAudio != nil {
		if err := p.SarvamAudio.validate(p); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("provider %q: %w", p.Name, errors.Join(errs...))
	}
	return nil
}

// scriptable reports whether a keyless request is answered by the scripted
// model: only when the entry asks for it and the format has one (anthropic,
// openai). Built-in, only anthropic and openai do; a groq or gemini run
// without a key fails loudly instead of looking like a real run.
func (p ProviderSpec) scriptable() bool {
	return p.Scripted && (p.Format == FormatAnthropic || p.Format == FormatOpenAI)
}

// withScriptedFallback returns a copy of specs with the scripted fallback
// on for the built-in anthropic and openai entries (Config.ScriptedFallback).
func withScriptedFallback(specs []ProviderSpec) []ProviderSpec {
	out := make([]ProviderSpec, len(specs))
	copy(out, specs)
	for i := range out {
		if out[i].Name == ProviderAnthropic || out[i].Name == ProviderOpenAI {
			out[i].Scripted = true
		}
	}
	return out
}

// slotPrefix is the default slot name prefix.
func (p ProviderSpec) slotPrefix() string {
	if p.SlotPrefix != "" {
		return p.SlotPrefix
	}
	return p.Name
}

// classify names a request's kind for this provider: message (counted),
// count_tokens, models, or other.
func (p ProviderSpec) classify(r *http.Request) string {
	switch {
	case matchAny(p.Message, r):
		return KindMessage
	case matchAny(p.CountTokens, r):
		return KindCountTokens
	case matchAny(p.Models, r):
		return KindModels
	case matchAny(p.Audio, r): // X8 voice agents
		return KindAudio
	default:
		return KindOther
	}
}

// credential returns the slot value the request carries in this provider's
// auth style, or "".
func (a Auth) credential(r *http.Request) string {
	if a.Query != "" {
		return r.URL.Query().Get(a.Query)
	}
	v := r.Header.Get(a.Header)
	if a.Prefix != "" {
		if len(v) < len(a.Prefix) || !strings.EqualFold(v[:len(a.Prefix)], a.Prefix) {
			return ""
		}
		v = v[len(a.Prefix):]
	}
	return strings.TrimSpace(v)
}

// apply puts key on an outgoing request in this auth style.
func (a Auth) apply(out *http.Request, key string) {
	if a.Query != "" {
		q := out.URL.Query()
		q.Set(a.Query, key)
		out.URL.RawQuery = q.Encode()
		return
	}
	out.Header.Set(a.Header, a.Prefix+key)
}

// providerTable is the validated table, by name, in order.
type providerTable struct {
	order  []string
	byName map[string]ProviderSpec
	auths  []Auth // distinct auth styles, table order
}

func indexProviders(specs []ProviderSpec) (*providerTable, error) {
	if len(specs) == 0 {
		return nil, errors.New("provider table is empty")
	}
	t := &providerTable{byName: make(map[string]ProviderSpec, len(specs))}
	for _, p := range specs {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if _, dup := t.byName[p.Name]; dup {
			return nil, fmt.Errorf("provider %q listed twice", p.Name)
		}
		p.BaseURL = strings.TrimRight(p.BaseURL, "/")
		t.byName[p.Name] = p
		t.order = append(t.order, p.Name)
		seen := false
		for _, a := range t.auths {
			if a == p.Auth {
				seen = true
			}
		}
		if !seen {
			t.auths = append(t.auths, p.Auth)
		}
	}
	return t, nil
}

// guess picks the provider whose message paths match r, for answering a
// request with no known credential in the right envelope. Anthropic when
// nothing matches.
func (t *providerTable) guess(r *http.Request) ProviderSpec {
	for _, name := range t.order {
		p := t.byName[name]
		if p.classify(r) != KindOther {
			return p
		}
	}
	if p, ok := t.byName[ProviderAnthropic]; ok {
		return p
	}
	return t.byName[t.order[0]]
}

func matchAny(patterns []string, r *http.Request) bool {
	p := r.URL.Path
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	for _, pat := range patterns {
		method, glob, err := splitPattern(pat)
		if err != nil || (method != "*" && method != r.Method) {
			continue
		}
		if ok, err := path.Match(glob, p); err == nil && ok {
			return true
		}
	}
	return false
}

func splitPattern(pat string) (string, string, error) {
	method, glob, ok := strings.Cut(strings.TrimSpace(pat), " ")
	glob = strings.TrimSpace(glob)
	if !ok || method == "" || !strings.HasPrefix(glob, "/") {
		return "", "", fmt.Errorf("pattern %q must be \"METHOD /path\"", pat)
	}
	if _, err := path.Match(glob, "/"); err != nil {
		return "", "", fmt.Errorf("pattern %q: %w", pat, err)
	}
	return strings.ToUpper(method), glob, nil
}

func parseBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse base url %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("base url must be an http(s) URL, got %q", raw)
	}
	if u.User != nil || u.RawQuery != "" {
		return "", errors.New("base url must not carry credentials or a query")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
