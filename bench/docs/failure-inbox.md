# Failure inbox

Strong agent teams all run the same loop by hand. Someone finds a bad
production session in the observability tool. Someone turns it into a test
case. The test joins the regression suite, and the next release has to pass
it. Heimdall already had the last two steps (`task validate`, `task admit`,
suite tiers and release gates) and a one-file importer (`task from-trace`,
[tasks-from-traces.md](tasks-from-traces.md)). The failure inbox adds the
first two: it keeps pulling flagged sessions from the tools you already run,
collapses repeats of the same failure into one item, and lets a person turn an
item into a task draft with one command or one click.

It is an integration, not a monitoring product. Langfuse, LangSmith, the
Bifrost gateway's logs and OpenTelemetry stay where you watch production; the
inbox only reads what they flagged.

The code is `bench/traceinbox` (sources, store, signature, accept), the
command `cmd/bifrost-bench/inbox.go`, the API `handlers/inbox.go` and the
Inbox page in the UI (`ui/app/inbox`, `ui/components/inbox`).

## The loop

1. **Flag.** Your tools already mark bad sessions: a low evaluation score, a
   thumbs-down from the user, an observation at level ERROR, an errored run,
   a gateway request that failed, retried, fell back or hit a budget.
2. **Pull.** `heimdall inbox pull` (or the schedule daemon) asks each source
   for what it flagged since the last pull, reads each flagged session,
   redacts it, and files it in the inbox. The inbox keeps the user's request,
   the agent's answers and every tool call with its arguments and result;
   keys, tokens, email addresses, phone numbers, long numbers and home folder
   names are removed first, names and street addresses are not.
3. **Dedupe.** Sessions with the same failure signature become one item with
   a count and first and last seen times, so fifty timeouts of the same refund
   tool are one line, not fifty.
4. **Triage.** A person accepts an item (it becomes a task draft), rejects it
   with a reason, marks it a duplicate of another item, or snoozes it.
5. **Confirm.** The draft says what happened, not what was right. A person
   edits every file with an EDIT ME marker, then runs `task validate` and
   `task admit`. Nothing is admitted on its own.
6. **Gate.** The accepted task carries tag `regression` and
   `provenance.incident`. Reports name the incident when the task fails, and a
   release gate can require the regression tasks to pass.
7. **Recur.** When the same failure shows up again in production after it
   was accepted, the item counts the recurrence and shows whether the latest
   experiment passes its task.

## Quick start

From the project folder (the one with `deploy/bench.yaml`):

```yaml
# inbox/sources.yaml
schedule: "*/30 * * * *"        # optional: the schedule daemon pulls every 30 minutes
sources:
  - name: prod-langfuse
    kind: langfuse
    host: https://cloud.langfuse.com
    public_key_env: LANGFUSE_PUBLIC_KEY
    secret_key_env: LANGFUSE_SECRET_KEY
    project: cm123projectid      # for links back to the trace
    suite: support               # suggested suite for its items
    filters:
      scores: [{name: helpfulness, below: 0.5}]
      feedback: {name: user-feedback, negative: ["0", "-1", "false"]}
      level_error: true
```

```console
$ heimdall inbox pull
prod-langfuse (langfuse): 14 flagged, 3 new items, 11 repeats, 0 already seen
$ heimdall inbox list
ID                STATE  SEEN  LAST SEEN         SOURCE         KIND      TASK  TITLE
inc-3f9a1c2b7d10  new    9     2026-11-18 05:12  prod-langfuse  tool-use  -     Refund order A-<n>, the tent arrived torn...
inc-7b20c4d9e811  new    4     2026-11-18 04:40  prod-langfuse  qa        -     What is the return window for tents?
...
$ heimdall inbox show inc-3f9a1c2b7d10
$ heimdall inbox accept inc-3f9a1c2b7d10 -suite support -id refund-timeout
accepted inc-3f9a1c2b7d10 as task support/refund-timeout (tag regression, provenance.incident inc-3f9a1c2b7d10)
wrote draft benchmarks/support/tasks/refund-timeout (template mcp, shape of restock)
  ...
The draft shows what happened in production, not what was right. next:
  1. edit .../instruction.md and delete its EDIT ME lines
  ...
  6. bifrost-bench task validate support/refund-timeout
  7. bifrost-bench task admit support/refund-timeout
```

`heimdall` and `bifrost-bench` are the same program.

## Commands

