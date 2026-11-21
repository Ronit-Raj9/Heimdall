# Live traffic scoring

Experiments answer "is version B better than A on these tasks?" before a
release. Live scoring watches the other half of the loop: after the release,
on real traffic. Point Heimdall at the Bifrost gateway your production agents
already use; it reads the gateway's request log (read-only), rebuilds
sessions, measures signals that need no ground truth, compares two live
versions with a sequential test that stays valid however often you look,
alerts, and sends the worst failing sessions to the
[failure inbox](failure-inbox.md), where a person turns them into regression
tasks.

The code is `bench/livescore`, the command `cmd/bifrost-bench/livescore.go`,
the API `handlers/livescore.go` and the Live page (`ui/app/live`,
`ui/components/livescore`).

## A separate evidence class

Every live score is labelled evidence `live`, and none of it ever enters an
experiment verdict, a leaderboard or a release decision. Two reasons:

- **No ground truth.** Nobody checked whether a production answer was
  right. A session that ends politely with a wrong refund amount scores as
  healthy here. Live signals see failures the system itself reports
  (errors, retries, budget refusals, failed tool results, loops), not
  wrong answers.
- **No control of inputs.** Each version saw different users asking
  different things. If version B gets the harder customers, its numbers move
  without B being worse. An experiment runs both arms on the same admitted
  tasks; live traffic cannot.

So a live result reads "version B abandoned more sessions than A on the
traffic each one received", never "B fails more tasks". When a live result
worries you, the next step is an experiment, and the inbox gives you the tasks
to run it on.

## Quick start

```bash
heimdall livescore init -baseline-key <virtual key id> -candidate-key <virtual key id>
#   or: -tag-header x-bf-lh-agent-version -baseline-tag v1 -candidate-tag v2
export BIFROST_SETUP_TOKEN=...      # the read credential; named in the config, never stored
heimdall livescore pull             # read new rows, score closed sessions, compare, alert, push to the inbox
heimdall livescore status           # versions, grouping reliability, the comparison, worst sessions
heimdall livescore compare          # the comparison table; -fail-on worse exits 1 for CI
heimdall livescore compare -restart # test new settings from now on (old data is never tested twice)
heimdall livescore compare -simulate 1000   # the A/A check on synthetic streams, no gateway
```

`livescore init` writes `livescore/config.yaml`; without keys or tags it
writes `EDIT ME` placeholders that fail validation, so an unedited file
never scores anything. Set `schedule: "*/15 * * * *"` and the schedule daemon
(`schedule daemon` or `serve -schedules`) runs a job named `livescore-pull`.
The Live page is `/live`; its Pull button needs the write token and a live
server.

## Configuration

```yaml
source:
  url: http://127.0.0.1:8080          # default: gateway.url of the bench config
  token_env: BIFROST_SETUP_TOKEN      # default: gateway.admin_token_env; the value stays in the environment
  ui_url: https://bifrost.example     # links to the Bifrost logs page; default: url
  window: 24h                         # how far back the first pull reads
  overlap: 15m                        # how far before the cursor each pull reads again, for late rows
versions:                             # every set field must match; values of one field are alternatives
  - name: v1
    virtual_keys: [vk-id-1]
  - name: v2
    teams: [team-id]                  # or customers, models (model or provider/model), or:
    tag: {header: x-bf-lh-agent-version, value: v2}
compare:
  baseline: v1
  candidate: v2
  alpha: 0.05                         # chance of any false decision, every signal and every look together
  min_pairs: 30                       # pairs before a signal may decide
  signals: [abandoned, tool_error_rate]   # default: the 13 signals marked "compared" below
sessions:
  gap: 10m                            # idle time that ends a session (30s to 24h)
sampling:
  rate: 0.25                          # share of sessions scored, chosen by a hash of the session
retention:
  days: 30                            # scored sessions older than this are dropped (1 to 400)
thresholds:
  - {signal: abandoned, above: 0.2, window: 24h, min_sessions: 20, version: v2}
alerts:
  - {url_env: LIVE_ALERT_WEBHOOK, events: [live_signal_threshold, live_comparison_worse]}
inbox:
  max_per_pull: 5                     # failure signatures pushed per pull (1 to 50)
  suite: support                      # suggested suite for the drafts
  # disabled: true
judge:                                # optional, off by default
  url: http://127.0.0.1:8080/v1       # an OpenAI-compatible chat endpoint, e.g. the gateway
  key_env: LIVE_JUDGE_KEY             # a virtual key with its own budget
  model: openai/gpt-5.4-mini
  sample_rate: 0.05
  max_per_pull: 20
  calibration: benchmarks/my-judged-suite   # where a calibration record may cover this judge
schedule: "*/15 * * * *"
```

