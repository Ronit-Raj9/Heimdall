# Agent event protocol

Print one JSON object per stdout line to supply a structured trace from any
framework. Set `schema` to `bifrost.agent.event` and `version` to `"1"`.
The collector detects the protocol without a `parser:` setting. You can also
select `parser: bifrost-agent-event` explicitly. A recognized protocol takes
priority over native or heuristic parsing; other stdout records are counted
as unrecognized. Use stderr for ordinary logs.

```jsonl
{"schema":"bifrost.agent.event","version":"1","type":"model_response","timestamp":"2026-01-01T00:00:00Z","id":"response1","summary":"Check the tests"}
{"schema":"bifrost.agent.event","version":"1","type":"tool_call","id":"call1","parent_event_id":"response1","tool_call_id":"tc1","tool":"shell","target":"pytest -q"}
{"schema":"bifrost.agent.event","version":"1","type":"tool_result","parent_event_id":"call1","tool_call_id":"tc1","tool":"shell","status":"success","summary":"12 tests passed"}
{"schema":"bifrost.agent.event","version":"1","type":"claim","claim":{"made":true,"text":"All tests pass.","matched":"all tests pass"}}
```

Supported types are `model_request`, `model_response`, `tool_call`,
`tool_result`, `retry`, `error`, `compaction`, and `claim`. A claim record
requires a `claim` object; `made` reports whether the agent asserts success.
Claims remain agent reports, and the task verifier decides success.

Optional fields include `timestamp` (RFC 3339), `id`, `parent_event_id`,
`tool_call_id`, `tool`, `target`, `args_sha256`, `result_sha256`, `summary`,
`status`, `is_error`, and `tokens` (fresh, cache_read, cache_write, out).
Status accepts `unknown`, `started`, `success`, or `error`. Token counts must
be nonnegative. IDs are local to the captured run and must be unique, including
across sessions. Parent IDs are translated to the bench's event IDs. Reusing
an ID, an invalid parent, or an invalid record adds a malformed-record count.
Missing timestamps use the process start time, or zero when it is unknown;
the parser never invents model or tool timing. Summary and target strings are
clipped at the same limits as native parsers, on rune boundaries.

Every built-in parser reads bounded lines, accepts records up to 1 MiB
excluding the newline, drains larger records, and continues at the next line.
Malformed and unrecognized records are counted. The existing capture cap
(50 MiB per stream by default) still applies. Streaming parsers retain only a
bounded line buffer plus normalized events; legacy extension parsers can keep
using the byte-slice interface, with a 50 MiB input limit. Parsing runs after
agent execution and adds no work to the gateway inference request path.

Trace diagnostics are stored in `run.agent.trace_diagnostics`,
`evidence.agent_trace`, and `canonical.agent_trace`. They include parser name
and version, bytes/records inspected, recognized records, emitted events,
malformed/oversized/unrecognized counts, capture truncation, and a reason.
Sources are `protocol`, `native_parser`, `generic_parser`, `stdout_tail`, or
`none`. Quality is `complete` for a fully parsed supported stream, `partial`
when records were lost or unrecognized, `degraded` for heuristic output or a
parser failure, `claim_only` for the stdout-tail fallback, and `unavailable`
when no output exists. Completeness describes parsing coverage, not independent
proof of an agent's internal work. Gateway and tool-boundary evidence retain
their own authority.

The run comparison screen and `agent check` show this quality and the loss
counts. Older sealed runs have no diagnostics and remain unknown; they are
never rewritten or upgraded based on a parser name alone.