| Command | What it does |
|---|---|
| `inbox pull [-source a,b] [-since 24h\|7d\|<RFC 3339>] [-format text\|json]` | Pull every source but the webhook (or the named ones) once. `-since` moves the window start for this pull only |
| `inbox list [-state s] [-source name] [-format table\|json]` | Items with counts, last seen, the accepted task and its release status |
| `inbox show <id>` | One item: why it was flagged, signature parts, occurrences, the redacted preview |
| `inbox accept <id> -suite <s> [-kind qa\|tool-use\|mcp\|memory] [-id <task>] [-force]` | Draft a task from the item (below) |
| `inbox reject <id> -reason "<why>" [-duplicate-of <id>]` | Reject, or mark as a duplicate of another item |
| `inbox snooze <id> [-days 7]` | Hide for 1 to 90 days; it reads as new afterwards |
| `inbox ingest -file <trace> [-reason] [-link] [-trace-id]` | Push one local trace file through the webhook's path |
| `inbox tasks [-suite s] [-admitted] [-ids]` | Tasks drafted from production incidents; `-ids` prints them as a YAML list for a suite tier |

Every subcommand takes `-config deploy/bench.yaml`.

## Sources

Secrets are only ever named by environment variable (`*_env` fields). A key,
a token or a URL with `user:password@` in `sources.yaml` is refused when the
file loads, as is an unknown key or a filter that does not apply to the
source's kind. The first pull of a source looks back `filters.window`
(default 24h); later pulls start at the cursor minus `filters.overlap`
(default 15m) so late data is not missed, and a trace already taken is
skipped before it is read again. `max_per_pull` (default 200, at most 5000)
caps the new sessions one pull reads per source; a capped pull says so and
holds the cursor, and the next pull continues where it stopped. A flagged
trace that could not be read (still ingesting, a transient error) also holds
the cursor, so the next pull tries it again.
Rate limits (429) and 502, 503, 504 answers are retried up to four times,
after `Retry-After` (at most 60 seconds) or a doubling wait.

`link` is a URL template back to the original trace. `{host}`, `{project}`,
`{trace_id}`, `{url}` and `{ui_url}` are replaced. Defaults: Langfuse
`{host}/project/{project}/traces/{trace_id}`, Bifrost
`{ui_url}/workspace/logs?selected_log=<row id>`, Maxim the log repository page, LangSmith none (its UI path
needs your organization id; set `link`).

### Langfuse

```yaml
- name: prod-langfuse
  kind: langfuse
  host: https://cloud.langfuse.com          # or your self-hosted URL
  public_key_env: LANGFUSE_PUBLIC_KEY        # basic auth user
  secret_key_env: LANGFUSE_SECRET_KEY        # basic auth password
  project: <project id>                     # links only
  environment: production                   # optional filter
  filters:
    scores: [{name: correctness, below: 0.5}, {name: verdict, values: [fail]}]
    feedback: {name: user-feedback, negative: ["0", "-1", "false"]}
    level_error: true                       # any observation at level ERROR
    tags: [flagged]                         # traces carrying any of these tags
```

How it reads: scores through `GET /api/public/v3/scores` (by name, with
`dataType=NUMERIC&valueMax=` for a numeric threshold; the API bound is
inclusive, the inbox keeps only values strictly below), error observations
and tagged root observations through `GET /api/public/v2/observations`
(cursor paging; an empty page with a cursor is followed), then each flagged
trace's observations with `fields=core,basic,io,model`, read by
traceimport's Langfuse reader. Scores on sessions or experiments, not
traces, are counted in a warning and skipped. Langfuse documents that
endpoints other than the v2 observations API can lag about 10 minutes, which
the default overlap covers.

### LangSmith

```yaml
- name: prod-langsmith
  kind: langsmith
  host: https://api.smith.langchain.com     # or https://eu.api.smith.langchain.com, or self-hosted
  api_key_env: LANGSMITH_API_KEY             # X-API-Key header
  project: <tracing project uuid>
  workspace: <workspace id>                  # optional, X-Tenant-Id
  link: "https://smith.langchain.com/o/<org id>/projects/p/{project}/r/{trace_id}"
  filters:
    errors: true                             # root runs with an error
    feedback: {name: user_score, below: 0.5} # root runs with feedback below 0.5
```

