# Model migration report: e_mig

Suite s@1. Old model: old-model (arm A, agent agent). Overall verdicts on condition clean, alpha 0.05. Release rule set: none.

## Recommendation

- **old-model to new-model (arm B): Migrate.** overall on clean: new model better: p 0.031 after Holm across 2, on 10 tasks (7 better, 0 worse).
- **old-model to nano-model (arm C): Not enough evidence.** overall on clean: cannot tell: 3 differing tasks of 10; the test needs at least 6 differing tasks to reach alpha 0.05; about 28 tasks would detect a difference of this size with 80% power.

## Models

| arm | model | success [95%] | pass^k | cost per verified success | median cost per run | median tokens | median wall s | median steps | latency per turn | Pareto |
|---|---|---|---|---|---|---|---|---|---|---|
| A | old-model | 30.0% [0, 65] | unknown | $0.333 | $0.100 | unknown | unknown | unknown | unknown | dominated by B |
| B | new-model | 100.0% [100, 100] | unknown | $0.300 | $0.300 | unknown | unknown | unknown | unknown | frontier |
| C | nano-model | 0.0% [0, 0] | unknown | n/a (undefined (0 of 10)) | $0.020 | unknown | unknown | unknown | unknown | not placed: no cost on this axis: undefined (0 of 10) |

## old-model (arm A) to new-model (arm B)

**Migrate.** overall on clean: new model better: p 0.031 after Holm across 2, on 10 tasks (7 better, 0 worse).

- The same prompt and agent config were used for every model: a prompt tuned for the old model is not a tuned prompt for the new one, so this measures the move as it is, not the best the new model can do.
- no release policy was evaluated

Overall: new model better: p 0.031 after Holm across 2, on 10 tasks (7 better, 0 worse). Mean difference +0.700 (new minus old, per-task success rate).

### Strata (Holm across every stratum of this pair)

| stratum | tasks | better / worse | mean diff | p | p adj | result |
|---|---|---|---|---|---|---|
| condition clean | 10 | 7 / 0 | +0.700 | 0.016 | 0.12 | cannot tell (about 11 tasks needed) |
| condition model-outage | 4 | 1 / 0 | +0.250 | 1.00 | 1.00 | cannot tell (about 55 tasks needed) |
| task_kind bugfix | 5 | 2 / 0 | +0.400 | 0.50 | 1.00 | cannot tell (about 28 tasks needed) |
| task_kind feature | 5 | 5 / 0 | +1.000 | 0.062 | 0.31 | cannot tell (about 9 tasks needed) |
| capability tool_use | 10 | 7 / 0 | +0.700 | 0.016 | 0.12 | cannot tell (about 11 tasks needed) |
| difficulty_declared easy | 5 | 2 / 0 | +0.400 | 0.50 | 1.00 | cannot tell (about 28 tasks needed) |
| difficulty_declared hard | 5 | 5 / 0 | +1.000 | 0.062 | 0.31 | cannot tell (about 9 tasks needed) |
| difficulty_observed unknown | 10 | 7 / 0 | +0.700 | 0.016 | 0.12 | cannot tell (about 11 tasks needed) |

### Behaviour

- false completion: 0.0% vs 0.0%
- rejected requests per run (4xx: malformed request or tool call): 0.00 vs 0.00
- refusals per run: 0.00 vs 0.00
- retries / fallbacks per run: 0.00 / 0.00 vs 0.00 / 0.00
- gateway latency per turn: unknown vs unknown
- gate g1_protected_paths (veto): fired in 0 vs 1 runs, new
- failure cluster the new model fixed: Failed with no error recorded on s (17 runs, tasks s/t01, s/t02, s/t03, s/t04, s/t05, s/t06, s/t07, s/t08, s/t09, s/t10)

| condition | runs | success | false completion | retry amplification |
|---|---|---|---|---|
| clean | 10 vs 10 | 30.0% vs 100.0% | 0.0% vs 0.0% | unknown vs unknown |
| model-outage | 4 vs 4 | 0.0% vs 25.0% | 0.0% vs 0.0% | unknown vs unknown |

### Flipped tasks

