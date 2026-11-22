package schemas

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Bifrost providers (lane A09). One table of every provider Bifrost core
// serves (core/schemas ModelProvider), shared by the key store (which
// provider ids a key may name), the fault proxy (upstream base URL, auth
// style, request paths), the gateway (the Bifrost provider and key config it
// writes per organization) and the key test call. A custom provider is any
// other id with an OpenAI-compatible base URL: Bifrost serves it as a custom
// provider with base_provider_type openai.

// Provider routes: how a run's calls reach the provider.
const (
	// RouteProxied: Bifrost's base_url is the fault proxy and its key is a
	// placeholder; the proxy swaps in the real key upstream. Model faults
	// and the call ledger work.
	RouteProxied = "proxied"
	// RouteDirect: the provider signs requests (AWS SigV4, Google OAuth,
	// token exchange), so a placeholder cannot be swapped. Bifrost holds
	// the real credential and calls the provider itself; model faults are
	// not available, the ledger comes from Bifrost's logs only.
	RouteDirect = "direct"
)

// ProviderField is one credential field. A provider with Fields stores its
// key value as a JSON object of these names; "api_key" is the plain key.
type ProviderField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	Help     string `json:"help,omitempty"`
}

// ProviderTest is the cheapest authenticated call that proves a key works.
// Path is appended to TestBaseURL (or the key's base URL). Header names the
// auth header ("Authorization" gets the "Bearer " prefix unless Prefix is
// set). Method "" is GET. None true: the key cannot be checked by a plain
// HTTP call (SigV4, OAuth); the test only checks the fields.
type ProviderTest struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
	Body   string `json:"body,omitempty"`
	Header string `json:"header,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	None   bool   `json:"none,omitempty"`
}

// BifrostProviderSpec is one Bifrost provider.
type BifrostProviderSpec struct {
	ID           string          `json:"id"` // key store id and Bifrost provider name
	Name         string          `json:"name"`
	Route        string          `json:"route"`                    // proxied | direct
	Format       string          `json:"format"`                   // wire format: openai | anthropic | gemini
	BaseURL      string          `json:"base_url,omitempty"`       // Bifrost's default upstream root (request paths are appended)
	TestBaseURL  string          `json:"test_base_url,omitempty"`  // OpenAI-style base the key test and key form use
	NeedsBaseURL bool            `json:"needs_base_url,omitempty"` // the key must carry a base URL (endpoint, workspace, server)
	Keyless      bool            `json:"keyless,omitempty"`        // a key value is optional (self-hosted servers)
	AuthHeader   string          `json:"auth_header,omitempty"`    // header the provider reads its key from
	AuthPrefix   string          `json:"auth_prefix,omitempty"`    // e.g. "Bearer "
	AuthQuery    string          `json:"auth_query,omitempty"`     // query parameter instead of a header
	KeyConfig    string          `json:"key_config,omitempty"`     // Bifrost key config block, e.g. azure_key_config
	Fields       []ProviderField `json:"fields,omitempty"`         // structured credential (value is a JSON object)
	Message      []string        `json:"message"`                  // conversation routes, "METHOD /path" globs
	Models       []string        `json:"models,omitempty"`         // list-models routes
	Audio        []string        `json:"audio,omitempty"`          // speech routes
	Test         ProviderTest    `json:"test"`                     // key test call
	KeyEnv       string          `json:"key_env,omitempty"`        // environment variable a local fault proxy reads
	Custom       bool            `json:"custom,omitempty"`         // an OpenAI-compatible custom provider
	Docs         string          `json:"docs,omitempty"`           // where to get a key
}

// openAIChat are the conversation routes of Bifrost's OpenAI-compatible providers.
var openAIChat = []string{"POST /v1/chat/completions", "POST /v1/completions", "POST /v1/responses"}
var openAIModels = []string{"GET /v1/models", "GET /v1/models/*"}

// CustomProviderID is the key store id of the older generic
// OpenAI-compatible key; the gateway serves it as the openai provider with
// the key's base URL. A named custom provider (any other id plus a base
// URL) gets its own Bifrost provider.
const CustomProviderID = "openai-compatible"

func compatSpec(id, name, base, test, env, docs string) BifrostProviderSpec {
	return BifrostProviderSpec{ID: id, Name: name, Route: RouteProxied, Format: "openai", BaseURL: base, TestBaseURL: test,
		AuthHeader: "Authorization", AuthPrefix: "Bearer ", Message: openAIChat, Models: openAIModels,
		Test: ProviderTest{Path: "/models"}, KeyEnv: env, Docs: docs}
}

func withPaths(p BifrostProviderSpec, msg ...string) BifrostProviderSpec {
	p.Message = msg
	return p
}

var awsFields = []ProviderField{
	{Name: "access_key", Label: "AWS access key id", Secret: true},
	{Name: "secret_key", Label: "AWS secret access key", Secret: true},
	{Name: "session_token", Label: "AWS session token (temporary credentials)", Secret: true},
	{Name: "region", Label: "AWS region", Required: true, Help: "e.g. us-east-1"},
}

// BifrostProviders returns the provider table, a new slice on every call.
// Base URLs and request paths are Bifrost core's defaults
// (core/providers/<name>).
func BifrostProviders() []BifrostProviderSpec {
	bedrockFields := append([]ProviderField{{Name: "api_key", Label: "Bedrock API key (instead of access keys)", Secret: true}}, awsFields...)
	return []BifrostProviderSpec{
		{ID: "openai", Name: "OpenAI", Route: RouteProxied, Format: "openai", BaseURL: "https://api.openai.com", TestBaseURL: "https://api.openai.com/v1",
			AuthHeader: "Authorization", AuthPrefix: "Bearer ", Message: []string{"POST /v1/chat/completions", "POST /v1/responses"}, Models: openAIModels,
			Audio: []string{"POST /v1/audio/transcriptions", "POST /v1/audio/speech", "GET /v1/realtime"},
			Test:  ProviderTest{Path: "/models"}, KeyEnv: "OPENAI_API_KEY", Docs: "platform.openai.com/api-keys"},
		{ID: "anthropic", Name: "Anthropic", Route: RouteProxied, Format: "anthropic", BaseURL: "https://api.anthropic.com", TestBaseURL: "https://api.anthropic.com/v1",
			AuthHeader: "x-api-key", Message: []string{"POST /v1/messages"}, Models: openAIModels,
			Test: ProviderTest{Path: "/models?limit=100", Header: "x-api-key"}, KeyEnv: "ANTHROPIC_API_KEY", Docs: "console.anthropic.com/settings/keys"},
		{ID: "gemini", Name: "Google Gemini", Route: RouteProxied, Format: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			TestBaseURL: "https://generativelanguage.googleapis.com/v1beta", AuthHeader: "x-goog-api-key",
			Message: []string{"POST /models/*:generateContent", "POST /models/*:streamGenerateContent"}, Models: []string{"GET /models", "GET /models/*"},
			Test: ProviderTest{Path: "/models?pageSize=200", Header: "x-goog-api-key"}, KeyEnv: "GEMINI_API_KEY", Docs: "aistudio.google.com/apikey"},
		{ID: "azure", Name: "Azure OpenAI", Route: RouteProxied, Format: "openai", NeedsBaseURL: true, AuthHeader: "api-key", KeyConfig: "azure_key_config",
			Message: []string{"POST /openai/v1/chat/completions", "POST /openai/v1/responses", "POST /openai/deployments/*/chat/completions", "POST /openai/responses"},
			Models:  []string{"GET /openai/v1/models", "GET /openai/models"},
			Test:    ProviderTest{Path: "/openai/v1/models", Header: "api-key"}, KeyEnv: "AZURE_API_KEY",
			Docs: "Azure portal, your Azure OpenAI resource, Keys and Endpoint; base URL is the endpoint https://<resource>.openai.azure.com; list deployment names as models"},
		{ID: "bedrock", Name: "AWS Bedrock", Route: RouteDirect, Format: "anthropic", KeyConfig: "bedrock_key_config", Fields: bedrockFields,
			Message: []string{"POST /model/*/converse", "POST /model/*/converse-stream", "POST /model/*/invoke"},
			Test:    ProviderTest{None: true}, Docs: "AWS IAM access keys or a Bedrock API key, plus the region"},
		{ID: "bedrock_mantle", Name: "AWS Bedrock Mantle", Route: RouteDirect, Format: "openai", KeyConfig: "bedrock_mantle_key_config", Fields: bedrockFields,
			Message: []string{"POST /v1/chat/completions", "POST /anthropic/v1/messages"},
			Test:    ProviderTest{None: true}, Docs: "AWS IAM access keys or a Bedrock API key, plus the region"},
		{ID: "vertex", Name: "Google Vertex AI", Route: RouteDirect, Format: "gemini", KeyConfig: "vertex_key_config",
			Fields: []ProviderField{
				{Name: "project_id", Label: "GCP project id", Required: true},
				{Name: "region", Label: "Region", Required: true, Help: "e.g. us-central1 or global"},
				{Name: "auth_credentials", Label: "Service account JSON", Secret: true, Required: true},
			},
			Message: []string{"POST /v1/projects/*/locations/*/publishers/*/models/*"},
			Test:    ProviderTest{None: true}, Docs: "a GCP service account key (JSON) with the Vertex AI User role"},
		{ID: "sagemaker", Name: "AWS SageMaker", Route: RouteDirect, Format: "openai", KeyConfig: "sagemaker_key_config", Fields: awsFields,
			Message: []string{"POST /endpoints/*/invocations"}, Test: ProviderTest{None: true}, Docs: "AWS IAM access keys and the endpoint region"},
		{ID: "databricks", Name: "Databricks", Route: RouteDirect, Format: "openai", NeedsBaseURL: true, KeyConfig: "databricks_key_config",
			Message: []string{"POST /serving-endpoints/chat/completions", "POST /ai-gateway/mlflow/v1/chat/completions"},
			Test:    ProviderTest{Path: "/api/2.0/serving-endpoints"}, Docs: "a personal access token; base URL is the workspace URL https://<workspace>.cloud.databricks.com"},
		{ID: "github-copilot", Name: "GitHub Copilot", Route: RouteDirect, Format: "openai", BaseURL: "https://api.githubcopilot.com",
			Message: []string{"POST /chat/completions", "POST /v1/messages", "POST /responses"},
			Test:    ProviderTest{Path: "/copilot_internal/v2/token", Header: "Authorization", Prefix: "token "}, TestBaseURL: "https://api.github.com",
			Docs: "a GitHub OAuth token (ghu_ or gho_) of an account with Copilot"},
		compatSpec("mistral", "Mistral AI", "https://api.mistral.ai", "https://api.mistral.ai/v1", "MISTRAL_API_KEY", "console.mistral.ai/api-keys"),
		compatSpec("groq", "Groq", "https://api.groq.com/openai", "https://api.groq.com/openai/v1", "GROQ_API_KEY", "console.groq.com/keys"),
		withPaths(compatSpec("cohere", "Cohere", "https://api.cohere.ai", "https://api.cohere.ai/v1", "COHERE_API_KEY", "dashboard.cohere.com/api-keys"),
			"POST /v2/chat", "POST /v1/chat"),
		withPaths(compatSpec("deepseek", "DeepSeek", "https://api.deepseek.com", "https://api.deepseek.com", "DEEPSEEK_API_KEY", "platform.deepseek.com/api_keys"),
			"POST /chat/completions", "POST /beta/completions"),
		compatSpec("xai", "xAI", "https://api.x.ai", "https://api.x.ai/v1", "XAI_API_KEY", "console.x.ai"),
		compatSpec("cerebras", "Cerebras", "https://api.cerebras.ai", "https://api.cerebras.ai/v1", "CEREBRAS_API_KEY", "cloud.cerebras.ai"),
		withPaths(compatSpec("fireworks", "Fireworks AI", "https://api.fireworks.ai/inference", "https://api.fireworks.ai/inference/v1", "FIREWORKS_API_KEY", "fireworks.ai/account/api-keys"),
			"POST /v1/chat/completions", "POST /v1/completions", "POST /v1/responses", "POST /v1/messages"),
		func() BifrostProviderSpec {
			p := compatSpec("openrouter", "OpenRouter", "https://openrouter.ai/api", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", "openrouter.ai/settings/keys")
			p.Test.Path = "/key" // /models is public; /key checks the key itself
			return p
		}(),
		withPaths(compatSpec("perplexity", "Perplexity", "https://api.perplexity.ai", "https://api.perplexity.ai", "PERPLEXITY_API_KEY", "perplexity.ai/settings/api"),
			"POST /chat/completions", "POST /v1/responses"),
		func() BifrostProviderSpec {
			p := compatSpec("huggingface", "Hugging Face", "https://router.huggingface.co", "https://huggingface.co", "HUGGINGFACE_API_KEY", "huggingface.co/settings/tokens")
			p.Message = []string{"POST /v1/chat/completions", "POST /*/v1/chat/completions"}
			p.Test.Path = "/api/whoami-v2"
			return p
		}(),
		compatSpec("nebius", "Nebius Token Factory", "https://api.tokenfactory.nebius.com", "https://api.tokenfactory.nebius.com/v1", "NEBIUS_API_KEY", "tokenfactory.nebius.com"),
		compatSpec("parasail", "Parasail", "https://api.parasail.io", "https://api.parasail.io/v1", "PARASAIL_API_KEY", "parasail.io"),
		compatSpec("baseten", "Baseten", "https://inference.baseten.co", "https://inference.baseten.co/v1", "BASETEN_API_KEY", "app.baseten.co/settings/api_keys"),
		compatSpec("minimax", "MiniMax", "https://api.minimax.io", "https://api.minimax.io/v1", "MINIMAX_API_KEY", "platform.minimax.io"),
		withPaths(compatSpec("byteplus", "BytePlus ModelArk", "https://ark.ap-southeast.bytepluses.com", "https://ark.ap-southeast.bytepluses.com/api/v3", "BYTEPLUS_API_KEY", "console.byteplus.com/ark"),
			"POST /api/v3/chat/completions", "POST /api/v3/responses"),
		func() BifrostProviderSpec {
			p := compatSpec("cloudflare", "Cloudflare Workers AI", "https://api.cloudflare.com/client/v4", "https://api.cloudflare.com/client/v4", "CLOUDFLARE_API_KEY", "dash.cloudflare.com/profile/api-tokens")
			p.Message = []string{"POST /accounts/*/ai/v1/chat/completions", "POST /accounts/*/ai/v1/responses", "POST /accounts/*/ai/run/*"}
			p.Models = []string{"GET /accounts/*/ai/models/search"}
			p.KeyConfig = "cloudflare_key_config"
			p.Fields = []ProviderField{{Name: "api_key", Label: "API token", Secret: true, Required: true}, {Name: "account_id", Label: "Account id", Required: true}}
			p.Test.Path = "/user/tokens/verify"
			return p
		}(),
		func() BifrostProviderSpec {
			p := compatSpec("replicate", "Replicate", "https://api.replicate.com", "https://api.replicate.com/v1", "REPLICATE_API_KEY", "replicate.com/account/api-tokens")
			p.Message = []string{"POST /v1/predictions", "POST /v1/models/*/*/predictions", "POST /v1/deployments/*/*/predictions"}
			p.Models = nil
			p.Test.Path = "/account"
			return p
		}(),
		{ID: "sarvam", Name: "Sarvam AI", Route: RouteProxied, Format: "openai", BaseURL: "https://api.sarvam.ai", TestBaseURL: "https://api.sarvam.ai",
			AuthHeader: "api-subscription-key", Message: []string{"POST /v1/chat/completions", "POST /v2/chat/completions"},
			Audio: []string{"POST /speech-to-text", "POST /speech-to-text-translate", "POST /text-to-speech"},
			Test:  ProviderTest{Method: "POST", Path: "/text-lid", Body: `{"input":"hello"}`, Header: "api-subscription-key"}, KeyEnv: "SARVAM_API_KEY", Docs: "dashboard.sarvam.ai"},
		{ID: "elevenlabs", Name: "ElevenLabs", Route: RouteProxied, Format: "openai", BaseURL: "https://api.elevenlabs.io", TestBaseURL: "https://api.elevenlabs.io/v1",
			AuthHeader: "xi-api-key", Message: []string{"POST /v1/text-to-speech/*", "POST /v1/text-to-speech/*/*"},
			Audio: []string{"POST /v1/speech-to-text"}, Models: []string{"GET /v1/models"},
			Test: ProviderTest{Path: "/models", Header: "xi-api-key"}, KeyEnv: "ELEVENLABS_API_KEY", Docs: "elevenlabs.io/app/settings/api-keys"},
		func() BifrostProviderSpec {
			p := compatSpec("ollama", "Ollama", "", "", "OLLAMA_API_KEY", "your Ollama server URL, e.g. https://ollama.example.com")
			p.NeedsBaseURL, p.Keyless = true, true
			return p
		}(),
		func() BifrostProviderSpec {
			p := compatSpec("vllm", "vLLM", "", "", "VLLM_API_KEY", "your vLLM server URL, e.g. https://vllm.example.com/v1")
			p.Message = append(append([]string{}, openAIChat...), "POST /v1/messages")
			p.NeedsBaseURL, p.Keyless = true, true
			return p
		}(),
		func() BifrostProviderSpec {
			p := compatSpec("sgl", "SGLang", "", "", "SGL_API_KEY", "your SGLang server URL, e.g. https://sglang.example.com/v1")
			p.Message = append(append([]string{}, openAIChat...), "POST /v1/messages")
			p.NeedsBaseURL, p.Keyless = true, true
			return p
		}(),
		compatSpec("kimchi", "Kimchi", "https://llm.kimchi.dev/openai", "https://llm.kimchi.dev/openai/v1", "KIMCHI_API_KEY", "kimchi.dev"),
		withPaths(compatSpec("wafer", "Wafer", "https://pass.wafer.ai/v1", "https://pass.wafer.ai/v1", "WAFER_API_KEY", "wafer.ai"),
			"POST /chat/completions", "POST /completions", "POST /responses"),
		compatSpec("opencode-zen", "OpenCode Zen", "https://opencode.ai/zen", "https://opencode.ai/zen/v1", "OPENCODE_API_KEY", "opencode.ai/zen"),
		compatSpec("opencode-go", "OpenCode Go", "https://opencode.ai/zen/go", "https://opencode.ai/zen/go/v1", "OPENCODE_GO_API_KEY", "opencode.ai"),
		compatSpec("typesafe", "Typesafe", "https://api.typesafe.ai", "https://api.typesafe.ai/v1", "TYPESAFE_API_KEY", "typesafe.ai"),
	}
}

// LookupBifrostProvider returns the built-in provider named id.
func LookupBifrostProvider(id string) (BifrostProviderSpec, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range BifrostProviders() {
		if p.ID == id {
			return p, true
		}
	}
	return BifrostProviderSpec{}, false
}

// BifrostProviderIDs lists the built-in provider ids, sorted.
func BifrostProviderIDs() []string {
	out := []string{}
	for _, p := range BifrostProviders() {
		out = append(out, p.ID)
	}
	sort.Strings(out)
	return out
}

var customIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// reservedProviderIDs are names a custom provider may not take: Bifrost
// providers this table leaves out (media only) and the key store aliases.
var reservedProviderIDs = map[string]bool{"runway": true, "runware": true, "google": true, "custom": true, CustomProviderID: true}

// ValidCustomProviderID reports whether id may name an OpenAI-compatible
// custom provider: a lowercase slug that is no built-in provider.
func ValidCustomProviderID(id string) bool {
	if !customIDRe.MatchString(id) || reservedProviderIDs[id] {
		return false
	}
	_, builtin := LookupBifrostProvider(id)
	return !builtin
}

// ResolveBifrostProvider maps a key store provider id to the Bifrost
// provider name and its spec. The older openai-compatible id and "google"
// are aliases of openai and gemini; any other unknown id is a custom
// OpenAI-compatible provider of that name (custom true).
func ResolveBifrostProvider(id string) (name string, spec BifrostProviderSpec, custom bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case CustomProviderID:
		id = "openai"
	case "google":
		id = "gemini"
	case "sglang": // Z05: the engine's own name; Bifrost calls the provider sgl
		id = "sgl"
	}
	if p, ok := LookupBifrostProvider(id); ok {
		return p.ID, p, false
	}
	return id, CustomProviderSpec(id), true
}

// CustomProviderSpec is the spec of an OpenAI-compatible custom provider.
func CustomProviderSpec(id string) BifrostProviderSpec {
	p := compatSpec(id, id+" (OpenAI-compatible)", "", "", "", "any server that speaks the OpenAI API; give its base URL ending in /v1")
	p.NeedsBaseURL, p.Custom = true, true
	return p
}

// SplitProviderModel splits "<provider>/<model>" when the prefix is a
// Bifrost provider (or a custom provider in custom), else returns "" and
// s. Only the first slash splits: "openrouter/openai/gpt-4o-mini" is
// provider openrouter, model "openai/gpt-4o-mini".
func SplitProviderModel(s string, custom ...string) (provider, model string) {
	s = strings.TrimSpace(s)
	pre, rest, ok := strings.Cut(s, "/")
	if !ok || rest == "" {
		return "", s
	}
	pre = strings.ToLower(pre)
	if _, known := LookupBifrostProvider(pre); known {
		return pre, rest
	}
	for _, c := range custom {
		if strings.EqualFold(c, pre) {
			return pre, rest
		}
	}
	return "", s
}

// CredentialFields reads a provider key's value: the JSON object of Fields
// for a provider with Fields (an unparsable value is the plain api_key),
// {"api_key": value} otherwise. The map holds secrets; never log it.
func CredentialFields(spec BifrostProviderSpec, value string) map[string]string {
	value = strings.TrimSpace(value)
	out := map[string]string{}
	if len(spec.Fields) > 0 && strings.HasPrefix(value, "{") {
		var raw map[string]any
		if json.Unmarshal([]byte(value), &raw) == nil {
			for k, v := range raw {
				switch t := v.(type) {
				case string:
					out[k] = strings.TrimSpace(t)
				case nil:
				default: // a pasted service account object
					b, _ := json.Marshal(t)
					out[k] = string(b)
				}
			}
			return out
		}
	}
	if value != "" {
		out["api_key"] = value
	}
	return out
}

// CheckCredential validates a key value against the provider's fields: the
// required ones present, and AWS keys as a pair (or an API key instead).
// Messages never echo a value.
func CheckCredential(spec BifrostProviderSpec, value, baseURL string) error {
	f := CredentialFields(spec, value)
	if spec.NeedsBaseURL && strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("%s needs a base URL", spec.Name)
	}
	for _, fd := range spec.Fields {
		if fd.Required && f[fd.Name] == "" {
			return fmt.Errorf("%s needs %s (%s)", spec.Name, fd.Label, fd.Name)
		}
	}
	switch spec.KeyConfig {
	case "bedrock_key_config", "bedrock_mantle_key_config", "sagemaker_key_config":
		hasPair := f["access_key"] != "" && f["secret_key"] != ""
		if (f["access_key"] != "") != (f["secret_key"] != "") {
			return fmt.Errorf("%s needs both the access key id and the secret access key", spec.Name)
		}
		if !hasPair && f["api_key"] == "" && spec.KeyConfig != "sagemaker_key_config" {
			return fmt.Errorf("%s needs access keys or an API key", spec.Name)
		}
		if !hasPair && spec.KeyConfig == "sagemaker_key_config" {
			return fmt.Errorf("%s needs the access key id and the secret access key", spec.Name)
		}
	}
	if len(spec.Fields) == 0 && !spec.Keyless && f["api_key"] == "" {
		return fmt.Errorf("%s needs an API key", spec.Name)
	}
	return nil
}

// BifrostKeyConfig builds the provider-specific key config block Bifrost
// expects on a provider key (core/schemas Key: azure_key_config, ...), and
// the key value to send. endpoint is the URL Bifrost must call for
// providers whose endpoint sits in the key config (azure: the fault proxy
// when proxied); baseURL is the key's own base URL. Returns "" block when
// the provider has none.
func BifrostKeyConfig(spec BifrostProviderSpec, fields map[string]string, baseURL, endpoint string) (block string, cfg map[string]any, value string) {
	value = fields["api_key"]
	opt := func(m map[string]any, k string) {
		if v := fields[k]; v != "" {
			m[k] = v
		}
	}
	switch spec.KeyConfig {
	case "azure_key_config":
		if endpoint == "" {
			endpoint = baseURL
		}
		return spec.KeyConfig, map[string]any{"endpoint": strings.TrimRight(endpoint, "/")}, value
	case "bedrock_key_config", "bedrock_mantle_key_config", "sagemaker_key_config":
		m := map[string]any{"region": fields["region"]}
		opt(m, "access_key")
		opt(m, "secret_key")
		opt(m, "session_token")
		return spec.KeyConfig, m, value
	case "vertex_key_config":
		m := map[string]any{"project_id": fields["project_id"], "region": fields["region"], "auth_credentials": fields["auth_credentials"]}
		opt(m, "project_number")
		return spec.KeyConfig, m, value
	case "databricks_key_config":
		return spec.KeyConfig, map[string]any{"workspace_url": strings.TrimRight(baseURL, "/"), "api_format": "auto"}, value
	case "cloudflare_key_config":
		return spec.KeyConfig, map[string]any{"account_id": fields["account_id"]}, value
	}
	return "", nil, value
}