How it reads: `POST /api/v2/runs/query` with `is_root`, `has_error` or the
filter `and(eq(feedback_key, "user_score"), lt(feedback_score, 0.5))` and
`min_feedback_modified_at` (so feedback given today on yesterday's run is
found), cursor paging; then each trace's runs through `GET
/api/v2/traces/{trace_id}/runs`. The runs become a conversation: the root
run's input (or the first LLM run's messages) is the request, each tool run
is a call with its result or error, the root output is the final answer.
LangSmith inputs and outputs are free JSON shaped by the framework that
traced them; the conversion reads the common shapes (OpenAI-style messages,
LangChain message objects, `input`, `question` or `query` strings) and is
best effort. Self-hosted LangSmith needs v0.16 or later for these endpoints.

### Maxim

```yaml
- name: prod-maxim
  kind: maxim
  host: https://api.getmaxim.ai              # optional, the default
  api_key_env: MAXIM_API_KEY                 # x-maxim-api-key header
  workspace: <workspace id>
  project: <log repository id>
  filters:
    errors: true                             # traces Maxim marks as errors
    evaluators: ["Task Success"]             # traces an online evaluator of this name failed
```

How it reads: `POST /v1/log-repositories/logs/search` (type `trace`, a
`timestamp` window, `traceFilters` with `isError = true` or
`evaluatorFilters` with `<name> is fail`, page paging of 50), then each trace
through `GET /v1/log-repositories/logs/traces?id=`. The first generation's
messages (or the trace input) are the request, each tool span is a call with
its result or error, the trace output is the final answer, and a failed
evaluator's reasoning is kept as the flag reason. The default `link` is
`https://app.getmaxim.ai/workspace/{workspace}/log-repository/{project}/logs?traceId={trace_id}`;
Maxim does not document its web app's trace URL, so set `link` if yours
differs. No real Maxim has been called yet; the tests use a local fake of
these endpoints.

### Bifrost gateway

```yaml
- name: gateway
  kind: bifrost
  # url: default gateway.url of the bench config
  # token_env: default gateway.admin_token_env of the bench config
  ui_url: https://bifrost.internal          # for links; default: url
  virtual_keys: [vk-prod-support]           # optional: only these keys
  filters:
    errors: true                            # requests with status error
    min_retries: 2                          # requests retried at least twice
    fallbacks: true                         # requests served by a fallback
    budget: true                            # requests refused by a budget (error type budget_exceeded, status 402)
```

How it reads: `GET /api/logs` with `start_time`, `end_time`,
`virtual_key_ids`, `sort_by=timestamp&order=asc` and offset paging (at most
5000 rows scanned per pull; with only `errors` or `budget` it adds
`status=error`), then `GET /api/logs/{id}` for the newest flagged row of each
session. Rows that share a `session_id` are one session; the newest row's
input history and output message are its conversation. Rows with content
logging turned off carry no messages and are skipped with a warning. Rows of
the Responses API (Codex CLI, the OpenAI Agents SDK) have no input history;
their conversation is read from `responses_input_history` and
`responses_output` instead: message items keep their role (developer reads
as system), function and custom tool calls and their outputs become tool
calls with results, reasoning items are dropped. Live scoring reads them the
same way.

### OTLP folder

```yaml
- name: collector
  kind: otlp_dir
  path: /var/lib/otel/traces       # where the collector's file exporter writes
  pattern: "*.json*"               # default
```

Point an OpenTelemetry collector's `file` exporter (OTLP JSON, one request
per line) at a folder. Each pull reads only the complete lines added since the
last one (a byte offset and a hash of the file's first bytes per file; a file
that shrank, or whose first bytes changed because it was replaced by rename or
copy-truncate, was rotated and is read from the start) and flags a trace when a span has status ERROR or a tool call
failed. Spans of one trace that land in two pulls are read as two partial
sessions; the collector's batch settings decide how often that happens. Turn
on message content capture in your instrumentation, or the spans hold no
conversation.

### Webhook

```yaml
- name: hook
  kind: webhook
  token_env: HEIMDALL_INBOX_TOKEN   # required; the webhook is off while it is empty
  rate_per_minute: 60               # default 60
  max_bytes: 1048576                # default 1 MiB, at most 8 MiB
```

Any tool can push one flagged trace while `heimdall serve` runs in live mode:

```console
$ curl -sS -X POST http://127.0.0.1:8090/api/bench/inbox/ingest \
    -H "Authorization: Bearer $HEIMDALL_INBOX_TOKEN" -H 'Content-Type: application/json' \
    -d '{"trace": {"id": "conv-42", "messages": [...]}, "reason": "thumbs down", "link": "https://...", "source": "support-widget"}'
{"item_id":"inc-3f9a1c2b7d10","created":true,"occurrences":1,"mode":"live"}
```

`trace` is any format traceimport reads (OTLP/JSON, a Langfuse export, a chat
transcript); `format` skips detection, `trace_id` picks one session of
several (at most 20 per push). The token is its own, never the write token,
and is compared by its SHA-256 in constant time. The checks run in this
order: live mode (403 otherwise), token (401), body size (413), rate (429
with `Retry-After`). With no webhook source, or an empty token variable, the
endpoint answers 503. It is one global bucket, not per caller.

### Passing sessions (goldens)

A push with `"passed": true` is a session that went well. It becomes an item
marked `golden` (its own signature, never merged with a failure of the same
request), so a good answer or a good tool path can be kept as a regression
task. Only a sample is kept: at most `golden_per_day` items per source per
day (UTC), default 5, 0 to keep none:

```yaml
- name: hook
  kind: webhook
  token_env: HEIMDALL_INBOX_TOKEN
  golden_per_day: 20
```

A passing push over the day's sample creates nothing and answers
`"skipped": 1`. The hosted per-org ingest route takes the same field.

## Signatures and dedupe

A signature is three parts, hashed:

- **Request sketch**: the first user request, normalized the way failure
  clusters normalize error lines (lowercase; numbers, ids, timestamps, paths
  and redaction markers become placeholders), cut into word 3-shingles; the
  three smallest shingle hashes (a bottom-k MinHash sketch). Requests that
  differ in an order number or a word or two usually share it; different
  requests almost never do.
- **Failing tool**: the first tool call that failed, if any.
- **Error fingerprint**: that tool's first stable error line (clusters'
  normalizer, so `timeout after 30s` and `timeout after 31s` match), else the
  first error line of the outcome hints, else the kind of flag
  (`flag:score:helpfulness`, `flag:feedback`, `flag:retries`, ...), so a
  thumbs-down and an error on the same request stay apart.

One signature is one item (`inc-` and 12 hex characters). A trace already
recorded on an item is not counted twice. Sources are not part of the
signature: the same failure pushed by the webhook and pulled from Langfuse
is one item. To merge two items the signature kept apart, reject one with
`-duplicate-of`.

## Accepting an item

`inbox accept` uses the same machinery as `task from-trace` (see
[tasks-from-traces.md](tasks-from-traces.md) for every file): the first
request as the instruction, the expected answer or the final state checks as
EDIT ME drafts, the observed tool calls as a fixture and a replay tool server
in place of the copied tool service, and, when the session failed, the
recorded calls as a mutant control that admission requires the checks to
fail. On top of that it writes:

```yaml
tags: [regression]
provenance:
  source: trace inbox item inc-3f9a1c2b7d10 (chat) sha256:<hash of the stored session>, trace id t-1
  created_by: task from-trace
  # The production incident this task was drafted from (failure inbox, docs/failure-inbox.md).
  incident:
    id: inc-3f9a1c2b7d10
    source: prod-langfuse
    trace_id: lf-1
    link: https://cloud.langfuse.com/project/p1/traces/lf-1
    first_seen: "2026-11-02T10:00:00Z"
    signature: f_3f9a1c2b7d104a2e
```

`-kind` must match the suite's template (default: the suite's). The task id
defaults to the item id. The item becomes accepted and links to the task;
`inbox list` shows whether the task is admitted yet.

### What should have happened (guided accept)

The accept form on the Inbox page asks what a correct agent does, and the
task is written finished instead of as an EDIT ME draft (the response says
`"finished": true`; only the readiness check is left). Over the API it is
`guide` on `POST /api/bench/inbox/{id}/accept`:

```json
{"suite": "support", "switch_grader": true,
 "guide": {"keep_turns": true,
           "calls": [{"tool": "issue_refund", "args": {"order": "A-1"}}],
           "forbidden": [{"tool": "apply_credit", "args": {"order": "A-1"}}],
           "ordered": false, "reply_rubric": "Says the refund takes 5 days"}}
```

- **qa and memory:** `answers` (one fact per entry), `match` (exact,
  contains_all, regex, numeric, choice), `wrong_answers`, and
  `recorded_wrong: true` to make the recorded answer a wrong answer that must
  fail. The answer file is written without markers.
