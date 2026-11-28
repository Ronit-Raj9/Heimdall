# Harness matrix

Every agent config in `agents/`: what it is built with, how its model traffic
reaches Bifrost, what evidence its runs carry, what the free conformance
check says, and whether it has ever run on a real model. Updated 2026-11-18
from what was actually run; nothing here is inferred from configuration.

**Only two configs have run on a real model: Codex and `custom-python`**
(the bench's own no-framework agent): 318 sealed runs in six experiments on
`gpt-5.4-mini`, $8.08 in all, listed in `docs/results/README.md`. Every
other config, the seven framework agents included, has run only on the fault
proxy's scripted model or against a fake API. A framework agent passing the
check proves the plumbing (it runs unchanged, reaches only the gateway, is
measured), not how well it codes.

Status levels, strongest first:

- **tested for real**: ran through the bench against a real provider model.
- **checked**: `agent check` passed (no FAIL row) on the scripted model:
  one real task through the whole pipeline (workspace, run key, agent, diff
  export, evidence, hidden-test grade, gates, seal) with a passive network
  observer beside the agent. $0. See `docs/adding-an-agent.md`.
- **smoke-tested with the scripted model**: ran as arms of an experiment
  through plan, run and seal, every model call answered by the scripted
  model. $0. Synthetic: never a finding.
- **smoke-tested (fake API)**: the container ran with the bench's hardening
  flags next to a fake server for its API format; requests, path and key
  were checked on the wire. Not a bench run.
- **configured only**: the YAML decodes and plans; nothing was run.

## All configs

| Config | Built with (pinned) | Language | How its traffic reaches Bifrost | Evidence level | `agent check` on the scripted model | Ever run on a real model |
|---|---|---|---|---|---|---|
| `codex`, `codex-task` (task-image variant, not listed) | Codex CLI 0.169.0 | Rust (npm package) | OpenAI Responses, streamed, to `/openai/v1` through a `bifrost` model provider in its config (`base_url`, key from `BIFROST_API_KEY`) | native (parser `codex`) + gateway | 8 PASS, 1 FAIL: egress, a DNS lookup of github.com from the `plugins` feature (2026-11-15; see below) | **yes**: 249 runs, `gpt-5.4-mini` |
| `custom-python` | none: one standard-library Python file | Python 3.12 | OpenAI chat completions to `/openai/v1` (`BENCH_BASE_URL`, `BENCH_API_KEY`) | gateway | no FAIL: 8 PASS, 4 WARN (the process observer joined too late for a 1 s run, no file event, `claim_only` evidence, no agent-reported steps), 2026-11-18 | **yes**: 69 runs, `gpt-5.4-mini` |
| `fw-plain-openai` | none: OpenAI Python SDK 3.50.0 | Python 3.12 | OpenAI chat completions; the SDK reads `OPENAI_BASE_URL`, `OPENAI_API_KEY` | gateway | no FAIL: 9 PASS, 3 WARN (no file event, `claim_only` evidence, no agent-reported steps; all expected on the scripted model), 2026-11-18 | no |
| `fw-langgraph` | LangGraph 1.2.22, langchain-openai 1.8.0 | Python 3.12 | OpenAI chat completions; `ChatOpenAI` reads `OPENAI_BASE_URL`, `OPENAI_API_KEY` | gateway | no FAIL: 9 PASS, 3 WARN (no file event, `claim_only` evidence, no agent-reported steps; all expected on the scripted model), 2026-11-18 | no |
| `fw-openai-agents` | OpenAI Agents SDK (openai-agents 0.24.0) | Python 3.12 | OpenAI Responses (the SDK default); `AsyncOpenAI()` under an `OpenAIResponsesModel` reads `OPENAI_BASE_URL`, `OPENAI_API_KEY` | gateway | no FAIL: 9 PASS, 3 WARN (no file event, `claim_only` evidence, no agent-reported steps; all expected on the scripted model), 2026-11-18 | no |
| `fw-crewai` | CrewAI 1.15.41 | Python 3.12 | OpenAI chat completions; `LLM(custom_openai=True)` reads `OPENAI_BASE_URL`, `OPENAI_API_KEY` | gateway | no FAIL: 9 PASS, 3 WARN (no file event, `claim_only` evidence, no agent-reported steps; all expected on the scripted model), 2026-11-18 | no |
| `fw-pydantic-ai` | Pydantic AI (pydantic-ai-slim 2.68.0) | Python 3.12 | OpenAI chat completions; `OpenAIProvider` reads `OPENAI_BASE_URL`, `OPENAI_API_KEY` | gateway, plus agent-reported OpenTelemetry spans | no FAIL: 10 PASS, 2 WARN (no file event, `claim_only` evidence); its OpenTelemetry spans reached the collector (4 events), 2026-11-18 | no |
| `fw-llamaindex` | LlamaIndex (llama-index-core 0.14.26, llama-index-llms-openai-like 0.9.0) | Python 3.12 | OpenAI chat completions; `OpenAILike` reads `OPENAI_API_BASE`, `OPENAI_API_KEY` | gateway | no FAIL: 9 PASS, 3 WARN (no file event, `claim_only` evidence, no agent-reported steps; all expected on the scripted model), 2026-11-18 | no |
| `fw-ai-sdk` | Vercel AI SDK (ai 7.0.176, @ai-sdk/openai 4.0.121) | Node 22.23.5 | OpenAI Responses (the provider default); `createOpenAI()` reads `OPENAI_BASE_URL`, `OPENAI_API_KEY` | gateway | no FAIL: 9 PASS, 3 WARN (no file event, `claim_only` evidence, no agent-reported steps; all expected on the scripted model), 2026-11-18 | no |
| `claude-code` | Claude Code 2.1.325 | Node (npm package) | Anthropic Messages, streamed, to `/anthropic` (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`) | native (parser `claudecode`) + gateway | no FAIL: 10 PASS, 2 WARN (no file event, no agent-reported steps); 4 model calls, native trace complete, 2026-11-18 | no: no Anthropic key |
| `claude-code-gpt` | Claude Code 2.1.325 | Node (npm package) | Anthropic Messages to `/anthropic`, model `openai/<model>`, translated by Bifrost | native (parser `claudecode`) + gateway | not run this round (provider openai) | no |
| `claude-code-gated` | Claude Code 2.1.325 | Node (npm package) | Anthropic Messages, streamed, to `/anthropic`; tool permissions asked through the approval channel (`approver: true`, `--permission-prompt-tool`) | native (parser `claudecode`) + gateway | not run this round (same harness as `claude-code`, approval channel on) | no |
| `opencode` | OpenCode (opencode-ai) 1.19.5 | TypeScript (npm package) | OpenAI chat completions, streamed (`@ai-sdk/openai-compatible`), to `/openai/v1` from its config file | native (parser `opencode`) + gateway | 10 PASS, 1 WARN, **1 FAIL: egress**, a DNS lookup of registry.npmjs.org at start despite its `OPENCODE_DISABLE_*` switches (2026-11-18; see below). It also exported 1001 OpenTelemetry spans to the run's collector | no |
| `gemini-cli` | Gemini CLI 0.68.0 | Node (npm package) | Gemini generateContent, SSE, to `/genai` (`GOOGLE_GEMINI_BASE_URL`, `GEMINI_API_KEY`) | gateway | cannot run: the fault proxy has no scripted Gemini model, so the check refuses provider `gemini` | no: no Gemini key or provider |
| `mini-swe-agent` | mini-swe-agent 2.4.6 (litellm) | Python | OpenAI chat completions to `/openai/v1` (`OPENAI_BASE_URL`, `OPENAI_API_KEY`) | native (parser `mini-swe-agent`, its trajectory file printed to stdout after the run) + gateway | not run this round (provider openai) | no |
| `heimdall-reflector` | none: OpenAI Python SDK 3.50.0 (the `fw-plain-openai` image, own id and a higher step cap) | Python 3.12 | OpenAI chat completions; the SDK reads `OPENAI_BASE_URL`, `OPENAI_API_KEY` | gateway | not run this round (the improver of improvement runs on the task `heimdall-improver/reflect-edit`; not a normal agent) | no |
| `bare-model` | none: one model call, no tools, no loop (`agents/images/bare-model/`) | Python 3.12 | OpenAI chat completions to `/openai/v1` (`BENCH_BASE_URL`, `BENCH_API_KEY`, `BENCH_NO_TOOLS=1`) | gateway | not run this round (the bare-model baseline an evaluation adds with "Compare with the bare model"; answer-graded benchmarks only) | no |
| `toy` | scripted toy agent | shell | Anthropic Messages to `/anthropic` | native (parser `claudecode`) + gateway | not run | no: smoke-tested with the scripted model (`runs/e_smoke_sdoj`, SYNTHETIC) |
| `oracle`, `noop`, `exploit`, `refuse` | scripted controls | shell | none: no model calls | outcome | not run | no model |
| `mcploop` | scripted MCP tool loop (adapter `scripted`, kind `mcploop`; B2) | Python 3.12 (standard library) | Anthropic Messages, streamed, to `/anthropic`; MCP tools from `BENCH_MCP_SERVERS`, direct or through Bifrost's MCP gateway; extra request headers from `BENCH_GATEWAY_HEADERS` | native (parser `claudecode`) + gateway | not run | no: proven against the scripted upstream `toolscript` only (`docs/bifrost-features.md`) |
| `team-control` | none: scripted control (adapter `scripted`, kind `team`) | Python 3.12 | no model call: a lead and two worker containers talk only through the team mailbox | gateway + mailbox ledger | not a harness; admission and tests only | no (control) |
| `subagents-control` | none: scripted control (adapter `scripted`, kind `subagents`) | Python 3.12 | no model call: one container reports a planner, two workers, a handoff and a reviewer as native agent events | self-reported agent events | not a harness; admission and tests only | no (control) |

"Evidence level" is what the runs carry with no extra work: **gateway**
(success, steps, tokens in four buckets, cost, time and the tool calls the
model requested, from Bifrost's rows and the proxy ledger) or **native**
(plus each step and tool target from the harness's own stream, through a
parser). Process and workspace observers run beside every local agent either
way (`docs/steps-and-evidence.md`).

## Framework agents

Seven agents, six written with popular frameworks and one with none, each
a small coding agent with list, read, write and run-command tools on
`/workspace` in its framework's standard idiom (checked against the pinned
package's source). Each lives in `examples/frameworks/<name>/` (agent, `requirements.in` and compiled
`requirements.txt` lock or `package.json` and `package-lock.json`,
Dockerfile). They import nothing from Heimdall. Each YAML sets only the
standard variables its SDK reads (`OPENAI_BASE_URL` or `OPENAI_API_BASE`, and
`OPENAI_API_KEY`) to `{{gateway.url}}/openai/v1` and the run key, plus
`AGENT_MODEL`, `AGENT_MAX_STEPS` and the optional session id. Guide:
`docs/frameworks.md`.

| Config | Image (`-coding-core`) | Added to the 333 MB suite base | Wire calls in the check | Step cap | Telemetry switched off | Session header | OpenTelemetry |
|---|---|---|---|---|---|---|---|
| `fw-plain-openai` | 404 MB | 71 MB | 2 x `POST /openai/v1/chat/completions` | loop bound | none needed | `default_headers` | not exported |
| `fw-langgraph` | 493 MB | 160 MB | 2 x `POST /openai/v1/chat/completions` | `recursion_limit` | LangSmith tracing (`LANGSMITH_TRACING`, `LANGCHAIN_TRACING_V2` false; off unless set) | `ChatOpenAI(default_headers=)` | not exported (LangChain traces through LangSmith) |
| `fw-openai-agents` | 466 MB | 133 MB | 2 x `POST /openai/v1/responses` | `max_turns` | `OPENAI_AGENTS_DISABLE_TRACING=1` (traces go to api.openai.com otherwise) | `AsyncOpenAI(default_headers=)` | not exported (the SDK's tracing is its own format) |
| `fw-crewai` | 1.69 GB | 1.36 GB | 2 x `POST /openai/v1/chat/completions` | `max_iter` | `CREWAI_DISABLE_TELEMETRY`, `CREWAI_DISABLE_TRACKING`, `CREWAI_TRACING_ENABLED=false`, `CREWAI_DISABLE_VERSION_CHECK` (a PyPI lookup), Chroma's `ANONYMIZED_TELEMETRY=False`, `HF_HUB_OFFLINE=1` | `LLM(default_headers=)` | not exported (CrewAI's own OpenTelemetry is its telemetry, switched off) |
| `fw-pydantic-ai` | 464 MB | 131 MB | 2 x `POST /openai/v1/chat/completions`, OTLP to the run's collector | `UsageLimits(request_limit=)` | none needed (Logfire not installed) | `AsyncOpenAI(default_headers=)` | **exported and verified**: `Agent.instrument_all()` with an OTLP/HTTP exporter when `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set; the check received 4 events (llm 2, agent 1, tool_call 1), kept as agent-reported |
| `fw-llamaindex` | 716 MB | 383 MB | 2 x `POST /openai/v1/chat/completions` | `max_iterations` | none needed (tokenizer and NLTK data ship in the package) | `OpenAILike(default_headers=)` | not exported |
| `fw-ai-sdk` | 537 MB | 204 MB | 2 x `POST /openai/v1/responses` | `stopWhen: stepCountIs()` | none needed; the model is a provider instance, never a string id (which would use the Vercel AI Gateway) | `createOpenAI({headers})` | not exported (`experimental_telemetry` off) |

The seven images share one base layer, so together they take about 2.8 GB
(333 MB base plus 2.44 GB of framework layers). "Wire calls" is what the
check's network observer saw leave the agent: nothing but the gateway in
every case. The scripted model answers the first call that offers tools
with one call of the first tool (every agent's `list_files`, which lists
`/workspace`), then plain text, so each agent made exactly two model calls,
answered one tool call, printed the scripted reply last and exited 0. Every framework
loop ended on a plain-text answer by itself, CrewAI included.

### Smoke of the matrix

`experiments/matrix-frameworks-smoke.yaml` puts the seven framework agents
through the experiment pipeline (plan, freeze, workspace, run key, agent,
diff export, hidden-test grade, gates, seal) with every model call answered
by the scripted model: each arm is switched to the anthropic provider, which
the fault proxy answers from its scripted model because it holds no
Anthropic key. The agents still speak OpenAI chat completions or Responses
to `/openai/v1`; Bifrost translates both to the Anthropic Messages API, so
the smoke also shows each agent running unchanged on a provider it was not
written for. $0: the cost in its report is the scripted tokens priced at the
pinned snapshot; nothing was spent. Synthetic: never a finding. The run
folders are in a scratch runs root, not committed.

- **First smoke, `e_matrix_frameworks_smoke_isqy` (21 runs, clean on t01
  and t02 plus model-outage on t01):** 21 of 21 sealed. Six agents exited 0
  and printed the scripted reply last in every run. `fw-openai-agents`
  exited 1 in all three: `UserError: Unknown prefix: anthropic`. The Agents
  SDK reads a provider prefix in a string model id as its own routing and
  refuses one it does not know, so on any provider but openai it never sent
  a request. Fixed in the agent: it now passes an `OpenAIResponsesModel`
  with its own client, which sends the id verbatim. The model-outage slots
  armed (7 of 7) and never fired: the probe returns 503 from the third model
  call on, and the scripted model ends a run in two. The rerun leaves that
  condition out; how each framework handles a failing model needs a real
  model.
- **Rerun with the fix, `e_matrix_frameworks_smoke_30uo` (14 runs, clean on
  t01 and t02):** 14 of 14 planned, ran, graded and sealed. Every agent,
  `fw-openai-agents` included, exited 0 in both runs and printed the
  scripted reply last; agent wall time 1.8 s (AI SDK) to 15.3 s
  (LlamaIndex). Every run fails its task, as it must (the scripted model
  edits nothing), so the verdict is `invalid, fix_harness`, which is what a
  smoke with no success reports.

**Gateway metrics in the smoke are mostly unknown, and why.** In both
smokes most runs report steps, tokens and tool calls as unknown ("gateway
request log incomplete: row ... has no ledger entry"), 17 of 21 in the
first and 13 of 14 in the rerun. The cause is outside the agents: every
bench instance on this machine (one per working copy) shares the lead's fault proxy and its key
slots `fp-slot-1` to `fp-slot-4`, and each leases slots from its own runs
root. The proxy's log shows other runs binding their plans to the slot a
smoke run was using while it ran, so the proxy tagged that run's ledger
lines with another run's id. Success, wall time, exit code, the diff and
the seal are unaffected. Separate bench instances need separate slots (or
one lease store) before their gateway metrics can be trusted; with one
instance per proxy, as in a normal install, the join is complete (the
`agent check` runs above all joined).

## Routes and images

| Config | API format it speaks | Bifrost route | Providers through Bifrost | Parser | Notes file | Image |
|---|---|---|---|---|---|---|
| `claude-code` | Anthropic Messages, streamed | `/anthropic` | anthropic (bare id); any configured provider with a `provider/` prefix | `claudecode` | `CLAUDE.md` | `bench/agent-claude-code:1` |
| `claude-code-gpt` | Anthropic Messages, streamed | `/anthropic` | openai, as `openai/<model>`; any configured provider by changing the prefix | `claudecode` | `CLAUDE.md` | `bench/agent-claude-code:1` |
| `codex` | OpenAI Responses, streamed | `/openai/v1` (`/responses`) | openai (bare id); others with a prefix | `codex` | `AGENTS.md` | `bench/agent-codex:1` |
| `opencode` | OpenAI chat completions, streamed (`@ai-sdk/openai-compatible`) | `/openai/v1` | anthropic by default (`anthropic/<model>`); any configured provider via `env.BENCH_MODEL` | `opencode` | `AGENTS.md` | `bench/agent-opencode:1` |
| `gemini-cli` | Gemini generateContent, SSE | `/genai` | gemini (bare id); any configured provider with a prefix in the model id | none | `GEMINI.md` | `bench/agent-gemini-cli:1` |
| `custom-python` | OpenAI chat completions, not streamed | `/openai/v1` | anthropic by default (`anthropic/<model>`); any configured provider via `env.BENCH_MODEL` | none | `AGENTS.md` | `bench/agent-custom-python:1` |
| `mini-swe-agent` | OpenAI chat completions, not streamed | `/openai/v1` | openai (bare id, current config) | `mini-swe-agent` | `AGENTS.md` | `bench/agent-mini-swe-agent:1` |
| `fw-*` (seven) | OpenAI chat completions, or Responses for `fw-openai-agents` and `fw-ai-sdk` | `/openai/v1` | openai (bare id in `AGENT_MODEL`); any configured provider by overriding `gateway.provider`, `model` and `env.AGENT_MODEL: "<provider>/{{model}}"` together | none | `AGENTS.md` | `bench/agent-fw-<name>:1`, built per suite (`build.base_arg`), so `bench/agent-fw-<name>:1-coding-core` |
| `toy` | Anthropic Messages | `/anthropic` | anthropic | `claudecode` | `CLAUDE.md` | `bench/coding-core-base:1` |
| `oracle`, `noop`, `exploit`, `refuse` | none (no model calls) | none | none | none | `CLAUDE.md` | `bench/coding-core-base:1` |
| `mcploop` | Anthropic Messages, streamed | `/anthropic` | anthropic | `claudecode` | `CLAUDE.md` | `bench/coding-core-base:1` |

"Providers through Bifrost" is what Bifrost's route can translate to. A run
can only use a provider the bench gateway configures. The bench sets up two,
both through the fault proxy (`providerSetups` in
`cmd/bifrost-bench/gateway.go`): `anthropic`, with the key slots from
`deploy/bench.yaml`, and `openai`, with four slots. A config with
`gateway.provider: gemini` needs that provider (and its key slots) added
there before it can run.

## What the fake-API smoke checked

Each config was rendered into a run plan by the real command adapter (`adapters.Builtin(...).Plan`, run key `sk-bf-smoke-runkey-0123456789`, `{{gateway.url}}` = `http://bifrost:8080`). The container then ran with the environment's flags (user 1000, `--read-only`, tmpfs `/tmp` and `/home/agent`, `--cap-drop ALL`, `no-new-privileges`, pids, memory and CPU limits, plan files and multi-line env delivered by the same base64 `sh` wrapper) on an `--internal` Docker network whose only other container was a fake API with the alias `bifrost`. The workspace was a small git repo with the config's notes file. The fake answered the first call with one shell tool call (`echo bench-fake > fake.txt`) and the second with "Done. All tests pass and fake.txt was written." mini-swe-agent 2.4.6 (default config `mini.yaml`) needs a `bash` tool call in every reply and stops only when a command's output starts with its submit marker, so for it the second answer was the `bash` call `echo COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT`. An earlier smoke whose fake answered it with plain text ended in `RepeatedFormatError`.

| Config | Requests seen | Credential | Result |
|---|---|---|---|
| `opencode` | 2 x `POST /openai/v1/chat/completions`, `stream: true`, model `anthropic/claude-sonnet-4-5`, 10 tools, `x-bf-session-id` set | `Authorization: Bearer <run key>` | bash tool ran, `fake.txt` written, exit 0. No title call (`--title` given), no catalogue fetch, no npm install, offline |
| `gemini-cli` | 2 x `POST /genai/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse`, 14 tools (web tools excluded), `x-bf-session-id` set | `x-goog-api-key: <run key>` | `run_shell_command` ran, exit 0. With model `openai/gpt-5.1` the path was `/genai/v1beta/models/openai/gpt-5.1:streamGenerateContent`, which Bifrost's `models/{model:*}` route accepts |
| `claude-code-gpt` | 2 x `POST /anthropic/v1/messages?beta=true`, streamed, model `openai/gpt-5.1`, `x-bf-session-id` set | `Authorization: Bearer <run key>` | Bash tool ran, exit 0. Claude Code logs `[claude-code:unrecognized_model]` to stderr and carries on |
| `claude-code` | 2 x `POST /anthropic/v1/messages?beta=true`, model `claude-sonnet-4-5` | `Authorization: Bearer <run key>` | Bash tool ran, exit 0 |
| `custom-python` | 2 x `POST /openai/v1/chat/completions`, model `anthropic/claude-sonnet-4-5`, 2 tools, `x-bf-session-id` set | `Authorization: Bearer <run key>` | `run_shell` ran, final message printed last, exit 0 |
| `mini-swe-agent` | 2 x `POST /openai/v1/chat/completions`, not streamed, model `gpt-5.6-terra`, 1 tool (`bash`), no session header | `Authorization: Bearer <run key>` | `bash` ran, `fake.txt` written, the submit command ended the loop (trajectory `exit_status: Submitted`, 2 API calls), exit 0 |

In the final smoke of `opencode`, `gemini-cli`, `claude-code-gpt`, `custom-python` and `mini-swe-agent`, the tool's shell saw the bench's `PATH` with a shim folder prepended (`/opt/bench-shims:...`), so tool-shim probes reach the commands these harnesses run.

The smoke tools are not in the tree: a fake server for OpenAI chat, Anthropic messages and Gemini generateContent (Python standard library), a planner that renders a config through `adapters.Builtin` into a `RunPlan`, and a runner that starts the container with the flags above.

## Caveats per harness

- **Run key in the tool shell.** OpenCode, Gemini CLI, Claude Code and mini-swe-agent pass their whole environment to the commands they run, so the model's shell can read the run key (`env | grep -c sk-bf-` printed 1). The custom template strips it (0), the Codex config excludes it, and the seven framework agents drop `OPENAI_API_KEY` from their tool shell's environment. The key is scoped to one run, one model and a network where only Bifrost is reachable.
- **Provider-side web tools.** `gemini-cli` excludes `google_web_search` and `web_fetch`, which run at the provider and would reach the internet past the evaluation network; `codex` disables `web_search`. Both Claude Code configs still offer `WebSearch`, a server-side tool; whether to disallow it is a decision for those configs. The framework agents offer only their four local tools.
- **Gateway-level tool calls** are read from the stored upstream response bodies: Anthropic messages and OpenAI chat and Responses today. A run whose upstream provider is gemini would have `tool_calls` unknown at the gateway level.
- **claude-code-gpt.** Bifrost converts the Messages request for the OpenAI provider (raw passthrough is only for Claude models). Claude-only server tools are unavailable, `cache_control` is dropped, and Claude Code's own cost figure uses Claude prices (the bench prices gateway rows). Anthropic does not support Claude Code on non-Claude models, so a regression on this arm can be a harness and model mismatch.
- **opencode.** The model id is set once, in `env.BENCH_MODEL`, read by the config through OpenCode's `{env:...}` substitution (checked on the wire). A repository that ships its own `opencode.json` could override the model; task repos do not. `small_model` is pinned to the run's model.
- **gemini-cli.** Gemini CLI can make side calls on its own flash models (next-speaker check, loop detection, compression). None appeared in the smoke; in a longer session the run's key refuses them and the gateway shows an error row.
- **mini-swe-agent** sends no session header; rows still join by virtual key.
- **Framework agents.** Each framework's telemetry and update checks are switched off in the YAML and the image (`docs/frameworks.md` lists the switches); a version bump can add a new call home, which the check's egress row catches. `fw-ai-sdk` must keep its model a provider instance: a plain string model id would route through the Vercel AI Gateway. `fw-llamaindex` reads `OPENAI_API_BASE`, not `OPENAI_BASE_URL`. `fw-crewai`'s image carries CrewAI's full dependency set (Chroma, LanceDB, ONNX Runtime) though the agent uses none of it.

## Findings from `agent check`

- **Codex CLI 0.169.0 looks up github.com at start.** With the config in
  `agents/codex.yaml`, which already sets `check_for_update_on_startup =
  false`, analytics and feedback off and no OpenTelemetry exporter,
  `bifrost-bench agent check -no-build -suite coding-core -task t01
  agents/codex.yaml` passes eight rows and fails the egress row: `tried to
  reach 1 host other than the gateway: github.com`. The observer saw DNS
  queries for `github.com` (A and AAAA, plus the resolver's search-suffix
  retry) and no connection attempt, because the internal network answers no
  name but `bifrost`. An earlier check run also listed `api.github.com`.
  The lookup comes from Codex's `plugins` feature, which `codex features
  list` shows as stable and on by default in 0.169.0; `codex --help`
  documents `--disable <FEATURE>` as `-c features.<name>=false`, so the
  config key is `plugins = false` under `[features]`. In
  `examples/acme-node/agents/codex.yaml` that one line removes the lookup:
  `agent check agents/codex.yaml -suite acme-orders -task ord01 -no-build`
  failed on the egress row before it and passed all nine rows twice after
  it.
- **Effect on the recorded results: none measured.** The lookup failed
  inside the evaluation network, Codex went on, and the egress gate reads
  the preflight probe, not the observer, so the 249 sealed Codex runs were
  graded as before. `agents/codex.yaml` is left unchanged on purpose: its
  resolved config hash identifies those runs on the leaderboard and in
  every manifest. A new experiment that wants a clean egress row should use
  a new config (a copy with `plugins = false` and its own id).
- **The check counted the bench's own event collector as egress.** An
  agent that exports OpenTelemetry spans to the per-run collector the bench
  points it at (`OTEL_EXPORTER_OTLP_*` = `http://toolproxy:9200`) failed the
  egress row with "tried to reach toolproxy". `fw-pydantic-ai` was the first
  to do it. Fixed on 2026-11-18: the egress row allows the collector's host
  while the collector ran (`collectorHosts` in
  `cmd/bifrost-bench/agentrows.go`, test
  `agentrows_collector_test.go`).
- **OpenCode 1.19.5 looks up registry.npmjs.org at start.** `agent check
  -no-build -suite coding-core -task t01 agents/opencode.yaml` on
  2026-11-18 failed only the egress row: DNS queries for
  `registry.npmjs.org` (plus the resolver's search-suffix retry) and no
  connection, as the internal network answers no other name. The config
  already sets `OPENCODE_DISABLE_AUTOUPDATE`, `OPENCODE_DISABLE_MODELS_FETCH`,
  `OPENCODE_DISABLE_DEFAULT_PLUGINS` and `OPENCODE_DISABLE_LSP_DOWNLOAD`; which
  part of OpenCode makes the lookup was not traced. `agents/opencode.yaml`
  was left unchanged.
- **Gemini CLI** cannot be checked this way: the fault proxy has no
  scripted Gemini model, so `agent check` refuses an agent whose provider is
  `gemini`.
