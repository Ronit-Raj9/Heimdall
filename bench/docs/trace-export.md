# Trace export (OpenTelemetry)

Heimdall can export any sealed run as an OpenTelemetry trace in OTLP/JSON, so a
team can read its runs next to the rest of its telemetry: in Maxim, Langfuse,
Jaeger, Grafana Tempo, or anything behind an OpenTelemetry Collector. One run is
one trace. The run is the root span; its sessions, model requests, tool calls,
grade and gates are child spans; injected faults are span events.

The export reads only sealed evidence. It verifies the run's seal first and
refuses a run that is not sealed or whose folder no longer matches its seal.
Nothing is written inside the run folder.

## Getting a trace

### Command line

```sh
# one run, to a file
bifrost-bench export otlp -exp e_smoke_sdoj -run r_ioap8m36je -out trace.json

# every sealed run of an experiment, one file per run (a folder, or a path ending in /)
bifrost-bench export otlp -exp e_smoke_sdoj -out traces/

# every sealed run as one document on stdout
bifrost-bench export otlp -exp e_smoke_sdoj > traces.json

# include tool arguments and results (redacted and capped)
bifrost-bench export otlp -exp e_smoke_sdoj -run r_ioap8m36je -payloads -out trace.json

# post to a collector instead of (or as well as, with -out) writing a file
bifrost-bench export otlp -exp e_smoke_sdoj -endpoint http://localhost:4318
```

`-runs <folder>` reads another runs root (for example `testdata/recorded`)
instead of the config's `runs_root`. When exporting a whole experiment, runs
that are not sealed yet are skipped with a note; a run whose seal does not
verify stops the export before anything is written.

### API and UI

`GET /api/bench/runs/{id}/otlp` returns the trace as a download named
`<run id>.otlp.json` (`?payloads=true` adds the redacted, capped payloads). It
answers 409 for a run that is not sealed or does not match its seal. The run
comparison page has a **Download trace (OTLP)** link in each arm's evidence
card.

## Sending to a backend

`-endpoint` posts each run as one OTLP/HTTP request with
`Content-Type: application/json`. An endpoint with no path gets the standard
`/v1/traces`; an endpoint with a path is used as given. Headers come from the
environment variable named by `-header-env`, in the
`OTEL_EXPORTER_OTLP_HEADERS` format: comma-separated `key=value` pairs, values
percent-encoded (a space is `%20`). Header values are never printed. Posting is
off unless `-endpoint` is set.

| Backend | `-endpoint` | Headers (in the `-header-env` variable) |
|---|---|---|
| Jaeger (v2, or v1.35+ with OTLP on) | `http://localhost:4318` | none |
| OpenTelemetry Collector | `http://<collector>:4318` | whatever your collector requires |
| Langfuse Cloud (EU) | `https://cloud.langfuse.com/api/public/otel/v1/traces` | `Authorization=Basic%20<base64 of pk-lf-...:sk-lf-...>` |
| Langfuse Cloud (US) | `https://us.cloud.langfuse.com/api/public/otel/v1/traces` | as above |
| Langfuse self-hosted (v3.22+) | `http://<host>:3000/api/public/otel/v1/traces` | as above |
| Maxim | `https://api.getmaxim.ai/v1/otel` | `x-maxim-api-key=<api key>,x-maxim-repo-id=<log repository id>` |

Examples:

```sh
# Jaeger all-in-one, UI on :16686
docker run --rm -d --name jaeger -p 16686:16686 -p 4318:4318 jaegertracing/jaeger:2.11.0
bifrost-bench export otlp -exp e_smoke_sdoj -endpoint http://localhost:4318

# Langfuse
export LANGFUSE_OTLP_HEADERS="Authorization=Basic%20$(printf '%s' "$LANGFUSE_PUBLIC_KEY:$LANGFUSE_SECRET_KEY" | base64 | tr -d '\n')"
bifrost-bench export otlp -exp e_smoke_sdoj \
  -endpoint https://cloud.langfuse.com/api/public/otel/v1/traces -header-env LANGFUSE_OTLP_HEADERS

# Maxim
export MAXIM_OTLP_HEADERS="x-maxim-api-key=$MAXIM_API_KEY,x-maxim-repo-id=$MAXIM_REPO_ID"
bifrost-bench export otlp -exp e_smoke_sdoj -endpoint https://api.getmaxim.ai/v1/otel -header-env MAXIM_OTLP_HEADERS
```

Jaeger 2.11.0 was checked by hand on 15 Nov 2026: it accepted the eight runs of
`testdata/recorded/e_smoke_sdoj` and a 32-request run of
`e_pilot_mixed_nc93`, and served them back under service `heimdall` with their
parent links and integer token counts intact. Maxim and Langfuse were not called (no keys are
used in this repository). Their endpoints and header names are the backends'
documented ones as of 15 Nov 2026 (Langfuse: OTLP over HTTP with JSON or protobuf, no gRPC, Basic auth from
the project keys; Maxim: `x-maxim-api-key` and `x-maxim-repo-id`, JSON or
protobuf). If a backend rejects the JSON body, send it to an OpenTelemetry
Collector and let the collector's `otlphttp` exporter forward it as protobuf.

