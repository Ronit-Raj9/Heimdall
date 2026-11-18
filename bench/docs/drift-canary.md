# Drift canary

An agent can get worse with no change by its team. A provider updates the
model behind an alias, a route starts falling back, a quantized deployment
replaces the full one, a tokenizer or a hidden system prompt changes. Public
postmortems describe exactly this: Anthropic's September 2025 postmortem says
normal validation "simply didn't capture the degradation users were
reporting", and Notion reported the same model behaving differently across
providers. A release gate never sees it, because nothing was released.

The drift canary re-runs a small, pinned suite on a schedule through the
Bifrost gateway and tests every look against a known-good baseline with the
same paired statistics the release verdict uses. When it alerts, the alert
says what moved: the served model under the same alias, the provider, the
latency, the token count at an identical prompt, new failure clusters and the
tasks that lost success.

Code: `bench/canary` (statistics, state machine, evidence, state files),
`cmd/bifrost-bench/canary.go` (the command, the engine executor, the schedule
daemon hook), `handlers/canary.go` (API), `ui/components/canaries` (page).

## Quick start

```
heimdall canary init nightly -agent agents/claude-code.yaml -suite coding-core@1.0.0 \
    -tasks t01,t02,t03,t04,t05,t06,t07,t08 -trials 3 \
    -target anthropic/claude-sonnet-4-5 -cron "0 6 * * *" -timezone Europe/London \
    -per-run-usd 2 -per-day-usd 5
heimdall canary baseline nightly        # run once, pin it as known-good
heimdall canary run nightly             # one look (and a confirmation after a flag)
heimdall canary status nightly          # state, baseline, looks, spend today
heimdall serve -schedules               # the daemon runs every scheduled canary
```

`bifrost-bench` works everywhere `heimdall` does.

## The definition: `canaries/<name>.yaml`

```yaml
name: nightly                     # must match the file name
description: Nightly check of the support agent on Sonnet through two providers
agent: agents/claude-code.yaml    # the frozen agent config
suite: coding-core@1.0.0
tasks: [t01, t02, t03, t04, t05, t06, t07, t08]   # or: tasks: all, with tier: nightly
trials: 3
conditions: [clean]               # default [clean]
targets:                          # one arm each; same model via two providers = equivalence check
  - {provider: anthropic, model: claude-sonnet-4-5}
  - {provider: bedrock, model: claude-sonnet-4-5}
schedule: {cron: "0 6 * * *", timezone: Europe/London}
budget: {per_run_usd: 2, per_day_usd: 5}  # both required unless smoke
alerts:
  - {url_env: CANARY_SLACK_URL, format: slack, events: [canary_drifted, canary_model_changed]}
sensitivity: normal               # low | normal | high
noise_floor: {latency_ms: 0.25, tokens: 0.10, cost_usd: 0.20, success: 0}
max_baseline_age_days: 30
smoke: false
```

The file is read strictly: an unknown key is an error, so a misspelled
`per_day_usd` can never pass as "no cap". Pick tasks the agent passes
reliably (at least three times in four): a canary measures a fall from a
known-good level, and tasks that already flip half the time hide a drift in
their own noise.

## Baseline

`canary baseline <name>` plans the definition, runs it once and pins the
sealed experiment as the known-good baseline. It records the experiment id,
the manifest hash, the frozen-config fingerprint and the served-model
fingerprint per target: the exact served model ids, the providers that
answered and any `system_fingerprint` the gateway rows carry. An invalid run
is never pinned.

`-from <exp_id>` re-pins an existing experiment instead of spending, but only
one frozen from the same config. Re-pinning keeps the history:
`baselines.jsonl` under `<runs>/.canaries/<name>/` has every baseline, the last
one current.

The frozen-config fingerprint covers the suite lock and evaluator
fingerprints, each target's resolved agent config hash and image, and the
counted slots with their task hashes. It leaves out the experiment id, time,
seed and spend cap, so every run of an unchanged definition has the same
fingerprint. When the team changes the agent config, its image, the suite or
the task selection, the next look plans, sees a different fingerprint and
stops with state `baseline_stale` before running anything: that change is the
team's own and belongs to a release verdict, not to a drift alarm.

