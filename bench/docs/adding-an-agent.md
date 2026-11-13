# Adding an agent

An agent is one YAML file in `agents/` and one container image. Any harness, any model, any kind of agent: if it can run headless in a container and send its model calls to a base URL you give it, the bench can measure it with no Go code. A parser is optional and only adds detail.

`docs/harness-matrix.md` lists every agent config that ships, how it reaches Bifrost and how far it has been tested. `agents/custom-python.yaml` is the template to copy.

## The three rules

An agent container must:

1. **Read the task from the instruction.** The bench writes it to `/tmp/bench/instruction.md` and also offers it as the template variable `{{instruction}}`, so it can be passed as an argument or read from the file.
2. **Edit `/workspace`, then exit.** The bench grades the diff of `/workspace` with hidden tests after the container exits. What the agent prints is never graded. Exit when done; the wall budget kills it otherwise.
3. **Send every model call to the given base URL with the given key.** The container is on an internal network where the only reachable host is Bifrost (`{{gateway.url}}`, `http://bifrost:8080` inside). Its only credential is the run's Bifrost virtual key, `{{run.key}}`. Telemetry, update checks, plugin installs and model catalogue downloads fail, so switch them off.

The container runs as user 1000, with a read-only root filesystem, tmpfs on `/tmp` and `/home/agent`, all capabilities dropped and the CPU, memory and pid limits from `budgets`. Anything the agent needs to write outside `/workspace` goes under its home or `/tmp`.

## Pick a Bifrost route

Bifrost exposes provider-compatible routes and translates any of them to any configured provider. Use the route of the API format your harness already speaks:

| Harness speaks | Base URL | Credential header the harness sends |
|---|---|---|
| OpenAI chat completions | `{{gateway.url}}/openai/v1` (posts to `/chat/completions`) | `Authorization: Bearer <key>` |
| OpenAI Responses | `{{gateway.url}}/openai/v1` (posts to `/responses`) | `Authorization: Bearer <key>` |
| Anthropic Messages | `{{gateway.url}}/anthropic` (posts to `/v1/messages`) | `Authorization: Bearer <key>` or `x-api-key` |
| Gemini generateContent | `{{gateway.url}}/genai` (posts to `/v1beta/models/<model>:streamGenerateContent`) | `x-goog-api-key` |

Other routes (Bedrock, Cohere, LiteLLM, LangChain, PydanticAI shapes) live in `transports/bifrost-http/integrations/` in the Bifrost repo.

**Model ids.** Bifrost reads the provider from the model id's prefix: `anthropic/claude-sonnet-4-5`, `openai/gpt-5.1`, `gemini/gemini-2.5-pro`. The prefix must be a provider Bifrost knows (`core/schemas/utils.go`, `ParseModelString`); a bare id goes to the route's own provider (the Anthropic route to `anthropic`, the GenAI route to `gemini`, the OpenAI route to `openai`). So an OpenAI-format harness reaches Claude models by sending `anthropic/<model>`, and Claude Code reaches GPT models by sending `openai/<model>`.

**What the run key allows.** The bench mints one virtual key per run that allows one provider (`gateway.provider`) and one bare model (`model`). Put the bare id in `model` and add the prefix in the config where the harness needs it, as `anthropic/{{model}}`. Every call the harness makes, including title, summary and sub-agent calls, must use that one model, or the gateway refuses it. Pin the harness's small or background model to the same id.

**Session headers.** If the harness can add request headers, send `x-bf-session-id: {{run.session}}` and `x-bf-session-affinity: off`. Bifrost groups the run's rows by it as a secondary key; the bench joins rows by virtual key either way.

## The YAML

Strictly decoded into `schemas.AgentConfig` (`schemas/agent.go`): an unknown field is an error.

