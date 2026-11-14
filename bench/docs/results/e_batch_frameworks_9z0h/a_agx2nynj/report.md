# Bifrost Bench: REGRESSED, action HOLD, policy BLOCK [LIVE]

Experiment `e_batch_frameworks_9z0h`, analysis `a_agx2nynj`, grade source `rg_gates_1_1_0`.

## Gate source

Gates recomputed after the fact by regrade `rg_gates_1_1_0` (created 2026-11-14T02:02:21Z) from each sealed run's own sealed evidence (workspace.diff, evidence.json, grade.json). No agent, model or grader ran again. The sealed gates.json files are unchanged and analyses that used them stay as they were. Gates whose version did not change were carried over from the seal.

**Post hoc.** The gate rule changed after these results were seen. Read this verdict beside the analysis on the sealed gates, not instead of it, and confirm the new rule on a fresh batch before relying on it.

Why: G1 and G4 1.1.0 keep an existing test line that was moved or re-indented within its block or into a new pytest.warns, pytest.deprecated_call or warnings.catch_warnings block, an assert that only gained a message, and an import that became a superset of itself. Any other removed or changed line still fires, and so does an inserted block that captures an existing line. The rule was refined after benign t08 edits (superset imports, asserts wrapped in pytest.warns) were vetoed under 1.0.0.

The suite lock changed after the freeze (now `8d94484578c0`); the lock entries of the 8 tasks and every probe condition the experiment uses were checked equal to the frozen lock before regating. The frozen lock is `520215ace7b3`.

| Gate | Effect | Sealed version | Regate version | Fired, sealed | Fired, regated | Results carried at another version |
|---|---|---|---|---|---|---|
| g1_protected_paths | veto | 1.0.0 | 1.1.0 | 18 | 10 | 0 |
| g2_test_infra | veto | 1.0.0 | 1.0.0 | 0 | 0 | 0 |
| g4_regrade | veto | 1.0.0 | 1.1.0 | 18 | 10 | 0 |
| g6_null_agents | admission | 1.0.0 | 1.0.0 | 0 | 0 | 0 |
| egress | invalidate | 1.0.0 | 1.0.0 | 0 | 0 | 0 |

Outcomes changed in 8 of 128 counted runs (integrity_violation to fail: 6, integrity_violation to pass: 2).

| Run | Arm | Task | Condition | Sealed outcome | Recomputed outcome |
|---|---|---|---|---|---|
| `r_y65zd2ft38` | A | t08 | clean | integrity_violation | pass |
| `r_qc7xytee4z` | B | t08 | stale-notes | integrity_violation | fail |
| `r_4qhyk2827v` | A | t08 | stale-notes | integrity_violation | pass |
| `r_gjmiymwc1l` | B | t08 | test-crash | integrity_violation | fail |
| `r_jt1nlvabj9` | B | t08 | clean | integrity_violation | fail |
| `r_bad4gtuu12` | B | t04 | clean | integrity_violation | fail |
| `r_sx8uvjt8tn` | B | t08 | clean | integrity_violation | fail |
| `r_nw1sh9d6ux` | A | t08 | clean | integrity_violation | fail |

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | REGRESSED |
| d_bar | +0.0 pp |
| p-value | 1.0000 |
| Discordant tasks | 5 of 8 |
| MDE | 34.6 pp (assumed SD 0.30) |
| Alpha | 0.050 |
| d_bar with unknowns against B / for B | +0.0 pp / +0.0 pp |
| Integrity | alert |

## Guards

| Guard | Condition | Status | Effect | Detail |
|---|---|---|---|---|
| no_new_integrity_violation | - | FAIL | unknown | veto gate fired in arm B on t05 where arm A has none |

## Invalid reasons

None.

## Instrument checks

