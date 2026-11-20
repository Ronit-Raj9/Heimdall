# Schedules and alerts

A release verdict compares two arms once. A schedule repeats an experiment on
a timetable, so a model or provider that drifts between releases is caught
too. Each scheduled run is an ordinary experiment: planned, frozen, run under
its spend cap, sealed and analysed exactly like one started with `check`. The
schedule only decides when it starts, refuses to start it when the day's
spend cap would be passed, and sends webhook alerts about the result.

## The schedules file

Schedules live in `schedules.yaml` in the project root (the folder that holds
`deploy/`). `-file` points the commands at another file.

```yaml
# Receivers for every schedule (optional).
alerts:
  - url_env: BENCH_ALERT_URL        # name of the variable that holds the URL
    secret_env: BENCH_ALERT_SECRET  # optional: name of the variable that holds the HMAC secret
    events: [verdict_regressed, policy_block, experiment_invalid, run_errors, schedule_failed]
    format: json                    # json (default) or slack
    timeout_s: 10                   # per attempt (default 10)

schedules:
  - name: nightly-codex
    experiment: experiments/codewins.reduced.yaml
    cron: "0 2 * * *"               # minute hour day-of-month month day-of-week
    timezone: Asia/Kolkata          # optional IANA zone; default is the host's local zone
    max_usd_per_day: 12             # required unless the experiment is a smoke experiment
    policy: policies/default.yaml   # optional: overrides the policy the experiment names
    fail_on: [regressed, invalid]   # optional: verdicts recorded as a failed check (this is the default)
    alerts:                         # optional: receivers for this schedule only
      - url_env: TEAM_SLACK_WEBHOOK
        format: slack
        events: [verdict_regressed, policy_block]
```

The file is decoded strictly: an unknown key (a misspelled `max_usd_per_day`,
say) is an error, never a silently missing cap. Paths are relative to the
project root.

A schedule's receivers are the file's top-level `alerts`, then the
schedule's own, then the `alerts:` list in `deploy/bench.yaml` if it has one
(same shape). All of them receive the events they subscribe to; an empty
`events` list subscribes to every event.

### Cron expressions

Five fields: minute (0-59), hour (0-23), day of month (1-31), month (1-12 or
`jan`-`dec`), day of week (0-7 or `sun`-`sat`; 0 and 7 are both Sunday). Each
field takes `*`, a value, a range `a-b`, a step `*/n`, `a-b/n` or `a/n`, or a
comma list of those. When both day of month and day of week are restricted
(neither starts with `*`), a day matches when either matches, as in classic
cron: `0 0 13 * 5` runs on the 13th and on every Friday. Times are in the
schedule's `timezone`. A local time skipped by a daylight saving change does
not run that day; a repeated one runs once. An expression that never matches
(`0 0 30 2 *`) is refused.

## Commands

```sh
bifrost-bench schedule add -name nightly-codex -experiment experiments/codewins.reduced.yaml \
    -cron "0 2 * * *" -max-usd-per-day 12 [-policy policies/default.yaml] [-fail-on regressed,invalid] [-timezone Asia/Kolkata]
bifrost-bench schedule list [-format table|json]
bifrost-bench schedule remove nightly-codex
bifrost-bench schedule run-now nightly-codex
bifrost-bench schedule daemon
```

Every subcommand also takes `-config deploy/bench.yaml` and `-file
<schedules.yaml>`. `add` validates the whole file before it saves it (the
experiment loads, the cron fires, the caps hold) and writes it atomically.
`add` does not set alert receivers; edit the file for those. `list` shows each
schedule's next time, today's spend against its cap, and its last trigger.
`run-now` runs one schedule once with every check a timed trigger has and
exits 0 when the check passed, 1 when it failed (a verdict in `fail_on`, or a
policy block), and 4 when it was refused, skipped or could not finish.

`schedule daemon` runs until interrupted. `bifrost-bench serve -schedules`
runs the same daemon in the background inside the server, off by default;
`-schedules-file <path>` points it at another file than `schedules.yaml` in
the project root. It needs the gateway, so `serve -offline -schedules` is
refused. A hosted server (`auth.mode: hosted`) runs no schedule daemon, so
it refuses `-schedules` and `-schedules-file` at startup. `run-now` and `daemon` need the live gateway,
like `check`.

## How the daemon runs

- Triggers run one at a time. Two schedules never run together, and a
  schedule never overlaps itself: a trigger that comes due while the same
  schedule is still running (in this process, or in another one, through a
  lock file) is recorded as `skipped`. Times missed while a long run was
  going are skipped and logged, never queued up.
- A run is the `check` flow: plan and freeze a new experiment from the file,
  run every slot, analyse it with the schedule's policy override, and update
  the results index.
- Every trigger appends one record to `<runs root>/.schedules/runs.jsonl`:
  when it was due, status (`ok`, `failed`, `error`, `refused`, `skipped`),
  the experiment id, verdict, policy decision, cost, today's spend and the
  alerts sent.

