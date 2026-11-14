# Leaderboard: coding-core@1.0.0, condition stale-notes

Ranked by verified_success (mean over tasks of the per-task rate); there is no blended score. Tier 1: cannot be told apart from the leader at alpha 0.05 (exact sign-flip test on the tasks both ran, Holm-adjusted). Tier 2: significantly behind the leader. Rank alone is not a claim that one agent beats another.

Scope all: each entry over its own tasks. Common tasks (8): t01, t02, t03, t04, t05, t06, t07, t08.

Reliability is pass^k, the chance that k tries of a task all succeed (unbiased, mean over tasks), with k shown per entry: min(3, the fewest trials per task among its experiments that repeat tasks) unless the query sets k.

| Rank | Tier | Agent | Label | Model | Config | Success [95% interval] | Passes | Reliability | Tasks | Runs | Cost per success | p vs leader | Tier reason |
|---:|---:|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|
| 1 | 1 | codex | codex baseline | gpt-5.4-mini | `dfd6f713e64a` | 78.1% [60.7, 95.6] | 26/33 | 60.4% (pass^2) | 8 | 33 | $0.0382 | - | leader |
| 2 | 1 | custom-python | custom-python gpt-5.4-mini | gpt-5.4-mini | `8bbc91c3b83d` | 58.3% [23.4, 93.3] | 10/17 | 41.7% (pass^2) | 8 | 17 | $0.0406 | 0.38 | p 0.38 on 8 common tasks |
| 3 | 1 | codex | codex with code-wins sentence | gpt-5.4-mini | `9c181777680c` | 50.0% [5.3, 94.7] | 8/16 | 50.0% (pass^2) | 8 | 16 | $0.0555 | 0.38 | p 0.38 on 8 common tasks |

Per-run medians (steps, tool calls, tokens, cost, wall time) and shares of runs (false completion, safe success). False completion is the share of runs that claimed success while the hidden verifier failed, over runs whose outcome and claim are known; a run whose verifier passed counts as 0.

| Rank | Config | Steps | Tool calls | Tokens | Cost | Wall time s | False completion (share of runs) | Safe success |
|---:|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | `dfd6f713e64a` | 8.0 | 7.0 | 115495 | $0.0277 | 20.7 | 18.2% | 78.8% |
| 2 | `8bbc91c3b83d` | 8.0 | 8.0 | 45010 | $0.0247 | 32.3 | 0.0% | 58.8% |
| 3 | `9c181777680c` | 8.0 | 7.0 | 111466 | $0.0256 | 19.4 | 37.5% | 50.0% |

Excluded runs: none.

Grade source latest: each experiment's newest regrade (by regrade.json created_at, ties by the larger id), read as analysis reads it; a run the regrade has no result for keeps its original grade.

- e_batch_codewins_f8f7: rg_gates_1_1_0, 32 of 32 runs regraded
- e_batch_frameworks_9z0h: rg_gates_1_1_0, 32 of 32 runs regraded
- e_pilot_mixed_nc93: original (no regrade), 2 runs

Generated 2026-11-14T02:41:41Z.