The file is read strictly: an unknown key, a version with no selector, a
secret pasted where a variable name belongs, a URL with credentials, a rate
threshold outside 0 to 1, a comparison naming an unknown version, or an
alert URL pasted into `url_env` is refused with the field named.

## Read-only, by construction

Every request live scoring sends to the gateway is a `GET` to `/api/logs` or
`/api/logs/{id}` with the setup token header. The HTTP client is wrapped in a
transport that refuses any other method before it leaves the process
(`readOnlyTransport`, tested in `TestReadOnlyTransportRefusesWrites`), and the
end-to-end tests record every request and fail on anything but a `GET` to the
log API: 288 requests in the throwaway-stack proof, every one a `GET`.
Nothing creates, changes or deletes a key, a rule or a setting. The only
other call is the optional judge's chat request, sent to the judge's own
endpoint with its own key.

A list row carries every number; a detail row is fetched only for an error
(its type and status) and for each session's last request (its input history
and output hold the transcript), so a pull costs about one detail read per
session.

## Selecting versions, and what Bifrost v2.5.1 offers

Verified from the v2.5.1 source (`transports/bifrost-http/handlers/logging.go`
`getLogs` and `parseMetadataFilters`, `framework/logstore/tables.go` `Log`,
`plugins/logging/main.go` `captureLoggingHeaders`,
`core/schemas/useragents.go` `HarnessSessionHeaders`), read 18 Nov 2026, and
on a throwaway v2.5.1 stack the same day:

| Selector | Query filter | Row field | Checked on the stack |
|---|---|---|---|
| `virtual_keys` | `virtual_key_ids` | `virtual_key_id` | yes: two keys, 140 rows each, no leak between them |
| `teams` | `team_ids` | `team_id` | source only |
| `customers` | `customer_ids` | `customer_id` | source only |
| `models` | `models` | `model`, `provider` | source only |
| `tag` | `metadata_<key>=<value>` | `metadata` | yes: `x-bf-lh-agent-version` logged as metadata `agent-version` with no gateway setting, filtered server side, and present on list rows |

Other headers reach `metadata` only when listed in Bifrost's
`client.logging_headers`, under their own lower-case name; set
`tag.header` to that name. Every row the gateway returns is checked again on
the client side against the whole selector, so a gateway that ignored a
filter cannot leak another version's rows in (tested with a fake gateway
that ignores the metadata filter). A row that matches two versions is kept
in the first and counted as overlap. Bifrost also filters on
`user_ids`, `business_unit_ids`, `project_ids`, `routing_rule_ids`,
`selected_key_ids`, `providers`, `aliases`, `status`, `session_id`,
`stop_reasons`, `tool_call_names`, `user_agents`, `apps` and latency, token
and cost ranges; they are not selectors here yet.

On the stack, a list row held `id`, `timestamp`, `status`, `provider`,
`model`, `number_of_retries`, `fallback_index`, `latency` (milliseconds),
`cost`, `token_usage`, `session_id`, `virtual_key_id`, `metadata`,
`content_hidden` and `input_history`, but no `output_message`, no
`error_details` and no `stop_reason`; the detail row adds those, plus
`attempt_trail`. Because only error rows and each session's last row are
read in detail, a `content_filter` stop on a request in the middle of a
session that did not error is not seen.

## Sessions

Bifrost logs requests, not sessions, so rows are grouped:

- **By session id.** Bifrost fills `session_id` from the `x-bf-session-id`
  header, or from a coding harness's own header (`x-claude-code-session-id`,
  `x-session-affinity`, `x-session-id`, `session-id`, `session_id`,
  `thread-id`, `conversation_id`, in that order). This is as reliable as the
  client: two conversations sent under one id are one session.
