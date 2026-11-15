# Agent events: how any agent reports its own steps

The gateway, the tool proxy and the two observers see every action that
crosses a model, tool, process or workspace boundary
([steps-and-evidence.md](steps-and-evidence.md)). They cannot see what stays
inside the agent: a plan, a subagent run, a handoff between agents, a memory
read, a framework's internal tool. Only the agent can report those. This page
defines one framework-neutral way to do it, with no code change for an agent
that already emits OpenTelemetry, and the trust model that keeps such
reports from ever moving a trusted number.

Code: `agentevents/` (decoding, capping, redaction, mapping),
`toolproxy/collector.go` (the collector), `environments/eventcollector.go`
(starting it, the agent's environment), `evidence/agentevents.go` (reading
every channel), `evidence/stepsreported.go` (the timeline join),
`metrics/reported.go`, `adapters/genericjsonl.go`. Schema:
[`spec/agent-event.schema.json`](../spec/agent-event.schema.json).

## Channels

An agent can use any of four channels; all four land in the same place.

| Channel | How the agent uses it | Needs |
|---|---|---|
| OTLP | Its OpenTelemetry SDK exports traces to `OTEL_EXPORTER_OTLP_ENDPOINT` (set by the bench) | The collector |
| Native HTTP | `POST $BENCH_EVENTS_URL` with JSON lines (or a JSON array, or one object) | The collector |
| Events file | Append one JSON object per line to `$BENCH_EVENTS_FILE` | Nothing beyond the run |
| Stdout | Print `@@heimdall-event {...}` on its own line | Nothing |

An agent whose whole stdout is native events can name `parser: generic_jsonl`
in its config instead; then every stdout line is an event and the prefix is
optional.

### The collector

The collector is part of the per-run tool proxy (`bench-tp-<run id>`, host
name `toolproxy`, port 9200): `POST /events` and `POST /otlp/v1/traces`. A
run with proxied services reaches it on its tool proxy. A run without them
gets the same container running the collector alone
(`bifrost-bench eventcollector`, no routes), started before its first agent
session on a service network of its own, which the agent joins; that adds a
network, a short-lived probe container and the collector container to such a
run. Remote agents (`remote_http`) get none. It has no route out: it only appends to a ledger in a
volume the agent cannot reach (`/ledger/agent-events.jsonl`) and answers with
counts. After every agent session (and again when service state is exported) the
ledger and its counters are copied into the run folder
(`agent_events.collector.jsonl`, `agent_events.counts.json`).

It never fails a run. A collector that cannot start is recorded as missing
(`agent_events.collector.json`) and the agent keeps the file and stdout
channels. Bounds per run: 5,000 events kept, 8 MiB of kept records, 4 MiB per
request (a larger body is refused with 413 and counted as one dropped event).
Everything over a cap, and every line that does not parse, is counted in the
dropped counter.

### Environment

The bench sets these in the agent container, each only when the agent config
does not set it:

| Variable | Value | When |
|---|---|---|
| `BENCH_EVENTS_FILE` | `/opt/bench/events/events.jsonl` (aux volume, sticky folder writable by the agent user) | Collector feature on |
| `BENCH_EVENTS_URL` | `http://toolproxy:9200/events` | Collector serving |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://toolproxy:9200/otlp` (SDKs add `/v1/traces`) | Collector serving |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | `http://toolproxy:9200/otlp/v1/traces` | Collector serving |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` | Collector serving |
| `OTEL_TRACES_EXPORTER` | `otlp` | Collector serving |
| `OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER` | `none` (the collector takes traces only) | Collector serving |
| `OTEL_SERVICE_NAME` | the agent config id | Collector serving |
| `OTEL_RESOURCE_ATTRIBUTES` | `heimdall.run_id=<run id>` | Collector serving |

Configuration (`deploy/bench.yaml`):

```yaml
events:
  collector: true   # default on; false: no collector, no events file, no OTEL_* variables
```

## OTLP: what is accepted

The receiver takes an OTLP/HTTP `ExportTraceServiceRequest` in both
encodings: protobuf (`Content-Type: application/x-protobuf`, decoded by a
small hand-written reader of the trace message fields, so the bench needs no
protobuf dependency; unknown fields are skipped) and JSON (anything else).
The bench sets `http/protobuf` because it is the one encoding every official
SDK exporter can send: the Python OTLP HTTP exporter sends protobuf only,
Java documents only `grpc` and `http/protobuf`, and the JavaScript and Go
exporters can send either. gRPC is not accepted: an SDK left on gRPC (the
Python and Java default when the protocol variable is unset) needs the
protocol variable, which the bench sets.

How a span becomes a step:

