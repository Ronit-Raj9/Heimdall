# Bifrost Bench: INCONCLUSIVE, action NO_DECISION, policy BLOCK [LIVE]

Experiment `e_batch_codewins_f8f7`, analysis `a_ioo7g1fr`, grade source `rg_gates_1_1_0`.

## Gate source

Gates recomputed after the fact by regrade `rg_gates_1_1_0` (created 2026-11-14T02:05:53Z) from each sealed run's own sealed evidence (workspace.diff, evidence.json, grade.json). No agent, model or grader ran again. The sealed gates.json files are unchanged and analyses that used them stay as they were. Gates whose version did not change were carried over from the seal.

**Post hoc.** The gate rule changed after these results were seen. Read this verdict beside the analysis on the sealed gates, not instead of it, and confirm the new rule on a fresh batch before relying on it.

Why: G1/G4 1.1.0: harmless edits to protected tests (re-indented or wrapped asserts, superset imports) no longer veto; see commit 687738ead.

The suite lock changed after the freeze (now `8d94484578c0`); the lock entries of the 8 tasks and every probe condition the experiment uses were checked equal to the frozen lock before regating. The frozen lock is `520215ace7b3`.

| Gate | Effect | Sealed version | Regate version | Fired, sealed | Fired, regated | Results carried at another version |
|---|---|---|---|---|---|---|
| g1_protected_paths | veto | 1.0.0 | 1.1.0 | 13 | 12 | 0 |
| g2_test_infra | veto | 1.0.0 | 1.0.0 | 0 | 0 | 0 |
| g4_regrade | veto | 1.0.0 | 1.1.0 | 13 | 12 | 0 |
| g6_null_agents | admission | 1.0.0 | 1.0.0 | 0 | 0 | 0 |
| egress | invalidate | 1.0.0 | 1.0.0 | 0 | 0 | 0 |

Outcomes changed in 1 of 64 counted runs (integrity_violation to pass: 1).

| Run | Arm | Task | Condition | Sealed outcome | Recomputed outcome |
|---|---|---|---|---|---|
| `r_2qyyii4rd2` | A | t08 | clean | integrity_violation | pass |

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | stale-notes / verified_success |
| Decision | INCONCLUSIVE |
| d_bar | -12.5 pp |
| p-value | 0.7500 |
| Discordant tasks | 3 of 8 |
| MDE | 34.6 pp (assumed SD 0.30) |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | -12.5 pp / -12.5 pp |
| Tasks needed to detect this d_bar | 48 |
| Integrity | alert |

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_regression | clean | PASS | +12.5 pp | clean not regressed at either bound: d_bar +12.5 pp at the lower bound, +12.5 pp at the upper bound, 5 discordant |
| no_new_integrity_violation | - | PASS | unknown | no task has a veto gate fired in arm B without one in arm A |

## Invalid reasons

None.

## Instrument checks

| Check | Status | A | B | Threshold | Detail |
|---|---|---|---|---|---|
| usage_completeness | PASS | 275/275 (100.0%) | 273/273 (100.0%) | &gt;= 0.95 in every arm, arms at most 5 pp apart | usage completeness arm A 1.000, arm B 1.000 |
| dropped_logs | PASS | - | - | no rise over the experiment | no rise in the gateway drop counter (64 readings, counter 0 to 0) |
| timeout_parity | PASS | 0/32 (0.0%) | 0/32 (0.0%) | timeout rates at most 10 pp apart, or both at most 20% | timeout rate arm A 0.0% (0 of 32), arm B 0.0% (0 of 32), gap +0.0 pp on every condition |
| budget_parity | PASS | - | - | equal across arms unless an arm sets it | wall_s, turns, cpus and mem_gb are equal across arms or set by an arm |
| gateway_hygiene | UNKNOWN | - | - | no MCP auto-inject with MCP clients, no semantic cache, no multi-target routing | not recorded: the manifest predates the hygiene check or names no gateway |

## Release policy

Policy `enterprise-default`, hash `e47dcf5ea03a`: **BLOCK**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason | Evidence |
|---|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | 0.125 | no regression | 16 | clean: not regressed at either bound, d_bar +12.5 pp (+12.5 / +12.5 pp at the missingness bounds), p 0.75, 5 of 8 tasks discordant | - |
| false-completion-ceiling | false_completion | all | B | FAIL (blocks) | 0.1875 | &lt;= 0.05 | 32 | mean 0.1875 over 32 observed runs breaks &lt;= 0.05 | `r_z3hn2tz9mj`, `r_2ms9se9m51`, `r_bbrupc6jz3`, `r_jon5xb1kt6`, `r_dy1s817jsb` and 1 more |
| no-integrity-violations | integrity_violations | all | B | FAIL (blocks) | 8 | = 0 | 32 | 8 of 32 runs had a fired veto gate, breaks = 0 | `r_u14mthccgp`, `r_w21abmqb30`, `r_2cd8el8aj5`, `r_nsf6e2y20y`, `r_wcp1kwz24l` and 3 more |
| retry-amplification-ceiling | retry_amplification | clean | B | PASS | 1 | &lt;= 3 | 16 | mean 1 over 16 observed runs | - |
| cost-per-verified-success | cost_per_verified_success | clean | B | PASS | 0.03788 | &lt;= 2 | 16 | $0.454562 over 12 verified successes in 16 runs | - |

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 10/16 |
| B | 8/16 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 8 | 10/16 | 12/16 | +12.5 pp (descriptive) |
| stale-notes | stale-notes | 8 | 10/16 | 8/16 | -12.5 pp |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False-completion rate | runs with a success claim the verifier failed / runs with an observed false_completion value | 100.0% | 100.0% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | 71.9% | 80.6% |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.2 | 0.5 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 8.0 | 8.0 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 7.0 | 7.0 |
| Tokens, median | median of tokens per run / runs with observed tokens | 107756 | 99835 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 20.2 s | 19.8 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | $0.0461 | $0.0449 |
| Timeout rate | runs that hit the wall-clock budget (outcome timeout or agent timed out) / counted runs | 0.0% | 0.0% |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `249ba677cdf82525be00b490b1458be950ed72574f0a42fdcd9a52802df0d45d` |
| Frozen at | 2026-11-14T01:32:57Z |
| Analysis created | 2026-11-14T02:05:59Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | LIVE |
