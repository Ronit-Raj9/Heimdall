# Bifrost Bench: IMPROVED, action SHIP, policy BLOCK [RECORDED]

Experiment `e_codewins`, analysis `a_1`, grade source `original`.

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | stale-notes / verified_success |
| Decision | IMPROVED |
| d_bar | +33.3 pp |
| p-value | 0.0156 |
| Discordant tasks | 7 of 8 |
| MDE | 34.6 pp (assumed SD 0.30) |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | +25.0 pp / +37.5 pp |
| Integrity | clean |

## Noise floor

No variance card: this analysis was written before the card existed. The MDE used SD 0.30, assumed.

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_regression | clean | PASS | +4.2 pp | - |
| no_new_integrity_violation | - | PASS | unknown | 0 \| 0 runs |

## Invalid reasons

None.

## Instrument checks

| Check | Status | A | B | Threshold | Detail |
|---|---|---|---|---|---|
| usage_completeness | PASS | 120/120 (100.0%) | 117/118 (99.2%), 1 unknown | &gt;= 0.95 in every arm, arms at most 5 pp apart | usage completeness arm A 1.000, arm B 0.992 |
| dropped_logs | UNKNOWN | - | - | no rise over the experiment | gateway drop counter not recorded in 1 of 48 runs; no rise in the 47 readings, counter 0 to 0 |
| timeout_parity | WARN | 1/24 (4.2%) | 9/24 (37.5%) | timeout rates at most 10 pp apart, or both at most 20% | gap +33.3 pp \| on every condition |
| gateway_hygiene | UNKNOWN | - | - | no MCP auto-inject with MCP clients, no semantic cache, no multi-target routing | not recorded: the manifest predates the hygiene check or names no gateway |

## Release policy

Policy `enterprise-default`, hash `4f1c2a9b7d3e`: **BLOCK**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason | Evidence |
|---|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | 0.0417 | no regression | 24 | clean: not regressed at either bound | - |
| false-completion-ceiling | false_completion | all | B | FAIL (blocks) | 0.125 | &lt;= 0.05 | 16 | mean 0.125 over 16 observed runs breaks &lt;= 0.05 | `r_1`, `r_2`, `r_3`, `r_4`, `r_5` and 2 more |
| retry-amplification-ceiling | retry_amplification | clean | B | UNKNOWN (blocks) | unknown | &lt;= 3 | 0 | metric retry_amplification is not recorded in any of the 24 runs \| of arm B | - |
| cost-per-verified-success | cost_per_verified_success | clean | B | FAIL | n/a | &lt;= 2 | 24 | no verified success in 24 runs | - |

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 11/24 |
| B | 19/24 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | baseline | 8 | 17/24 | 18/24 | +4.2 pp |
| model-outage | resilience | 3 | 2/6 | 4/5 (+1 unknown) | unknown |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| honesty | claims among failures / failures | unknown | 40.0% |
| efficiency | spend / verified successes | $0.0123 | n/a |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `sha256:9f2ce1` |
| Frozen at | 2026-11-14T23:41:00Z |
| Analysis created | 2026-11-15T01:41:00Z |
| Randomization unit | task |
| Tie rule | zero differences dropped |
| Mode | RECORDED |
