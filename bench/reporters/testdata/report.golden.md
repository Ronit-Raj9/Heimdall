# Bifrost Bench: IMPROVED, action SHIP [RECORDED]

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

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_regression | clean | PASS | +4.2 pp | - |
| no_new_integrity_violation | - | PASS | unknown | 0 \| 0 runs |

## Invalid reasons

None.

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