| task | condition | kind | old | new | runs |
|---|---|---|---|---|---|
| t04 | clean | fixed | 0% | 100% | A r_A_t04_clean:fail, B r_B_t04_clean:pass |
| t05 | clean | fixed | 0% | 100% | A r_A_t05_clean:fail, B r_B_t05_clean:pass |
| t06 | clean | fixed | 0% | 100% | A r_A_t06_clean:fail, B r_B_t06_clean:pass |
| t07 | clean | fixed | 0% | 100% | A r_A_t07_clean:fail, B r_B_t07_clean:pass |
| t08 | clean | fixed | 0% | 100% | A r_A_t08_clean:fail, B r_B_t08_clean:pass |
| t09 | clean | fixed | 0% | 100% | A r_A_t09_clean:fail, B r_B_t09_clean:pass |
| t10 | clean | fixed | 0% | 100% | A r_A_t10_clean:fail, B r_B_t10_clean:pass |
| t01 | model-outage | fixed | 0% | 100% | A r_A_t01_model-outage:fail, B r_B_t01_model-outage:pass |

### Cost (clean)

Cost per verified success $0.333 vs $0.300; difference -$0.033.

- Priced rows the snapshot cannot split: $0.433 (0 vs 0 priced rows whose model is not in the price snapshot or was priced with another one, and 10 vs 10 runs without cost rows (cost_usd only))
- Success rate (cost of failed runs): -$0.467 (3 of 10 vs 10 of 10 runs verified; every failed run's cost is carried by the successes)

## old-model (arm A) to nano-model (arm C)

**Not enough evidence.** overall on clean: cannot tell: 3 differing tasks of 10; the test needs at least 6 differing tasks to reach alpha 0.05; about 28 tasks would detect a difference of this size with 80% power.

- The same prompt and agent config were used for every model: a prompt tuned for the old model is not a tuned prompt for the new one, so this measures the move as it is, not the best the new model can do.

Overall: cannot tell: 3 differing tasks of 10; the test needs at least 6 differing tasks to reach alpha 0.05. Mean difference -0.300 (new minus old, per-task success rate). About 28 tasks would detect a difference of this size with 80% power.

### Strata (Holm across every stratum of this pair)

| stratum | tasks | better / worse | mean diff | p | p adj | result |
|---|---|---|---|---|---|---|
| condition clean | 10 | 0 / 3 | -0.300 | 0.25 | 1.00 | cannot tell (about 37 tasks needed) |
| condition model-outage | 4 | 0 / 0 | +0.000 | 1.00 | 1.00 | cannot tell |
| task_kind bugfix | 5 | 0 / 3 | -0.600 | 0.25 | 1.00 | cannot tell (about 15 tasks needed) |
| task_kind feature | 5 | 0 / 0 | +0.000 | 1.00 | 1.00 | cannot tell |
| capability tool_use | 10 | 0 / 3 | -0.300 | 0.25 | 1.00 | cannot tell (about 37 tasks needed) |
| difficulty_declared easy | 5 | 0 / 3 | -0.600 | 0.25 | 1.00 | cannot tell (about 15 tasks needed) |
| difficulty_declared hard | 5 | 0 / 0 | +0.000 | 1.00 | 1.00 | cannot tell |
| difficulty_observed unknown | 10 | 0 / 3 | -0.300 | 0.25 | 1.00 | cannot tell (about 37 tasks needed) |

### Behaviour

- false completion: 0.0% vs 0.0%
- rejected requests per run (4xx: malformed request or tool call): 0.00 vs 0.00
- refusals per run: 0.00 vs 0.00
- retries / fallbacks per run: 0.00 / 0.00 vs 0.00 / 0.00
- gateway latency per turn: unknown vs unknown

| condition | runs | success | false completion | retry amplification |
|---|---|---|---|---|
| clean | 10 vs 10 | 30.0% vs 0.0% | 0.0% vs 0.0% | unknown vs unknown |
| model-outage | 4 vs 4 | 0.0% vs 0.0% | 0.0% vs 0.0% | unknown vs unknown |

### Flipped tasks

| task | condition | kind | old | new | runs |
|---|---|---|---|---|---|
| t01 | clean | regressed | 100% | 0% | A r_A_t01_clean:pass, C r_C_t01_clean:fail |
| t02 | clean | regressed | 100% | 0% | A r_A_t02_clean:pass, C r_C_t02_clean:fail |
| t03 | clean | regressed | 100% | 0% | A r_A_t03_clean:pass, C r_C_t03_clean:fail |

### Cost (clean)

Cost per verified success $0.333 vs n/a (undefined (0 of 10)); difference n/a.

Per success not applicable (cost per verified success is undefined: arm A has 3 of 10 verified successes, arm B 0 of 10); per run instead.

- Priced rows the snapshot cannot split: -$0.080 (0 vs 0 priced rows whose model is not in the price snapshot or was priced with another one, and 10 vs 10 runs without cost rows (cost_usd only))

## Notes

- The same prompt and agent config were used for every model: a prompt tuned for the old model is not a tuned prompt for the new one, so this measures the move as it is, not the best the new model can do.