- **tool-use and mcp:** the recorded calls marked right go to `calls`, the
  wrong ones to `forbidden`, and a missing call is added to `calls`. They
  become `checks.yaml` (docs/declarative-checks.md), a reference that makes
  the right calls through the replay tool server, a mutant that skips the
  last right call and, when a forbidden call has arguments, a mutant that
  also makes it. Such a task is graded by its checks, so the benchmark must
  use `grader: checks`; `switch_grader: true` moves it (its other tasks keep
  their verifier and need their readiness checks again), otherwise the
  accept answers 409. A `reply_rubric` needs a pinned judge: the benchmark's,
  or `judge: {provider, model}` with the switch.
- `instruction` replaces the first request; `keep_turns: true` adds the
  user's later messages to the instruction, in order, so a multi-turn failure
  keeps every request.
- `use_recorded: true` takes the recorded answer or the recorded calls that
  did not fail as the expected ones: the accept of a golden.

`GET /api/bench/inbox/{id}` returns `session` (the redacted requests, tool
calls with their arguments, and the final answer) for the form.

### Bulk accept

`POST /api/bench/inbox/accept` takes `{"ids": [...], "suite", "kind"?,
"guide"?, "switch_grader"?, "judge"?}` (at most 100 items) and accepts each
into the benchmark with the same guide, for example `{"use_recorded": true}`
for a day's goldens or one shared `reply_rubric`. One item failing does not
stop the others: the answer lists `accepted` and `failed` with the reason.
The Inbox page has the same as "Accept several at once".

## Closing the loop

- **Reports.** When an experiment runs tasks with `provenance.incident`, its
  report (`report.md` of every analysis: `run`, `check`, `compare`, the server and scheduled runs) gets a section:

  ```
  ## Production incidents

  2 of 9 tasks in this experiment came from production incidents (failure inbox, docs/failure-inbox.md); 1 failed a run.

  - Regression of incident inc-3f9a1c2b7d10 from 2026-11-02 (prod-langfuse trace lf-1, https://...): task support/refund-timeout failed 2 of 3 runs in arm candidate.
  - Incident inc-7b20c4d9e811 from 2026-11-05: task support/return-window passed every graded run.
  ```

- **Recurrence.** When a later pull or push brings the same signature after
  the item was accepted, the item counts a recurrence (`recurrences`,
  `last_recurrence`), the pull prints it, and the scheduled pull's run record
  names it. `inbox list`, `inbox show` and the Inbox page show how the task
  did in the latest experiment that ran it (from the results index): passes,
  fails, or not run yet.
- **Release gate.** Accepted tasks carry tag `regression`; `inbox list` says
  how many tasks came from production incidents. Suite tiers (see the tiers
  in `suite.yaml`) take task ids: `heimdall inbox tasks -suite support
  -admitted -ids` prints the admitted incident tasks as a YAML list to put
  under a `regression` tier, and the release experiment runs that tier, so
  the gate requires them to pass.

## Scheduled pulls

Set `schedule:` (a 5-field cron) in `inbox/sources.yaml`, and the schedule
daemon (`heimdall schedule daemon`, or `heimdall serve -schedules`) adds a
schedule named `inbox-pull` that pulls every source on that timetable.
Alternatively write it in `schedules.yaml` yourself:

```yaml
schedules:
  - name: inbox-pull
    job: inbox-pull
    cron: "*/30 * * * *"
```

A job schedule names no experiment, no policy and no `max_usd_per_day`, and
spends nothing. A pull where every source failed is a failed run and fires
the schedule's `schedule_failed` alert.

## The Inbox page

`/inbox` in the UI: counts by state, a table of items (title, source, why it
was flagged, occurrences, first and last seen, suggested kind and suite, the
accepted task and its release status, a link to the original trace), the
redacted preview of the selected item, and Accept (choose the suite, the
kind, the task id), Reject (a reason, optionally a duplicate) and Snooze
actions. Actions need the write token and live mode. The API:

| Route | Auth |
|---|---|
| `GET /api/bench/inbox?state=&source=` | none |
| `GET /api/bench/inbox/{id}` | none |
| `POST /api/bench/inbox/{id}/accept` `{suite, kind?, task_id?}` | write token |
| `POST /api/bench/inbox/{id}/reject` `{reason, duplicate_of?}` | write token |
| `POST /api/bench/inbox/{id}/snooze` `{days}` | write token |
| `POST /api/bench/inbox/ingest` | the webhook's own token |

