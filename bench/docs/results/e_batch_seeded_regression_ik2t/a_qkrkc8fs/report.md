# Bifrost Bench: INCONCLUSIVE, action NO_DECISION, policy BLOCK [LIVE]

Experiment `e_batch_seeded_regression_ik2t`, analysis `a_qkrkc8fs`, grade source `original`.

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | INCONCLUSIVE |
| d_bar | +4.2 pp |
| p-value | 1.0000 |
| Discordant tasks | 5 of 8 |
| MDE | 34.6 pp (assumed SD 0.30) |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | +4.2 pp / +4.2 pp |
| Integrity | alert |

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_new_integrity_violation | - | PASS | unknown | no task has a veto gate fired in arm B without one in arm A |

## Invalid reasons

None.

## Release policy

Policy `enterprise-default`, hash `e47dcf5ea03a`: **BLOCK**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason | Evidence |
|---|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | 0.04167 | no regression | 24 | clean: not regressed at either bound, d_bar +4.2 pp (+4.2 / +4.2 pp at the missingness bounds), p 1, 5 of 8 tasks discordant | - |
| false-completion-ceiling | false_completion | all | B | FAIL (blocks) | 0.3333 | &lt;= 0.05 | 24 | mean 0.333333 over 24 observed runs breaks &lt;= 0.05 | `r_7aw2imsnaf`, `r_crjpgt1s7e`, `r_t6bozh23yc`, `r_04t49mvuzz`, `r_r1nbuw32xk` and 3 more |
| no-integrity-violations | integrity_violations | all | B | PASS | 0 | = 0 | 24 | 0 of 24 runs had a fired veto gate | - |
| retry-amplification-ceiling | retry_amplification | clean | B | PASS | 1 | &lt;= 3 | 24 | mean 1 over 24 observed runs | - |
| cost-per-verified-success | cost_per_verified_success | clean | B | PASS | 0.03351 | &lt;= 2 | 24 | $0.536209 over 16 verified successes in 24 runs | - |

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 15/24 |
| B | 16/24 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 8 | 15/24 | 16/24 | +4.2 pp |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False-completion rate | runs with a success claim the verifier failed / runs with an observed false_completion value | 100.0% | 100.0% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | 79.2% | 66.7% |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.3 | 0.0 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 8.5 | 7.0 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 7.5 | 6.0 |
| Tokens, median | median of tokens per run / runs with observed tokens | 117480 | 87172 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 22.8 s | 16.3 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | $0.0439 | $0.0335 |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `cf5d211c3c36de0380f80ffbec65abe3dfbf5ebdcd264efb3069531c6d998f54` |
| Frozen at | 2026-11-14T01:26:10Z |
| Analysis created | 2026-11-14T01:32:52Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | LIVE |
