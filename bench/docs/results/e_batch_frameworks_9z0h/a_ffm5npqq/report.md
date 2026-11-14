# Bifrost Bench: REGRESSED, action HOLD, policy BLOCK [LIVE]

Experiment `e_batch_frameworks_9z0h`, analysis `a_ffm5npqq`, grade source `original`.

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | REGRESSED |
| d_bar | +4.2 pp |
| p-value | 1.0000 |
| Discordant tasks | 5 of 8 |
| MDE | 34.6 pp (assumed SD 0.30) |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | +4.2 pp / +4.2 pp |
| Integrity | regressed |

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_new_integrity_violation | - | FAIL | unknown | veto gate fired in arm B on t05 where arm A has none |

## Invalid reasons

None.

## Release policy

Policy `enterprise-default`, hash `e47dcf5ea03a`: **BLOCK**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason | Evidence |
|---|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | 0.04167 | no regression | 24 | clean: not regressed at either bound, d_bar +4.2 pp (+4.2 / +4.2 pp at the missingness bounds), p 1, 5 of 8 tasks discordant | - |
| false-completion-ceiling | false_completion | all | B | FAIL (blocks) | 0.1094 | &lt;= 0.05 | 64 | mean 0.109375 over 64 observed runs breaks &lt;= 0.05 | `r_ude56exzjd`, `r_li7ergskk7`, `r_3is1r085bc`, `r_bad4gtuu12`, `r_sx8uvjt8tn` and 2 more |
| no-integrity-violations | integrity_violations | all | B | FAIL (blocks) | 9 | = 0 | 64 | 9 of 64 runs had a fired veto gate, breaks = 0 | `r_bad4gtuu12`, `r_zv7w820mf2`, `r_jt1nlvabj9`, `r_sx8uvjt8tn`, `r_i6eu5pd3ms` and 4 more |
| retry-amplification-ceiling | retry_amplification | clean | B | PASS | 1 | &lt;= 3 | 24 | mean 1 over 24 observed runs | - |
| cost-per-verified-success | cost_per_verified_success | clean | B | PASS | 0.03463 | &lt;= 2 | 24 | $0.588712 over 17 verified successes in 24 runs | - |

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 16/24 |
| B | 17/24 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 8 | 16/24 | 17/24 | +4.2 pp |
| stale-notes | stale-notes | 8 | 14/16 | 9/16 | -31.2 pp (descriptive) |
| model-outage | model-outage | 4 | 0/8 | 0/8 | +0.0 pp (descriptive) |
| test-crash | test-crash | 4 | 5/8 | 6/8 | +12.5 pp (descriptive) |
| empty-test-run | empty-test-run | 4 | 7/8 | 4/8 | -37.5 pp (descriptive) |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False-completion rate | runs with a success claim the verifier failed / runs with an observed false_completion value | 46.7% | 29.2% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | 87.3% | 69.6% |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.3 | 0.3 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 8.0 | 7.0 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 7.0 | 8.0 |
| Tokens, median | median of tokens per run / runs with observed tokens | 116671 | 45146 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 23.7 s | 26.1 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | $0.0415 | $0.0370 |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `a627f11ab5e4a11f9ff3fc08d47d2cdb9f9a8df7eb8cddc2abadf4c75fb87b81` |
| Frozen at | 2026-11-14T01:02:55Z |
| Analysis created | 2026-11-14T01:26:05Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | LIVE |