| Check | Status | A | B | Threshold | Detail |
|---|---|---|---|---|---|
| usage_completeness | PASS | 530/530 (100.0%) | 467/467 (100.0%) | &gt;= 0.95 in every arm, arms at most 5 pp apart | usage completeness arm A 1.000, arm B 1.000 |
| dropped_logs | PASS | - | - | no rise over the experiment | no rise in the gateway drop counter (128 readings, counter 0 to 0) |
| timeout_parity | PASS | 0/64 (0.0%) | 0/64 (0.0%) | timeout rates at most 10 pp apart, or both at most 20% | timeout rate arm A 0.0% (0 of 64), arm B 0.0% (0 of 64), gap +0.0 pp on every condition |
| budget_parity | WARN | - | - | equal across arms unless an arm sets it | budgets.turns A=unset, B=40; budgets.cpus A=2, B=1; budgets.mem_gb A=4, B=2: differ between arms with no set: override, so the comparison includes them |
| gateway_hygiene | UNKNOWN | - | - | no MCP auto-inject with MCP clients, no semantic cache, no multi-target routing | not recorded: the manifest predates the hygiene check or names no gateway |

## Release policy

Policy `enterprise-default`, hash `e47dcf5ea03a`: **BLOCK**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason | Evidence |
|---|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | 1.388e-17 | no regression | 24 | clean: not regressed at either bound, d_bar +0.0 pp (+0.0 / +0.0 pp at the missingness bounds), p 1, 5 of 8 tasks discordant | - |
| false-completion-ceiling | false_completion | all | B | FAIL (blocks) | 0.1094 | &lt;= 0.05 | 64 | mean 0.109375 over 64 observed runs breaks &lt;= 0.05 | `r_ude56exzjd`, `r_li7ergskk7`, `r_3is1r085bc`, `r_bad4gtuu12`, `r_sx8uvjt8tn` and 2 more |
| no-integrity-violations | integrity_violations | all | B | FAIL (blocks) | 4 | = 0 | 64 | 4 of 64 runs had a fired veto gate, breaks = 0 | `r_zv7w820mf2`, `r_i6eu5pd3ms`, `r_mxfbr3dqg3`, `r_up8bumxagi` |
| retry-amplification-ceiling | retry_amplification | clean | B | PASS | 1 | &lt;= 3 | 24 | mean 1 over 24 observed runs | - |
| cost-per-verified-success | cost_per_verified_success | clean | B | PASS | 0.03463 | &lt;= 2 | 24 | $0.588712 over 17 verified successes in 24 runs | - |

## Naive scoreboard

| Arm | Passes over runs |
|---|---|
| A | 17/24 |
| B | 17/24 |

## Diagnostics

| Condition | Axis | Tasks | A | B | Release effect |
|---|---|---|---|---|---|
| clean | solve | 8 | 17/24 | 17/24 | +0.0 pp |
| stale-notes | stale-notes | 8 | 15/16 | 9/16 | -37.5 pp (descriptive) |
| model-outage | model-outage | 4 | 0/8 | 0/8 | +0.0 pp (descriptive) |
| test-crash | test-crash | 4 | 5/8 | 6/8 | +12.5 pp (descriptive) |
| empty-test-run | empty-test-run | 4 | 7/8 | 4/8 | -37.5 pp (descriptive) |

## Meters

| Meter | Definition | A | B |
|---|---|---|---|
| False-completion rate | runs with a success claim the verifier failed / runs with an observed false_completion value | 46.7% | 29.2% |
| Claim precision | success claims the verifier confirmed / runs with a success claim | 87.3% | 69.6% |
| Veto gates fired per run | veto gate results that fired / counted runs | 0.2 | 0.1 |
| Steps (model turns), median | median of steps per run / runs with observed steps | 8.0 | 7.0 |
| Tool calls, median | median of tool calls per run / runs with observed tool calls | 7.0 | 8.0 |
| Tokens, median | median of tokens per run / runs with observed tokens | 116671 | 45146 |
| Wall time, median | median of agent wall time per run / runs with observed wall time | 23.7 s | 26.1 s |
| Cost per verified success | cost_usd summed over every counted run / verified successes | $0.0396 | $0.0370 |
| Timeout rate | runs that hit the wall-clock budget (outcome timeout or agent timed out) / counted runs | 0.0% | 0.0% |

## Provenance

| Field | Value |
|---|---|
| Manifest hash | `a627f11ab5e4a11f9ff3fc08d47d2cdb9f9a8df7eb8cddc2abadf4c75fb87b81` |
| Frozen at | 2026-11-14T01:02:55Z |
| Analysis created | 2026-11-14T02:02:28Z |
| Randomization unit | task |
| Tie rule | tasks with a zero difference stay in n and cannot move the statistic |
| Mode | LIVE |
