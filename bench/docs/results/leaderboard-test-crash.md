# Leaderboard: coding-core@1.0.0, condition test-crash

Ranked by verified_success (mean over tasks of the per-task rate); there is no blended score. Tier 1: cannot be told apart from the leader at alpha 0.05 (exact sign-flip test on the tasks both ran, Holm-adjusted). Tier 2: significantly behind the leader. Rank alone is not a claim that one agent beats another.

Scope all: each entry over its own tasks. Common tasks (4): t02, t04, t06, t08.

Reliability is pass^k, the chance that k tries of a task all succeed (unbiased, mean over tasks), with k shown per entry: min(3, the fewest trials per task among its experiments that repeat tasks) unless the query sets k.

| Rank | Tier | Agent | Label | Model | Config | Success [95% interval] | Passes | Reliability | Tasks | Runs | Cost per success | p vs leader | Tier reason |
|---:|---:|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|
| 1 | 1 | custom-python | custom-python gpt-5.4-mini | gpt-5.4-mini | `8bbc91c3b83d` | 75.0% [0.0, 100.0] | 7/9 | 75.0% (pass^2) | 4 | 9 | $0.0265 | - | leader |
| 2 | 1 | codex | codex gpt-5.4-mini | gpt-5.4-mini | `dfd6f713e64a` | 62.5% [0.0, 100.0] | 6/9 | 50.0% (pass^2) | 4 | 9 | $0.0416 | 1.00 | too few differing tasks (1 of 4 common) to separate |

Per-run medians (steps, tool calls, tokens, cost, wall time) and shares of runs (false completion, safe success). False completion is the share of runs that claimed success while the hidden verifier failed, over runs whose outcome and claim are known; a run whose verifier passed counts as 0.

| Rank | Config | Steps | Tool calls | Tokens | Cost | Wall time s | False completion (share of runs) | Safe success |
|---:|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | `8bbc91c3b83d` | 8.0 | 8.0 | 55981 | $0.0207 | 26.0 | 0.0% | 77.8% |
| 2 | `dfd6f713e64a` | 7.0 | 6.0 | 90935 | $0.0281 | 18.4 | 11.1% | 66.7% |

Excluded runs: none.

Grade source latest: each experiment's newest regrade (by regrade.json created_at, ties by the larger id), read as analysis reads it; a run the regrade has no result for keeps its original grade.

- e_batch_frameworks_9z0h: rg_gates_1_1_0, 16 of 16 runs regraded
- e_pilot_mixed_nc93: original (no regrade), 2 runs

Generated 2026-11-14T02:41:42Z.
