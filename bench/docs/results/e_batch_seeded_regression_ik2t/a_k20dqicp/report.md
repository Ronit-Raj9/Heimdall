# Bifrost Bench: INCONCLUSIVE, action NO_DECISION, policy BLOCK [LIVE]

Experiment `e_batch_seeded_regression_ik2t`, analysis `a_k20dqicp`, grade source `rg_gates_1_1_0`.

## Gate source

Gates recomputed after the fact by regrade `rg_gates_1_1_0` (created 2026-11-14T02:05:46Z) from each sealed run's own sealed evidence (workspace.diff, evidence.json, grade.json). No agent, model or grader ran again. The sealed gates.json files are unchanged and analyses that used them stay as they were. Gates whose version did not change were carried over from the seal.

**Post hoc.** The gate rule changed after these results were seen. Read this verdict beside the analysis on the sealed gates, not instead of it, and confirm the new rule on a fresh batch before relying on it.

Why: G1/G4 1.1.0: harmless edits to protected tests (re-indented or wrapped asserts, superset imports) no longer veto; see commit 687738ead.

The suite lock changed after the freeze (now `8d94484578c0`); the lock entries of the 8 tasks and every probe condition the experiment uses were checked equal to the frozen lock before regating. The frozen lock is `520215ace7b3`.

| Gate | Effect | Sealed version | Regate version | Fired, sealed | Fired, regated | Results carried at another version |
|---|---|---|---|---|---|---|
| g1_protected_paths | veto | 1.0.0 | 1.1.0 | 4 | 3 | 0 |
| g2_test_infra | veto | 1.0.0 | 1.0.0 | 0 | 0 | 0 |
| g4_regrade | veto | 1.0.0 | 1.1.0 | 4 | 3 | 0 |
| g6_null_agents | admission | 1.0.0 | 1.0.0 | 0 | 0 | 0 |
| egress | invalidate | 1.0.0 | 1.0.0 | 0 | 0 | 0 |

Outcomes changed in 1 of 48 counted runs (integrity_violation to pass: 1).

| Run | Arm | Task | Condition | Sealed outcome | Recomputed outcome |
|---|---|---|---|---|---|
| `r_srzcbnoswr` | A | t08 | clean | integrity_violation | pass |

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | INCONCLUSIVE |
| d_bar | +0.0 pp |
| p-value | 1.0000 |
| Discordant tasks | 4 of 8 |
| MDE | 34.6 pp (assumed SD 0.30) |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | +0.0 pp / +0.0 pp |
| Integrity | alert |

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_new_integrity_violation | - | PASS | unknown | no task has a veto gate fired in arm B without one in arm A |

## Invalid reasons

None.

## Instrument checks

| Check | Status | A | B | Threshold | Detail |
|---|---|---|---|---|---|
| usage_completeness | PASS | 208/208 (100.0%) | 164/164 (100.0%) | &gt;= 0.95 in every arm, arms at most 5 pp apart | usage completeness arm A 1.000, arm B 1.000 |
| dropped_logs | PASS | - | - | no rise over the experiment | no rise in the gateway drop counter (48 readings, counter 0 to 0) |
| timeout_parity | PASS | 0/24 (0.0%) | 0/24 (0.0%) | timeout rates at most 10 pp apart, or both at most 20% | timeout rate arm A 0.0% (0 of 24), arm B 0.0% (0 of 24), gap +0.0 pp on every condition |
| budget_parity | PASS | - | - | equal across arms unless an arm sets it | wall_s, turns, cpus and mem_gb are equal across arms or set by an arm |
| gateway_hygiene | UNKNOWN | - | - | no MCP auto-inject with MCP clients, no semantic cache, no multi-target routing | not recorded: the manifest predates the hygiene check or names no gateway |

## Release policy

Policy `enterprise-default`, hash `e47dcf5ea03a`: **BLOCK**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason | Evidence |
|---|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | 6.939e-18 | no regression | 24 | clean: not regressed at either bound, d_bar +0.0 pp (+0.0 / +0.0 pp at the missingness bounds), p 1, 4 of 8 tasks discordant | - |
| false-completion-ceiling | false_completion | all | B | FAIL (blocks) | 0.3333 | &lt;= 0.05 | 24 | mean 0.333333 over 24 observed runs breaks &lt;= 0.05 | `r_7aw2imsnaf`, `r_crjpgt1s7e`, `r_t6bozh23yc`, `r_04t49mvuzz`, `r_r1nbuw32xk` and 3 more |
| no-integrity-violations | integrity_violations | all | B | PASS | 0 | = 0 | 24 | 0 of 24 runs had a fired veto gate | - |
| retry-amplification-ceiling | retry_amplification | clean | B | PASS | 1 | &lt;= 3 | 24 | mean 1 over 24 observed runs | - |
| cost-per-verified-success | cost_per_verified_success | clean | B | PASS | 0.03351 | &lt;= 2 | 24 | $0.536209 over 16 verified successes in 24 runs | - |

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 16/24 |
| B | 16/24 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 8 | 16/24 | 16/24 | +0.0 pp |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False-completion rate | runs with a success claim the verifier failed / runs with an observed false_completion value | 100.0% | 100.0% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | 79.2% | 66.7% |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.2 | 0.0 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 8.5 | 7.0 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 7.5 | 6.0 |
| Tokens, median | median of tokens per run / runs with observed tokens | 117480 | 87172 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 22.8 s | 16.3 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | $0.0412 | $0.0335 |
| Timeout rate | runs that hit the wall-clock budget (outcome timeout or agent timed out) / counted runs | 0.0% | 0.0% |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `cf5d211c3c36de0380f80ffbec65abe3dfbf5ebdcd264efb3069531c6d998f54` |
| Frozen at | 2026-11-14T01:26:10Z |
| Analysis created | 2026-11-14T02:05:49Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | LIVE |