## A look and the statistics, in plain words

Every look freezes a fresh experiment from the same definition, runs it
through the gateway and reads the sealed runs back, each checked against its
seal. For each target, watched condition and metric it then compares the
look with the baseline task by task:

1. Per task, average the metric over the trials, in the baseline and now.
2. Take the difference per task (now minus baseline) and run the exact paired
   sign-flip test (the release verdict's test, `stats.SignFlip`): if nothing
   changed, each task's difference is as likely to be positive as negative,
   so p is the share of the 2^n sign patterns at least as extreme as the one
   seen. With 8 tasks every pattern is enumerated; there is no approximation.
3. Metrics and their direction: success (higher is better), cost per run,
   tokens per run and model latency (median latency of the run's answered
   model requests, from the gateway rows; higher is worse). A significant
   change in the good direction is reported as `better` and never alarms.
4. Multiplicity: success is one family and cost, tokens and latency another;
   Holm's step-down holds each family to the per-look level, so several
   targets or metrics cannot add up to a false flag. Levels: `low` 0.05,
   `normal` 0.10, `high` 0.20.
5. Noise floor: provider inference is not deterministic (identical requests
   at temperature 0 give different completions; latency and token counts
   move on every call), so a change also has to pass its floor to flag:
   by default latency 25%, tokens 10%, cost 20% relative, success any drop.
   A significant change under its floor is `below_floor`.
6. A test that cannot ever reach its level, because too few tasks changed
   (with 5 changed tasks the smallest possible p is 0.0625), says
   `cannot_tell`, never "no change".

### Repeated looks: the confirmation rule

A canary looks every day, so even a test with a 1% false-flag rate would
alarm several times a year. The canary therefore never alarms on one look.
A flagged look moves the state to `watching` and immediately takes a
confirmation look (a second fresh experiment, still within the daily cap).
Only a second consecutive flag on the same target, condition and metric
moves the state to `drifted` and sends `canary_drifted`. A clear look goes
back to `steady`. Invalid, refused and stale looks carry no information and
never break or make a pair. If the daily cap leaves no room for the
confirmation, the state stays `watching` and the next scheduled look
confirms or clears it.

We chose confirmation over alpha spending because a monitor has no fixed
horizon to spend alpha over, and because confirmation also demands that the
same thing moved twice. The two looks share one baseline, so they are not
independent (an unlucky high baseline makes both looks look worse); the
simulation below measures the rate with that correlation included rather
than assuming independence.

### Simulated false-alarm rate and power

`heimdall canary simulate` runs the same `Compare` and `NextState` code on
synthetic looks: true per-task success rates evenly spread over 0.75 to 0.95
(reliable tasks) or over 0.15 to 0.85 (`-spread`), cost, tokens and latency
with log-normal run noise (sigma 0.15), a confirmation after every first
flag, and a re-pin after every alarm. 400 replications of 365 daily triggers,
8 tasks (test: `canary/simulate_test.go`, `CANARY_SIM=1` prints the table):

| sensitivity | trials | flag per look | false alarms per trigger | P(any false alarm, 30 days) | P(any, 365 days) |
|---|---|---|---|---|---|
| low (0.05) | 1 | 0.0071 | 0.00025 | 0.7% | 8.6% |
| low (0.05) | 3 | 0.0054 | 0.00011 | 0.3% | 3.9% |
| normal (0.10) | 1 | 0.0125 | 0.00038 | 1.1% | 12.8% |
| normal (0.10) | 3 | 0.0097 | 0.00026 | 0.8% | 9.1% |
| high (0.20) | 1 | 0.0222 | 0.00089 | 2.6% | 27.8% |
| high (0.20) | 3 | 0.0197 | 0.00103 | 3.1% | 31.5% |

With the wide spread of task rates the normal rates are 0.00045 (1 trial)
and 0.00055 (3 trials) per trigger. The flag-per-look column is also the
cost overhead: that share of triggers pays for one extra confirmation look.
The unit test holds normal sensitivity at or below 0.5% false alarms per
trigger.

Power, the share of drifts alarmed on the trigger the drift starts (within
three triggers in brackets), every task losing the same success points:

| drop | normal, 1 trial | normal, 3 trials | high, 3 trials |
|---|---|---|---|
| 20 points | 0.7% (2.0%) | 10.5% (22.3%) | 23.3% (45.3%) |
| 30 points | 2.0% (6.5%) | 33.3% (54.5%) | 59.0% (83.3%) |
| 45 points | 11.3% (26.8%) | 80.7% (95.3%) | 89.7% (98.5%) |
| 60 points | 40.5% (64.5%) | 97.8% (100%) | 99.3% (100%) |

A latency that doubles is caught on its first trigger in every replication.

## Limits: what 8 tasks can see

- **Only large drifts.** With 8 tasks and binary outcomes the sign-flip test
  needs at least 5 tasks moving the same way to reach level 0.10 at all
  (6 for 0.05). With 1 trial per task the canary reliably catches a collapse
  (most tasks failing), not a slide. With 3 trials per task, the detectable
  effect at 80% power on the first trigger is a drop of about 45 points of
  success; about 30 points within three triggers. A 10-point slide is
  invisible to an 8-task canary; use the nightly suite and trends for that.
- **Nondeterminism needs a noise floor.** Latency and token counts change on
  identical prompts; the floors keep that from flagging. Raise them on a
  noisy route, never to zero.
- **A drift is relative to the baseline.** A baseline pinned on a bad day
  makes later good days look better, not worse; pin from a run you checked,
  and re-pin when `baseline_stale` says the baseline is old.
- **Served-model evidence is only as good as the gateway rows.** Bifrost
  records the model each provider says it served; a provider that serves a
  different model under the same reported id cannot be seen there. That is
  why the canary also measures behaviour, not only metadata.
- **Canary experiments are ordinary sealed experiments.** They show in the
  experiment list, the results index and trends; the release verdict page
  does not apply to them (a single-target canary has one arm), and the
  canary's own comparison is what decides its state.
- **No real provider call has been made by this feature's tests.** Every
  test runs scripted parts at zero cost (rules of the adoption wave).

## States

| state | meaning |
|---|---|
| `no_baseline` | nothing pinned yet |
| `steady` | the last compared look found no drift |
| `watching` | one look flagged; a confirmation look decides |
| `drifted` | two consecutive looks flagged the same target and metric |
| `invalid` | the last look could not be compared: a harness problem (a seal that does not verify, an egress fire, an instrument gate above 5% of runs, graded coverage under 90%, or a baseline that no longer reads back) |
| `baseline_stale` | the frozen config changed since the baseline (nothing was run), or the baseline is older than `max_baseline_age_days` (the look still compared) |

The model fidelity gate fires when the served model differs from the
requested one. In a release experiment that is an instrument fault; for the
canary it is the signal itself, so it never makes a look invalid here and is
reported under "what changed" instead.

## What changed

Each compared look carries, per target, against the baseline:

- the served model ids under the same requested alias, and whether they
  changed (`canary_model_changed` alerts once per baseline);
- the providers that answered and any `system_fingerprint` in the raw rows;
- median model latency and its relative shift;
- the input token count of the first model request of each task: every run
  of a task sends the identical first prompt, so a change there is a
  tokenizer or provider-side prompt change;
- model request error rate and format errors (requests refused with 400 or
  422, tool calls with no name);
- per-task success flips;
- failure clusters (`bench/clusters` signatures) that the look has and the
  baseline did not.

The alert's `changes` list and the UI's "what changed" panel carry the same
lines, flagged tests first.

## Provider equivalence

Two targets with the same model through different providers run as two arms
of the same experiment, paired by task. Every look reports, per pair, the
sign-flip test of provider B minus provider A on success, cost, latency and
format errors (Holm over the four): `differs`, `no_difference_detected`, or
`cannot_tell` when too few tasks differ for any test to reach the level.
"No difference detected" is not proof that the providers are the same: with
8 tasks only large differences are detectable.

## Cost control

- `budget.per_run_usd` and `budget.per_day_usd` are required unless the
  canary is smoke; a definition without them does not load, so the daemon
  never schedules it.
- Before planning, the day's spend (every look, confirmations included, in
  the schedule's timezone) plus the whole per-run cap must fit the daily cap,
  because the engine may spend up to the cap before it stops leasing.
- After planning, the forecast (the high end of `bench/forecast` for the plan,
  or the most expensive of the canary's last five runs, whichever is larger)
  must fit the per-run cap; otherwise the look is refused before anything
  runs, since a run stopped at its cap would leave slots unrun and be invalid.
- The per-run cap is the frozen experiment's `budget.total_usd`.
- The schedule daemon also keeps its own ledger for the `canary-<name>`
  entry with `max_usd_per_day` set to the daily cap.

Smoke canaries (`smoke: true`, scripted agent and scripted model) run at zero
cost and may leave the caps at 0. `canaries/smoke-toy.yaml` is one; the
`-inject-fault <probe>` flag of `canary run`, refused on any canary that is
not smoke, replaces the watched condition with a fault probe so the alarm
path can be tested for free:

```
heimdall canary baseline smoke-toy
heimdall canary run smoke-toy                              # steady
heimdall canary run smoke-toy -inject-fault model-outage   # watching, then drifted
```

An injected look checks each run's probe receipt: if the forced fault did not
fire in every run (for example a shared gateway sent the requests through
another fault proxy slot), the look is `invalid`, never a pass of the alarm
path.

### What the zero-cost end-to-end run showed (18 Nov 2026)

The real pipeline (planner, scheduler, Docker runs, Bifrost v2.5.1, the fault
proxy's scripted model, seals, clusters), the scripted toy agent, the
`smoke-toy` canary, on a throwaway stack with no provider keys. Costs are the
scripted token counts priced at the snapshot; nothing was billed.

| step | experiment | result |
|---|---|---|
| `canary baseline smoke-toy` | `e_canary_smoke_toy_3hzq` | pinned, 8 runs, success 100% |
| `canary run smoke-toy` | `e_canary_smoke_toy_q5e9` | clear, state steady: success, cost and tokens unchanged on 8 tasks, latency p 0.41 |
| `canary run smoke-toy -inject-fault model-outage` | `e_canary_smoke_toy_xznu` | flagged, state watching: success 100% to 0% on 8 of 8 tasks, p 0.0078; cost down 36% (better, not an alarm) |
| (its confirmation look, taken at once) | `e_canary_smoke_toy_o63c` | flagged again on the same key, state drifted, exit code 1; what changed: model request errors 33% (from 0%), 8 tasks lost success, 8 new failure clusters "the model is unavailable" |

On the shared development stack, where other working copies leased the same
fault proxy slots at the same time, two injected looks were marked
`invalid` ("the injected fault model-outage did not fire in 3 of 8 runs"):
the guard above doing its job.

## Schedule, alerts, API and UI

- Every canary with a `schedule` becomes a daemon entry named
  `canary-<name>` (`schedule daemon` or `serve -schedules`); the definition
  file is the single source of its cron and caps.
- Alerts go through `bench/alerts` to the canary's receivers and to
  `bench.yaml`'s alerts: `canary_drifted` (on entering drifted),
  `canary_model_changed`, `experiment_invalid` (an invalid look) and
  `schedule_failed` (refused or failed). The JSON payload adds `canary` and
  `changes`; `decision` is the canary state.
- API: `GET /api/bench/canaries`, `GET /api/bench/canaries/{name}`,
  `POST /api/bench/canaries/{name}/run` (write token; 202, runs in the
  background; 409 while that canary runs).
- UI: `/canaries`, a list with state badges, the history chart (the trends
  chart, one line per target, success per look) and the "what changed"
  panel, the tests, the equivalence report and every look.
- State files: `<runs>/.canaries/<name>/records.jsonl` (every look) and
  `baselines.jsonl`; a lock file keeps one canary from overlapping itself.
- Exit codes of `canary run`: 0 steady or baseline_stale, 1 drifted,
  2 watching, 4 invalid, refused or error.
