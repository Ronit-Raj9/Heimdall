# Tasks from traces

A suite should grow from what actually goes wrong. `bifrost-bench task
from-trace` turns one recorded production session, usually a failure, into a
task draft in an existing suite, in one command. It reads a local file and
never the network: an OTLP/JSON trace with the OpenTelemetry GenAI
conventions, a Langfuse trace export, or a plain chat transcript. It writes
an ordinary scenario task (qa, tool-use, mcp or memory) with the user's
request as the instruction, the agent's answer or tool calls as a starting
point for the checks, the observed tool results as a replayable fixture, and
the whole session as a transcript for the person who finishes it.

A trace shows what happened, not what was right. So every judgement the
draft holds (the expected answer, the final state the checks want) carries an
EDIT ME marker that `task validate` refuses, and the draft is never admitted
on its own: a person confirms it, then `task admit` proves it like any other
task.

The code is `bench/traceimport` (parsers, redaction, the draft writer and the
replay server) and `cmd/bifrost-bench/taskfromtrace.go`. The draft is laid
out by the same machinery as `task init -kind` (`scaffold.InitScenarioTask`),
so it has the suite's shape: its services, controls and verifier.

## Walk-through

The example is a support agent that told a customer their refund was issued
while the refund tool had timed out. You need a project with a scenario
suite (`bifrost-bench init`, then `bifrost-bench suite init support -template
mcp`); every command below runs from the project folder.

### 1. Export the failing session

Export one session to a file. Any of these works:

- **OpenTelemetry.** The span JSON of the trace as OTLP/HTTP sends it (an
  `ExportTraceServiceRequest`, `{"resourceSpans": [...]}`), or the
  collector's `file` exporter output (one such object per line). Turn on
  message content capture in your instrumentation (for the Python
  instrumentations, `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true`):
  without it the spans hold no messages and there is nothing to draft from.
  A run exported by `bifrost-bench export otlp` reads back too.
- **Langfuse.** The trace with its observations, from the public API:
  `curl -u "$LANGFUSE_PUBLIC_KEY:$LANGFUSE_SECRET_KEY"
  "$LANGFUSE_HOST/api/public/traces/<trace id>" > failing-session.json`, or
  the newer Observations API filtered to the trace:
  `.../api/public/v2/observations?traceId=<trace id>&fields=core,basic,io`
  (one page holds up to 1,000 observations).
- **A chat transcript.** The messages your application sent and received,
  in the OpenAI chat format: `{"id": "...", "messages": [...]}`, with
  `tool_calls` on assistant messages and `role: tool` results.

The file stays on your machine. `task from-trace` reads it and nothing else.

### 2. See the sessions in the file

```console
$ bifrost-bench task from-trace -list failing-session.json
SESSION                           TURNS  TOOLS  OUTCOME                FIRST REQUEST
5b8efff798038103d269b633813fc60c  3      2      error (1 tool errors)  Please refund order A-1001, the tent arrived torn....
0af7651916cd43dd8448eb211c80319c  2      0      ok                     What is the return window for tents?
otlp format, 2 sessions
```

A session is one trace (OTLP, Langfuse) or one conversation (chat). The
outcome is what the trace records: `error` when a span or observation has an
error status or the transcript says it failed, `ok` only on an explicit
success signal (a root span with status OK, `"status": "success"`), else
`unknown`. The listing is redacted like everything else.

### 3. Draft the task