## Maxim failures in bulk

The Maxim page (`/maxim`, linked from Production failures and from
[Settings > Integrations](/settings?tab=integrations)) turns a Maxim log repository's production failures into
regression tasks in one pass, on the org's own Maxim connection (the API
key stays in the org key store):

1. **Pull.** Say which traces failed: online evaluators that failed them,
   traces Maxim marks as errored, or an evaluator score below a threshold
   (of named evaluators, or any). A window of 1 to 90 days, at most 1000
   traces. Every trace goes through the inbox (redaction, signature, dedupe)
   as source `maxim-bulk`.
2. **Groups.** Inbox items that failed the same way form one group: the
   same first failing tool and error line, or, when no tool failed, the
   same failing evaluator or low score. The request is ignored on purpose,
   so one group spans many user requests.
3. **Draft.** Pick groups and a benchmark; each group becomes one task (up
   to 5) through the inbox accept, and the group's other items are marked
   duplicates of it. Each draft also gets `verifier/checks.yaml`
   (declarative checks: every tool that failed in production must now
   return a result without an error, and a reply rubric) and
   `verifier/scenario.json` (a scenario row: the first request opens, later
   user turns become facts), and the tags `maxim` and `pending-review`.
   Everything generated is labelled generated.
4. **Review.** A person says what should have happened: the right answer
   for a question and answer task (one line is matched exactly, several
   lines are facts that must all be present; the recorded production answer
   becomes a wrong answer readiness must fail), a rubric, and optionally a
   cleaner request. Accepting drops `pending-review`. A tool-use or MCP
   draft whose state checks still hold EDIT ME lines is "files to finish"
   until a person edits them.
5. **Readiness.** Accepting queues the task's readiness check (the
   `suite.admit` job for that one task). Only an admitted task is used by
   evaluations, so a draft nobody accepted never reaches a gate or a
   report card. Reject removes the draft's folder and its entry in
   `suite.yaml`.

Imports are kept as `inbox/bulk/<id>.json` (the newest 200). API:
`GET|POST /api/bench/integrations/maxim/batches`, `GET .../batches/{id}`,
`POST .../batches/{id}/draft`, and `POST
.../batches/{id}/tasks/{suite}/{task}/accept|reject|readiness`. Maxim's
search and trace endpoints are read as its public OpenAPI description
states them (22 Nov 2026); the tests use a local fake of them, and no real
Maxim has been called.

## Storage

Plain files under `inbox/` in the project, no database:

| Path | What |
|---|---|
| `inbox/sources.yaml` | the sources (you write it) |
| `inbox/items.jsonl` | one item per line, rewritten atomically under a lock |
| `inbox/items/<id>/session.json` | the redacted session of the first occurrence (accept drafts from it) |
| `inbox/items/<id>/preview.md` | the redacted preview, at most 16 KiB |
| `inbox/cursors.json` | per source: the window start, trace ids already taken, OTLP file offsets, the last error |
| `inbox/log.jsonl` | pulls and every state change, appended |

Caps: 5000 items, 20 trace references and 8 reasons per item, 8 MiB per
stored session (a larger one is skipped with a warning), 64 KiB per text field and 1000 tool calls per session (from
traceimport). The store is behind a small interface
(`traceinbox.Store`), so a hosted build can index the same items.

## Privacy

Every session is redacted with traceimport's redactor before anything is
written; there is no switch to turn it off in the inbox. It scrubs private
keys, authorization, cookie and API key headers, bearer and basic
credentials, JWTs, common API key formats (OpenAI, Stripe, AWS, GitHub,
Slack, Google, GitLab, Hugging Face, Langfuse), values assigned to names
ending in token, secret, password or key, email addresses, phone numbers and
digit runs of nine or more. The raw session is never stored, and the item
keeps only counts of what was scrubbed.

Redaction is pattern matching. It does **not** catch names, street
addresses, dates of birth, free-text medical or financial details, or a
secret in a format it does not know. Do not point the inbox at traces that
hold regulated data (health, payment, government identifiers) without a
review of what those traces contain, and read every draft before it is
admitted: `inbox accept` runs the admission secret scan on the draft and
prints anything it finds.

## What is verified and what is not

Read on 18 Nov 2026; no real Langfuse, LangSmith or Bifrost deployment has
been called. Every source is tested against a local fake that speaks the
documented API (`bench/traceinbox/*_test.go`): paging, cursors, auth, rate
limit retries, and planted secrets that must never reach disk.

