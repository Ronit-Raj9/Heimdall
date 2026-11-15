# Leaderboard

The leaderboard ranks **agent configurations** across experiments. An experiment answers "is B better than A?" for two arms; the leaderboard pools every sealed run of one suite version under one condition, from every experiment in the run folder, and lines up every configuration that ran. It is a reading of the evidence already sealed, not a new measurement: nothing runs and nothing is written.

There is **no blended score**. Entries are ordered by verified success; steps, tool calls, tokens, cost, time, false completion and safe success sit beside it as their own columns. Rank alone is never a claim that one agent beats another: the tier column says whether an entry can be told apart from the leader at all.

## Where it lives

| Part | Path |
|---|---|
| Compute and Markdown | `leaderboard/` (pure: reads manifests, runs, `metrics.json` and `proxy.ledger.jsonl` through `leaderboard.Source`, and regrades through `leaderboard.RegradeSource`) |
| API | `GET /api/bench/leaderboard?suite=&condition=&scope=&synthetic=&grade_source=&k=`, `handlers/leaderboard.go`, returns `schemas.Leaderboard` plus `mode` |
| CLI | `bifrost-bench leaderboard [-config deploy/bench.yaml] [-suite id@version] [-condition clean] [-scope all\|common] [-synthetic exclude\|only] [-grade-source original\|latest\|reviewed] [-k n] [-format md\|json] [-out file]` |
| Contract | `schemas/leaderboard.go`, `handlers/testdata/contract/Leaderboard*.keys`, `ui/lib/types/bench.ts` |

Absent parameters take the defaults: the newest suite with runs, `clean`, `all`, `exclude`, grade source `original`, and each entry's own k. A malformed parameter is a 400 (CLI: exit 64): so is a `grade_source` other than `original` or `latest` (a regrade id is not accepted here; `latest` picks one per experiment), and a `k` that is not an integer of 2 or more. A well-formed suite or condition with no runs is an empty board, so the picker still lists what exists. The CLI reads the run folder only: no gateway, no Docker, and it writes nothing into it.

## What one entry is

One entry per **resolved agent config hash**: the manifest's `arms[x].config_hash`, or `compatibility.config_hash` for the compatibility agent. Two arms of different experiments with the same resolved config (same image, argv, model, notes, budgets, gateway policy) are the same entry, and their runs pool. An A/A copy of an arm pools with A. The agent id, model and label shown come from the newest experiment that contributed a graded run.

## Which runs count

For each slot the counted run is picked as the store picks it: the first sealed run whose outcome is not `aborted_infra`, else the last attempt when it is a sealed `aborted_infra`. A slot with only unsealed runs is in flight. Planned slots with no run are not runs and are not mentioned.

A run is **graded** when it is sealed, its outcome scores (pass 1; fail, timeout, budget, crash, refusal and `integrity_violation` 0, intention to treat as in DESIGN.md 9.3), and its experiment's verdict is not invalid. A timeout or a crash counts as a failure: leaving it out would let an agent climb by crashing on the tasks it cannot solve.

Every other run of the board's suite and condition is counted in `excluded`, by reason:

| Reason | Meaning |
|---|---|
| `experiment verdict invalid` | The experiment's newest analysis decided INVALID: an instrument gate fired, so none of its runs are evidence. Wins over every other reason |
| `not sealed` | The slot's run is still in flight |
| `infra_error`, `invalid_evaluator`, `unknown` | The outcome gives no score (the bench, the gateway or the grader failed, or the provider became unreachable and the run ended on it: `docs/upstream-failures.md`) |
| `aborted_infra` | Every attempt failed on infrastructure: before the agent started, or with no model call answered because the provider was unreachable |
| `external APIs live (not repeatable)` | The experiment set `external: live` or `external: record`, so its runs called real external hosts and cannot be repeated (`docs/external-apis.md`). Replay runs count normally |

A run's success is its `verified_success` metric (the default primary endpoint: hidden verifier passed under the clean regrade and no veto gate fired), read from `metrics.json`; a run measured before that metric existed falls back to its outcome. Which outcome and metrics a run is read with is the grade source, below.

## Grade source