- **By idle gap.** Without an id, requests of the same virtual key and user
  belong together until a gap longer than `sessions.gap`. This is a
  heuristic: two conversations running at once on one key are merged, and
  one conversation with a long pause is split. A session whose requests
  overlap in time by more than 250 ms is marked `concurrent`, the visible
  sign of a merge. (Back-to-back requests of one conversation overlap by a
  fraction of a millisecond on v2.5.1, because the row is stamped and its
  latency measured at slightly different points; that is tolerated.)

A session is scored once it has been idle for the gap and no row of it is
still processing, so a running session is never scored half way; the cursor
stays at the first row of the oldest open session. A later request under
the same id after that starts a new scored segment. A session still open
after `source.window` (a client that reuses one id all day) is scored as it
stands and flagged `cut`, so it cannot hold the cursor back for good; so is
one with more than 400 requests. Status and the Live
page report how many sessions came from each rule and how many are
concurrent, with a sentence saying how far to trust them. On the stack: 140
sessions sent with `x-bf-session-id` became exactly 140 sessions, and three
sessions without one, 32 s apart with a 30 s gap, became exactly three, none
concurrent.

Sampling picks a session by a hash of its version and key, never by
anything it did, so a session is scored whole or not at all, the same on
every pull.

## Signals

All of them are `live` evidence. A value that cannot be measured is unknown,
never zero: a session without a tool call has no tool error rate, a session
whose transcript Bifrost did not serve (content logging off,
`content_hidden`) has no transcript signals, and a row without a cost makes
the session's cost unknown.

| Signal | Kind | Compared | How, and what it does not see |
|---|---|---|---|
| `request_error_rate` | rate | yes | share of the session's requests whose row has status error (after Bifrost's retries) |
| `tool_error_rate` | rate | yes | share of tool results that say they failed: an MCP `isError`, an object with an `error` field, text starting `Error:` (traceimport's rules). A tool that fails silently is not seen |
| `loop` | rate | yes | the same tool call (name and canonical arguments) 3 or more times in a row, triage's `pattern_loop` rule |
| `repeated_calls` | ordinal | no | tool calls identical to an earlier call of the session |
| `retried` | rate | yes | a request with `number_of_retries` above 0 |
| `fallback` | rate | yes | a request with `fallback_index` above 0 |
| `refusal` | rate | yes | `stop_reason` `content_filter`, a `refusal` field on the model's message, or a final answer opening with a refusal phrase. A heuristic: a refusal in other words is missed, and a correct refusal of a bad request counts the same |
| `budget_hit` | rate | yes | a request refused by a governance budget (error type `budget_exceeded` or status 402) |
| `rate_limited` | rate | no | a request answered 429 |
| `injection_marker` | rate | yes | a tool result holding a prompt-injection phrase (ignore previous instructions, new system instructions, chat-template tags, requests to send credentials, do not tell the user). A fixed list: a novel injection is missed. It says the agent was exposed, not that it obeyed |
| `abandoned` | rate | yes | the session's last request failed and nothing followed within the gap |
| `turn_latency_ms` | ordinal | yes | mean of Bifrost's `latency` over the session's requests |
| `session_ms` | ordinal | yes | first request's timestamp to the last one's plus its latency; includes the agent's own time between requests |
| `cost_usd` | ordinal | yes | Bifrost's own `cost`, summed: its pricing, not the bench's pinned snapshot |
| `tokens` | ordinal | yes | prompt plus completion tokens from `token_usage` |
| `judge_score` | rate | only when calibrated | a model judge's 0 to 1 score on a sample (below) |

Read cost and tokens next to abandonment: a version that fails early is
cheaper. In the stack proof the faulty candidate was decided better on cost
and tokens for exactly that reason, and worse on abandonment and retries;
the comparison's result is worse whenever any signal is worse.

## Comparing two live versions

The comparison is looked at after every pull, forever, so a fixed-sample
test would raise false alarms at every look. Each signal is tested with a
betting e-process (testing by betting: Shafer and Vovk; Waudby-Smith and
Ramdas, 2023):

1. Sessions of the two versions are paired in order of scoring: the i-th
   baseline session with the i-th candidate session, per signal, among
   sessions where the signal is known. The pairing depends only on arrival
   order, never on a value, so unequal traffic shares are fine (the larger
   stream waits in a queue of at most 5,000 per signal; the oldest is dropped
   past that, and the count is shown).
2. A rate signal gives d = candidate - baseline in [-1, 1]; an ordinal one
   gives d = sign(candidate - baseline), so one huge latency cannot dominate.
3. For a fixed bet lambda, the product of (1 + lambda d) is a nonnegative
   martingale under the null, and so is the average over the fixed grid of
   bets 0.02, 0.05, 0.1, 0.2, 0.35 and 0.5. Ville's inequality bounds the
   chance it ever reaches 1/a by a.
4. Each signal has two one-sided processes (candidate higher, candidate
   lower), each tested at alpha / (2 x signals): Bonferroni, which needs no
   assumption about how the signals depend on each other. With the default
   13 signals and alpha 0.05 the threshold is an e-value of 520.
5. A signal may decide only after `min_pairs` pairs (default 30). That only
   removes chances to decide, so the bound holds.

Per signal the result is **worse**, **better** or **not yet known**, with the
pairs, both means, both e-values and, while short, the pairs still needed.
The comparison is worse when any signal is worse, better when one is better
and none worse, otherwise not yet known. A crossing is kept: Ville's bound
covers the whole path, so a decision made at any look stays valid.

The null it tests: the two versions' sessions are independent draws with
the same mean (rate signals) or the same distribution (ordinal signals) over
the comparison's lifetime. A shift that hits both versions alike (a busy
afternoon) cancels in the pairs; a shift that hits one version's traffic
alone (it was rolled out to harder customers) does not, and is exactly the
"no control of inputs" caveat above.

