# Open models on your Modal account

An evaluation can use an open model (Qwen, DeepSeek, Llama) that Heimdall
serves for you on your own Modal account, with vLLM or SGLang. You pay Modal
for the GPU time; Heimdall never uses its own Modal account for you.

## How it works

1. Connect Modal once: Workers, Connect a platform, Modal, with a Modal API
   token (`ak-...:as-...`). You do not have to make Modal your default worker.
2. Models page, **Serve an open model on your Modal account**: pick a model,
   an engine (vLLM 0.33.0 or SGLang 0.5.23), a GPU and a time limit. You can
   name the evaluation it is for.
3. Heimdall deploys a Modal app named `heimdall-om-<id>` that runs the
   engine's OpenAI-compatible server behind a new API key made for this
   deployment only. The app scales to zero: no container runs, and no GPU is
   billed, while no request comes, and an idle container stops after 5
   minutes.
4. When the model answers, Heimdall adds the endpoint as one of your
   provider keys (provider `vllm` or `sgl`, the deployment's `https://` URL
   as base URL, the deployment's key as value). Use it in an agent as
   `vllm/<model id>`, for example `vllm/Qwen/Qwen2.5-7B-Instruct`. Calls go
   agent, Bifrost, fault proxy, your Modal endpoint, so outage and
   stream-cut faults and the usage ledger work as for any provider.
5. When the named evaluation finishes, or the time limit passes, Heimdall
   stops the Modal app and deletes the key. You can stop it any time.

The first start of a deployment builds the engine image (CUDA devel base,
since the engines compile kernels with nvcc at start) on your account and
downloads the model; that takes a few minutes. Later cold starts reuse the
image. A server that dies while loading ends its container at once, so a
failed start does not hold a GPU until the startup timeout.

## Models and GPUs

`GET /api/bench/open-models/catalog` lists them with each GPU's count and
price per hour (Modal list prices, read 2026-11-22: T4 $0.59, L4 $0.80, A10
$1.10, L40S $1.95, A100 40 GB $2.10, A100 80 GB $2.50, H100 $3.95, H200
$4.54 per GPU hour). The recommended GPU is the cheapest one, from L4 up,
with enough memory. T4 is offered for models up to 2B parameters only and
runs them in fp16.

| Model | Licence | Recommended GPU |
|---|---|---|
| Qwen2.5 0.5B / 1.5B Instruct | Apache-2.0 | L4 |
| DeepSeek R1 Distill Qwen 1.5B / 7B | MIT | L4 |
| Qwen2.5 7B Instruct, Qwen2.5 Coder 7B, Qwen3 8B | Apache-2.0 | L4 |
| Llama 3.1 8B Instruct | Llama 3.1 Community License (gated) | L4 |
| Qwen2.5 32B Instruct | Apache-2.0 | A100 80 GB |
| Llama 3.3 70B Instruct | Llama 3.3 Community License (gated) | 2 x H200 |

Gated Llama models download with your Hugging Face token: accept the
licence on Hugging Face and add the token as the org secret `HF_TOKEN`.
Models with tool calls start with the engine's tool call parser (`hermes`
for Qwen, `llama3_json` for Llama on vLLM); DeepSeek R1 distills and Qwen3
start with a reasoning parser.

## Cost limits

Each deployment shows `max_usd`: its GPU price per hour times its time
limit (default 2 hours, at most 24). That is the most its GPU can cost, and
less when it is idle. An organization runs at most 4 deployments at once.
The reaper checks every 30 seconds; a stop that fails (Modal down) is
retried and the deployment stays `stopping` until it succeeds.
`GET /api/bench/open-models/modal-apps` lists `heimdall-om-*` apps on your
account that no running deployment owns, so a leftover can be stopped by
hand (`modal app stop <name>`).

## API

| Route | Role | What |
|---|---|---|
| `GET /api/bench/open-models/catalog` | viewer | models, engines, GPUs and prices |
| `GET /api/bench/open-models` | viewer | the org's deployments, newest first |
| `POST /api/bench/open-models` | admin | `{"model", "engine", "gpu", "experiment_id", "max_hours"}`; answers 202 with the deployment in state `starting` |
| `GET /api/bench/open-models/{id}` | viewer | one deployment |
| `POST /api/bench/open-models/{id}/stop` | admin | stop the app and delete the key now |
| `GET /api/bench/open-models/modal-apps` | admin | leftover serving apps on Modal |

States: `starting`, `ready`, `stopping`, `stopped`, `failed` (never became
ready; its app is stopped). Hosted mode only: local mode answers 503.

## Operator

The server needs Python with the `modal` package (`HEIMDALL_MODAL_PYTHON`,
the same as for Modal sandboxes), a database and the keystore master key.

A live end-to-end test deploys Qwen2.5 0.5B on one L4, waits for it, sends
one chat completion, stops it and checks no serving app is left:

    HEIMDALL_MODAL_LIVE=1 HEIMDALL_MODAL_LIVE_ENV=/path/to/modal.env \
    HEIMDALL_MODAL_PYTHON=/path/to/python go test ./openmodel -run TestModalLive -v

## GPU tasks on Modal

Separate from serving models: a GPU task (`resources.gpu`, see
[GPU tasks](gpu-tasks.md)) of an organization whose default worker is Modal
runs in a Modal GPU sandbox. The runner declares 8 virtual GPUs of one type
(`HEIMDALL_SANDBOX_GPU`, default `L4`), the coordinator leases GPU slots by
them, and each run's sandbox asks Modal for that type and the task's count
(`L4`, `H100:2`). The hosted planner accepts GPU tasks from the GPUs the
org's workers declare. Daytona and E2B sandboxes have no GPUs and refuse
GPU tasks.