```console
$ bifrost-bench task from-trace -suite support -id refund-timeout -trace failing-session.json \
    -session 5b8efff798038103d269b633813fc60c
wrote draft benchmarks/support/tasks/refund-timeout (template mcp, shape of restock)
  session 5b8efff798038103d269b633813fc60c: 3 turns, 2 tool calls, outcome error (1 tool errors)
  added refund-timeout to the tasks list of suite.yaml
  tool service tools replays the 2 observed calls (replay/, tools.observed.json); its calls.json is the state the checks read
  redacted: 1 email, 2 header value, 1 long digit run, 1 phone number, 1 secret value

The draft shows what happened in the trace, not what was right. next:
  1. edit benchmarks/support/tasks/refund-timeout/instruction.md and delete its EDIT ME lines
  2. edit benchmarks/support/tasks/refund-timeout/solution/reference.sh and delete its EDIT ME lines
  3. edit benchmarks/support/tasks/refund-timeout/verifier/tests/checks.py and delete its EDIT ME lines
  4. edit benchmarks/support/tasks/refund-timeout/verifier/requirements.md and delete its EDIT ME lines
  5. review the rest of benchmarks/support/tasks/refund-timeout (from-trace.md has the whole session)
  6. bifrost-bench task validate support/refund-timeout
  7. bifrost-bench task admit support/refund-timeout
```

Flags: `-kind qa|tool-use|mcp|memory` (default: the suite's template; it
must match it), `-session <id>` when the file holds several sessions,
`-format otlp|langfuse|chat` to skip detection, `-redact=false` to keep the
text as recorded (not recommended), `-replay=false` to keep the suite's own
tool service, `-license`, `-force` to replace an existing task folder,
`-max-bytes` (default 32 MiB).

### 4. What the draft holds

