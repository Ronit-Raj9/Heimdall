# Notifications: Slack, Teams, PagerDuty, email and webhooks

Heimdall tells you when something about your agent changes, without anyone
opening the site: an evaluation finished, an agent got worse, a release is
blocked, a scheduled check failed, a worker went offline. Each event goes
to the organization's hooks (Integrations > Notifications) and, by email, to every
member who asked for it. A weekly digest sums up the week.

## Events

| Event | When | Default for |
|---|---|---|
| `evaluation.finished` | An evaluation ended: done, failed or cancelled. Sent once per evaluation, from the first status refresh after its last benchmark ends (a read, the end of a run job, or the server's one minute sweep). | Slack, Teams |
| `evaluation.regressed` | A finished evaluation found the agent worse: a failed `max_regression` gate rule, a significant "regressed" verdict against the other version, or, for a scheduled evaluation, a drop from the previous run of the same schedule (see below). | Slack, Teams, PagerDuty |
| `release.blocked` | The evaluated version cannot be released without an override (its gate failed, or the release policy blocks it). | Slack, Teams |
| `schedule.failed` | A scheduled check could not start (`refused`), could not finish (`error`) or ran and failed its check (`failed`). A trigger skipped because the previous run was still going is not a failure. | |
| `worker.offline` | A connected worker of the organization stopped sending heartbeats (hosted servers, checked every minute). Sent once per drop; it fires again only after the worker came back. | |
| `experiment.finished` | A single comparison ended (older event, kept for existing receivers). | signed JSON |
| `job.failed` | Any other background job failed. | |
| `ping` | The Send test button. Always delivered to the hook tested. | |

A PagerDuty hook takes only the problem events (`evaluation.regressed`,
`release.blocked`, `schedule.failed`, `worker.offline`, `job.failed`):
routine finishes never page anyone.

### Scheduled evaluations

A scheduled evaluation starts with the trigger and finishes later. When it
finishes, Heimdall compares it with the newest earlier finished run of the
same schedule (reruns are not counted) and:

- records the outcome on the schedule (its history shows the score, the
  change and the verdict instead of "started"), and sends the schedule's
  own alerts for that outcome;
- sends `evaluation.regressed` when the score dropped by more than the
  gate's `max_regression`, or, with no such rule, when the whole interval
  of the new score sits below the previous score. Noise between two runs of
  the same check does not alert.

## Payloads

Every delivery to a signed JSON hook is an `Event`:

```json
{
  "id": "evt_6f0c2a91d4e8b7a3",
  "type": "evaluation.regressed",
  "org_id": "org_x",
  "at": "2026-11-22T09:12:03Z",
  "data": { "...": "the event's data, below" },
  "summary": {
    "title": "support-bot v8 regressed: 54% (-7 points)",
    "text": "7 points lower, beyond its interval, than the previous run of schedule nightly (ev_41, 61%)",
    "severity": "error",
    "link": "https://heimdall.example.com/evaluations/ev_42",
    "fields": [{ "label": "Score", "value": "54%" }, { "label": "Change", "value": "-7 points vs previous run" }],
    "dedup_key": "heimdall-regressed-nightly"
  }
}
```

`summary` is the human line chat and paging formats show; JSON receivers
get it too. Headers on every format: `X-Heimdall-Event`,
`X-Heimdall-Delivery`, `X-Heimdall-Timestamp` and `X-Heimdall-Signature`
(`sha256=` HMAC of `"<timestamp>.<body>"` with the hook's secret).

### `evaluation.finished`, `evaluation.regressed`, `release.blocked`

`data` is the same evaluation notice for all three
(`handlers/evalnotify.go` `EvaluationNotice`). Scores are verified success
rates from 0 to 1 over clean runs; `change` is score minus the baseline's.

| Field | Meaning |
|---|---|
| `evaluation_id`, `name`, `status`, `mode` | The evaluation. `status` is `done`, `failed` or `cancelled`. |
| `agent`, `version` | The evaluated agent version (the last arm). |
| `score`, `ci_low`, `ci_high`, `runs` | Its score with a 95% interval, over `runs` clean runs. Absent when no benchmark finished. |
| `baseline` | `{source, version, evaluation_id, score, runs}`. `source` is `previous_run` (the previous run of the same schedule) or `baseline` (the gate's baseline: the other version, or the live one). |
| `change` | `score - baseline.score`. |
| `capabilities` | Per capability `{capability, score, baseline, change}`. |
| `gate` | `{state, summary}`: `pass`, `fail` or `pending`. |
| `regressed`, `regressions` | Whether it counts as a regression, and why (one sentence each). |
| `blocked` | Why a release needs an override; empty when it does not. |
| `cost_usd` | Clean cost of every arm; absent when any run's cost is unknown. |
| `schedule` | The schedule that started it, if any. |
| `link` | The report card: absolute when the server knows its public address. |
| `finished_at`, `note` | When it ended; `note` says why scores are missing, if they are. |

### `schedule.failed`

`{schedule, status, reason, job_id, exp_id, evaluation_id, link}`, where
`status` is `error`, `refused` or `failed`.

### `worker.offline`

`{worker_id, name, pool, host, last_seen, link}`.

## Formats

Each hook has a format, fixed when it is created (`format` in
`POST /api/bench/integrations/webhooks`):

| Format | URL | Body |
|---|---|---|
| `json` | Any https address | The signed `Event` above. |
| `slack` | A Slack incoming webhook (`https://hooks.slack.com/services/...`) | Block Kit: title, text, the fields and an "Open in Heimdall" button; `text` carries the title for notifications. |
| `teams` | A Teams Workflows webhook ("Post to a channel when a webhook request is received") | An Adaptive Card message with the same facts and an "Open in Heimdall" action. |
| `pagerduty` | Empty for `https://events.pagerduty.com/v2/enqueue` (or the EU address) | Events API v2 `trigger` with `routing_key` (the hook's `routing_key`, required, stored encrypted), a stable `dedup_key` per problem (per schedule or agent for regressions, per worker, per schedule for failures) so repeats join one incident, the summary, severity, `custom_details` and the link. Send test sends a `resolve` for an unused key: it checks the key and pages nobody. |

Bodies are rendered when the event is queued, so a retry sends the same
bytes and a PagerDuty dedup key never changes. Failed deliveries are
retried with backoff for 24 hours and listed under Recent deliveries.

## Email

Each member chooses for themselves (Integrations > Notifications > Email me, or
`GET/PUT /api/bench/notifications/email`): which of `evaluation.finished`,
`evaluation.regressed`, `release.blocked`, `schedule.failed` and
`worker.offline` reach them by email, and whether they get the weekly
digest. A member who never chose gets the digest and no event email.
Viewers can change their own choice too; nobody can change another
member's. `POST /api/bench/notifications/email/test` sends a test to the
caller's own address.

The weekly digest goes out after 09:00 UTC each Monday, once per ISO week,
for organizations where something finished that week: each agent's latest
score and its change over the week, regressions, blocked releases, failed
evaluations and the week's cost. A week with nothing finished sends
nothing.

Email needs a transport on the server (`deploy/hosted/.env`):

| Variable | What |
|---|---|
| `HEIMDALL_SMTP_URL` | `smtp://user:pass@host:587` (STARTTLS) or `smtps://user:pass@host:465` (TLS). |
| `HEIMDALL_RESEND_API_KEY` | Send through Resend instead. |
| `HEIMDALL_POSTMARK_TOKEN` | Send through Postmark (a server API token) instead. |
| `HEIMDALL_MAIL_FROM` | From address, e.g. `Heimdall <alerts@example.com>`. Required with any transport. |

The first transport set wins, in that order. With none set, nothing leaves
the server: every email is written to the server log, and the Email me
panel says so. A transport set without a valid `HEIMDALL_MAIL_FROM` is
refused at start (logged as an error) and the server falls back to the log.
The address in links comes from `auth.public_url`.

State lives in each project under `.heimdall/notifications/` (mode 0600):
`prefs.json` (choices), `history.jsonl` (finished evaluations, for the
digest) and `digest.json` (the last week sent).
