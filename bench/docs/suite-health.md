# Suite health

A benchmark stays trustworthy only while its tasks do. Over time a verifier
can turn flaky, a task can become something every agent passes, an
environment can start failing for reasons that have nothing to do with the
agent. `suite health` reads every sealed run of a suite, across experiments,
and reports per task what the record says about each of those. Quarantine
takes a task out of new plans without touching the lock.

```sh
bifrost-bench suite health coding-core
bifrost-bench suite health -json coding-core > health.json
bifrost-bench suite health -auto-quarantine coding-core
bifrost-bench suite quarantine coding-core/t07 -reason "reference fails one run in three"
bifrost-bench suite restore coding-core/t07
```

`suite health` takes `-config` (the run store is `runs_root`, the suites
`benchmarks_root`), or `-root <benchmarks>` with `-runs <run store>`. The API
serves the same report at `GET /api/bench/suites/{id}/health`, and the suite
page shows it as a health column with a detail panel per task.

## Two kinds of instability, kept apart

The report never mixes these, in words or in numbers.

**Verifier flakiness** is the grader disagreeing with itself: the same output
graded pass once and fail another time, or the known-good reference failing.
A flaky verifier corrupts every verdict that touches the task, whichever agent
ran it. This is the serious finding, and the only one auto-quarantine acts on.

**Agent outcome variance** is one agent config passing a task in some trials
and failing it in others. That is normal: agents are stochastic, and repeated
trials exist to measure exactly this. It is reported as variance, never as a
defect of the task.

## What each column means

All numbers are computed from the counted sealed runs of every experiment
whose manifest names the suite id, for the task version currently loaded.
Runs of an older task version, and runs whose seal does not verify, are left
out and counted in the notes.

### Verifier

Evidence of flakiness, each kind with the experiment and run ids behind it:

| Kind | What was seen |
| --- | --- |
| `regrade_disagrees` | A regrade at the same suite version and grader version, on the same output hash, reached a different verdict than the original grade. A regrade under a new grader or suite version is a deliberate change and is not counted. Gate-only regrades (regate) say nothing about the verifier and are skipped. |
| `same_output_differs` | Runs whose grades record the same output hash (`grade.diff_sha256`) under the same suite and grader version got both pass and fail, in one experiment or across several. Runs are grouped by the hash the grade recorded, so answer, state and artifact tasks take part when their grader records one. |
| `admission_reference` | The task's last admission graded the reference several times in a row (`reference_results` in the lock) and not every result was pass. Mixed results are inconsistent; no pass at all means the reference never passes. |
| `oracle_failed` | A run of the scripted oracle agent, which applies the reference solution, was graded fail. |

`checks` counts the comparisons that could have shown flakiness (regrade
pairs, output groups of two or more runs, oracle runs, admission records), so
"ok (0 checks)" reads as "no evidence either way", not as "proven stable".

### Agent variance

Per agent config (grouped by config hash), non-synthetic scored runs only:
trials, passes and pass rate. `configs` counts configs with at least two
scored trials, `unstable` those with a pass rate strictly between 0 and 1,
and `mean_variance` is the mean Bernoulli variance p(1-p) across them.

### Infra

Every sealed attempt of the task's slots, aborted_infra retries and synthetic
runs included, since infrastructure trouble belongs to the task's environment
whoever runs it:

- `rate`: (aborted_infra + infra_error + invalid_evaluator) / attempts;
  above 10% the task is flagged `infra_trouble`;
- `timeout_rate`: runs where the agent hit its wall-time cap (outcome
  `timeout`) / attempts; above 20% the task is flagged `timeouts`.

A run whose grader returned `invalid_evaluator` counts here, not as verifier
flakiness: the grader said it could not decide, which is a failure to grade,
not a contradiction.

### Difficulty and discrimination

From non-synthetic scored runs (pass, fail, timeout, budget, crash, refusal,
integrity_violation), pooled over every config:

- `pass_rate` with a 95% Wilson score interval, `runs` and `configs`;
- `observed`: easy at a pass rate of 0.7 or more, hard at 0.3 or less, medium
  in between, and `unknown` below 6 scored runs or 2 distinct configs (both
  adjustable with `-min-runs` and `-min-configs`);
- `match`: the observed label against the task's declared `difficulty`
  (`match`, `mismatch`, `undeclared`, or `unknown` without enough data);
- `discrimination`: `always_pass` or `always_fail` when every config scored
  the same extreme (the task cannot tell agents apart: no information),
  `discriminating` otherwise, `single_config` with one config;
- `spread`: best minus worst config pass rate.

Synthetic runs are left out of difficulty and variance: experiments declared
`smoke: true` and every config on the scripted adapter (oracle, noop,
exploit, refuse, toy). A real agent run against the scripted model in an
experiment not marked smoke is not detected here; the leaderboard's ledger
check is the place for that.

### Flags

| Flag | Meaning |
| --- | --- |
| `flaky_verifier` | at least one piece of verifier evidence |
| `saturated` | with enough data, every config always passed |
| `dead` | with enough data, every config always failed (check the oracle and admission before calling the task hard) |
| `no_discrimination` | saturated or dead |
| `difficulty_mismatch` | observed difficulty differs from the declared one |
| `infra_trouble`, `timeouts` | above the thresholds above |
| `insufficient_data` | too few scored runs or configs to label difficulty |

The JSON report carries the thresholds it applied (`thresholds`) and a count
of tasks per flag (`counts`).

## Quarantine

The lock already has a `quarantined` task status, but setting it would change
the lock and its hash, which every manifest and seal written before refers
to. Quarantine is therefore recorded in a sidecar beside the lock,
`suite.health.json`, which is hashed into nothing:

```json
{
  "suite": "coding-core",
  "quarantined": {
    "t07": {"reason": "reference fails one run in three", "at": "2026-11-15T12:00:00Z", "source": "manual", "task_version": "1.0.0"}
  }
}
```

- `suite.lock` and its hash never change; re-locking a suite with a
  quarantined task gives the same bytes (tested against every committed
  lock).
- New plans skip a quarantined task under `tasks: all` and log one line per
  skipped task with its reason. An explicit task list that names one is
  refused unless `plan` or `run` is given `-include-quarantined`. `agent check`
  only warns.
- Existing manifests are unaffected: resuming an experiment runs its frozen
  slots, a remote worker's check that its project reproduces the manifest
  ignores quarantine, and analysis of old experiments never reads the
  sidecar.
- `suite restore` removes the entry; the file is deleted when it becomes
  empty, so a fully restored suite looks untouched.
- A broken sidecar fails the plan rather than silently running a
  quarantined task.

`suite health -auto-quarantine` quarantines every task with verifier
flakiness evidence that is not already quarantined, with `source: auto`, the
evidence kinds in the reason and the evidence lines in the record. It never
quarantines on difficulty, saturation, dead tasks, variance or infra: those
are findings to read, not proof that verdicts are wrong. A manual quarantine
is never replaced by an automatic one.

## API and UI

`GET /api/bench/suites/{id}/health` returns the same `SuiteHealth` JSON as
`suite health -json`, computed on each request (503 when the server has no
run store). `GET /api/bench/suites` adds to each task its `quarantine`
record, and to each suite `quarantined_count`. The suite page shows a health
column (flaky verifier badge, observed against declared difficulty,
discrimination, infra rate, quarantined state with reason) and a detail
panel per task.

## Where the code lives

| Part | File |
| --- | --- |
| Health computation, Wilson interval | `taskhealth/health.go`, `taskhealth/accumulate.go` |
| Sidecar read and write, quarantine, restore | `suite/healthfile.go` |
| Planner skip and refusal | `engine/taskscreen.go` |
| `suite health`, `quarantine`, `restore`, run store adapter | `cmd/bifrost-bench/suitehealth.go` |
| API route and suite summary fields | `handlers/suitehealth.go` |
| Payload types | `schemas/suitehealth.go` |