The comparison keeps its state (queues and log wealth per bet) in
`livescore/state.json` and consumes each scored session once, in order, so
retention dropping old sessions never changes it and looking at it never
re-tests old data. Changing the versions, the selectors, alpha, the minimum
pairs, the signals, the gap or the sample rate marks it stale; `compare
-restart` starts a new one that consumes only sessions scored after the
restart, and the restart count is shown.

The bench's other sequential rules do not fit live traffic, so this one is
built inside `bench/livescore`: the drift canary (W5) runs the exact paired
sign-flip on a fixed suite and confirms a flag with a second look, and
adaptive trials (W7) split alpha over a few pre-declared looks at paired
tasks. Both need paired tasks and a known number of looks; live traffic has
neither.

### False alarms, simulated

`TestSimAAFalseAlarmRate` runs the comparison code path on synthetic A/A
streams (both versions from one distribution in which errors, retries,
abandonment, loops and latency are correlated through a latent trouble
level), 600 sessions per version, looked at every 10 sessions, all 13
signals:

| Traffic split | Streams | Any decision | Rate | Bound |
|---|---:|---:|---:|---:|
| 50 / 50 | 400 | 2 | 0.50% | 5% |
| 80 / 20 | 400 | 4 | 1.00% | 5% |

The rate sits well under alpha because Bonferroni and Ville are both
conservative. `TestSimLooksDoNotMatter` shows the decisions are the same
whether the comparison is looked at after every session or every 100.
`TestSimPower`: a candidate abandoning 15 more sessions in 100 is decided
worse in 100 of 100 streams, at a median of 182 pairs; latency, which did not
change, did not decide. `livescore compare -simulate N` runs the same check
with your sample size.

## Alerts

Through the bench's signed webhooks (`bench/alerts`, receivers by
environment variable only, [schedules-and-alerts.md](schedules-and-alerts.md)):

- `live_signal_threshold`: a threshold's signal mean over its window, on at
  least `min_sessions` sessions, crossed `above` or `below`. This is a
  monitoring rule on an observed mean, not a statistical claim.
- `live_comparison_worse`: the comparison decided worse. `changes` holds one
  line per worse signal with both means, the pairs and the e-value.

An open alert is not sent again until its rule closes (the mean comes back,
or a new comparison starts), so a pull every few minutes does not repeat it.
Alerts use the config's `alerts:` and the bench config's.

## Failures to the inbox