| Span | Reported type |
|---|---|
| `gen_ai.operation.name` = `execute_tool`, OpenInference `openinference.span.kind` = `TOOL`, a span with `gen_ai.tool.name`, Claude Code's `claude_code.tool` | `tool_call` (name from `gen_ai.tool.name` or `tool.name`, id from `gen_ai.tool.call.id` or `tool_call.id`, input from `gen_ai.tool.call.arguments`, `tool.parameters` or `input.value`, output from `gen_ai.tool.call.result` or `output.value`) |
| `invoke_agent`, `create_agent`, kind `AGENT` | `agent`; nested in another agent span: `subagent_start` |
| `chat`, `text_completion`, `generate_content`, `embeddings`, kind `LLM` or `EMBEDDING` | `llm` (a model call the agent reports; the gateway already counts model turns) |
| `retrieval`, kind `RETRIEVER` or `RERANKER` | `retrieval` |
| A name starting with `handoff`, or containing `memory` | `handoff`, `memory_read` or `memory_write` |
| Anything else | `span` |

Span and parent ids become the event's id and parent, so the nesting of a
multi-agent run survives. A span status of ERROR marks the step failed.

### Frameworks: what reaches the collector with no code change

Checked against each project's public documentation on 15 Nov 2026. With
only the standard `OTEL_*` variables the bench sets, few frameworks export
spans on their own; most need a one-line setup or an instrumentation
package. Listed honestly:

| Framework | With the bench's variables alone | What else it needs |
|---|---|---|
| Claude Code (and the Claude Agent SDK through it) | Close: needs two more variables | `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1` in the agent config's `env` (`OTEL_TRACES_EXPORTER=otlp` is set by the bench) |
| Google ADK (Python) | Yes, under `adk web` or `adk api_server` | Programmatic runs call `maybe_set_otel_providers()` |
| LangChain and LangGraph | Through LangSmith's OTel mode | `pip install "langsmith[otel]"`, `LANGSMITH_OTEL_ENABLED=true`, `LANGSMITH_TRACING=true`, `LANGSMITH_OTEL_ONLY=true` |
| Any Python agent instrumented by an OpenTelemetry or OpenLLMetry instrumentation package (OpenAI Agents SDK, LlamaIndex through OpenInference) | Yes, when started under `opentelemetry-instrument <command>` | The instrumentation package installed in the agent image |
| OpenAI Agents SDK (Python) | No: its built-in tracing goes to OpenAI | An instrumentor (`opentelemetry-instrumentation-openai-agents` or OpenInference) |
| Pydantic AI | No | A TracerProvider and `Agent.instrument_all()`; endpoint variables are read from the environment |
| CrewAI | No: its own telemetry stays on a private provider | A third-party instrumentation |
| smolagents | No | `SmolagentsInstrumentor().instrument()` |
| Vercel AI SDK | No | Register an OTel SDK and telemetry in code |
| Strands Agents | No | `StrandsTelemetry().setup_otlp_exporter()`; endpoint from the environment |
| Semantic Kernel, AutoGen, AG2, Mastra | No | A TracerProvider or exporter set up in code |
| Codex CLI | No | `[otel]` in `~/.codex/config.toml`; whether it reads `OTEL_*` is not documented |
| Gemini CLI | Partly | `GEMINI_TELEMETRY_ENABLED=true`; it reads the endpoint variable but not the protocol variable, and its `http` option sends JSON, which the collector accepts |

Tested with a real SDK: `TestAgentEventsOTelSDKDocker` (evidence, tag
integration) runs an agent that only uses the OpenTelemetry API under
`opentelemetry-instrument` (Python SDK 1.27.0, `opentelemetry-distro`
0.48b0, `opentelemetry-exporter-otlp-proto-http` 1.27.0), with only the
variables above, against the collector running alone; its three GenAI spans
arrive over http/protobuf and land in the timeline with their nesting. The
JSON and protobuf decoders are also unit-tested against hand-built requests
(`agentevents/agentevents_test.go`). No JavaScript, Go or Java SDK was
tried.

## The native protocol

One JSON object per event ([schema](../spec/agent-event.schema.json)):

```json
{"type": "tool_call", "id": "t1", "parent_id": "a1", "name": "search",
 "tool_call_id": "call_9", "input": {"q": "rate limit"}, "output": "3 hits",
 "status": "ok", "start_time": "2026-11-15T12:00:03.100Z",
 "end_time": "2026-11-15T12:00:03.400Z", "attributes": {"k": "v"}}
```

Types: `plan`, `thought_summary`, `tool_call`, `tool_result`,
`subagent_start`, `subagent_end`, `handoff`, `memory_read`, `memory_write`,
`retrieval`, `message`, `error`, `custom` (an unknown type is kept as
`custom`). Only `type` is required. Times are RFC 3339 or Unix milliseconds;
a missing start time is the time the bench received the event. `parent_id`
names the enclosing event for nesting.

Three lines with the helpers (`clients/python/heimdall/events.py`, standard
library only, and `clients/typescript/src/events.ts`); both do nothing when
the variables are unset:

