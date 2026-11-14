# Leaderboard: coding-core@1.0.0, condition memory-trap

Ranked by verified_success (mean over tasks of the per-task rate); there is no blended score. Tier 1: cannot be told apart from the leader at alpha 0.05 (exact sign-flip test on the tasks both ran, Holm-adjusted). Tier 2: significantly behind the leader. Rank alone is not a claim that one agent beats another.

Scope all: each entry over its own tasks. Common tasks (8): t01, t02, t03, t04, t05, t06, t07, t08.

Reliability is pass^k, the chance that k tries of a task all succeed (unbiased, mean over tasks), with k shown per entry: min(3, the fewest trials per task among its experiments that repeat tasks) unless the query sets k.

| Rank | Tier | Agent | Label | Model | Config | Success [95% interval] | Passes | Reliability | Tasks | Runs | Cost per success | p vs leader | Tier reason |
|---:|---:|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|
| 1 | 1 | codex | codex baseline | gpt-5.4-mini | `dfd6f713e64a` | 81.2% [50.1, 100.0] | 13/16 | 75.0% (pass^2) | 8 | 16 | $0.0301 | - | leader |
| 2 | 1 | codex | codex with memory hygiene note | gpt-5.4-mini | `79fa18980129` | 75.0% [43.4, 100.0] | 12/16 | 62.5% (pass^2) | 8 | 16 | $0.0335 | 1.00 | too few differing tasks (3 of 8 common) to separate |

Per-run medians (steps, tool calls, tokens, cost, wall time) and shares of runs (false completion, safe success). False completion is the share of runs that claimed success while the hidden verifier failed, over runs whose outcome and claim are known; a run whose verifier passed counts as 0.

| Rank | Config | Steps | Tool calls | Tokens | Cost | Wall time s | False completion (share of runs) | Safe success |
|---:|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | `dfd6f713e64a` | 7.0 | 6.0 | 100338 | $0.0255 | 28.0 | 0.0% | 81.2% |
| 2 | `79fa18980129` | 7.0 | 6.0 | 91156 | $0.0229 | 33.2 | 6.2% | 75.0% |

Excluded runs: none.

Grade source latest: each experiment's newest regrade (by regrade.json created_at, ties by the larger id), read as analysis reads it; a run the regrade has no result for keeps its original grade.

- e_memory_trap_wn4b: original (no regrade), 32 runs

Generated 2026-11-14T02:41:42Z.