After each pull, the failing sessions (abandoned, request errors, tool
errors, loops, injection markers, budget hits, refusals) are grouped by the
failure inbox's own signature (the request sketch, the first failing tool,
the normalized error), the groups ranked by size and then by severity, and
the top `inbox.max_per_pull` pushed through the inbox's store: a new item,
or one more occurrence of an existing one. Items have `source_kind` `live`
and source `live-<version>`; that is their provenance, and an accepted
item's task carries `provenance.incident.source: live-<version>`. Every
pushed session keeps its inbox item id, so the Live page links the worst
sessions to `/inbox?item=<id>`, and `inbox accept <id> -suite <suite>` drafts
the regression task as for any inbox item (checked: a live item accepted
into a tool-use suite wrote a draft with the incident provenance).

## Privacy

- **Nothing textual is kept by live scoring.** `livescore/sessions.jsonl`
  holds numbers, flags, a hashed session id (`ls_` and 16 hex characters of
  a hash of the version and the client's session key, never the key), the
  model name and the Bifrost row id for the link back. Prompts, outputs,
  tool arguments and tool results are dropped with the row in memory. A test
  writes a card number, an email, a prompt, an answer and an upstream error
  into the fixture traffic and fails if any of them appears in the folder.
- **Redaction is always on** for what goes to the inbox: the transcript of
  a pushed session goes through the inbox's redactor (traceimport: keys,
  tokens, emails, phone numbers, long digit runs) before it is stored, as
  every inbox item is. Names and street addresses are not caught.
- **Sampling** (`sampling.rate`) scores a share of sessions; the rest are
  counted and skipped without a detail read.
- **Retention** (`retention.days`, default 30) drops older scored sessions
  at each pull; the inbox keeps its own items until a person acts on them.
- The read credential is named by variable and never written, logged or
  shown; status shows the variable's name.

## The judge (optional)

`judge:` scores a sample of sessions through an OpenAI-compatible chat
endpoint: the redacted transcript, a fixed prompt (or `prompt_file`), and a
`SCORE: <0 to 1>` answer (a plain decimal such as `0.75`; an out-of-range
number, a sign or an exponent is rejected, never cut to a valid prefix). A judge score is reported per session and over
time, and it never decides anything unless a judge calibration record
([judge-calibration.md](judge-calibration.md)) in the `calibration` suite
covers the exact model and the prompt's hash (template name
`live_session`); only then may `judge_score` be listed in
`compare.signals`, and uncalibrated scores are skipped by the test even if
listed. Today no calibration flow writes records for a live prompt, so in
practice the judge is report-only. The judge is the one paid part: it calls
a model on its own key. It has been tested against a local fake that speaks
the chat completions API only; no real judge call has been made.

## API and UI

| Route | What |
|---|---|
| `GET /api/bench/livescore` | status: source (read-only, variable name only), every version's selector, counts, last-24-hour means and series, grouping reliability, the comparison, worst sessions with inbox items, privacy, judge |
| `GET /api/bench/livescore/sessions?version=&flag=&limit=` | scored sessions, newest first, numbers and flags only |
| `GET /api/bench/livescore/compare` | the comparison; 404 until one has started |
| `POST /api/bench/livescore/pull` | one pull now; write token, live mode only |

Every response says `evidence: live`. The Live page shows the evidence note,
what is read and kept, the comparison table (result badge, sample counts,
means, e-values, pairs still needed), every signal over time per version on
a shared scale, and the worst sessions linked to their inbox items. The
contract key lists are `handlers/testdata/contract/Live*.keys`.

## Proof at $0

- **Fixture rows** (`go test ./livescore/`): a fake gateway speaking the log
  API serves scripted sessions. Two versions by virtual key, the candidate
  failing every second session: the comparison decides worse on abandonment
  and tool errors, one threshold alert and one comparison alert are sent
  once, the failing sessions become at most 3 inbox items with source kind
  live, every request is a GET, no fixture text lands on disk. An A/A
  fixture (both 20% abandoned) does not decide. Also: selectors by tag with
  a gateway that ignores the filter, open sessions waiting, stable sampling,
  credential errors without the credential in the message, restart, retention,
  the judge reported and not decided, config refusals.