```python
from heimdall import events
plan = events.emit("plan", output="read the failing test, then fix the limiter")
with events.step("tool_call", "search", parent_id=plan, input={"q": "limiter"}) as s:
    s.output = run_search("limiter")
```

The agent templates (`agents/templates/`) show the same in a few inline
lines, and examples are in `clients/examples/agent_events.py` and
`agent-events.ts`.

## Trust model

Every reported byte is the agent's own word.

- **Inert and capped.** Each field is capped (ids 128 bytes, names 256,
  input and output 2 KiB, 32 attributes), control characters are removed,
  and secrets are redacted: the bench's redaction rules, bare API key shapes
  and the run's gateway key. The UI renders every string as text.
- **Separate.** Reported events carry source and origin `agent_reported`,
  evidence `native`, strength `partial`, and the common event type
  `agent_step` whatever the agent called them (the reported type is in
  `reported_type`). Metrics that read tool calls, tool results or errors
  from events select by type, so a report can never pose as one.
- **Never counted as trusted.** Reported rows in the timeline are never
  `counted`, so they never add to `agent_steps`. They never change a
  requested call's execution state, a tool proof class, a gate or an
  outcome.
- **Corroboration, not promotion.** A reported step that a trusted witness
  also saw links to that witness's row (`witness_of`) and is marked
  `corroborated`; the trusted row is what counts. A reported step with no
  trusted witness is `agent_reported_only`. A trusted witness with no report
  is normal. When they disagree (the agent says a call succeeded, the tool
  proxy ledger says it failed) the row is marked `conflicts` with the reason.

## Timeline rows

Each reported event becomes one `agent_event` row of source `agent_reported`
in `steps.jsonl`, with `event_type`, `channel`, `report_id`, `parent_seq` (the
reported parent's row) and `corroboration`. Its turn is the last model turn
whose response ended before it. Matching, within one second before the
step's start to one second after its end:

| Reported | Trusted witness |
|---|---|
| `tool_call` or `tool_result` | A tool proxy ledger row whose tool name fits (the same rule as for requested calls), else a command root whose command line holds the reported input, else the gateway's requested call with the same tool call id when a trusted witness saw that call run |
| `llm` span | The gateway model turn that started in the window |
| Anything else | None can exist: `agent_reported_only` |

A reported tool call id that the complete gateway log never had is recorded
as a conflict. A run with no reported events has exactly the timeline it had
before.

## Metrics

| Metric | Counts | Unknown when |
|---|---|---|
| `reported_steps` | Reported internal actions: plans, subagent runs, handoffs, memory reads and writes, retrievals and distinct tool calls | The agent reported nothing |
| `reported_tool_calls` | Distinct reported tool calls (a `tool_result` with no reported call counts once) | The agent reported nothing |
| `subagents_spawned` | `subagent_start` events and agent spans nested in another agent span | The agent reported nothing |
| `events_dropped` | Reported events refused at a cap or unparseable | No collector ran and nothing was reported |

All four are native evidence, partial strength, origin `agent_reported`.
**Unknown, never zero, when nothing was reported:** an agent that did
nothing internal and an agent that is not instrumented look the same from
outside, so a run without reports cannot be read as zero plans or zero
subagents. `agent_steps` is unchanged by reports: a corroborated action is
already counted through its trusted witness, and an uncorroborated one has
no trusted witness to count.

## Agent check and the agents page

`agent check` has an `agent_events` row: PASS with the count, channels and
kinds when the agent reported events (and the collector address when it
served), WARN "agent reported nothing (gateway and observer evidence only)"
when it reported nothing or events were dropped, SKIP when the collector is
switched off. It never fails. The agents page shows "reports events
(native)" for a `generic_jsonl` agent; what a run reported over OTLP shows in
`agent check` and on the run page.

## Files

| File | Contents |
|---|---|
| `agent_events.jsonl` | The run's reported events after normalization, one `Event` per line |
| `agent_events.json` | `AgentEventStatus`: collector state, received, kept, dropped, per channel and per type |
| `agent_events.collector.jsonl`, `agent_events.counts.json` | The collector's raw ledger and counters, copied from its volume |
| `agent_events.collector.json` | Whether the collector ran (`on`, or `missing` with a reason) |

`GET /api/bench/runs/{id}/steps` carries the status as `agent_events` beside
the timeline (absent when no collector ran and nothing was reported).

## Limits

- An agent that emits neither OTLP nor native events exposes only what the
  gateway, the tool proxy and the observers can see.
- Reports are never verified as true. A corroborated report is consistent
  with a trusted record; an uncorroborated one may be invented.
- gRPC export is not accepted; an SDK must use OTLP over HTTP.
- Span mapping covers the GenAI and OpenInference conventions and a few name
  patterns; a framework with its own attribute names shows its spans as
  `span`.
- Remote agents (`remote_http`) report through their own channel
  ([remote-execution.md](remote-execution.md)), which uses the same
  normalizer (`agentevents.FromNativeJSONL`, `agentevents.FromOTLP`).
