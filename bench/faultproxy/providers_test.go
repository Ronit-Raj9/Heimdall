package faultproxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultProvidersValid(t *testing.T) {
	specs := DefaultProviders()
	tbl, err := indexProviders(specs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"anthropic", "openai", "gemini", "groq", "mistral", "xai", "openrouter", "fireworks", "deepseek", "cerebras", "perplexity", "ollama", "vllm", "sgl"} {
		if _, ok := tbl.byName[name]; !ok {
			t.Errorf("built-in table lacks %s", name)
		}
	}
	scripted := map[string]bool{}
	for _, p := range specs {
		if p.scriptable() {
			scripted[p.Name] = true
		}
	}
	if len(scripted) != 0 {
		t.Errorf("scripted providers = %v, want none by default (X01)", scripted)
	}
	clear(scripted)
	for _, p := range withScriptedFallback(specs) {
		if p.scriptable() {
			scripted[p.Name] = true
		}
	}
	if len(scripted) != 2 || !scripted["anthropic"] || !scripted["openai"] {
		t.Errorf("scripted providers with the fallback = %v, want anthropic and openai", scripted)
	}
	if specs[0].Scripted {
		t.Error("withScriptedFallback changed its input")
	}
	specs[0].Name = "changed"
	if DefaultProviders()[0].Name != "anthropic" {
		t.Error("DefaultProviders shares its slice")
	}
}

func TestClassifyByProvider(t *testing.T) {
	tbl, err := indexProviders(DefaultProviders())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		provider, method, path, kind string
	}{
		{"anthropic", "POST", "/v1/messages", KindMessage},
		{"anthropic", "POST", "/v1/messages/count_tokens", KindCountTokens},
		{"anthropic", "POST", "/v1/chat/completions", KindOther},
		{"openai", "POST", "/v1/chat/completions", KindMessage},
		{"openai", "POST", "/v1/responses", KindMessage},
		{"openai", "POST", "/v1/responses/input_tokens", KindCountTokens},
		{"openai", "GET", "/v1/responses", KindOther},
		{"openai", "GET", "/v1/models/gpt-5", KindModels},
		{"gemini", "POST", "/models/gemini-3-pro:generateContent", KindMessage},
		{"gemini", "POST", "/models/gemini-3-pro:streamGenerateContent", KindMessage},
		{"gemini", "POST", "/models/gemini-3-pro:countTokens", KindCountTokens},
		{"gemini", "GET", "/models", KindModels},
		{"gemini", "POST", "/models/a/b:generateContent", KindOther},
		{"deepseek", "POST", "/chat/completions", KindMessage},
		{"deepseek", "POST", "/v1/chat/completions", KindOther},
		{"groq", "POST", "/v1/chat/completions", KindMessage},
		{"fireworks", "POST", "/v1/messages", KindMessage},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := tbl.byName[tc.provider].classify(r); got != tc.kind {
			t.Errorf("%s %s %s = %s, want %s", tc.provider, tc.method, tc.path, got, tc.kind)
		}
	}
	if g := tbl.guess(httptest.NewRequest("POST", "/v1/responses", nil)); g.Format != FormatOpenAI {
		t.Errorf("guess for /v1/responses = %s", g.Name)
	}
	if g := tbl.guess(httptest.NewRequest("GET", "/nowhere", nil)); g.Name != "anthropic" {
		t.Errorf("fallback guess = %s", g.Name)
	}
}

func TestAuthStyles(t *testing.T) {
	cases := []struct {
		name  string
		auth  Auth
		setup func(r *http.Request)
		want  string
	}{
		{"x-api-key", AuthXAPIKey, func(r *http.Request) { r.Header.Set("x-api-key", "k1") }, "k1"},
		{"bearer", AuthBearer, func(r *http.Request) { r.Header.Set("Authorization", "Bearer k2") }, "k2"},
		{"bearer case", AuthBearer, func(r *http.Request) { r.Header.Set("Authorization", "BEARER k3") }, "k3"},
		{"bearer missing prefix", AuthBearer, func(r *http.Request) { r.Header.Set("Authorization", "k4") }, ""},
		{"goog", AuthGoogKey, func(r *http.Request) { r.Header.Set("x-goog-api-key", "k5") }, "k5"},
		{"query", AuthQuery, func(r *http.Request) { r.URL.RawQuery = "alt=sse&key=k6" }, "k6"},
		{"absent", AuthXAPIKey, func(*http.Request) {}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/x", nil)
			tc.setup(r)
			if got := tc.auth.credential(r); got != tc.want {
				t.Errorf("credential = %q, want %q", got, tc.want)
			}
			out := httptest.NewRequest("POST", "/x?alt=sse", nil)
			tc.auth.apply(out, "real")
			if got := tc.auth.credential(out); got != "real" {
				t.Errorf("apply then credential = %q", got)
			}
		})
	}
}