| File | Kinds | What it is | EDIT ME |
|---|---|---|---|
| `instruction.md` (memory: each session's) | all | The first user request as sent, after redaction. Memory: the first request to the first session, the last request to the last session | yes, refused by `task validate` |
| `verifier/answer.yaml` | qa, memory | The agent's final answer as `expected`. When the session failed or gave no answer, `expected` is a placeholder and the recorded answer becomes a `wrong_answers` entry | yes, one per judgement line, refused |
| `solution/answer.txt` | qa, memory | The recorded answer; empty when the session failed, so admission fails until you write the right one | no |
| `verifier/tests/checks.py` | tool-use, mcp | No checks yet. With the replay server, `STATE` is its `calls.json` and there is one commented example check per observed tool | yes, refused |
| `solution/reference.sh` | tool-use, mcp | The observed calls in order, each with its recorded result as a comment | yes (admission, not validation, proves it) |
| `controls/mutant-observed.sh` | tool-use, mcp, when the session failed | The recorded calls exactly as the session made them, carrying on past tool errors, listed under `controls.mutants`: admission requires the verifier to fail it, which proves the checks catch the failure that happened. Written for the replay server and for MCP services | no |
| `tools.observed.json` | tool-use, mcp | Every observed call: name, arguments, result or error, time | no |
| `replay/server.py`, `replay/Dockerfile` | tool-use, mcp | The replay tool server (below), in place of the copied service | no |
| `verifier/requirements.md` | when the shape has one | What a correct result holds, and why: for you to write | yes |
| `from-trace.md` | all | The whole recorded session: source, redaction counts, outcome hints, every turn and tool call | no |
| `task.yaml` | all | The copied shape with the new id, a title from the request, and provenance `created_by: task from-trace`, `source: trace <file> (<format>) sha256:<hash of the file>, trace id <id>` | no |

`tools.observed.json` and `from-trace.md` sit beside `task.yaml`, outside
the workspace: the agent never sees them.

### 5. Edit

Decide what the right outcome was. The trace is evidence of what happened;
it is not the answer key.

- **Instruction.** Rewrite the request as a user would send it, with what
  the agent needs and how to tell when it is done. Say nothing about how the
  result is checked. Delete the comment and the EDIT ME line.
- **qa and memory.** In `answer.yaml`, write the correct answer (or keep the
  recorded one if it was right), pick `match`, add plausible wrong answers,
  and delete every EDIT ME line. Put the same correct answer in
  `solution/answer.txt`.
- **tool-use and mcp.** Write the checks: with the replay server, over the
  calls the agent made (`{"name": "refund_tried", "path": "calls", "where":
  {"tool": "issue_refund"}, "length": 1}`); with your own service, over its
  exported state. Make `reference.sh` do what a correct agent does. In the
  example a correct agent tells the customer the refund failed, so the right
  checks are about which calls it makes and the reference must not stop on
  the replayed timeout. Keep `controls/mutant-observed.sh` when the session
  really was wrong: your checks must fail it.
- **Review the rest.** The controls, the workspace (`repo/`) and any seed
  data come from the task whose shape was copied. Replace what does not fit.

### 6. Validate

```console
$ bifrost-bench task validate support/refund-timeout
error   tasks/refund-timeout/instruction.md: line 1: still holds the <!-- EDIT ME --> marker; rewrite the draft and delete the marker
warning tasks/refund-timeout/task.yaml: controls.mutants: empty; a mutant that fails the hidden tests shows they catch a near miss
error   tasks/refund-timeout/verifier/tests/checks.py: line 1: still holds an EDIT ME line; finish the file and delete the line
support/refund-timeout: 2 errors, 1 warning
```

It passes once every EDIT ME line is gone and the rest is valid. The secret
scan admission uses runs here too; a redacted draft passes it.

### 7. Admit

`bifrost-bench task admit support/refund-timeout` builds the replay image
from `replay/` when it is missing, runs the reference three times (it must
pass every time), the empty control and the mutants (they must fail), and
records the task in `suite.lock`. From then on the task runs in experiments
like any other.

Checked on 15 Nov 2026 in Docker: a tool-use draft from a failing chat
transcript (the agent adjusted the wrong item), with the instruction, the
checks over `calls.json` and the reference written by hand, was admitted
through the tool proxy and the replay server: the reference passed 3 of 3,
and the empty control, the observed-calls mutant, the exploit, noop and
refuse all failed.

## Formats

| Format | Read | Verified against |
|---|---|---|
| `otlp` | `resourceSpans` (also `resource_spans` and the older `instrumentationLibrarySpans`), one object or one per line, grouped by `traceId`. `execute_tool` spans (or any span with `gen_ai.tool.name`): `gen_ai.tool.call.id`, `gen_ai.tool.call.arguments`, `gen_ai.tool.call.result`, the span status. `gen_ai.input.messages` and `gen_ai.output.messages` (role and parts of type `text`, `tool_call`, `tool_call_response`, reading `response` or `result`). The older indexed `gen_ai.prompt.<i>.*` and `gen_ai.completion.<i>.*` attributes, including `tool_calls.<j>.*`. Span events `gen_ai.<role>.message` and `gen_ai.choice`. `gen_ai.response.model` or `gen_ai.request.model`. Status ERROR, its message or `error.type` | The OpenTelemetry GenAI semantic conventions (message JSON schema with `ToolCallResponsePart.response`; their spans page still shows `result`, so both are read); a round trip of the bench's own exporter types. The event and indexed forms follow common instrumentations and are best effort |
| `langfuse` | A trace with `observations` (`GET /api/public/traces/{id}`), a list of them or a `{"data": [...]}` page, and observation rows with `traceId`, `type`, `startTime` (Observations API v2), grouped by trace. `GENERATION` input and output, `TOOL` observations (name, input as arguments, output as result, `metadata.tool_call_id`), level `ERROR` with `statusMessage`, trace `input`, `output` and `scores` | Langfuse's public API reference for the trace and observation fields and types. Input and output are free JSON whose shape depends on the integration: message lists, message objects, `choices`, plain strings and objects with one obvious text field are read. The UI's table export has no documented column list and is **unverified**: it is read only when its rows look like the API's traces |
| `chat` | `{"messages": [...]}` with optional `id`, `model`, `status`, `error`; a bare message array; a list or `data` page of conversations; one conversation or one message per line. OpenAI `tool_calls` and `function_call`, `role: tool` and `role: function` results, content parts, and Anthropic-style `tool_use` and `tool_result` blocks | The OpenAI chat format |

Several LLM spans or generations each carry the history so far; a message is
added once, and a repeat that is new in a longer history is added again. A
tool result is matched to its call by id, else by name. A tool result that
is an MCP result with `isError`, an object with an `error` field, or text
starting with `Error:` counts as a failed call.

## Privacy

Redaction is on by default and runs before anything is written or printed.
It scrubs, from every turn, tool argument, result, error, outcome hint and
the file name: emails, phone numbers, digit runs of nine or more (account and
card numbers), bearer and basic credentials, JWTs, API keys (`sk-`, AWS,
GitHub, Slack, Google, GitLab, Hugging Face and Langfuse key shapes), private
key blocks, values assigned to names ending in token, secret, password or
`_key`, and the values of headers and JSON keys named authorization, cookie,
set-cookie, api-key or x-api-key. Each value becomes `[REDACTED <kind>]`. The
command reports what it scrubbed by count, never by value. Trace and tool
call ids keep their digits so the session can still be found. After writing,
the command runs the admission secret scan on the draft and prints any
finding.

Redaction is pattern based. It does not catch names, postal addresses or
free-text secrets, so read `from-trace.md` before you commit the task.

## Size and malformed input

A file over `-max-bytes` (default 32 MiB) is refused before it is read in
full. At most 100,000 JSON values (lines), 10,000 sessions, 5,000 turns and
1,000 tool calls per session are kept; one text field is cut at 64 KiB with
a note of how much was dropped, and the counts are in `from-trace.md`.
Invalid JSON, text that is not UTF-8, a file of the wrong format and a
session with no user request each end with an error that says what was
wrong, and a draft that fails half-way is removed with `suite.yaml` restored.

## The replay server

For tool-use and mcp suites whose tool service is seeded through `files:`
(both templates are), the draft swaps the copied service for `tools`, built
from `replay/` in the task folder: a standard-library Python server seeded
with `tools.observed.json`. It answers MCP (`tools/list`, `tools/call` on
`/mcp`) and plain HTTP (`GET /` lists the tools, `POST /tools/<name>` with
the arguments). A call gets the first unused recorded call of that tool with
equal arguments, else the first unused one of that tool, else the last one
again; a recorded error is replayed as an error, and a tool the session
never called is unknown. Every call the agent makes is written to
`calls.json` (tool, arguments, which recorded call answered, whether the
arguments matched), which the bench exports and the checks read. The tool
proxy sits in front of it as for any service, so tool faults and the ledger
work unchanged. `-replay=false` keeps the suite's own service; the observed
calls are then only a fixture, and a tool-use reference script refuses to
run until you map each tool to the service's HTTP route.

## Limits

- **Drafts need a human.** The command never decides what was right and
  never admits. An unedited draft fails `task validate`; a draft whose
  markers were deleted without thought can still pass validation, which is
  why admission runs the reference and the controls.
- **Tool results are replayed, not live.** The replay server returns what
  production returned, in order. It keeps no state: an agent that calls a
  tool in another order or with other arguments gets the nearest recorded
  answer, not what a real service would say. Use your own tool service
  (`-replay=false`) to test against live behaviour.
- **Multimodal content is skipped.** Images, audio and files in messages are
  counted and left out; the instruction is text only.
- **The outcome is a hint.** Most traces do not say whether the user was
  helped. Scores are listed as hints and never decide the status.
- **One session per draft.** A memory draft splits one recorded
  conversation across the suite's sessions; a trace of a real multi-session
  conversation still needs its sessions arranged by hand.
- **Formats.** The Langfuse UI table export and the event and indexed OTLP
  forms are best effort (see the table). A format we cannot read ends with
  an error, never a half-filled draft. The importer has its own OTLP decoder
  and does not share types with `bench/traceexport`; a test reads a trace
  built with the exporter's types.
