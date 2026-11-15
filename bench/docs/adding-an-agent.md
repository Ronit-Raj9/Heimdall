# Adding your own agent

An agent is one YAML file in `agents/` and one container image. Any harness, any model, any kind of agent: a CLI, a script or a server. If it can run headless in a container, read the task, edit the workspace and send its model calls to a base URL and key it is given, the bench measures it with no Go code and no SDK. A parser is optional and only adds detail.

Three commands take an agent from nothing to a free conformance run:

```
bifrost-bench agent init mybot -template python          # or command, http-service
bifrost-bench agent build agents/mybot.yaml -suite coding-core
bifrost-bench agent check agents/mybot.yaml -suite coding-core
```

`agent check` runs one real task through the normal pipeline with the fault proxy answering every model call from its scripted model, so it costs nothing, and prints a PASS, WARN or FAIL row for each thing an agent must get right, with a one-line fix. When every row passes, put the agent in an experiment (`experiments/<name>.yaml`, arm `agent: agents/mybot.yaml`) and run it on a real model.

`docs/harness-matrix.md` lists the agent configs that ship (Claude Code, Codex, OpenCode, Gemini CLI, mini-swe-agent, `custom-python`) and how far each has been tested. `docs/bring-your-own-design.md` is the contract these commands implement.

## The three templates

Every command takes `-config deploy/bench.yaml`; the project root is that file's grandparent, and `agent init` writes there. `-tag` sets the image tag (default `bench/agent-<id>:1`); `-force` overwrites existing files, which are otherwise never touched.

| Template | For | Writes |
|---|---|---|
| `command` | Any command-line agent | `agents/<id>.yaml` with argv placeholders for the task file, workspace and model, and env with the gateway URL and run key under the names most CLIs read (`OPENAI_BASE_URL`, `OPENAI_API_KEY`). `agents/images/<id>/Dockerfile` installs `example-cli`, a 60-line stand-in you replace with your CLI |
| `python` | Writing a new agent | A coding agent in one standard-library Python file (`agent.py`: a tool loop with `run_shell` and `write_file` against OpenAI chat completions), trimmed from `agents/images/custom-python` |
| `http-service` | Agents that are servers (LangGraph, FastAPI, an Agents SDK app) | A launcher delivered through the YAML's `files:` and a 60-line example server (`server.py`, `http.server` only, no pip install) |

All three generated Dockerfiles start

```dockerfile
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}
```

and every generated YAML sets `build.base_arg: BASE_IMAGE`, so the image is built on each suite's toolchain (see "One image per suite" below). They end with `USER 1000` and `WORKDIR /workspace`. The `python` and `http-service` templates need `python3` in the suite's base image.

### http-service: an agent that is a server

The command adapter starts `python3 /tmp/bench/launcher.py`. The launcher:

1. starts the server (`AGENT_SERVER_CMD`) in the run container, with its output on stderr and without the run key in its environment,
2. polls `AGENT_HEALTH_URL` until it answers 200 (`AGENT_STARTUP_TIMEOUT_S`, default 60),
3. POSTs the task to `AGENT_RUN_URL` as `{"instruction", "workspace", "base_url", "api_key", "model", "session_id"}`,
4. takes a 200 or 201 answer as the result; a 202 answer `{"id": ...}` is polled at `AGENT_STATUS_URL` (`{id}` in it is replaced, otherwise `/<id>` is appended) while its `status` is `queued`, `pending`, `running` or `in_progress`,
5. prints the result's `final_message` last on stdout, stops the server and exits 0 when `status` is `completed` (or absent on a 200), 1 otherwise. The whole run is bounded by `AGENT_RUN_TIMEOUT_S` (default 840). Keep both timeouts under `budgets.wall_s`, so the launcher reports a timeout before the bench kills the container.

Your server listens on loopback inside the run container and makes its own model calls to `base_url` with `api_key`, sending `session_id` as `x-bf-session-id`. Replace `server.py` with your server and its dependencies baked into the image.

## What an agent must and must not do

An agent container must:

1. **Read the task.** The bench writes it to `/tmp/bench/instruction.md` (`{{instruction_file}}`) and also offers it as `{{instruction}}`.
2. **Edit `/workspace`, then exit 0.** The bench grades the diff of `/workspace` with hidden tests after the container exits; what the agent prints is never graded. Exit 0 when the run is finished, solved or not. A non-zero exit means the agent crashed. The wall budget kills an agent that does not exit.
3. **Send every model call to the given base URL with the given key.** The only reachable host is Bifrost (`{{gateway.url}}`, `http://bifrost:8080` inside). The only credential is the run's virtual key, `{{run.key}}`, which allows one provider and one model.
4. **Print the final message last on stdout.** The completion claim is read from the last 4 KB of stdout (or by a parser), so false completion can be measured.
5. **Send the session id** as the header `x-bf-session-id: {{run.session}}` (and `x-bf-session-affinity: off`) when the harness can add headers. Rows join by the run key either way.

