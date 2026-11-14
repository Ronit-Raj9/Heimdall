# Harness matrix

Every agent config in `agents/`, how it reaches Bifrost, and how far it has been tested. Filled from what was actually run on 2026-11-14. Only Codex has run on a real model so far.

Status levels:

- **tested for real**: ran through the bench against a real provider model. Codex only, so far.
- **smoke-tested with the scripted model**: ran through the full bench pipeline against the fault proxy's scripted model.
- **smoke-tested (fake API)**: the container ran with the bench's hardening flags on a throwaway internal network next to a fake server for its API format; the requests, path and key were checked on the wire and the agent completed one tool call and a final answer. Not a bench run.
- **configured only**: the YAML decodes and plans; nothing was run.

| Config | Harness | API format it speaks | Bifrost route | Providers through Bifrost | Parser | Notes file | Image | Status |
|---|---|---|---|---|---|---|---|---|
| `claude-code` | Claude Code 2.1.325 | Anthropic Messages, streamed | `/anthropic` | anthropic (bare id); any configured provider with a `provider/` prefix | `claudecode` | `CLAUDE.md` | `bench/agent-claude-code:1` | smoke-tested (fake API). Spike S7 (real Claude Code) not run: no Anthropic key |
| `claude-code-gpt` | Claude Code 2.1.325 | Anthropic Messages, streamed | `/anthropic` | openai, as `openai/<model>`; any configured provider by changing the prefix | `claudecode` | `CLAUDE.md` | `bench/agent-claude-code:1` | smoke-tested (fake API). Needs the openai provider in the gateway |
| `codex` | Codex CLI 0.169.0 | OpenAI Responses, streamed | `/openai/v1` (`/responses`) | openai (bare id); others with a prefix | `codex` | `AGENTS.md` | `bench/agent-codex:1` | tested for real, clean condition only: pilot `e_pilot_codex_elat`, 4 runs (2 arms x tasks `t01`, `t05`, 1 trial), `gpt-5.4-mini` through Bifrost's `openai` provider and the fault proxy, $0.08 in all, 2 of 4 verified. Fault conditions: real batch in progress <!-- TODO(lead): fill after batch -->. Fake-API captures in `adapters/testdata/codex-0.169.0-*.jsonl` |
| `opencode` | OpenCode (opencode-ai) 1.19.5 | OpenAI chat completions, streamed (`@ai-sdk/openai-compatible`) | `/openai/v1` | anthropic by default (`anthropic/<model>`); any configured provider via `env.BENCH_MODEL` | `opencode` | `AGENTS.md` | `bench/agent-opencode:1` | smoke-tested (fake API); parser checked on the captured output |
| `gemini-cli` | Gemini CLI 0.68.0 | Gemini generateContent, SSE | `/genai` | gemini (bare id); any configured provider with a prefix in the model id | none | `GEMINI.md` | `bench/agent-gemini-cli:1` | smoke-tested (fake API). Configured only for real runs: no Gemini key, and the gateway has no gemini provider |
| `custom-python` | Template agent, `agents/images/custom-python/agent.py` | OpenAI chat completions, not streamed | `/openai/v1` | anthropic by default (`anthropic/<model>`); any configured provider via `env.BENCH_MODEL` | none | `AGENTS.md` | `bench/agent-custom-python:1` | smoke-tested (fake API). Real batch in progress (`gpt-5.4-mini` through the `openai` provider, clean and fault conditions) <!-- TODO(lead): fill after batch --> |
| `mini-swe-agent` | mini-swe-agent 2.4.6 (litellm) | OpenAI chat completions, not streamed | `/openai/v1` | openai (bare id, current config) | none | `AGENTS.md` | `bench/agent-mini-swe-agent:1` | smoke-tested (fake API) |
| `toy` | Scripted toy agent | Anthropic Messages | `/anthropic` | anthropic | `claudecode` | `CLAUDE.md` | `bench/coding-core-base:1` | smoke-tested with the scripted model (`runs/e_smoke_sdoj`, SYNTHETIC) |
| `oracle`, `noop`, `exploit`, `refuse` | Scripted controls | none (no model calls) | none | none | none | `CLAUDE.md` | `bench/coding-core-base:1` | controls; they make no model calls |

"Providers through Bifrost" is what Bifrost's route can translate to. A run can only use a provider the bench gateway configures. The bench sets up two, both through the fault proxy (`providerSetups` in `cmd/bifrost-bench/gateway.go`): `anthropic`, with the key slots from `deploy/bench.yaml`, and `openai`, with four slots. A config with `gateway.provider: gemini` needs that provider (and its key slots) added there before it can run.

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

## Caveats per harness

- **Run key in the tool shell.** OpenCode, Gemini CLI, Claude Code and mini-swe-agent pass their whole environment to the commands they run, so the model's shell can read the run key (`env | grep -c sk-bf-` printed 1). The custom template strips it (0); the Codex config excludes it. The key is scoped to one run, one model and a network where only Bifrost is reachable.
- **Provider-side web tools.** `gemini-cli` excludes `google_web_search` and `web_fetch`, which run at the provider and would reach the internet past the evaluation network; `codex` disables `web_search`. Both Claude Code configs still offer `WebSearch`, a server-side tool; whether to disallow it is a decision for those configs.
- **Gateway-level tool calls** are read from the stored upstream response bodies: Anthropic messages and OpenAI chat and Responses today. A run whose upstream provider is gemini would have `tool_calls` unknown at the gateway level.
- **claude-code-gpt.** Bifrost converts the Messages request for the OpenAI provider (raw passthrough is only for Claude models). Claude-only server tools are unavailable, `cache_control` is dropped, and Claude Code's own cost figure uses Claude prices (the bench prices gateway rows). Anthropic does not support Claude Code on non-Claude models, so a regression on this arm can be a harness and model mismatch.
- **opencode.** The model id is set once, in `env.BENCH_MODEL`, read by the config through OpenCode's `{env:...}` substitution (checked on the wire). A repository that ships its own `opencode.json` could override the model; task repos do not. `small_model` is pinned to the run's model.
- **gemini-cli.** Gemini CLI can make side calls on its own flash models (next-speaker check, loop detection, compression). None appeared in the smoke; in a longer session the run's key refuses them and the gateway shows an error row.
- **mini-swe-agent** sends no session header; rows still join by virtual key.

## Reproducing the smoke

The smoke tools are not in the tree: a fake server for OpenAI chat, Anthropic messages and Gemini generateContent (Python standard library), a planner that renders a config through `adapters.Builtin` into a `RunPlan`, and a runner that starts the container with the flags above. The method is in `docs/adding-an-agent.md`, "Check it before a real run".