## Spend caps

Two caps apply, and both are needed for a run that can spend money.

1. **The experiment's own spend cap** (`budget.total_usd`). The engine
   enforces it already: a slot is leased only after a reservation keeps the
   spend so far plus every in-flight estimate under the cap, and leasing
   stops at the cap (`engine/scheduler.go`). A run whose cost could not be
   observed counts at the per-run estimate.
2. **The schedule's daily cap** (`max_usd_per_day`). Before each run the
   daemon adds today's recorded spend for the schedule and the experiment's
   whole spend cap; if the sum is above `max_usd_per_day`, the run is
   refused and a `schedule_failed` alert says why. After the run, its cost
   (the larger of the engine's spend and the sum of the sealed runs'
   `cost_usd`) is added to the ledger, `<runs root>/.schedules/spend.json`,
   keyed by schedule and by date in the schedule's timezone. The day rolls
   over at local midnight.

So that a forgotten schedule cannot spend without limit, a non-smoke
experiment must have both `max_usd_per_day` and `budget.total_usd`, and its
spend cap must not be above the daily cap (else no run could ever start).
`add` and the daemon refuse a schedule that breaks this. A smoke experiment
(`smoke: true`) with no `max_usd_per_day` needs neither. A smoke experiment
with a daily cap must also have `budget.total_usd`, so the daily cap holds:
the smoke flag alone does not prove that every part (a model-played user, a
judge) is scripted. A spend ledger that cannot be read refuses every run until it is
fixed, rather than letting runs through uncounted.

## Alerts

Events:

| Event | Sent when |
|---|---|
| `verdict_regressed` | the verdict is REGRESSED |
| `policy_block` | the release policy blocked the release |
| `experiment_invalid` | the verdict is INVALID |
| `run_errors` | at least one run ended `infra_error` or `aborted_infra`, or a slot hit a store error or lost its lease |
| `schedule_failed` | the run could not finish (plan, run or analysis failed), or it was refused (daily cap, unreadable experiment) |
| `live_signal_threshold` | X6 live scoring: a live signal's mean crossed its threshold ([live-scoring.md](live-scoring.md)) |
| `live_comparison_worse` | X6 live scoring: the comparison of two live versions decided the candidate worse |

A JSON alert is a POST with this body:

```json
{
  "event": "verdict_regressed",
  "schedule": "nightly-codex",
  "experiment": "e_codewins_reduced_x1y2",
  "decision": "regressed",
  "action": "block",
  "policy": "block",
  "link": "/verdict?exp=e_codewins_reduced_x1y2",
  "summary": {"runs": 24, "run_errors": 0, "cost_usd": 3.81, "spent_today_usd": 3.81, "max_usd_per_day": 12},
  "message": "verdict regressed, action block, 24 runs, 0 run errors, cost 3.8100 USD",
  "sent_at": "2026-11-16T02:41:07Z"
}
```

`link` is a path on the bench UI; prefix it with the address the UI is served
at. `format: slack` sends `{"text": "..."}` with the same facts on one line,
which Slack incoming webhooks and compatible receivers accept.

Headers: `X-Heimdall-Event` (the event), `X-Heimdall-Delivery` (attempt
number, from 1) and, when `secret_env` is set, `X-Heimdall-Signature:
sha256=<hex>`, the HMAC-SHA256 of the exact request body under the secret.
When `secret_env` names a variable that is not set, the alert is not sent at
all rather than sent unsigned.

Verifying the signature on the receiver (Python):

```python
import hashlib, hmac

def verify(secret: bytes, body: bytes, header: str) -> bool:
    want = "sha256=" + hmac.new(secret, body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, header)
```

In Go, `alerts.Verify(secret, body, header)` does the same.

### Delivery

Each receiver gets the first attempt and up to 3 retries, waiting 1 s, 2 s
and 4 s between them. A network error, a timeout, a 429 or a 5xx is retried;
any other status is final, and redirects are not followed. One receiver
failing never stops the next. Every attempt is appended to
`<runs root>/.schedules/deliveries.jsonl` with the time, event, schedule,
experiment, the name of the URL variable, attempt number, HTTP status and
error. The log never contains the URL or the secret; an error that would
carry the address is reduced to its cause ("dial failed: connection
refused").

## Safety

- No secret in a file: `url_env` and `secret_env` hold environment variable
  names. A value that is not a variable name (a URL pasted into `url_env`)
  is refused when the file is loaded. A URL with user info is refused too.
- Only `https` URLs to any host, or plain `http` to a loopback host
  (`localhost`, `127.0.0.0/8`, `::1`) for a local relay. Any other scheme is
  refused before anything is sent.
- Scheduling adds no network access to runs. The daemon only talks to the
  gateway (through the normal run path) and to the alert receivers.
- The state folder `<runs root>/.schedules` is outside every experiment and
  every sealed run, and is git-ignored with the runs root.