- **Langfuse**: the public API's OpenAPI reference
  (https://cloud.langfuse.com/generated/api/openapi.yml): v3 scores
  (`name`, `dataType`, `valueMax`, `fromTimestamp`, `toTimestamp`,
  `fields=subject`, cursor; `limit` at most 100), v2 observations (`level`,
  `traceId`, `fromStartTime`, `toStartTime`, `isRootObservation`, `filter`,
  `fields`, cursor; `limit` at most 1000; input and output as raw strings),
  basic auth. That reference no longer lists `GET /api/public/traces` or
  `GET /api/public/traces/{id}`, so traces are read from their observations.
  Not verified: the UI trace link path; the shape of `value` for BOOLEAN and
  CATEGORICAL scores beyond the reference's description.
- **LangSmith**: the OpenAPI description
  (https://api.smith.langchain.com/openapi.json, version 0.1.0) for `POST
  /api/v2/runs/query`, `GET /api/v2/traces/{trace_id}/runs`, the `X-API-Key`
  and `X-Tenant-Id` headers; the filter syntax page
  (https://docs.langchain.com/langsmith/trace-query-syntax) for
  `feedback_key` and `feedback_score`. Not verified: the run input and output
  shapes of each framework (best effort), the UI link path.
- **Bifrost**: this repository's gateway source
  (`transports/bifrost-http/handlers/logging.go`,
  `framework/logstore/tables.go`, `plugins/governance`), not a live gateway.
  Not verified: whether every governance refusal is written to the logs, and
  whether a list row carries `error_details` (the inbox reads the detail row
  for that).
- **OTLP folder**: the collector file exporter's one-request-per-line JSON is
  the format traceimport already reads; rotation behavior is inferred from
  file size, not tested against a real collector.

## Pulling from the hosted server

The hosted server runs a pull in its own process (it only makes HTTP calls), from the Pull now button. It differs from a local pull in four ways:

- Every URL it reads (gateway, Langfuse or LangSmith host, judge) must be public https. Loopback, private and link-local addresses are refused, again at connect time, and redirects are not followed.
- A `*_env` field names an org secret (Settings > Secrets), never a server variable.
- One pull makes at most 2000 requests, reads at most 256 MiB and runs at most 5 minutes; pull again to continue from the cursor.
- The paid live judge runs only when ticked for that pull, with the org's own provider key ([Settings > Model keys](/settings?tab=keys)): a key labelled like `judge.key_env`, or one whose base URL has the judge URL's host. `otlp_dir` sources are local only.

The page lists each missing step with a link (Settings > Secrets, or the setup form).

## User feedback

Your app can send one request when a user rates a session, for example with a thumbs down. The session lands in the Inbox as a failure to turn into a task (source "feedback"), and a thumbs up with a session becomes a golden candidate.

The route is `POST /api/bench/orgs/{org}/feedback` on the hosted server, with one of the org's ingest tokens (Inbox page, Push failures in, Generate token) as the bearer. The checks run in this order: the token (401), the body size, at most 64 KiB (413), and the org's rate limit (429).

The body:

```json
{"session_id": "s-123", "rating": "down", "score": null, "comment": "it said refunded but it was not",
 "trace": null, "source": "support-widget", "user": "opaque-id"}
```

- `rating` is `down` or `up`. Instead you can send `score` from 0 to 10: 0 to 3 counts as down, 8 to 10 as up, anything between is only logged.
- `comment` is at most 2000 characters and is cleaned of secrets and personal data before it is kept.
- `user` is stored only as a short hash, never as sent.
- `trace` is the session in any format the Inbox reads. Without it, the first Bifrost source of the Inbox is asked for the session (the last 7 days of gateway logs, at most 5000 rows).

The answer is `{"item_id": "...", "created": true, "note": ""}`. When no trace was sent and no session could be found, the answer is 202 with the note "Saved. No session was found to attach: send the trace with the feedback, or add a Bifrost source." Every request is logged in `inbox/feedback.jsonl`.

### From a terminal

```sh
curl -X POST https://heimdall.example.com/api/bench/orgs/acme/feedback \
  -H "Authorization: Bearer $HEIMDALL_INBOX_TOKEN" -H "Content-Type: application/json" \
  --data '{"session_id": "s-123", "rating": "down", "comment": "it said refunded but it was not"}'
```