## What each span means

All spans share the run's trace id. Ids are derived, not random: the trace id
is the first 16 bytes of `sha256("heimdall/trace/" + run id)`, and each span id
the first 8 bytes of `sha256("heimdall/span/" + run id + "/" + key)`, where the
key names the record the span comes from. Exporting a run twice gives the same
bytes, and a re-export to a backend lands on the same trace.

| Span name | Kind | Parent | Comes from | Times |
|---|---|---|---|---|
| `heimdall.run <task>` | internal | none | `run.json`, slot, seal, `metrics.json` | run start to seal, widened to cover every child |
| `invoke_agent <agent config>` | internal | run | `Evidence.Sessions`, one per session; else the single agent execution | session start and end |
| `chat <model>` | client | the session whose time holds it, else run | gateway rows of one logical model request (a fallback row joins the row it fell back from) | first request to last response |
| `tool_call <tool>` | internal | the `chat` span that asked for it | a tool call the model requested (`Evidence.RequestedTools`, else the gateway rows) | from the response that asked for it to the first later request that carried its result back; zero length if the result never came back |
| `execute_tool <tool>` | internal | its session, else run | the tool proxy ledger (`Evidence.ToolCalls`): what actually ran | call start plus latency |
| `command <program>` | internal | the `chat` span of the row's turn; turn 0 (before the first response), or a turn with no `chat` span, under its session span, else run | a `command` row of the step timeline (`Evidence.Steps`, the process observer); the program is the first word of the row's summary without its directory | process start plus its recorded duration; zero length when it has none |
| `file_change <op>` | internal | as for `command` | a `file_change` row of the step timeline (the workspace observer) | zero length, at the time the observer saw it |
| `heimdall.grade` | internal | run | `Evidence.Grade` | the grade stage (`transitions.jsonl`) |
| `heimdall.gate <gate>` | internal | run | `Evidence.Gates`, one per gate | the gate stage |

Span events:

| Event | On | Meaning |
|---|---|---|
| `heimdall.fault` | the `chat` span it hit, else the run | a fault the fault proxy or a tool shim injected (`heimdall.fault.source`, `heimdall.fault.summary`) |
| `heimdall.fault` | `execute_tool` | the tool proxy answered with an injected fault (`heimdall.fault.mode`: error, timeout, poison) |
| `heimdall.retry` | `chat` | one physical retry: attempt number, the status and reason of the attempt before it |
| `heimdall.fallback` | `chat` | the gateway served the request from a fallback: index, requested and served model |

Status: a `chat` span whose last row failed, an `execute_tool` span with a tool
error, a session that crashed or timed out, a fired veto or invalidate gate, an
invalid evaluator, and a run whose outcome is `infra_error`, `aborted_infra`,
`invalid_evaluator` or `integrity_violation` have status ERROR. Nothing is
marked OK. A `command` span with a non-zero exit code has status ERROR too.

Timeline spans (`command`, `file_change`) come from the run's step timeline
([steps-and-evidence.md](steps-and-evidence.md)), one per row, including the
rows that witness a tool call and are not counted again by `agent_steps`. A
file change happens after the turn's response, outside the `chat` span's own
window, so it is a child span rather than an event of that span. Their span id
key is `step:<seq>`. A run without a timeline adds none.

### Attributes

Model and tool spans use the OpenTelemetry GenAI semantic conventions. The names
were checked on 15 Nov 2026 against
`open-telemetry/semantic-conventions-genai` (`docs/gen-ai/client-inference.md`,
`docs/gen-ai/execute-tool.md`), where the GenAI conventions now live.