A sealed run is never rewritten. When a gate rule is refined after the fact, `bifrost-bench regate` writes a child artifact beside the sealed runs: `regrades/<regrade_id>/regrade.json` (gate versions, note, `created_at`) and `regrades/<regrade_id>/runs/<run_id>/regate.json` (the recomputed gates, outcome, Y and the metrics they change, hashed and tied to the run's seal). `grade_source` picks which the board reads:

| Value | What each run is read with |
|---|---|
| `original` (default) | The outcome in `run.json` and `metrics.json`, as sealed |
| `latest` | Per experiment, its newest regrade: the one with the latest `created_at` in `regrade.json`, ties broken by the larger regrade id, so the choice never depends on listing order. A run that regrade has a result for takes its outcome, Y and metrics; any other run keeps its original grade |

The regrade is read exactly as `bifrost-bench check -grade-source <regrade_id>` reads it (`store.FSStore.SealedRuns`), so the regate's hash and parent seal are checked; a regate that does not verify fails the board rather than falling back silently. A regrade folder without `regrade.json` (interrupted before its first write) is skipped. Runs a regrade has no result for: compatibility runs, which analysis never reads and so are never regated, and runs sealed after the regrade was written. An experiment with no regrade reads as original. The experiment-verdict exclusion still reads the newest analysis, whatever grade source that analysis used.

The board says what it read: `grade_source` is the value asked for, and `grade_sources` lists every experiment with runs on the board (graded or excluded, newest experiment first) with the `source` used (`original` or the regrade id), its `runs` on the board and how many of them were `regraded_runs`. The Markdown report ends with the same list.

## Columns

| Column | Definition |
|---|---|
| `success` | Mean over tasks of the per-task rate (verified successes / graded runs on that task). Bounds: descriptive 95% paired-t interval over the task rates (`stats.PairedT`), clipped to [0, 1], absent with one task. The task is the unit, as in the verdict |
| `passes` | Verified successes over graded runs, pooled across tasks |
| `trials` | The k of `reliability`: the query's `k` when set, else the entry's default k (below) |
| `reliability` | Mean over tasks of the unbiased pass^k with k = `trials` (`stats.PassHatK`): the chance k tries in a row all succeed. Every pooled graded run of a task enters the estimate. Unknown when k < 2, or when any task has fewer than k graded runs (one task short makes it unknown rather than a mean over the tasks that happen to have more runs). The Markdown cell states k as `(pass^k)` |
| `metrics.steps`, `tool_calls`, `tokens`, `cost_usd`, `wall_time_s` | Median over graded runs of the run's metric. `tokens` is the bench's total metric: fresh input + cache read + cache write + output over every message request |
| `metrics.false_completion` | Share of runs that claimed success while the hidden verifier failed, over the runs whose outcome and claim are known. False completion is not applicable on a run whose verifier passed, and such a run is never a false completion, so it counts as 0, as the release policy counts it (`policy/evaluate.go`, `notApplicableValue`). A failed run whose claim could not be read is unknown and left out, with the count in the reason. Labelled "False completion (share of runs)" |
| `metrics.safe_success` | Mean of the run's 0/1 value over the runs that observed it; a not-applicable value is left out |
| `metrics.cost_per_success` | Observed `cost_usd` summed over every graded run, failures included, divided by verified successes. Not applicable with no success ("undefined (0 of N)"); unknown when any run's cost is unknown, because a partial sum would flatter the entry |
| `resilience` | Clean board only. For each fault condition this config also ran: mean over the tasks graded both clean and under it of (rate under it minus clean rate), as a fraction; reason "n tasks". The probe effect of DESIGN.md 9.1, pooled across experiments |

### Default k

pass^k needs a k. Pooling made the old default (the fewest graded runs on any task) grow with every experiment added, up to pass^8 or pass^9, where almost any agent scores near zero and the number stops saying anything. The default is now tied to the design instead:

- For each experiment arm an entry's runs in scope came from, its trials per task under the condition are read from the manifest's slots (the highest trial number of that arm and condition; from the runs' slots when the manifest lists none).
- `trials` is the fewest of these among the arms that repeat tasks (2 or more trials per task). An arm with one trial per task, such as a pilot, measured no repeats: it does not set k, though its runs still enter the estimate. When no arm repeats, k is 1 and reliability is unknown.
- k = min(trials, 3). Beyond three tries in a row the estimate says more about the sample size than about the agent.

The fewest rather than the most keeps k within what every repeating experiment ran, so each pooled task normally has at least k graded runs. Entries can end up with different k; the column states each one. Set `k` (API) or `-k` (CLI) to put every entry on the same k.

A value no run observed is `unknown`, never 0. When some runs observed it and some did not, the value is the aggregate of the observed ones and its reason says how many were unknown ("1 of 4 runs unknown").

## Scope

- `all`: each entry over its own tasks. `common_tasks` (the tasks every listed entry ran under the condition) is still reported.
- `common`: every entry is restricted to `common_tasks`, so every success rate is over the same tasks. Prefer it whenever entries ran different task sets; with `all`, an entry that ran only the easy tasks can rank above one that ran all of them.

## Order and tiers

Order: success descending (unknown last), then lower cost per success (anything not observed last), then more graded runs, then config hash. The order is total, so the same run folder always gives the same board.

Tiers answer "can this entry be told apart from the leader?":

1. For each entry after the leader, take the tasks both have in scope. d_t = the leader's rate minus the entry's rate on task t.
2. `stats.SignFlip(d)` gives the exact two-sided p, the same test the release verdict uses (DESIGN.md 9.3).
3. The p-values of all compared entries are Holm-adjusted together (step-down, monotone), because a board with many entries makes many comparisons. `p_vs_leader` is the adjusted p.
4. **Tier 2** (significantly behind) when the adjusted p is below alpha (0.05) and the leader is ahead on those tasks. Everything else is **tier 1**, with the reason in plain words:
   - `too few common tasks (n) to separate`: with n differing tasks the smallest possible p is 2 x 0.5^n (`stats.MinPossibleP`), which stays above 0.05 below 6. No split could separate them.
   - `same success as the leader on all n common tasks`.
   - `no tasks in common with the leader`: there is nothing to compare; use scope `common` or run the missing tasks.
   - `p 0.62 on 8 common tasks`: compared, not significant.
   - `ahead of the leader on them: the task sets differ`: with scope `all` an entry can trail on its own tasks yet beat the leader on the shared ones. It is not "behind".

Tier 1 is not a tie claim. It means the evidence cannot separate the two; more tasks might.

## Synthetic runs

`synthetic=exclude` (default) shows live runs only; `synthetic=only` shows scripted runs only, under a SYNTHETIC banner, because their numbers exercise the pipeline and are not findings (DESIGN.md 19.1).

The unit of classification is the (experiment, config) pair. It is synthetic when:

- the config uses the scripted adapter (oracle, noop, exploit, refuse, toy), or
- any sealed run of that config in that experiment ran against the fault proxy's scripted model, as its proxy ledger shows.

Every ledger line the fault proxy writes carries `upstream`: `real` when the provider's requests go to its API (with a key, or a keyless provider such as ollama), `scripted` when the scripted model answers them. It names the provider's route rather than who wrote that one response, so injected and rejected lines carry it too. A run whose ledger has any labelled line is scripted exactly when a line says `scripted`; no fingerprint is consulted. Ledgers written before the field existed have no labelled line and fall back to the scripted model's fingerprint: a passed `message` request answered 200 with exactly 32 output tokens and a response id of the form `msg_`, `resp_` or `chatcmpl-` followed by 24 lowercase base-36 characters (`faultproxy/scripted.go`, `utils.NewID`). Real Anthropic message ids mix upper and lower case; real OpenAI response ids carry 50 hex characters. A pair rather than a config is the unit so that a dry run of a real agent against the scripted model (no provider key) makes that experiment's runs synthetic without hiding the same config's live runs in other experiments. Runs of one pair share the proxy's upstream, so a run that made no model call follows the others.

Every list on a board (suites, conditions, entries, exclusions) is computed after this filter: a live board never mentions scripted runs, and the other way round.

## Honest limits

- **Pooling across experiments is exploratory.** Each experiment randomized arm order within its own tasks; runs pooled from different experiments, days or gateway versions were not randomized against each other. The release verdict remains the only confirmatory claim (DESIGN.md 9.3: "A re-analysis that pools old and new runs is labelled exploratory").
- **Tiers compare with the leader only.** Two tier-1 entries are not claimed equal to each other, and two tier-2 entries are not ordered by a test.
- **The leader is the point estimate's leader.** Who leads can change with one more run; the tiers, not the ranks, carry the uncertainty.
- **Few tasks, few tiers.** With fewer than 6 differing tasks no entry can reach tier 2, whatever its rate. A board with small experiments is mostly tier 1, by design.
- **The success interval is descriptive.** The paired-t interval over tasks is approximate, as in the verdict, and takes no part in the tiers.
- **Synthetic detection on older runs is a fingerprint.** Runs recorded by a fault proxy that predates the ledger `upstream` field are classified by the scripted model's fixed usage and id format; newer runs use the field.
- **Entries can differ in k.** The default k follows each entry's own experiments, so a 2-trial entry and a 3-trial entry report pass^2 and pass^3. Compare reliability across entries only at the same k, or pass `k`.
- **Grade source `reviewed`** applies human reviews on top of the sealed grades and never reads a regrade; `latest` never applies reviews. The board then reports `reviewed_runs` and `review_changed_runs`, and `reviewed_runs` per experiment in `grade_sources` (`docs/human-review.md`).
- **Regrades are per experiment.** With grade source `latest`, an experiment that was never regated reads as sealed beside experiments that were. `grade_sources` says which is which.
- **Computed on request.** Every request reads each run's `metrics.json` and, for live configs, `proxy.ledger.jsonl`; with grade source `latest`, each regated experiment's sealed runs once more through the store's reader. That is fine for hundreds of runs; a very large run folder would want a cache.
