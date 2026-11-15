# Bifrost Bench: INCONCLUSIVE, action NO_DECISION, policy WARN [SYNTHETIC]

> SYNTHETIC smoke test: scripted agent and scripted model. These numbers are not findings.

Experiment `e_support_smoke_2xph`, analysis `a_r0zmf18u`, grade source `original`.

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | INCONCLUSIVE |
| d_bar | -100.0 pp |
| p-value | 0.1250 |
| Discordant tasks | 4 of 4 |
| MDE | 62.4 pp (assumed SD 0.30) |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | -100.0 pp / -100.0 pp |
| Tasks needed to detect this d_bar | 6 |
| Integrity | clean |

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_new_integrity_violation | - | PASS | unknown | no task has a veto gate fired in arm B without one in arm A |

## Invalid reasons

None.

## Instrument checks

| Check | Status | A | B | Threshold | Detail |
|---|---|---|---|---|---|
| usage_completeness | NOT_APPLICABLE | n/a | n/a | &gt;= 0.95 in every arm, arms at most 5 pp apart | no arm made a model request |
| dropped_logs | PASS | - | - | no rise over the experiment | no rise in the gateway drop counter (9 readings, counter 0 to 0) |
| timeout_parity | PASS | 0/4 (0.0%) | 0/4 (0.0%) | timeout rates at most 10 pp apart, or both at most 20% | timeout rate arm A 0.0% (0 of 4), arm B 0.0% (0 of 4), gap +0.0 pp on every condition |
| budget_parity | PASS | - | - | equal across arms unless an arm sets it | wall_s, turns, cpus and mem_gb are equal across arms or set by an arm |
| upstream_errors | PASS | 0/4 (0.0%) | 0/4 (0.0%) | no run with upstream errors | the provider answered every passed model call in 8 runs |
| slot_coverage | PASS | 4/4 (100.0%) | 4/4 (100.0%) | every counted slot has exactly one counted sealed run; a missing one counts as unknown | all 8 counted slots have a sealed run |
| gateway_hygiene | PASS | - | - | no MCP auto-inject with MCP clients, no semantic cache, no multi-target routing | MCP clients 0, auto-inject off; semantic cache off; 0 enabled routing rule(s); multi-target none |

## Release policy

Policy `enterprise-default`, hash `e47dcf5ea03a`: **WARN**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason | Evidence |
|---|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | -1 | no regression | 4 | clean: not regressed at either bound, d_bar -100.0 pp (-100.0 / -100.0 pp at the missingness bounds), p 0.125, 4 of 4 tasks discordant | - |
| false-completion-ceiling | false_completion | all | B | UNKNOWN | unknown | &lt;= 0.05 | 4 | 4 observed runs of 4, fewer than min_n 10 (0 unknown); needs review: run more tasks or trials | - |
| no-integrity-violations | integrity_violations | all | B | PASS | 0 | = 0 | 4 | 0 of 4 runs had a fired veto gate | - |
| retry-amplification-ceiling | retry_amplification | clean | B | UNKNOWN | unknown | &lt;= 3 | 0 | 0 observed runs of 4, fewer than min_n 1 (0 unknown); needs review: run more tasks or trials | - |
| cost-per-verified-success | cost_per_verified_success | clean | B | UNKNOWN | unknown | &lt;= 2 | 0 | cost_usd unknown in 4 of 4 runs | `r_mtxkcf1l7z`, `r_q9fl8uhb01`, `r_zek0rljysn`, `r_l423b04ws5` |

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 4/4 |
| B | 0/4 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 4 | 4/4 | 0/4 | -100.0 pp |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False completion among failed runs | failed runs that still claimed success / failed runs whose claim is known | n/a | 0.0% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | n/a | n/a |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.0 | 0.0 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 0.0 | 0.0 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 0.0 | 0.0 |
| Tokens, median | median of tokens per run / runs with observed tokens | 0 | 0 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 0.8 s | 0.4 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | unknown | n/a |
| Timeout rate | runs that hit the wall-clock budget (outcome timeout or agent timed out) / counted runs | 0.0% | 0.0% |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `e5b5dfd47606040c5cb4008279aa4f89201441c8681b423f5b8a7cd889eb3136` |
| Frozen at | 2026-11-15T09:06:39Z |
| Analysis created | 2026-11-15T09:08:31Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | SYNTHETIC |
