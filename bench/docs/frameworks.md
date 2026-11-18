# Framework agents: add yours in ten minutes

Heimdall measures an agent through the gateway, not through the framework it
was built with. An agent built with LangGraph, the OpenAI Agents SDK, CrewAI,
Pydantic AI, LlamaIndex, the Vercel AI SDK or no framework at all runs
**unchanged**: its own code reads the standard base URL and key variables of
its SDK, and the agent's YAML points those variables at Bifrost with the
run's virtual key. No Heimdall import, no adapter code, no parser.

Seven such agents ship as examples, one folder each under
`examples/frameworks/` with a config in `agents/fw-*.yaml`. Each is a small
coding agent (list, read and write files in `/workspace`, run a shell
command) in its framework's standard idiom (the APIs its documentation
shows, checked against the pinned package's source). Every one
passes `agent check` on the scripted model at $0, and all seven ran through
plan, run and seal in a scripted smoke experiment. **None has run on a real
model yet**; `docs/harness-matrix.md` keeps that record per agent.

## The examples

| Config | Framework (pinned) | Language | Wire format to Bifrost | Variables the framework reads | Step cap | Telemetry switched off |
|---|---|---|---|---|---|---|
| `fw-plain-openai` | none: OpenAI Python SDK 3.50.0 | Python 3.12 | chat completions | `OPENAI_BASE_URL`, `OPENAI_API_KEY` (`OpenAI()`) | loop bound, `AGENT_MAX_STEPS` | none needed |
| `fw-langgraph` | LangGraph 1.2.22, langchain-openai 1.8.0 | Python 3.12 | chat completions | `OPENAI_BASE_URL`, `OPENAI_API_KEY` (`ChatOpenAI`, through the openai client) | `recursion_limit` = 2 x steps + 1 | `LANGSMITH_TRACING=false`, `LANGCHAIN_TRACING_V2=false` (off unless set; explicit) |
| `fw-openai-agents` | OpenAI Agents SDK 0.24.0 | Python 3.12 | Responses (the SDK default) | `OPENAI_BASE_URL`, `OPENAI_API_KEY` (`AsyncOpenAI()` under an `OpenAIResponsesModel`) | `Runner.run_sync(max_turns=)` | `OPENAI_AGENTS_DISABLE_TRACING=1` (traces go to api.openai.com otherwise) |
| `fw-crewai` | CrewAI 1.15.41 | Python 3.12 | chat completions | `OPENAI_BASE_URL`, `OPENAI_API_KEY` (`LLM(..., custom_openai=True)`) | `Agent(max_iter=)` | `CREWAI_DISABLE_TELEMETRY`, `CREWAI_DISABLE_TRACKING`, `CREWAI_TRACING_ENABLED=false`, `CREWAI_DISABLE_VERSION_CHECK` (PyPI lookup), Chroma's `ANONYMIZED_TELEMETRY=False`, `HF_HUB_OFFLINE=1` |
| `fw-pydantic-ai` | Pydantic AI 2.68.0 (`pydantic-ai-slim[openai]`) | Python 3.12 | chat completions | `OPENAI_BASE_URL`, `OPENAI_API_KEY` (`OpenAIProvider`) | `UsageLimits(request_limit=)` | none needed (Logfire not installed); exports OpenTelemetry spans to the bench's collector |
| `fw-llamaindex` | LlamaIndex core 0.14.26, llms-openai-like 0.9.0 | Python 3.12 | chat completions | `OPENAI_API_BASE`, `OPENAI_API_KEY` (`OpenAILike`; LlamaIndex does not read `OPENAI_BASE_URL`) | `agent.run(max_iterations=)` | none needed (tokenizer and NLTK data ship in the package) |
| `fw-ai-sdk` | Vercel AI SDK 7.0.176, @ai-sdk/openai 4.0.121 | Node 22.23.5 | Responses (the provider default) | `OPENAI_BASE_URL`, `OPENAI_API_KEY` (`createOpenAI()`) | `stopWhen: stepCountIs()` | none needed; the model is a provider instance, so the Vercel AI Gateway (the SDK's default for a plain string id) is never used |

The CrewAI, Agents SDK, LlamaIndex, Pydantic AI and AI SDK variables were
read from the pinned packages' source; no framework documentation page was
re-fetched for this (the session's web-search quota was spent), and the
LangSmith switches are LangChain's documented names, not re-checked. All of
them were then confirmed on the wire: `agent check` records every name the
agent looks up and every connection it opens, and its egress row passes only
when the gateway (and the bench's own event collector) is all the agent
contacted. Re-check after every version bump: a new release can add
a new call home.

`agents/custom-python.yaml` is the bench's original no-framework agent (the
standard library only, no SDK). It and Codex are the only configs with real
runs.

## Add your own

You need your agent's code, a Dockerfile and one YAML file. Start from the
example closest to your framework.

**1. Make the agent read its endpoint from the environment.** Most SDKs do
already: the OpenAI clients read `OPENAI_BASE_URL` and `OPENAI_API_KEY`;
LlamaIndex reads `OPENAI_API_BASE`. If your code passes `base_url=` or
`api_key=` explicitly, read them from those variables. Read the model id from
a variable too (the examples use `AGENT_MODEL`), so an experiment can vary it.
Then make sure the agent

- reads the task from the file the bench gives it (`{{instruction_file}}`,
  `/tmp/bench/instruction.md`; the examples take it as the first argument),
- works in `/workspace` and exits 0 when it is done, solved or not (a
  non-zero exit means it crashed),
- prints its final message last on stdout,
- stops by itself: set the framework's step cap (table above),
- keeps the key out of the commands its model runs (the examples drop
  `OPENAI_API_KEY` from the tool shell's environment),
- reaches nothing but the gateway: switch off telemetry, trace upload and
  update checks, and bake every dependency into the image.

Optionally add the session header `x-bf-session-id` (the examples read it
from `AGENT_SESSION_ID` and pass it through the framework's default-headers
option); without it `agent check` warns, and gateway rows still join by the
run key.

**2. Pin and package it.** Copy an example Dockerfile. It starts
`ARG BASE_IMAGE=bench/coding-core-base:1` / `FROM ${BASE_IMAGE}`, so the
agent's commands see the suite's own Python, pytest and git, installs the
framework into its own virtualenv off the task's `PATH`, puts a `fw-agent`
launcher on the `PATH`, and ends `USER 1000`. Pin exactly: `requirements.in`
names your top-level packages with `==`, and the image installs the full lock
compiled from it:

```
uv pip compile requirements.in --python-version 3.12 \
  --python-platform x86_64-manylinux_2_28 --no-header -o requirements.txt
```

For Node, pin exact versions in `package.json`, commit `package-lock.json`
and install with `npm ci` (see `examples/frameworks/ai-sdk/Dockerfile`, which
copies Node from a pinned Node image).

**3. Write `agents/<id>.yaml`.** Copy `agents/fw-langgraph.yaml` and change
the id, image, Dockerfile path and telemetry switches. The part that makes it
run unchanged:

```yaml
env:
  OPENAI_BASE_URL: "{{gateway.url}}/openai/v1"   # Bifrost's OpenAI-compatible route
  OPENAI_API_KEY: "{{run.key}}"                   # the run's virtual key, env only
  AGENT_MODEL: "{{model}}"                        # bare id: the openai provider
  AGENT_MAX_STEPS: "40"
  AGENT_SESSION_ID: "{{run.session}}"
model: gpt-5.4-mini
gateway:
  provider: openai
  max_retries: 0
```

To run the same agent on another provider, override the provider, the model
and the model id the agent sends, together:
`set: {gateway.provider: anthropic, model: claude-sonnet-4-5, env.AGENT_MODEL: "anthropic/{{model}}"}`.
Bifrost translates the agent's OpenAI-format requests (chat completions or
Responses) for that provider; the smoke experiment below runs all seven
examples that way.

**4. Check it for free.**

```
bifrost-bench agent check agents/<id>.yaml -suite coding-core
```

It builds the image for the suite, runs one task through the whole pipeline
with every model call answered by the fault proxy's scripted model (nothing
is spent), and prints a PASS, WARN or FAIL row for each requirement with a
one-line fix. Exit 0 means no row failed. The scripted model calls only the
first tool the agent offers, once, then answers in plain text, so expect
three WARN rows on a plain agent: no file event (the scripted model asks for
no edit), `claim_only` evidence and no agent-reported steps (both optional;
see below). The other rows must pass.

**5. Put it in an experiment.** Add it to `experiments/matrix-frameworks.yaml`
(`matrix.agents`) or give it an arm of its own. Run a one-trial pilot with a
small `budget.total_usd` before the full matrix.

## Pitfalls the examples hit

- **Provider-prefixed model ids.** To reach a non-OpenAI provider through
  `/openai/v1`, the agent sends `anthropic/<model>`. Some frameworks read that
  prefix themselves. The OpenAI Agents SDK, given the string, raises
  `UserError: Unknown prefix: anthropic` (found by the smoke on 2026-11-18);
  `fw-openai-agents` therefore passes an `OpenAIResponsesModel` with its own
  client, which sends the id verbatim. CrewAI would route `anthropic/...` to
  its native Anthropic provider; `custom_openai=True` keeps it on the
  OpenAI-compatible endpoint. A bare id on the openai provider needs neither.
- **The base URL variable.** LlamaIndex reads `OPENAI_API_BASE`, not
  `OPENAI_BASE_URL`; set both.
- **A string model in the AI SDK** goes to the Vercel AI Gateway. Use a
  provider instance (`createOpenAI()(model)`).
- **Tool order.** The scripted model calls the first tool offered. The AI
  SDK sends tools sorted by name unless `toolOrder` says otherwise; the other
  frameworks keep the order they are given. Put a harmless tool first (the
  examples' `list_files`) so `agent check` exercises one tool call safely.

## Evidence without a parser

With no parser, a framework agent's runs carry gateway-level evidence:
verified success, steps, tokens in four buckets, cost, wall time, the tool
calls the model asked for (read from the stored responses), and false
completion from the last lines of stdout. That is the same for every arm of a
matrix, which is what makes frameworks comparable.

An agent can add what no outside witness sees by reporting its own steps:
the bench sets the standard `OTEL_EXPORTER_OTLP_*` variables to a per-run
collector. `fw-pydantic-ai` shows the pattern: when
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set it installs an OTLP/HTTP span
exporter and calls `Agent.instrument_all()`, as the Pydantic AI docs show for
OpenTelemetry without Logfire. Its check row reads "4 events received (otlp
4; kinds llm 2, agent 1, tool_call 1)". Such events are agent-reported: shown
with their own badge, never counted in a trusted metric
([agent-events.md](agent-events.md)). The other examples do not export spans:
LangChain's tracing goes through LangSmith, CrewAI's OpenTelemetry is its own
telemetry, and instrumenting the rest needs extra packages
(OpenInference); none of that was verified here.

## The matrix and its smoke

- `experiments/matrix-frameworks.yaml`: Codex and the seven examples, all on
  `gpt-5.4-mini` through the openai provider, eight tasks, two trials, 128
  runs. Not run: it costs money. Its comment holds the forecast and what the
  matrix can and cannot conclude (eight tasks cannot separate 28 pairs on
  success; read it for cost, tokens, steps and false completion per
  framework).
- `experiments/matrix-frameworks-smoke.yaml`: the seven examples through
  plan, run and seal on the scripted model, $0 because each arm is switched
  to the anthropic provider, which the bench's fault proxy answers from its
  scripted model when it holds no Anthropic key (check the proxy's
  `/health` first). Run on 2026-11-18: 14 of 14 sealed, every agent exited
  0 and printed its final message last. Its first run found the Agents SDK
  prefix pitfall above. It cannot show how a framework handles a failing
  model: the scripted model ends every run in two calls, before the
  model-outage probe fires (`docs/harness-matrix.md`).

## Image sizes

All images share the suite base layer (`bench/coding-core-base:1`, 333 MB);
`docs/harness-matrix.md` lists each image's size. CrewAI is by far the
largest: its dependencies include Chroma, LanceDB, ONNX Runtime and the
Kubernetes client, none of which the example uses.