An agent must not:

- reach any host other than the gateway: no telemetry, update checks, plugin or model catalogue downloads, `pip install` or `npm install` at start. Bake dependencies into the image and switch the rest off in `ENV` or the YAML `env`;
- put the run key in argv (it shows in `ps` and in the written plan; the planner refuses it) or in the environment of the commands its model runs;
- need root, write outside `/workspace`, `/tmp` and `$HOME`, or delete `/workspace/.git`;
- call a second model (title, summary, sub-agent): the run key refuses any model but the run's.

The container runs as uid 1000 with a read-only root filesystem, tmpfs on `/tmp` and `/home/agent`, all capabilities dropped, `no-new-privileges` and the CPU, memory and pid limits from `budgets`, on an internal network.

## The YAML

Strictly decoded into `schemas.AgentConfig` (`schemas/agent.go`) and validated against `spec/agent.schema.json`: an unknown field is an error.

| Field | Meaning |
|---|---|
| `id` | Config id, equal to the file name without `.yaml` |
| `adapter` | `command` for any agent. `scripted` is for the control agents only |
| `image` | The agent image, pinned by tag. With `build.base_arg` it equals `build.tag` and the image that runs is `<tag>-<suite id>` |
| `build.dockerfile` | Dockerfile, relative to the project root |
| `build.tag` | The tag `agent build` builds; equals `image`. With `base_arg` it needs a tag part (`name:tag`) |
| `build.base_arg` | Dockerfile ARG that receives the suite's base image: one image per suite |
| `build.context` | Build context, relative to the project root. Default: the Dockerfile's folder. Set it when the Dockerfile copies your agent's code from elsewhere in your repository |
| `argv` | The command, one list item per argument. Templated |
| `env` | Environment variables. Values are templated, names are literal |
| `files` | Container path to file content, written before `argv` starts. Content is templated, paths are literal and absolute |
| `model` | The bare model id the run's key allows. Set per experiment; an arm may vary it |
| `notes.project_file` | The repo notes file the agent reads (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`). The stale-notes probe overwrites it |
| `notes.user_file`, `notes.user_path` | Optional user-level instructions: a file in the project installed at an absolute path outside `/workspace` |
| `parser` | Optional parser name from `adapters.BuiltinParsers()`. Omit for gateway-level evidence |
| `budgets` | `wall_s`, `turns`, `cpus`, `mem_gb`, `pids_limit`, `usd`. The wall time used is the stricter of `wall_s` and the task's timeout |
| `gateway.provider` | The Bifrost provider the run's key allows |
| `gateway.max_retries` | Provider retries; `0` lets every fault reach the agent |
| `gateway.fallbacks` | Fallback models, only when the arm declares them |

### Template variables

`argv` items, `env` values and `files` contents may use these, and nothing else (`adapters/command.go`). An unknown variable fails planning. File content is a template too, so a launcher or config file must not contain a literal `{{`.

| Variable | Value |
|---|---|
| `{{instruction}}` | The task text |
| `{{instruction_file}}` | `/tmp/bench/instruction.md` |
| `{{workspace}}` | `/workspace` |
| `{{model}}` | The config's `model` |
| `{{gateway.url}}` | Bifrost as the agent reaches it, no trailing slash: `http://bifrost:8080` |
| `{{run.key}}` | The run's virtual key. Allowed in `env` and `files` only |
| `{{run.id}}` | The run id, `r_...` |
| `{{run.session}}` | The session id for `x-bf-session-id` (the run id) |
| `{{exp.id}}` | The experiment id |
| `{{task.id}}` | The task id |

Values set by an arm's `set:` override are templated the same way, so `env.BENCH_MODEL: "openai/{{model}}"` works in an override.

## One image per suite: `build.base_arg`

A suite names a base image with its toolchain (Python and pytest, Node, Go). An agent that edits code in that suite should see the same toolchain the grader uses. With `build.base_arg`:

- the Dockerfile declares the ARG and starts `FROM ${<arg>}`;
- `bifrost-bench agent build agents/<id>.yaml -suite <suite>` runs `docker build -f <dockerfile> -t <tag>-<suite id> --build-arg <arg>=<the suite's base image tag> <context>` and prints the tag and the image id;
- the planner resolves the agent's image to `<tag>-<suite id>` before it hashes the config, so the manifest, the config hash, the run plan and `agent check` all name the same per-suite tag. One function does it, `engine.ResolveAgentImage`.

Without `base_arg` nothing changes: the one `image` runs in every suite and the config hashes of existing agents are unchanged (`engine/checkplan_test.go` pins `agents/codex.yaml` and `agents/custom-python.yaml`).

The manifest records, per arm, the local image id the tag named at plan time (`arms.<id>.image_id`). A run whose tag now names a different image is refused before its agent starts, so rebuilding an image never changes what a frozen experiment measures: restore the image or plan a new experiment.

## Pick a Bifrost route

Bifrost exposes provider-compatible routes and translates any of them to any configured provider. Use the route of the API format your agent already speaks:

| Agent speaks | Base URL | Credential header |
|---|---|---|
| OpenAI chat completions | `{{gateway.url}}/openai/v1` (posts to `/chat/completions`) | `Authorization: Bearer <key>` |
| OpenAI Responses | `{{gateway.url}}/openai/v1` (posts to `/responses`) | `Authorization: Bearer <key>` |
| Anthropic Messages | `{{gateway.url}}/anthropic` (posts to `/v1/messages`) | `Authorization: Bearer <key>` or `x-api-key` |
| Gemini generateContent | `{{gateway.url}}/genai` (posts to `/v1beta/models/<model>:streamGenerateContent`) | `x-goog-api-key` |

**Model ids.** Bifrost reads the provider from the model id's prefix: `anthropic/claude-sonnet-4-5`, `openai/gpt-5.1`. A bare id goes to the route's own provider. So an OpenAI-format agent reaches Claude models by sending `anthropic/<model>`.

**What the run key allows.** One provider (`gateway.provider`) and one bare model (`model`). Put the bare id in `model` and the prefix where the agent needs it, as `anthropic/{{model}}`. Pin any small or background model to the same id. To switch provider in an arm, override both: `set: {gateway.provider: openai, model: gpt-5.4-mini, env.BENCH_MODEL: "openai/{{model}}"}`. The gateway must have that provider configured.

## Framework agents

A framework agent needs only the base URL and the key, from env. What each framework documents, checked on 2026-11-15:

| Framework | Base URL | Key | Also set |
|---|---|---|---|
| OpenAI Python SDK | `OPENAI_BASE_URL` (read by the client when `base_url` is not passed) | `OPENAI_API_KEY` | |
| OpenAI Agents SDK | `OPENAI_BASE_URL` (its default OpenAI provider reads it) | `OPENAI_API_KEY` | Tracing exports to OpenAI by default: set `OPENAI_AGENTS_DISABLE_TRACING=1` (or call `set_tracing_disabled(True)`). It uses the Responses API by default; `set_default_openai_api("chat_completions")` switches to chat completions |
| LangChain / LangGraph with `langchain-openai` (`ChatOpenAI`) | `base_url=` argument, else `OPENAI_API_BASE`, else `OPENAI_BASE_URL` (read by the underlying `openai` client) | `OPENAI_API_KEY` | |
| CrewAI (1.15 docs) | `base_url=`, else `OPENAI_BASE_URL`, else `OPENAI_API_BASE` (legacy) | `OPENAI_API_KEY` | A non-OpenAI model id through an env-configured endpoint needs `custom_openai=True` on the `LLM`. Telemetry: `CREWAI_DISABLE_TELEMETRY=true` (or `OTEL_SDK_DISABLED=true`) |

So for an OpenAI-format framework agent the YAML env is:

```yaml
env:
  OPENAI_BASE_URL: "{{gateway.url}}/openai/v1"
  OPENAI_API_KEY: "{{run.key}}"
  BENCH_MODEL: "anthropic/{{model}}"        # the model id your code passes
  BENCH_SESSION_ID: "{{run.session}}"       # send as the x-bf-session-id header
```

Not verified here, check before relying on it: the variable names `langchain-anthropic` (`ChatAnthropic`) and the Anthropic SDK read for a base URL, and LangSmith's tracing switches (keep tracing off; it would try to reach LangSmith). `agent check` shows on the wire whether a call left for any host other than the gateway.

## `agent check`: the free conformance test

```
bifrost-bench agent check <agents/id.yaml> -suite <suite> [-task <task>] [-json] [-no-build] [-runs checks] [-config deploy/bench.yaml]
```

It builds the image (unless `-no-build`, or the YAML has no `build.dockerfile`, when it uses the existing image), plans a one-arm, one-slot manifest through the same planner as an experiment (the suite's first task unless `-task`; a draft or unlocked suite only warns), and runs it through the normal pipeline: workspace, run key, agent, diff export, evidence, hidden-test grade, gates, seal.

- **Free.** The fault proxy binds plan mode `scripted` to the run's key slot: every call of that run is answered by its scripted model, whatever provider the agent calls and whether or not the proxy holds a real key, and no request or key is forwarded. The check first asks the proxy (`GET /health`) for `plan_modes` and `scripted_providers` and refuses to run, exit 4, when the proxy is older than mode `scripted`, when `BENCH_FAULTPROXY_TOKEN` is not set, or when the agent's provider has no scripted model. It never runs and hopes.
- **Apart from your runs.** Check runs are sealed under `checks/` in the project root (`-runs` to change), never in `runs/`: they enter no experiment, verdict, leaderboard or run list. The folder holds the run as usual (`agent.stream.jsonl`, `agent.stderr.txt`, `plan.json`, `evidence.json`) plus `netwatch.jsonl`, so a failing row can be read in full. Add `checks/` to `.gitignore`.
- **Observed on the wire.** For a check only, the agent joins the network namespace of a holder container on the evaluation network, and a passive observer with only `CAP_NET_RAW` in that namespace records what leaves the agent: DNS names, connection attempts, and the request line and header names of plain HTTP requests (no header values except `x-bf-session-id`). It needs `python3` and `sleep` in the helper image. A connection to a bare IP address fails with "network unreachable" before any packet leaves, so it is not seen; names are.

Exit 0 when no row fails (WARN rows included), 1 when a row fails, 4 when the check could not run. `-json` prints `{agent, suite, task, image, image_id, exp_id, run_id, run_dir, outcome, rows: [{id, title, status, detail, fix}], result, error}` for CI. The task outcome is shown but is not a row: the scripted model solves nothing.

### The rows

| Row | PASS when | WARN or FAIL, and the fix |
|---|---|---|
| `image` builds | the per-suite image built, or the prebuilt image exists | FAIL: build error or missing image. Run `agent build` and fix the first failing step; base_arg needs `build.dockerfile` and a tagged `build.tag` equal to `image` |
| starts as the unprivileged user | the container ran as uid 1000 and the image's `USER` is not root | FAIL: the container never started, or exit 126/127 (argv[0] not found or not executable). WARN: image `USER` is root; end the Dockerfile with `USER 1000` and test as that user |
| model call reached Bifrost with the run key | Bifrost logged the run's model calls under its key and the proxy answered each from the scripted model | FAIL: none reached Bifrost (check the base URL variable and route); requests reached the gateway but none was logged (wrong credential or route); every call failed (the key allows one provider and one model). FAIL as well if any call went to a real provider |
| session id sent | every gateway request carried `x-bf-session-id` equal to `{{run.session}}` | WARN: missing on some or all requests, a different id, or not observable |
| exited by itself inside the budget | exit 0 before `wall_s` | FAIL: killed at the wall budget (exit when done; cap your own turns and time), or a non-zero exit (the last stderr line is shown) |
| diff exported | the trusted export ran; the file count is shown (0 is expected: the scripted model asks for no edit) | FAIL: export failed (keep `/workspace` a git work tree). WARN: paths dropped (symlinks, devices, submodules) |
| no egress outside the gateway | the egress gate held and the observer saw only the gateway | FAIL: the agent tried other hosts (listed; resolver search-suffix retries folded in), or the egress gate fired. WARN: the observer could not run |
| evidence level | `gateway only (no parser)`, or `native: parser <name>` with events read | WARN: the parser read no events, or the gateway rows are incomplete |
| completion claim captured | a final message was read from the end of stdout (or by the parser) | WARN: print the final message last on stdout |

A broken agent shows it at once. One that makes no model call, tries to reach a telemetry host and exits 3:

```
agent check broken on coding-core/t01 (scripted model, nothing spent)
image bench/coding-core-base:1 41c1672b2c99
PASS  image builds                                 prebuilt bench/coding-core-base:1 (41c1672b2c99)
PASS  starts as the unprivileged user              ran as uid 1000 (image USER agent), no capabilities, read-only root
FAIL  model call reached Bifrost with the run key  no model call reached Bifrost
      fix: send every model call to {{gateway.url}}/<route> with {{run.key}} from env; check the base URL variable your SDK reads
WARN  session id sent                              no request to the gateway was seen
      fix: send the header x-bf-session-id: {{run.session}} (and x-bf-session-affinity: off) on every model call; rows still join by the run key without it
FAIL  exited by itself inside the budget           exited with code 3: telemetry failed: <urlopen error [Errno -3] Temporary failure in name resolution>
      fix: exit 0 when a run finished (solved or not); a non-zero exit means the agent crashed
PASS  diff exported                                exported, 0 files changed (expected: the scripted model asks for no edit)
FAIL  no egress outside the gateway                tried to reach 1 host other than the gateway: heimdall-egress.example.com
      fix: switch off telemetry, update checks and downloads at start, bake dependencies into the image, and send model calls only to {{gateway.url}}
PASS  evidence level                               gateway only (no parser): success, steps, tokens, cost and time; add a parser for tool targets
PASS  completion claim captured                    "I could not finish the task.", no success claim, from outcome
result: FAIL
```

### What the scripted model covers

Mode `scripted` answers by the provider's wire format at the fault proxy, after Bifrost has translated the agent's route, so an agent on any route works as long as its provider has a scripted model:

| Provider format | Scripted shapes |
|---|---|
| Anthropic (`anthropic`; and the Anthropic-style `/v1/messages` route of an OpenAI-compatible provider) | Messages, streamed and not; `count_tokens`; models list and model |
| OpenAI (`openai`, `groq`, `mistral`, `xai`, `openrouter`, `fireworks`, `deepseek`, `cerebras`, `perplexity`, `ollama`) | Chat Completions, streamed (with `include_usage`) and not; Responses, streamed and not; `input_tokens`; models list and model |
| Gemini (`gemini`) | none: such a call gets a 501 that says so, and `agent check` refuses an agent whose provider is gemini |

The scripted model answers the first call that offers tools with one call of the first tool (arguments are the zero values of its required fields), then plain text. It never edits code, so the task fails; the check measures the plumbing, not the agent's skill.

## Evidence levels

Every value carries the level it was observed at (DESIGN.md 9.5). With one YAML and no code:

| Level | Source | Values |
|---|---|---|
| `outcome` | Hidden verifier, the engine's clock, the tail of stdout | `verified_success`, `wall_time_s`, `false_completion` |
| `gateway` | Bifrost's rows for the run's key, and the fault proxy's ledger | `steps`, `tokens` (four buckets), `cost_usd`, and the `tool_calls` the model requested, read from the stored upstream responses (Anthropic messages and OpenAI chat and Responses bodies) |
| `native` | Your agent's own stdout, through a parser | Each model turn, each tool call with its target (file, command, URL) and result, retries the agent saw, compactions, the final claim from the last message |

Gateway level is the framework-independence claim: any agent that accepts the gateway base URL gets success, steps, tokens, cost and time with no adapter code. A parser (`schemas.Parser`, one file in `adapters/` and one line in `BuiltinParsers()`) adds the native level; targets let the diagnostics table align what arm A and arm B did on the same task. A parser is pure, skips lines it does not understand and never fails a run. `adapters/opencode.go` is a short example; test it on output captured from the pinned harness version (`adapters/testdata/`).

For framework-neutral structured detail, an agent can print one `bifrost.agent.event` version `1` JSON object per line. Records use `type` values such as `model_response`, `tool_call`, `tool_result`, `retry`, `error`, `compaction` and `claim`; tool records may include `tool`, `target`, `args_sha256`, `result_sha256` and `summary`. The bench stamps run identity and records trace quality. Malformed or oversized captured lines remain in the raw stream and are reported in diagnostics. See the [protocol examples and limits](agent-event-protocol.md).

## Build the image well

- Build on the suite base (`base_arg`), so the agent sees the same Python, pytest and git the grader uses.
- Pin the harness version exactly (`ARG ..._VERSION=x.y.z`). A new release is a new image tag, never a silent upgrade.
- Install as root, then drop to `USER 1000`. Run the binary once at build time (`--version`) so a broken install fails the build.
- Put every switch that keeps the agent off the network in `ENV` or the YAML `env`: no auto-update, no telemetry, no catalogue fetch, no plugin install at start.

## Before a real run

1. `agent check` passes every row (WARN rows are worth fixing: a missing session header or final message costs evidence).
2. `GOWORK=off go test ./adapters/` decodes every shipped `agents/*.yaml` strictly and plans it; for your own project, `agent check` does the same through the planner.
3. Add the agent to an experiment and run it with a small `budget.total_usd` first.
