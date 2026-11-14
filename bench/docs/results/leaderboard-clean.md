# Leaderboard: coding-core@1.0.0, condition clean

Ranked by verified_success (mean over tasks of the per-task rate); there is no blended score. Tier 1: cannot be told apart from the leader at alpha 0.05 (exact sign-flip test on the tasks both ran, Holm-adjusted). Tier 2: significantly behind the leader. Rank alone is not a claim that one agent beats another.

Scope all: each entry over its own tasks. Common tasks (8): t01, t02, t03, t04, t05, t06, t07, t08.

Reliability is pass^k, the chance that k tries of a task all succeed (unbiased, mean over tasks), with k shown per entry: min(3, the fewest trials per task among its experiments that repeat tasks) unless the query sets k.

| Rank | Tier | Agent | Label | Model | Config | Success [95% interval] | Passes | Reliability | Tasks | Runs | Cost per success | p vs leader | Tier reason |
|---:|---:|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|
| 1 | 1 | codex | codex with code-wins sentence | gpt-5.4-mini | `9c181777680c` | 72.9% [39.7, 100.0] | 13/18 | 62.5% (pass^2) | 8 | 18 | $0.0381 | - | leader |
| 2 | 1 | custom-python | custom-python gpt-5.4-mini | gpt-5.4-mini | `8bbc91c3b83d` | 70.8% [33.0, 100.0] | 18/25 | 62.5% (pass^3) | 8 | 25 | $0.0333 | 1.00 | too few differing tasks (4 of 8 common) to separate |
| 3 | 1 | codex | codex baseline | gpt-5.4-mini | `dfd6f713e64a` | 69.3% [49.0, 89.6] | 58/83 | 51.5% (pass^2) | 8 | 83 | $0.0412 | 1.00 | p 1.00 on 8 common tasks |
| 4 | 1 | codex | codex told to skip tests | gpt-5.4-mini | `b4410b6803e3` | 66.7% [33.4, 100.0] | 16/24 | 50.0% (pass^3) | 8 | 24 | $0.0335 | 1.00 | too few differing tasks (4 of 8 common) to separate |
| 5 | 1 | codex | codex with memory hygiene note | gpt-5.4-mini | `79fa18980129` | 56.2% [21.4, 91.1] | 9/16 | 37.5% (pass^2) | 8 | 16 | $0.0546 | 1.00 | too few differing tasks (5 of 8 common) to separate |

Per-run medians (steps, tool calls, tokens, cost, wall time) and shares of runs (false completion, safe success). False completion is the share of runs that claimed success while the hidden verifier failed, over runs whose outcome and claim are known; a run whose verifier passed counts as 0.

| Rank | Config | Steps | Tool calls | Tokens | Cost | Wall time s | False completion (share of runs) | Safe success |
|---:|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | `9c181777680c` | 7.0 | 6.0 | 86616 | $0.0231 | 20.1 | 5.6% | 72.2% |
| 2 | `8bbc91c3b83d` | 7.0 | 8.0 | 43104 | $0.0246 | 29.0 | 20.0% | 72.0% |
| 3 | `dfd6f713e64a` | 8.0 | 7.0 | 102747 | $0.0264 | 23.7 | 20.5% | 69.9% |
| 4 | `b4410b6803e3` | 7.0 | 6.0 | 87172 | $0.0221 | 16.3 | 33.3% | 66.7% |
| 5 | `79fa18980129` | 8.0 | 7.0 | 104836 | $0.0242 | 31.8 | 31.2% | 56.2% |

Resilience: success under each condition minus success clean, mean over the tasks graded under both, in percentage points:

| Rank | Config | empty-test-run | memory-trap | model-outage | stale-notes | test-crash |
|---:|---|---:|---:|---:|---:|---:|
| 1 | `9c181777680c` | - | - | - | -22.9 pp (8 tasks) | - |
| 2 | `8bbc91c3b83d` | -37.5 pp (4 tasks) | - | -91.7 pp (4 tasks) | -12.5 pp (8 tasks) | +25.0 pp (4 tasks) |
| 3 | `dfd6f713e64a` | +10.5 pp (4 tasks) | +11.9 pp (8 tasks) | -68.6 pp (4 tasks) | +8.8 pp (8 tasks) | -7.5 pp (4 tasks) |
| 4 | `b4410b6803e3` | - | - | - | - | - |
| 5 | `79fa18980129` | - | +18.8 pp (8 tasks) | - | - | - |

Excluded runs: none.

Grade source latest: each experiment's newest regrade (by regrade.json created_at, ties by the larger id), read as analysis reads it; a run the regrade has no result for keeps its original grade.

- e_memory_trap_wn4b: original (no regrade), 32 runs
- e_batch_codewins_f8f7: rg_gates_1_1_0, 32 of 32 runs regraded
- e_batch_seeded_regression_ik2t: rg_gates_1_1_0, 48 of 48 runs regraded
- e_batch_frameworks_9z0h: rg_gates_1_1_0, 48 of 48 runs regraded
- e_pilot_mixed_nc93: original (no regrade), 2 runs
- e_pilot_codex_elat: original (no regrade), 4 runs

Generated 2026-11-14T02:41:40Z.
