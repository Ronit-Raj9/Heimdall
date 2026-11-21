# Models and providers

Heimdall never calls a model provider directly. Every model call an agent makes goes through the [Bifrost](https://github.com/maximhq/bifrost) gateway, which gives one endpoint for every provider and a request log the agent cannot edit. So the models you can evaluate are the models Bifrost can reach: closed models, open-weight models, and any server that speaks the OpenAI API.

The model picker in the evaluation wizard and the "Models and keys" page both read `GET /api/bench/models`.

## Every Bifrost provider

Each of these is selectable as the `provider` of an evaluation arm, and you can add a key for each on the keys page. The id is what you type in config files and the API.

| Provider | Id | What it is |
|---|---|---|
| OpenAI | `openai` | GPT and o-series models |
| Anthropic | `anthropic` | Claude models |
| Azure OpenAI | `azure` | OpenAI models on your Azure deployment |
| AWS Bedrock | `bedrock` | Claude, Llama, Mistral, DeepSeek and others on AWS |
| Google Vertex AI | `vertex` | Gemini and partner models on Google Cloud |
| Google Gemini | `gemini` | Gemini models through AI Studio keys |
| Mistral | `mistral` | Mistral, Devstral and Codestral models |
| Groq | `groq` | fast hosting of open models |
| Cohere | `cohere` | Command models |
| DeepSeek | `deepseek` | DeepSeek models, first-party |
| xAI | `xai` | Grok models |
| Cerebras | `cerebras` | fast hosting of open models |
| Fireworks | `fireworks` | hosting of open models |
| OpenRouter | `openrouter` | one key for many vendors; model ids look like `vendor/model` |
| Perplexity | `perplexity` | Sonar models with web search |
| Hugging Face | `huggingface` | inference providers for open models |
| Nebius | `nebius` | hosting of open models |
| Parasail | `parasail` | hosting of open models |
| Baseten | `baseten` | hosting of open and custom models |
| Databricks | `databricks` | models served on Databricks |
| Cloudflare Workers AI | `cloudflare` | open models on Cloudflare |
| AWS SageMaker | `sagemaker` | your own SageMaker endpoints |
| Replicate | `replicate` | hosting of open models |
| Ollama | `ollama` | models on your own machine, no key |
| vLLM | `vllm` | your own vLLM server |
| SGLang | `sgl` | your own SGLang server |
| MiniMax | `minimax` | MiniMax models, first-party |
| BytePlus | `byteplus` | ByteDance models |
| Sarvam | `sarvam` | Indian-language speech and chat |
| ElevenLabs | `elevenlabs` | speech (voice benchmarks) |
| GitHub Copilot | `github-copilot` | models through a Copilot subscription |
| Custom, OpenAI-compatible | any name you choose | any server that speaks the OpenAI API, with your base URL |

Bifrost core also knows a few more provider ids (for example `bedrock_mantle`, `opencode-go`, `opencode-zen`, `wafer`, `typesafe`, `kimchi`, and the media providers `runway` and `runware`). They work through the gateway, but the picker does not suggest models for them.

## Adding a key

1. Open **Models and keys** (or, in the wizard, the "Add a key" link next to the model picker).
2. Pick the provider. The form says where to get a key and what it usually starts with.
3. Paste the key. For a custom or self-hosted provider, also give the base URL (ending in `/v1` for OpenAI-compatible servers).
4. Optional: limit the key to some models, or give it a budget in dollars.
5. Press **Test**. Heimdall makes the provider's cheapest signed-in call (a model list where the provider has one) and records whether the key works and which models it serves.

The same through the API:

```bash
curl -sS -X POST http://127.0.0.1:8090/api/bench/models/keys \
  -H "Authorization: Bearer $HEIMDALL_TOKEN" -H 'Content-Type: application/json' \
  -d '{"provider":"deepseek","label":"team key","value":"sk-...","budget_usd":10}'
curl -sS -X POST -H "Authorization: Bearer $HEIMDALL_TOKEN" http://127.0.0.1:8090/api/bench/models/keys/<id>/test
```

Keys are encrypted at rest and never shown again (only the last 4 characters). On the hosted platform each organization has its own keys, and Bifrost routing for that organization is built from them for every provider, not only OpenAI and Anthropic. During a run the agent never holds your key: it gets a fresh Bifrost virtual key for that run only, revoked afterwards.

Locally, without the web app, put the key in `deploy/.env` (only the fault proxy reads it).

## Open-weight models

Open-weight models are first-class: the catalog marks them with `"open_weights": true` and a `family`, and you can filter the picker to open models only. The same family is often served by several providers. Pick the one you have a key for; the scores are comparable only when the provider serves the same weights and settings, so name the provider in the arm.

| Family | Who makes it | Served natively by | Also through |
|---|---|---|---|
| DeepSeek (V3.x, R1) | DeepSeek | `deepseek` | `openrouter`, `fireworks`, `together`-style endpoints, `nebius`, `huggingface`, `bedrock` |
| GLM (4.5, 4.6 and later) | Zhipu AI (Z.ai) | custom OpenAI-compatible endpoint (Z.ai API) | `openrouter`, `fireworks`, `huggingface` |
| Kimi (K2) | Moonshot AI | custom OpenAI-compatible endpoint (Moonshot API) | `openrouter`, `groq`, `fireworks`, `huggingface` |
| MiMo | Xiaomi | custom OpenAI-compatible endpoint (Xiaomi MiMo API) | `openrouter` |
| MiniMax (M1, M2) | MiniMax | `minimax` | `openrouter`, `fireworks` |
| Mistral (Large, Medium, Devstral, Codestral) | Mistral AI | `mistral` | `bedrock`, `vertex`, `openrouter` |
| Qwen (Qwen3, Qwen3-Coder) | Alibaba | custom OpenAI-compatible endpoint (Alibaba Cloud Model Studio) | `openrouter`, `fireworks`, `cerebras`, `nebius`, `huggingface` |
| Llama | Meta | none (Meta does not run a public API for every model) | `groq`, `cerebras`, `fireworks`, `bedrock`, `openrouter`, `nebius`, `huggingface` |
| gpt-oss | OpenAI (open weights) | none | `groq`, `cerebras`, `fireworks`, `openrouter` |
| Inkling | Thinking Machines | none | `openrouter`, or your own `vllm` / `sgl` server from the Hugging Face weights |
| Your own fine-tune | you | `vllm`, `sgl`, `ollama` on your machine | `baseten`, `sagemaker`, `huggingface` |

Model ids are not invented here. The catalog lists only ids checked against each provider's public model list, and the picker also accepts any other id the provider serves. To see exactly what your key can call, test the key: the provider's own model list is saved with it.

Inkling is the open-weight model family from Thinking Machines (weights on Hugging Face as `thinkingmachines/Inkling`). The catalog lists it under `openrouter` as `thinkingmachines/inkling` and `thinkingmachines/inkling-small`, marked open weights with family `inkling`; you can also serve the weights yourself through `vllm`, `sgl` or `huggingface`.

## Custom OpenAI-compatible endpoints

Any server that speaks the OpenAI chat completions API works: a vendor API that has no native Bifrost provider (Zhipu, Moonshot, Xiaomi, Alibaba), a `together`-style host, LiteLLM, or your own vLLM, SGLang or Ollama server.

1. On the keys page pick **OpenAI-compatible endpoint**.
2. Give it a name (this becomes the provider id in your arms), the base URL ending in `/v1`, and the key (leave empty for a server with no auth).
3. Test it. If the server has `/v1/models`, its model list is saved.
4. In the wizard, pick that provider and type or pick the model id.

The catalog marks these providers with `"custom_base_url": true` and says which Bifrost provider carries the traffic (`"bifrost_provider"`). Your agent still talks only to Bifrost; Bifrost forwards to your base URL.

A self-hosted model costs you machine time, not tokens, so Heimdall shows its cost as unknown unless you add a price (see below).

## Pricing: unknown stays unknown

Cost numbers come from a pinned price list, `deploy/pricing-snapshot.json`, not from the provider's bill and not from guesses.

- Each entry is the provider's official price per 1 million input and output tokens (and cached input where the provider states one), with the source URL and the date it was checked.
- A run's cost is its token counts (as Bifrost recorded them) times those prices.
- If a model has no entry, its cost is **unknown**. Heimdall never fills in a guess, a similar model's price or zero. The report card leaves the cost out, the estimate says `"known": false`, and the agent leaderboard shows a dash.
- The same model on two providers can have two prices. The price is looked up by provider and model together.
- Prices change. The date on each entry tells you how old it is. To update one, edit the snapshot with the new official price, its source URL and today's date.

## Where to look next

- [evaluations.md](evaluations.md): run your agent on many benchmarks with one of these models.
- [model-matrix.md](model-matrix.md): the same agent across many models in one grid.
- [routing-experiments.md](routing-experiments.md): compare Bifrost routing and fallback rules.
