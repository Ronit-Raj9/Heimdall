package handlers

// catalogProviders is the static provider and model list behind GET /models:
// every provider Bifrost core serves, plus OpenAI-compatible vendors reached
// through a custom base URL. Model ids were checked on 21 Nov 2026 against
// each provider's public docs or model list (sources next to each block and
// in deploy/pricing-snapshot.json "model_sources"); never add an id that was
// not seen there. Open-weight models carry modelOpen(id, family, ...).
//
// Ranks are rough input price order (lower is cheaper), on the same scale as
// the original entries: about 5 at $0.05, 20 at $0.15, 30 at $0.40, 50 at $1,
// 70 at $2, 85 at $3 and 95 at $5 per million input tokens; a model with no
// known price sits at 90.
func catalogProviders() []ModelProviderInfo {
	return []ModelProviderInfo{
		{ID: "openai", Name: "OpenAI", DefaultBaseURL: "https://api.openai.com/v1", KeyPrefix: "sk-",
			KeyHint: "Create a key at platform.openai.com/api-keys",
			Models: []ModelOption{modelChat("gpt-4o-mini", 20, true), modelChat("gpt-4.1-mini", 30, false), modelChat("gpt-5-mini", 35, false),
				modelChat("o4-mini", 60, false), modelChat("gpt-4.1", 70, false), modelChat("gpt-5", 75, false), modelChat("gpt-4o", 80, false),
				modelChat("gpt-5.6-sol", 92, false), modelChat("gpt-5.5", 95, false),
				modelVoice("gpt-4o-mini-transcribe", "voice-stt", 20), modelVoice("whisper-1", "voice-stt", 30), modelVoice("gpt-4o-mini-tts", "voice-tts", 20)}},
		{ID: "anthropic", Name: "Anthropic", DefaultBaseURL: "https://api.anthropic.com/v1", KeyPrefix: "sk-ant-",
			KeyHint: "Create a key at console.anthropic.com/settings/keys",
			Models: []ModelOption{modelChat("claude-haiku-4-5", 50, true), modelChat("claude-sonnet-4-5", 85, false), modelChat("claude-opus-4-5", 95, false),
				modelChat("claude-sonnet-5-5", 70, false), modelChat("claude-opus-5-5", 92, false)}},
		// Gemini 2.5 is listed as deprecated on aistudio.google.com/docs/models
		// (21 Nov 2026); kept for old keys, newer ids added.
		{ID: "gemini", Name: "Google Gemini", DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta",
			KeyHint: "Create a key at aistudio.google.com/apikey",
			Models: []ModelOption{modelChat("gemini-2.5-flash-lite", 10, true), modelChat("gemini-2.5-flash", 40, false), modelChat("gemini-2.5-pro", 80, false),
				modelChat("gemini-3.5-flash-lite", 12, false), modelChat("gemini-3.8-flash", 42, false),
				modelChat("gemini-4-hydrogen", 66, false), modelChat("gemini-4-carbon", 70, false), modelChat("gemini-4-neon", 85, false)}},
		{ID: "vertex", Name: "Google Vertex AI", NeedsBaseURL: false,
			KeyHint: "A Google Cloud service account JSON with the Vertex AI User role (console.cloud.google.com/iam-admin/serviceaccounts); set the project and region in Bifrost",
			Models:  []ModelOption{}},
		{ID: "azure", Name: "Azure OpenAI", NeedsBaseURL: true,
			KeyHint: "Key and endpoint from your Azure OpenAI resource (portal.azure.com, Keys and Endpoint); model ids are your deployment names",
			Models:  []ModelOption{}},
		{ID: "bedrock", Name: "AWS Bedrock",
			KeyHint: "An AWS access key with bedrock:InvokeModel (console.aws.amazon.com/iam), or a Bedrock API key; model ids are Bedrock model or inference profile ids",
			Models:  []ModelOption{}},
		{ID: "sagemaker", Name: "AWS SageMaker", NeedsBaseURL: true,
			KeyHint: "An AWS access key that can invoke your SageMaker endpoint; the model id is the endpoint name",
			Models:  []ModelOption{}},
		{ID: "mistral", Name: "Mistral AI", DefaultBaseURL: "https://api.mistral.ai/v1",
			KeyHint: "Create a key at console.mistral.ai/api-keys",
			// docs.mistral.ai/models.md and /inference/pricing.md, 21 Nov 2026.
			// Devstral and Magistral are past retirement on Mistral's own API.
			Models: []ModelOption{modelOpen("ministral-3b-2512", "mistral", 10, true), modelOpen("ministral-8b-2512", "mistral", 20, false),
				modelOpen("ministral-14b-2512", "mistral", 24, false), modelOpen("mistral-small-2603", "mistral", 20, false),
				modelChat("codestral-2508", 27, false), modelOpen("mistral-large-4", "mistral", 42, false), modelChat("mistral-medium-3-5", 64, false)}},
		{ID: "deepseek", Name: "DeepSeek", DefaultBaseURL: "https://api.deepseek.com",
			KeyHint: "Create a key at platform.deepseek.com/api_keys",
			// api-docs.deepseek.com/quick_start/pricing, 21 Nov 2026.
			Models: []ModelOption{modelOpen("deepseek-flash", "deepseek", 27, true), modelOpen("deepseek-v4-pro", "deepseek", 62, false)}},
		{ID: "minimax", Name: "MiniMax", DefaultBaseURL: "https://api.minimax.io/v1",
			KeyHint: "Create a key at platform.minimax.io (Account, API Keys)",
			// platform.minimax.io/docs/api-reference/text-openai-api.md, 21 Nov 2026.
			Models: []ModelOption{modelOpen("MiniMax-M3.1-Flash", "minimax", 20, true), modelOpen("MiniMax-M3", "minimax", 27, false)}},
		{ID: "zai", Name: "Zhipu Z.ai (GLM)", DefaultBaseURL: "https://api.z.ai/api/paas/v4", BifrostProvider: "openai", CustomBaseURL: true,
			KeyHint: "Create a key at z.ai/manage-apikey/apikey-list; OpenAI-compatible endpoint https://api.z.ai/api/paas/v4",
			// docs.z.ai/guides/overview/pricing and /guides/llm/glm-5.3.md, 21 Nov 2026.
			Models: []ModelOption{modelOpen("glm-5.3-flash", "glm", 20, true), modelOpen("glm-4.5-air", "glm", 25, false), modelOpen("glm-5.3-flashx", "glm", 29, false),
				modelOpen("glm-4.6", "glm", 40, false), modelOpen("glm-4.7", "glm", 40, false), modelOpen("glm-5.2", "glm", 62, false), modelOpen("glm-5.3", "glm", 62, false)}},
		{ID: "moonshot", Name: "Moonshot AI (Kimi)", DefaultBaseURL: "https://api.moonshot.ai/v1", BifrostProvider: "openai", CustomBaseURL: true,
			KeyHint: "Create a key at platform.kimi.ai; OpenAI-compatible endpoint https://api.moonshot.ai/v1. K2 models are closed to new users and retire end of Dec 2026",
			// platform.kimi.ai/docs/guide/kimi-k3-1-pro-quickstart.md, 21 Nov 2026.
			Models: []ModelOption{modelOpen("kimi-k3", "kimi", 90, false), modelOpen("kimi-k3.1-pro", "kimi", 90, false)}},
		{ID: "xiaomi", Name: "Xiaomi MiMo", DefaultBaseURL: "https://api.xiaomimimo.com/v1", BifrostProvider: "openai", CustomBaseURL: true,
			KeyHint: "Create a key at platform.xiaomimimo.com; OpenAI-compatible endpoint https://api.xiaomimimo.com/v1",
			// mimo.mi.com/docs/en-US/price/pay-as-you-go, updated 18 Nov 2026.
			Models: []ModelOption{modelOpen("mimo-v2.6-flash", "mimo", 10, true), modelOpen("mimo-v2.6-pro", "mimo", 31, false)}},
		{ID: "qwen", Name: "Alibaba Qwen (Model Studio)", DefaultBaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", BifrostProvider: "openai", CustomBaseURL: true,
			KeyHint: "Create a key at modelstudio.console.alibabacloud.com (international, Singapore); OpenAI-compatible endpoint",
			// alibabacloud.com/help/en/model-studio/model-pricing, 21 Nov 2026.
			Models: []ModelOption{modelOpen("qwen3.8-27b", "qwen", 34, false), modelChat("qwen3.8-flash", 20, true), modelChat("qwen3-coder-flash", 27, false),
				modelChat("qwen3.7-plus", 30, false), modelChat("qwen3.8-max", 70, false)}},
		{ID: "openrouter", Name: "OpenRouter", DefaultBaseURL: "https://openrouter.ai/api/v1", KeyPrefix: "sk-or-",
			KeyHint: "Create a key at openrouter.ai/settings/keys; model ids look like vendor/model",
			// openrouter.ai/api/v1/models, fetched 21 Nov 2026.
			Models: []ModelOption{modelChat("openai/gpt-4o-mini", 21, true), modelChat("google/gemini-2.5-flash", 41, false),
				modelOpen("meta-llama/llama-3.3-70b-instruct", "llama", 45, false), modelChat("anthropic/claude-sonnet-4.5", 86, false),
				modelOpen("openai/gpt-oss-120b", "gpt-oss", 9, false), modelOpen("deepseek/deepseek-v4.1-flash", "deepseek", 6, false),
				modelOpen("deepseek/deepseek-v4-pro", "deepseek", 51, false), modelOpen("deepseek/deepseek-r1-0528", "deepseek", 38, false),
				modelOpen("z-ai/glm-5.3", "glm", 9, false), modelOpen("z-ai/glm-4.6", "glm", 34, false),
				modelOpen("moonshotai/kimi-k3.1-pro", "kimi", 20, false), modelOpen("moonshotai/kimi-k3", "kimi", 20, false),
				modelOpen("moonshotai/kimi-k2.6", "kimi", 40, false), modelOpen("moonshotai/kimi-k2-thinking", "kimi", 40, false),
				modelOpen("xiaomi/mimo-v2.6-pro", "mimo", 31, false), modelOpen("minimax/minimax-m3", "minimax", 27, false),
				modelOpen("minimax/minimax-m2.7", "minimax", 40, false), modelOpen("mistralai/mistral-large-4-0", "mistral", 42, false),
				modelOpen("mistralai/devstral-2512", "mistral", 30, false), modelChat("mistralai/mistral-medium-3-5", 64, false),
				modelOpen("qwen/qwen3.8-27b", "qwen", 7, false), modelOpen("qwen/qwen3-coder", "qwen", 27, false),
				modelOpen("qwen/qwen3-coder-next", "qwen", 15, false), modelChat("qwen/qwen3.8-max-0902", 70, false),
				modelOpen("meta-llama/llama-4-maverick", "llama", 28, false),
				modelOpen("thinkingmachines/inkling", "inkling", 50, false), modelOpen("thinkingmachines/inkling-small", "inkling", 32, false),
				modelChat("x-ai/grok-4.7", 70, false), modelChat("google/gemini-4-carbon", 70, false)}},
		{ID: "groq", Name: "Groq", DefaultBaseURL: "https://api.groq.com/openai/v1", KeyPrefix: "gsk_",
			KeyHint: "Create a key at console.groq.com/keys",
			// console.groq.com/docs/models, 21 Nov 2026.
			Models: []ModelOption{modelOpen("llama-3.1-8b-instant", "llama", 5, true), modelOpen("openai/gpt-oss-120b", "gpt-oss", 25, false), modelOpen("llama-3.3-70b-versatile", "llama", 45, false),
				modelOpen("openai/gpt-oss-20b", "gpt-oss", 15, false), modelOpen("qwen/qwen3.8-27b", "qwen", 44, false), modelOpen("minimaxai/minimax-m2.7", "minimax", 90, false),
				modelVoice("whisper-large-v3-turbo", "voice-stt", 10)}},
		{ID: "fireworks", Name: "Fireworks AI", DefaultBaseURL: "https://api.fireworks.ai/inference/v1",
			KeyHint: "Create a key at fireworks.ai/account/api-keys; model ids look like accounts/fireworks/models/<model>",
			// fireworks.ai/pricing and docs.fireworks.ai/nexus/open-models.md, 21 Nov 2026.
			Models: []ModelOption{modelOpen("accounts/fireworks/models/glm-5p3-flash", "glm", 20, true), modelOpen("accounts/fireworks/models/gpt-oss-120b", "gpt-oss", 20, false),
				modelOpen("accounts/fireworks/models/deepseek-v4p1-flash", "deepseek", 27, false), modelOpen("accounts/fireworks/models/minimax-m3", "minimax", 27, false),
				modelOpen("accounts/fireworks/models/glm-5p3", "glm", 62, false), modelChat("accounts/fireworks/models/qwen3p8-max", 70, false),
				modelOpen("accounts/fireworks/models/kimi-k3", "kimi", 85, false)}},
		{ID: "cerebras", Name: "Cerebras", DefaultBaseURL: "https://api.cerebras.ai/v1",
			KeyHint: "Create a key at cloud.cerebras.ai",
			// inference-docs.cerebras.ai/models/overview, 21 Nov 2026.
			Models: []ModelOption{modelOpen("gpt-oss-120b", "gpt-oss", 90, true), modelOpen("qwen-3.8-27b", "qwen", 90, false)}},
		{ID: "xai", Name: "xAI (Grok)", DefaultBaseURL: "https://api.x.ai/v1",
			KeyHint: "Create a key at console.x.ai",
			// docs.x.ai/docs/models, 21 Nov 2026.
			Models: []ModelOption{modelChat("grok-4.7", 90, true)}},
		{ID: "cohere", Name: "Cohere", DefaultBaseURL: "https://api.cohere.ai/v1",
			KeyHint: "Create a key at dashboard.cohere.com/api-keys",
			// docs.cohere.com/docs/models, 21 Nov 2026.
			Models: []ModelOption{modelChat("command-r7b-12-2024", 90, true), modelChat("command-a-plus-05-2026", 90, false), modelChat("command-a-reasoning-08-2025", 90, false)}},
		{ID: "perplexity", Name: "Perplexity", DefaultBaseURL: "https://api.perplexity.ai",
			KeyHint: "Create a key at perplexity.ai/account/api/keys",
			// docs.perplexity.ai/getting-started/models, 21 Nov 2026.
			Models: []ModelOption{modelChat("sonar", 90, true)}},
		{ID: "nebius", Name: "Nebius Token Factory", DefaultBaseURL: "https://api.tokenfactory.nebius.com/v1",
			KeyHint: "Create a key at tokenfactory.nebius.com; any model id from its catalog", Models: []ModelOption{}},
		{ID: "huggingface", Name: "Hugging Face Inference Providers", DefaultBaseURL: "https://router.huggingface.co/v1", KeyPrefix: "hf_",
			KeyHint: "Create a token with Inference Providers permission at huggingface.co/settings/tokens; model ids look like org/model", Models: []ModelOption{}},
		{ID: "parasail", Name: "Parasail", DefaultBaseURL: "https://api.parasail.io/v1",
			KeyHint: "Create a key at saas.parasail.io", Models: []ModelOption{}},
		{ID: "baseten", Name: "Baseten", DefaultBaseURL: "https://inference.baseten.co/v1",
			KeyHint: "Create a key at app.baseten.co/settings/api_keys", Models: []ModelOption{}},
		{ID: "databricks", Name: "Databricks", NeedsBaseURL: true,
			KeyHint: "A Databricks personal access token; base URL is your workspace serving endpoint, e.g. https://<workspace>.cloud.databricks.com/serving-endpoints", Models: []ModelOption{}},
		{ID: "cloudflare", Name: "Cloudflare Workers AI", NeedsBaseURL: true,
			KeyHint: "A Cloudflare API token with Workers AI access; base URL https://api.cloudflare.com/client/v4/accounts/<account id>/ai/v1", Models: []ModelOption{}},
		{ID: "replicate", Name: "Replicate", KeyPrefix: "r8_",
			KeyHint: "Create a token at replicate.com/account/api-tokens", Models: []ModelOption{}},
		{ID: "byteplus", Name: "BytePlus ModelArk", DefaultBaseURL: "https://ark.ap-southeast.bytepluses.com/api/v3",
			KeyHint: "Create a key in the BytePlus ModelArk console (console.byteplus.com/ark)", Models: []ModelOption{}},
		{ID: "github-copilot", Name: "GitHub Copilot", DefaultBaseURL: "https://api.githubcopilot.com",
			KeyHint: "A GitHub token for an account with a Copilot plan", Models: []ModelOption{}},
		{ID: "ollama", Name: "Ollama (self-hosted)", NeedsBaseURL: true,
			KeyHint: "Your Ollama server URL, e.g. https://ollama.example.com/v1; any non-empty key value if the server has no auth", Models: []ModelOption{}},
		{ID: "vllm", Name: "vLLM (self-hosted)", NeedsBaseURL: true,
			KeyHint: "Your vLLM server URL ending in /v1 and its --api-key value", Models: []ModelOption{}},
		{ID: "sgl", Name: "SGLang (self-hosted)", NeedsBaseURL: true,
			KeyHint: "Your SGLang server URL ending in /v1 and its --api-key value", Models: []ModelOption{}},
		{ID: "sarvam", Name: "Sarvam AI (speech)", DefaultBaseURL: "https://api.sarvam.ai",
			KeyHint: "Create a subscription key at dashboard.sarvam.ai; used for Indian-language speech to text and text to speech",
			Models:  []ModelOption{modelVoice("saarika:v2.5", "voice-stt", 15), modelVoice("bulbul:v2", "voice-tts", 15), modelChat("sarvam-m", 15, true)}},
		{ID: "elevenlabs", Name: "ElevenLabs (speech)", DefaultBaseURL: "https://api.elevenlabs.io/v1",
			KeyHint: "Create a key at elevenlabs.io/app/settings/api-keys; used for speech to text and text to speech", Models: []ModelOption{}},
		// The proxy models below were seen live on the xcodebest
		// OpenAI-compatible proxy. The snapshot prices the gpt-6.2 ones
		// (deploy/pricing-snapshot.json); any other model is costed from the
		// price set on the org's key.
		{ID: "openai-compatible", Name: "OpenAI-compatible endpoint", NeedsBaseURL: true, BifrostProvider: "openai", CustomBaseURL: true,
			KeyHint: "Any server that speaks the OpenAI API (vLLM, Ollama, LiteLLM, Together, Fireworks, DeepSeek...): give its base URL ending in /v1",
			Models: []ModelOption{modelChat("gpt-6.2-sol", 90, false), modelChat("gpt-6.2-astra", 90, false), modelChat("gpt-6.1-sol", 90, false),
				modelChat("gpt-6.1-terra", 90, false), modelChat("gpt-6-sol", 90, false), modelChat("gpt-6-astra", 90, false),
				modelChat("gpt-5.6-sol", 90, false), modelChat("gpt-5.6-luna", 90, false), modelChat("gpt-5.6-terra", 90, false)}},
	}
}
