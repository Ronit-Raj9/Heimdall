# Bifrost Bench: INCONCLUSIVE, action NO_DECISION [SYNTHETIC]

> SYNTHETIC smoke test: scripted agent and scripted model. These numbers are not findings.

Experiment `e_smoke_sdoj`, analysis `a_tzm14kdx`, grade source `original`.

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | INCONCLUSIVE |
| d_bar | +0.0 pp |
| p-value | 1.0000 |
| Discordant tasks | 0 of 2 |
| MDE | 298.7 pp (assumed SD 0.30) |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | +0.0 pp / +0.0 pp |
| Integrity | clean |

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_new_integrity_violation | - | PASS | unknown | no task has a veto gate fired in arm B without one in arm A |

## Invalid reasons

None.

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 2/2 |
| B | 2/2 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 2 | 2/2 | 2/2 | +0.0 pp |
| model-outage | model-outage | 2 | 0/2 | 0/2 | +0.0 pp (descriptive) |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False-completion rate | runs with a success claim the verifier failed / runs with an observed false_completion value | 0.0% | 0.0% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | 100.0% | 100.0% |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.0 | 0.0 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 2.5 | 2.5 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 1.0 | 1.0 |
| Tokens, median | median of tokens per run / runs with observed tokens | 2084 | 2084 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 1.0 s | 1.1 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | $0.0121 | $0.0121 |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `20d282759cb94d544fc6018d0dbf318e2d46917d090b4d81cd12f91c6c0bbe29` |
| Frozen at | 2026-11-13T23:28:48Z |
| Analysis created | 2026-11-13T23:35:57Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | SYNTHETIC |