func TestValidateProvider(t *testing.T) {
	good := ProviderSpec{Name: "p", BaseURL: "https://x", KeyEnv: "K", Auth: AuthBearer, Format: FormatOpenAI, Message: []string{"POST /v1/chat/completions"}}
	mutate := func(f func(*ProviderSpec)) ProviderSpec {
		p := good
		f(&p)
		return p
	}
	cases := []struct {
		name string
		spec ProviderSpec
		ok   bool
	}{
		{"good", good, true},
		{"keyless without env", mutate(func(p *ProviderSpec) { p.KeyEnv, p.Keyless = "", true }), true},
		{"no name", mutate(func(p *ProviderSpec) { p.Name = "" }), false},
		{"bad url", mutate(func(p *ProviderSpec) { p.BaseURL = "ftp://x" }), false},
		{"bad format", mutate(func(p *ProviderSpec) { p.Format = "cohere" }), false},
		{"two auth places", mutate(func(p *ProviderSpec) { p.Auth = Auth{Header: "h", Query: "key"} }), false},
		{"no key env", mutate(func(p *ProviderSpec) { p.KeyEnv = "" }), false},
		{"no message paths", mutate(func(p *ProviderSpec) { p.Message = nil }), false},
		{"bad pattern", mutate(func(p *ProviderSpec) { p.Message = []string{"/v1/chat"} }), false},
	}
	for _, tc := range cases {
		if err := tc.spec.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err %v, want ok %t", tc.name, err, tc.ok)
		}
	}
}

func TestLoadProviders(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, file, body string
		ok               bool
		check            func(t *testing.T, specs []ProviderSpec)
	}{
		{"yaml override and add", "p.yaml", `
- name: groq
  base_url: http://groq-mirror:8080/openai
  key_env: GROQ_KEY_2
  auth: {header: Authorization, prefix: "Bearer "}
  format: openai
  message: ["POST /v1/chat/completions"]
- name: together
  base_url: https://api.together.xyz
  key_env: TOGETHER_API_KEY
  auth: {header: Authorization, prefix: "Bearer "}
  format: openai
  scripted: true
  message: ["POST /v1/chat/completions"]
`, true, func(t *testing.T, specs []ProviderSpec) {
			byName := map[string]ProviderSpec{}
			for _, p := range specs {
				byName[p.Name] = p
			}
			if byName["groq"].BaseURL != "http://groq-mirror:8080/openai" || byName["groq"].KeyEnv != "GROQ_KEY_2" {
				t.Errorf("groq not replaced: %+v", byName["groq"])
			}
			if !byName["together"].scriptable() || byName["anthropic"].BaseURL != "https://api.anthropic.com" {
				t.Errorf("merge lost entries")
			}
			if len(specs) != len(DefaultProviders())+1 {
				t.Errorf("%d providers", len(specs))
			}
		}},
		{"json wrapped", "p.json", `{"providers":[{"name":"vllm","base_url":"http://vllm:8000","keyless":true,"auth":{"header":"Authorization","prefix":"Bearer "},"format":"openai","message":["POST /v1/chat/completions"]}]}`,
			true, func(t *testing.T, specs []ProviderSpec) {
				// vllm is a built-in (A09 catalog): the file entry replaces it.
				for _, p := range specs {
					if p.Name == "vllm" && (!p.Keyless || p.BaseURL != "http://vllm:8000" || p.OrgOnly) {
						t.Errorf("vllm entry %+v", p)
					}
				}
			}},
		{"invalid entry", "bad.yaml", "- name: x\n  base_url: nope\n", false, nil},
		{"not a table", "bad2.yaml", "providers: 3\n", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.file)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			specs, err := LoadProviders(path)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok %t", err, tc.ok)
			}
			if tc.check != nil {
				tc.check(t, specs)
			}
		})
	}
	if _, err := LoadProviders(filepath.Join(dir, "missing.yaml")); err == nil || !strings.Contains(err.Error(), "missing.yaml") {
		t.Errorf("missing file err = %v", err)
	}
}

func TestDefaultSlots(t *testing.T) {
	tbl, err := indexProviders(DefaultProviders())
	if err != nil {
		t.Fatal(err)
	}
	slots := withDefaultSlots([]SlotConfig{{Name: "fp-slot-1", Auth: "fp-slot-1"}}, tbl)
	byProvider := map[string][]string{}
	for _, s := range slots {
		byProvider[s.Provider] = append(byProvider[s.Provider], s.Name)
	}
	if strings.Join(byProvider["anthropic"], ",") != "fp-slot-1" ||
		len(byProvider["openai"]) != 24 || byProvider["openai"][23] != "oa-slot-24" ||
		len(byProvider["groq"]) != 24 || byProvider["groq"][0] != "groq-slot-1" ||
		byProvider["gemini"][0] != "gm-slot-1" {
		t.Errorf("slots = %v", byProvider)
	}
}

// TestSelfHostedOpenModels: vLLM and SGLang sit next to Ollama as keyless
// built-ins whose default in-network host an organization slot's https base
// URL replaces (Z05), and they carry the Anthropic messages route vLLM and
// SGLang serve.
func TestSelfHostedOpenModels(t *testing.T) {
	tbl, err := indexProviders(DefaultProviders())
	if err != nil {
		t.Fatal(err)
	}
	for name, base := range map[string]string{"vllm": "http://vllm:8000", "sgl": "http://sglang:30000"} {
		p := tbl.byName[name]
		if !p.Keyless || !p.PerSlotBase || p.OrgOnly || p.BaseURL != base || p.Format != FormatOpenAI {
			t.Errorf("%s = %+v, want keyless, per-slot base, shared slots, base %s", name, p, base)
		}
		has := false
		for _, m := range p.Message {
			has = has || m == "POST /v1/messages"
		}
		if !has {
			t.Errorf("%s has no /v1/messages route: %v", name, p.Message)
		}
	}
}
