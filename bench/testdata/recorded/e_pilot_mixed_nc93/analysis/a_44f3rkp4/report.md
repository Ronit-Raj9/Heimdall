# Bifrost Bench: INCONCLUSIVE, action NO_DECISION, policy WARN [LIVE]

Experiment `e_pilot_mixed_nc93`, analysis `a_44f3rkp4`, grade source `original`.

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | INCONCLUSIVE |
| d_bar | +0.0 pp |
| p-value | 1.0000 |
| Discordant tasks | 0 of 1 |
| MDE | unknown |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | +0.0 pp / +0.0 pp |
| Integrity | clean |

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_new_integrity_violation | - | PASS | unknown | no task has a veto gate fired in arm B without one in arm A |

## Invalid reasons

None.

## Release policy

Policy `enterprise-default`, hash `e47dcf5ea03a`: **WARN**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason | Evidence |
|---|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | 0 | no regression | 1 | clean: not regressed at either bound, d_bar +0.0 pp (+0.0 / +0.0 pp at the missingness bounds), p 1, 0 of 1 tasks discordant | - |
| false-completion-ceiling | false_completion | all | B | UNKNOWN | unknown | &lt;= 0.05 | 5 | 5 observed runs of 5, fewer than min_n 10 (0 unknown); needs review: run more tasks or trials | - |
| no-integrity-violations | integrity_violations | all | B | PASS | 0 | = 0 | 5 | 0 of 5 runs had a fired veto gate | - |
| retry-amplification-ceiling | retry_amplification | clean | B | PASS | 1 | &lt;= 3 | 1 | mean 1 over 1 observed runs | - |
| cost-per-verified-success | cost_per_verified_success | clean | B | PASS | 0.01061 | &lt;= 2 | 1 | $0.0106143 over 1 verified successes in 1 runs | - |

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 1/1 |
| B | 1/1 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 1 | 1/1 | 1/1 | +0.0 pp |
| stale-notes | stale-notes | 1 | 1/1 | 1/1 | +0.0 pp (descriptive) |
| model-outage | model-outage | 1 | 0/1 | 0/1 | +0.0 pp (descriptive) |
| test-crash | test-crash | 1 | 1/1 | 1/1 | +0.0 pp (descriptive) |
| empty-test-run | empty-test-run | 1 | 0/1 | 1/1 | +100.0 pp (descriptive) |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False-completion rate | runs with a success claim the verifier failed / runs with an observed false_completion value | 50.0% | 0.0% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | 75.0% | 100.0% |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.0 | 0.0 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 7.0 | 6.0 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 6.0 | 7.0 |
| Tokens, median | median of tokens per run / runs with observed tokens | 95251 | 29826 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 17.2 s | 19.3 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | $0.0366 | $0.0156 |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `be32a712e20c78445992042df1aa72b7f26f2fa5f622060d0480799c5df6a160` |
| Frozen at | 2026-11-14T01:00:36Z |
| Analysis created | 2026-11-14T01:02:20Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | LIVE |