- **A throwaway Bifrost v2.5.1 stack** (`go test -tags integration ./livescore/
  -run TestIntegrationLiveStack` with `HDX6_*` set; compose project `hd-x6`,
  Postgres 18 on tmpfs, the bench fault proxy with no provider key so every
  model call is answered by its scripted model), 18 Nov 2026. The test sets
  up the provider and four virtual keys the way the bench does for runs,
  then sends scripted chat sessions with a tool call through Bifrost's
  `/v1/chat/completions`:
  - v1 (virtual key on `fp-slot-1`): 70 healthy sessions. v2 (`fp-slot-2`):
    70 sessions, every second one's second request answered 503 by the fault
    proxy (and Bifrost's one retry), every fourth one's tool result an error.
    Live scoring read 140 rows per version and scored 70 sessions each:
    **worse**, abandoned and retried decided at 70 pairs (0% against 50%,
    e-value 248,864 over the threshold 520); request errors (0% against 25%,
    e-value 464) and tool errors (0% against 25.7%, e-value 289) were not yet
    known at 70 pairs; cost and tokens decided better (failed sessions stop
    early). Two inbox items: 35 abandoned sessions with the 503, 18 with the
    tool error.
  - A/A: two more keys, both healthy, selected by the header tag
    `x-bf-lh-agent-version`: 70 sessions each, **not yet known**, largest
    e-value 6.8.
  - Gap grouping: three sessions without a session id, 32 s apart, gap 30 s:
    exactly three sessions, none concurrent.
  - Live scoring sent 288 requests, every one a `GET` to `/api/logs`.
- **The CLI** on the same stack in a fresh project: `livescore init
  -baseline-key ... -candidate-key ...`, `livescore pull` (140 rows each, 70
  sessions each, two inbox items), `livescore compare -fail-on worse` (exit
  1, the table above), `livescore status`, `livescore compare -simulate 300`
  (2 of 300 A/A streams decided anything), `inbox list` (two items, source
  `live-candidate`, titles redacted) and `inbox accept` into a tool-use suite.

No real provider, no real Langfuse or Maxim, and no real judge was called.

## What this is not

- **Not shadow traffic.** Nothing is replayed or duplicated; only traffic
  that already happened is read. Comparing two versions on the same inputs
  is what experiments are for.
- **Not ground truth.** No signal here says an answer was right. A
  calibrated judge is the closest it gets, and it is report-only until a
  calibration record covers it.
- **Not a replacement for Maxim or Langfuse online evaluations.** Those
  score production traces with evaluators you configure and keep the traces;
  live scoring adds a sequential A/B decision with a false-alarm guarantee
  on gateway-level signals, and a path from failures to regression tasks.
  It can read their verdicts later as more signals: the failure inbox
  already pulls Langfuse scores and LangSmith feedback, and the same fetch
  code could feed a score per session into this comparison (not built).
- **Not a monitor of the gateway itself.** Bifrost's own dashboards and
  metrics cover throughput, provider health and spend.

## Limits

- Signals come from what Bifrost logged. With content logging off for a row
  (`content_hidden`) the transcript signals are unknown; with logging off
  entirely there is nothing to read.
- Gap grouping merges concurrent conversations on one key; send
  `x-bf-session-id`.
- The test's null assumes the two versions' traffic is comparable over time;
  a rollout that sends different users to each version breaks it, and no
  statistic can tell.
- Refusal and injection detection are phrase lists in English.
- Bonferroni over 13 signals is conservative: small effects need many
  sessions (see `-simulate`). Narrow `compare.signals` to what you care about
  for more power.
- A pull reads at most 20,000 list rows per version and 5,000 detail rows;
  past that it stops, holds the cursor and says so. Rows already scored do
  not count toward the 20,000, so the next pull reads past them.
- Teams, customers and models as selectors are verified from the v2.5.1
  source only, not on a stack.
- The judge has been tested against a fake endpoint only.

## Pulling from the hosted server

The hosted server runs a pull in its own process (it only makes HTTP calls), from the Pull now button. It differs from a local pull in four ways:

- Every URL it reads (gateway, Langfuse or LangSmith host, judge) must be public https. Loopback, private and link-local addresses are refused, again at connect time, and redirects are not followed.
- A `*_env` field names an org secret (Settings > Secrets), never a server variable.
- One pull makes at most 2000 requests, reads at most 256 MiB and runs at most 5 minutes; pull again to continue from the cursor.
- The paid live judge runs only when ticked for that pull, with the org's own provider key (Models & keys): a key labelled like `judge.key_env`, or one whose base URL has the judge URL's host. `otlp_dir` sources are local only.

The page lists each missing step with a link (Settings > Secrets, or the setup form).
