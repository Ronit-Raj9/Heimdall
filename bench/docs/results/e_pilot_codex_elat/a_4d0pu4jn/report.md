# Bifrost Bench: INCONCLUSIVE, action NO_DECISION [LIVE]

Experiment `e_pilot_codex_elat`, analysis `a_4d0pu4jn`, grade source `original`.

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

## Instrument checks

| Check | Status | A | B | Threshold | Detail |
|---|---|---|---|---|---|
| usage_completeness | PASS | 13/13 (100.0%) | 11/11 (100.0%) | &gt;= 0.95 in every arm, arms at most 5 pp apart | usage completeness arm A 1.000, arm B 1.000 |
| dropped_logs | PASS | - | - | no rise over the experiment | no rise in the gateway drop counter (4 readings, counter 0 to 0) |
| timeout_parity | PASS | 0/2 (0.0%) | 0/2 (0.0%) | timeout rates at most 10 pp apart, or both at most 20% | timeout rate arm A 0.0% (0 of 2), arm B 0.0% (0 of 2), gap +0.0 pp on every condition |
| budget_parity | PASS | - | - | equal across arms unless an arm sets it | wall_s, turns, cpus and mem_gb are equal across arms or set by an arm |
| gateway_hygiene | UNKNOWN | - | - | no MCP auto-inject with MCP clients, no semantic cache, no multi-target routing | not recorded: the manifest predates the hygiene check or names no gateway |

## Release policy

None.

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 1/2 |
| B | 1/2 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 2 | 1/2 | 1/2 | +0.0 pp |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False-completion rate | runs with a success claim the verifier failed / runs with an observed false_completion value | 100.0% | 100.0% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | 50.0% | 50.0% |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.0 | 0.0 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 6.5 | 5.5 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 5.5 | 4.5 |
| Tokens, median | median of tokens per run / runs with observed tokens | 84952 | 69124 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 15.5 s | 15.2 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | $0.0406 | $0.0412 |
| Timeout rate | runs that hit the wall-clock budget (outcome timeout or agent timed out) / counted runs | 0.0% | 0.0% |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `4fbb7197a037ba506fde2e1b61df2bba2396ef223ea4e5134f0ad912c86be494` |
| Frozen at | 2026-11-14T00:10:16Z |
| Analysis created | 2026-11-14T01:23:13Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | LIVE |