| Field | Meaning |
|---|---|
| `id` | Config id, equal to the file name without `.yaml` |
| `adapter` | `command` for any CLI agent. `scripted` is for the control agents only |
| `image` | The agent image, pinned by tag (and by digest in a locked experiment) |
| `build.dockerfile`, `build.tag` | Optional: how to build `image`, relative to `bench/`. `tag` equals `image` |
| `argv` | The command, one list item per argument. Templated |
| `env` | Environment variables. Values are templated, names are literal |
| `files` | Container path to file content, written before `argv` starts. Content is templated, paths are literal and absolute |
| `model` | The bare model id the run's key allows. Set per experiment; an arm may vary it |
| `notes.project_file` | The repo notes file the agent reads (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`). The stale-notes probe overwrites it |
| `notes.user_file`, `notes.user_path` | Optional user-level instructions: a file in `bench/` installed at an absolute path outside `/workspace` |
| `parser` | Optional parser name from `adapters.BuiltinParsers()`. Omit for gateway-level evidence |
| `budgets` | `wall_s`, `turns`, `cpus`, `mem_gb`, `pids_limit`, `usd`. The wall time used is the stricter of `wall_s` and the task's timeout |
| `gateway.provider` | The Bifrost provider the run's key allows |
| `gateway.max_retries` | Provider retries; `0` lets every fault reach the agent |
| `gateway.fallbacks` | Fallback models, only when the arm declares them |

### Template variables

`argv` items, `env` values and `files` contents may use these, and nothing else (`adapters/command.go`, DESIGN.md 16.5). An unknown variable fails planning.

| Variable | Value |
|---|---|
| `{{instruction}}` | The task text |
| `{{instruction_file}}` | `/tmp/bench/instruction.md` |
| `{{workspace}}` | `/workspace` |
| `{{model}}` | The config's `model` |
| `{{gateway.url}}` | Bifrost as the agent reaches it, no trailing slash: `http://bifrost:8080` |
| `{{run.key}}` | The run's virtual key. Allowed in `env` and `files` only; a config that puts it in `argv` is rejected, because argv shows in `ps` and in the written plan |
| `{{run.id}}` | The run id, `r_...` |
| `{{run.session}}` | The session id for `x-bf-session-id` (the run id today) |
| `{{exp.id}}` | The experiment id |
| `{{task.id}}` | The task id |

Values set by an arm's `set:` override are templated the same way, so `env.BENCH_MODEL: "openai/{{model}}"` works in an override.

### Switching provider in an arm

The configs in this folder keep the provider-qualified model id in one place, so an arm that changes the model provider is a short override. For `opencode` and `custom-python`:

```yaml
arms:
  A: {label: claude}
  B: {label: gpt, set: {gateway.provider: openai, model: gpt-5.1, env.BENCH_MODEL: "openai/{{model}}"}}
```

For Claude Code, `agents/claude-code-gpt.yaml` is the same config with every model field prefixed `openai/`. The gateway must have the target provider configured (`deploy/bench.yaml`).

## Build the image

- Start `FROM bench/coding-core-base:1`, so the agent sees the same Python, pytest and git the grader uses. Keep `USER agent` and `WORKDIR /workspace` at the end.
- Pin the harness version exactly (`ARG ..._VERSION=x.y.z`). A new release is a new image tag, never a silent upgrade.
- Install as root, then drop back to `agent`. Run the binary once at build time (`--version`) so a broken install fails the build.
- Put every switch that keeps the harness off the network in `ENV` or in the YAML `env`: no auto-update, no telemetry, no catalogue fetch, no plugin install at start.
- Build with `docker build -f agents/images/<name>/Dockerfile -t bench/agent-<name>:1 agents/images/<name>`.

## What you get without a parser

Every value carries the level it was observed at (DESIGN.md 9.5). With one YAML and no code:

| Level | Source | Values |
|---|---|---|
| `outcome` | Hidden verifier, the engine's clock, the tail of stdout | `verified_success`, `wall_time_s`, `false_completion` (the claim is read from the last 4 KB of stdout, so print the final message last) |
| `gateway` | Bifrost's rows for the run's key, and the fault proxy's ledger | `steps` (model turns), `tokens` (four buckets), `cost_usd`, and `tool_calls` the model requested, read from the stored upstream responses (Anthropic messages and OpenAI chat and Responses bodies are read today) |

This is the framework-independence claim: any agent that accepts the gateway base URL gets success, steps, tokens, cost and time with no adapter code.

## What a parser adds

A parser (`schemas.Parser`, one file in `adapters/` and one line in `BuiltinParsers()`) reads the agent's own stdout into normalized events at the `native` level: each model turn, each tool call with its target (the file, command or URL it acts on) and its result, retries and errors the harness saw, compactions, and the final success claim from the agent's last message rather than the stdout tail. Targets let the diagnostics table align what arm A and arm B did on the same task. A parser is pure, must skip lines it does not understand, and must never fail a run. `adapters/opencode.go` is a short example; test it on output captured from the pinned version (`adapters/testdata/`).

## Worked example: the custom template

`agents/images/custom-python/agent.py` is a real coding agent in 150 lines of standard-library Python. It reads the instruction file, calls `{{gateway.url}}/openai/v1/chat/completions` with the run key in a loop, offers two tools (`run_shell` in `/workspace` with a timeout, and `write_file`), executes the calls the model asks for, and stops on a final answer or the turn cap. It prints one JSON line per step and then the final message as plain text. It reads `AGENTS.md` from the workspace into its system prompt, so the stale-notes probe applies to it.

The image adds one file to the base:

```dockerfile
FROM bench/coding-core-base:1
USER root
COPY agent.py /opt/bench-agent/agent.py
RUN chmod 0755 /opt/bench-agent/agent.py \
 && ln -s /opt/bench-agent/agent.py /usr/local/bin/bench-agent
USER agent
WORKDIR /workspace
```

The YAML wires the three rules through env, with the provider prefix in one variable:

```yaml
id: custom-python
adapter: command
image: bench/agent-custom-python:1
argv: [bench-agent]
env:
  BENCH_BASE_URL: "{{gateway.url}}/openai/v1"
  BENCH_API_KEY: "{{run.key}}"
  BENCH_MODEL: "anthropic/{{model}}"
  BENCH_INSTRUCTION_FILE: "{{instruction_file}}"
  BENCH_SESSION_ID: "{{run.session}}"
model: claude-sonnet-4-5
notes: {project_file: AGENTS.md}
budgets: {wall_s: 900, turns: 40, cpus: 1, mem_gb: 2}
gateway: {provider: anthropic, max_retries: 0}
```

No `parser:` line, so its runs carry outcome and gateway evidence. To make it yours: copy both, rename the id and image, replace the loop in `agent.py` (or the whole image) with your agent, and keep the env contract.

## Check it before a real run

1. Decode and plan: `GOWORK=off go test ./adapters/` decodes every `agents/*.yaml` strictly and plans it (`TestShippedAgentConfigs`); add the pins that matter for yours, as `adapters/catalog_test.go` does.
2. Build the image.
3. Smoke it against a fake endpoint: run the container with the bench's flags on a throwaway internal Docker network next to a small server that speaks the harness's API format, and check that requests arrive at the right path with the key and that the agent edits `/workspace` and exits. `docs/harness-matrix.md` says how each shipped config was checked.
4. Then run it through the bench with the scripted model, and finally with a real model.