| Span | GenAI attributes |
|---|---|
| `chat` | `gen_ai.operation.name` (`chat`), `gen_ai.provider.name`, `gen_ai.system` (deprecated alias of the provider, kept for older backends), `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.response.id`, `gen_ai.request.stream`, `gen_ai.conversation.id` (the run's gateway session), `gen_ai.usage.input_tokens` (fresh plus cache read plus cache write, as the convention asks), `gen_ai.usage.output_tokens`, `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.cache_write.input_tokens`, `error.type` (the HTTP status, or `_OTHER`) |
| `execute_tool` | `gen_ai.operation.name` (`execute_tool`), `gen_ai.tool.name`, `gen_ai.tool.type` (`function`, or `extension` for an external API), `gen_ai.tool.call.arguments` and `gen_ai.tool.call.result` (opt-in in the convention; only with `-payloads`), `error.type` |
| `tool_call` | `gen_ai.tool.name`, `gen_ai.tool.call.id`, `gen_ai.tool.type` |
| `invoke_agent` | `gen_ai.operation.name` (`invoke_agent`), `gen_ai.agent.name` (the agent config id), `gen_ai.conversation.id` |

Bifrost provider names map to the convention's values where they differ
(`bedrock` to `aws.bedrock`, `vertex` to `gcp.vertex_ai`, `gemini` to
`gcp.gemini`, `azure` to `azure.ai.openai`, `mistral` to `mistral_ai`, `xai` to
`x_ai`); the raw name is kept in `heimdall.gateway.provider`.

Everything else is under `heimdall.*`:

- Run: `heimdall.run.id`, `heimdall.experiment`, `heimdall.slot`,
  `heimdall.pair`, `heimdall.suite`, `heimdall.task`, `heimdall.task.version`,
  `heimdall.task.hash`, `heimdall.arm`, `heimdall.condition`, `heimdall.trial`,
  `heimdall.run.attempt`, `heimdall.agent.config_id`,
  `heimdall.agent.config_hash`, `heimdall.agent.adapter`,
  `heimdall.agent.version`, `heimdall.outcome`, `heimdall.verified_success`,
  `heimdall.cost_usd`, `heimdall.manifest_hash`, `heimdall.suite_lock_hash`,
  `heimdall.run_input_hash`, `heimdall.canonical_hash`, `heimdall.seal_hash`,
  `heimdall.claim.made`, `heimdall.answer.source`, `heimdall.answer.sha256`,
  `heimdall.gateway.log_complete`, and `heimdall.metric.<name>` for every
  observed metric.
- `chat`: `heimdall.logical_step`, `heimdall.logical_request_id`,
  `heimdall.gateway.request_ids`, `heimdall.attempts`, `heimdall.retries`,
  `heimdall.fallbacks` (from the canonical evidence fields `LogicalSteps`,
  `Attempts`, `Retries` and `Fallbacks` when the run has them; older runs get
  the same counts from the gateway rows by the same rule), `heimdall.cost_usd`
  (from the bench's own cost ledger), `heimdall.fault_injected`.
- `execute_tool`: `heimdall.tool.seq`, `heimdall.tool.server`,
  `heimdall.tool.protocol`, `heimdall.tool.args_sha256`,
  `heimdall.tool.result_sha256`, `heimdall.tool.args_bytes` and
  `heimdall.tool.result_bytes` (only when the ledger kept the whole value;
  otherwise `heimdall.tool.<kind>_clipped`), `http.response.status_code`,
  `heimdall.tool.fault`, and `heimdall.tool.replayed`, `unmatched`, `blocked`,
  `undelivered` when true.
- `tool_call`: `heimdall.tool.args_sha256`, `heimdall.tool.result_returned`,
  `heimdall.tool.executed` (only when an execution source recorded it).
- `command` and `file_change`: `heimdall.step.kind`, `heimdall.step.seq`,
  `heimdall.step.turn`, `heimdall.step.source`, `heimdall.step.proof`,
  `heimdall.step.summary` (redacted, capped), `heimdall.step.counted` (when
  true), `heimdall.step.witness_of` (when set), `heimdall.step.conflict` (when
  set), `heimdall.session.index` (multi-session runs). `command` adds
  `process.pid`, `process.parent_pid` and `process.exit.code` (each when
  known); `file_change` adds `heimdall.file.op` and `file.path`.
- Grade and gates: `heimdall.grade.*` (passed, invalid evaluator, test counts,
  reason, mode, reward, `dimension.<name>`, failed tests) and
  `heimdall.gate.*` (name, version, effect, fired, detail, evidence).

The resource carries `service.name` = `heimdall`, `service.version` (the bench
version), `heimdall.experiment` and `heimdall.run.id`. The instrumentation scope
is `github.com/maximhq/bifrost/bench/traceexport`, version 1.0.0; the version
changes when a span or attribute changes meaning.

## Secrets and payloads

- By default no argument, result, answer or claim text is exported: only
  sha256 hashes and sizes.
- `-payloads` (or `?payloads=true`) adds tool arguments and results, the answer
  and the claim text, each capped at 2048 bytes.
- Every string in the trace is redacted with the bench's own redaction
  (`utils.RedactSecrets`, the one evidence applies to gateway errors), plus bare
  bearer and basic tokens, `sk-` and `pk-` shaped keys, and fault nonces (a
  probe's receipt stays in the run folder). Raw gateway bodies are never read.
  Free text such as error messages and gate details is capped at 500 bytes.

## Limits

- A value the run did not observe is left out, never written as zero: a model
  request without usage has no token attributes, a request without a priced
  cost row has no cost. An injected fault is priced as an observed zero (the
  provider never billed it), so its cost is 0.
- Times come from different clocks (the bench, the gateway, the fault proxy,
  the tool proxy) and can disagree by milliseconds. A fault event is placed on
  the model request whose time holds it, or the nearest within 50 ms.
- Model spans need the gateway rows (`Evidence.Requests`). A run with no rows
  (an agent with external model traffic) has no `chat` or `tool_call` spans.
- Events parsed from the agent's own stream are not exported; their times are
  the agent's and are often unreliable.
- One POST per run, no retries and no compression. A large run (hundreds of
  model requests) is a few hundred kilobytes.
- Export only: the trace import side (`bench/traceimport`) reads the same
  OTLP/JSON types from `bench/traceexport/otlp.go`.
