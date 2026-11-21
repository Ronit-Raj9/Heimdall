# Model migration report

Every frontier release raises the same question: should this agent move to
the new model? `heimdall migrate` plans a model matrix experiment for one
agent config on the old model and each new one, shows the cost and time
forecast, runs it when told to, and writes a report that answers per model
pair: migrate, do not migrate, or not enough evidence. The answer follows
the release policy and is never stronger than the statistics. Code:
`bench/migration/`, `cmd/bifrost-bench/migrate.go`, `handlers/migration.go`,
the page `/migration` (`ui/app/migration/`, `ui/components/migration/`).

```
heimdall migrate -agent agents/codex.yaml -from gpt-5.4-mini -to gpt-5.4,gpt-5.4-nano \
  -suite coding-core@1.0.0 [-trials 3] [-conditions clean,model-outage] [-tasks t01,t02] \
  [-provider openai] [-budget-usd 5] [-reserve-usd 0.1] [-policy policies/default.yaml] [-out migrations/x] [-allow-unpriced] [-yes]

heimdall migrate report -exp <id> [-from <model|arm>] [-to <model|arm>[,...]] [-condition clean] \
  [-alpha 0.05] [-grade-source original] [-policy <file>|none] [-suite-tasks=false] [-out <dir>] [-json]

GET /api/bench/experiments/{id}/migration?from=&to=&condition=&alpha=&grade_source=
```

## migrate

1. Writes `experiment.yaml` into `-out` (default `migrations/<from>-to-<to>`):
   a `matrix:` of the old model first, then each new one (with `-provider`
   when it differs from the agent config's), the same tasks, trials and
   conditions for every model, `budget.total_usd` from `-budget-usd`,
   `budget.reserve_usd` from `-reserve-usd` (set it with `-allow-unpriced`:
   a run of an unpriced model has an unknown cost, and without a reserve the
   scheduler counts the first such run at the whole remaining budget, so the
   experiment stops after one run), and the release policy. The primary condition is `clean` when it is run, else the
   first of `-conditions`. The report afterwards names the frozen arms (A for
   the old model, then B, C...), so one model through two providers compares
   one direction only. In `migrate report`, `provider/model` names one of
   those arms. It is an ordinary experiment file: `heimdall run` runs it
   too.
2. Plans it with the planner (`docs/model-matrix.md`: an unpriced model is
   refused unless `-allow-unpriced`) and prints the slots and the forecast
   (`docs/forecast.md`).
3. Without `-yes` nothing runs. With a budget, a forecast that says the
   budget is exceeded (`exceeds`, `likely_exceeds`) refuses to run even with
   `-yes`; one that cannot confirm the budget (`unknown`, `may_exceed`) runs
   with a warning, and the engine's own budget still stops the experiment.
4. With `-yes` it freezes, runs, analyzes, and writes `report.md` and
   `report.json` into `-out`.

## migrate report

Builds the report of any sealed experiment from its runs. `-from` and each
`-to` are a model name (the arms whose config requests it) or an arm id; an
empty `-from` is arm A and an empty `-to` is every other arm of the same
agent config. Pairs are old arm against new arm of the same agent; when no
arm shares the agent the pair is still made and the report notes it compares
harnesses as well as models, and when both arms request the same model it
notes the pair compares config versions.

### Verdicts

For each pair, the per-task verified success rate of the new arm minus the
old one's, over the tasks both ran, is tested with the two-sided
sign-flip test (`stats.SignFlip`, as the verdict and the matrix). Exact means full enumeration of the 2^n sign vectors, which `stats.SignFlip` does for up to 22 nonzero task differences; with 23 or more it estimates p by Monte Carlo (1,000,000 random sign vectors, `Exact=false` in the result), so a p near alpha on a large suite carries sampling error. The reports:

- **Overall**, on the overall condition (the experiment's primary, `-condition`
  to change it), Holm-adjusted across the pairs.
- **Per stratum**: every condition (so every fault probe), and on the overall
  condition every task kind, capability, tag and declared difficulty (from
  the suite's `task.yaml`), and observed difficulty. Observed difficulty is
  the task's pooled success over every arm of the experiment on that
  condition, labelled easy, medium or hard by `taskhealth.DifficultyLabel`
  (unknown below its minimum runs and configs). Swapping the arm labels
  within a task does not change that pooled rate, so stratifying on it keeps
  the permutation test valid. All strata of a pair are one Holm family.

A test result is `better`, `worse` (adjusted p below alpha) or `cannot_tell`.
A `cannot_tell` carries two numbers: `min_tasks`, the fewest differing tasks
with which the test could reach alpha after Holm at all (the smallest exact
p with n differing tasks is 2^(1-n)), and `tasks_needed`, the paired t
estimate of the tasks that would detect the observed difference with 80%
power at alpha over the family size (the verdict's MDE rule,
`stats.TasksNeeded`), from the observed spread; 0 when the difference is 0.
More trials per task never substitute for tasks: the test is over tasks.

### Cost

Per model: cost per verified success, median cost per run, tokens, wall
time, steps, gateway latency per turn, pass^k and success with its interval
(the matrix's numbers, `leaderboard.Matrix`), and its Pareto position
(frontier, or dominated by which arm; `docs/cost-explain.md`). Per pair, the
cost explanation of the new arm against the old on the overall condition:
the waterfall of why cost per success moved, and any silent cache break.

### Behaviour

- failure clusters only the new model shows, and the ones it fixed
  (`clusters.Build` over the experiment's failures, by config hash);
- gate firings per gate in each arm: new, fixed or both;
- false completion rate (a passing run counts as no false completion);
- rejected requests per run (provider 4xx: malformed request or tool call),
  refusals per run (refusal responses in the raw rows plus refusal
  outcomes), retries and fallbacks per run, gateway latency per turn;
- every condition: success, false completion and retry amplification in each
  arm, beside the condition strata's tests.

### Flipped tasks

Every task, per condition, whose success rate differs between the arms,
regressions first, with every run of both arms and its pair id (the UI links
each to `/runs?pair=`).

### Recommendation

- `do_not_migrate` when the overall verdict is `worse`, or any stratum is
  `worse` after Holm across the strata, or the release policy blocks on a
  failed check;
- `migrate` when the overall verdict is `better`, no stratum is `worse`, and
  the policy does not block (a warning is a caveat);
- `not_enough_evidence` otherwise, with the tasks needed; and always for a
  synthetic experiment.

The policy is evaluated with `policy.Evaluate` exactly as `check` would on a
two-arm experiment of the pair's runs (old arm as baseline A, new arm as
candidate B): the experiment's own policy file, else `policies/default.yaml`,
or `-policy`. A policy that was asked for (by `-policy` or by the experiment)
and cannot be read fails the report; only an absent implicit
`policies/default.yaml` gives a statistics-only report, and `-policy none`
asks for one. The experiment's own policy is checked against the hash frozen
in its manifest. A policy check that is unknown blocks only as "not enough
evidence", never as "do not migrate". Every report notes that the same
prompt and config were used for every model: a prompt tuned for the old
model is not a tuned prompt for the new one, so the report measures the move
as it is, not the best the new model can do.

## A real output (excerpt)

`heimdall migrate report -exp e_batch_seeded_regression_ik2t` on a scratch
copy of the real batch (Codex on gpt-5.4-mini, arm B told to skip the tests,
8 tasks, 3 trials, clean), run on 18 Nov 2026:

```
## Recommendation
- **gpt-5.4-mini to gpt-5.4-mini (arm B): Do not migrate.** release policy enterprise-default blocks:
  false-completion-ceiling: mean 0.333333 over 24 observed runs breaks <= 0.05.

Overall: cannot tell: 5 differing tasks of 8; the test needs at least 6 differing tasks to reach alpha 0.05.
Mean difference +0.042 (new minus old, per-task success rate). About 783 tasks would detect a difference of this size with 80% power.

| stratum | tasks | better / worse | mean diff | p | p adj | result |
| condition clean | 8 | 3 / 2 | +0.042 | 1.00 | 1.00 | cannot tell (about 1275 tasks needed) |
| task_kind bugfix | 2 | 0 / 1 | -0.167 | 1.00 | 1.00 | cannot tell (about 30 tasks needed) |
| difficulty_observed medium | 3 | 1 / 2 | -0.111 | 1.00 | 1.00 | cannot tell (about 503 tasks needed) |

### Flipped tasks
| t03 | clean | regressed | 67% | 0% | A r_skq7ft355i:pass, B r_7aw2imsnaf:fail, ... |
| t05 | clean | regressed | 67% | 33% | ... |
```

Success alone cannot tell the two configs apart on 8 tasks; the release
policy still blocks the candidate, because a third of its runs claimed
success while the hidden tests failed. The same command on the other five
real experiments: `e_batch_codewins_f8f7`, `e_batch_frameworks_9z0h` and
`e_memory_trap_wn4b` give "do not migrate" (the candidate breaks the
false-completion ceiling and fires veto gates), `e_pilot_codex_elat` and
`e_pilot_mixed_nc93` give "not enough evidence" (one or two tasks). None of
the six is a model matrix (every arm uses gpt-5.4-mini), so each report
notes it compares config versions or harnesses, not models.

## A scripted run through the pipeline

On 18 Nov 2026 `migrate` ran end to end through the real pipeline (stock
Bifrost and the fault proxy of the local stack, Docker workspaces, hidden
tests, seals) at $0: the scripted toy agent (`agents/toy.yaml`) on the
proxy's scripted Anthropic model, with no Anthropic key in the stack, into a
scratch runs root:

```
heimdall migrate -agent agents/toy.yaml -from claude-sonnet-4-5 -to claude-haiku-4-5 \
  -suite coding-core@1.0.0 -trials 1 -tasks t01,t02 -conditions clean,model-outage -budget-usd 1 -out <scratch> -yes
```

It wrote the experiment file, planned 8 runs (2 tasks, 2 conditions, 2
models), printed the forecast ("no history, cannot estimate" on an empty
runs root, so the budget fit was unknown and it warned), ran and sealed all
8, analyzed, and wrote `report.md` and `report.json`:

```
**Synthetic: scripted agent, scripted model or smoke experiment. The numbers are not findings.**
- **claude-sonnet-4-5 to claude-haiku-4-5 (arm B): Not enough evidence.** synthetic experiment: its numbers are not findings.
| A | claude-sonnet-4-5 | 100.0% [100, 100] | ... | $0.00740 | ... | dominated by B |
| B | claude-haiku-4-5  | 100.0% [100, 100] | ... | $0.00247 | ... | frontier |
| condition model-outage | 2 | 1 / 0 | +0.500 | 1.00 | 1.00 | cannot tell (about 27 tasks needed) |
### Cost (clean)
- Price of the served model: -$0.00493 (USD per million tokens, A then B: input $3.000 vs $1.000, ..., output $15.000 vs $5.000)
```

The scripted model answers both model names with the same tokens, so the
whole cost difference is the price part, as it should be; the ledger priced
the scripted tokens at the snapshot's prices, and no provider was called.

## Limits

- The overall condition's strata (kind, capability, tag, difficulty) need
  the suite's tasks; when the suite no longer loads they are left out with a
  warning.
- Holm across every stratum is conservative: with eight strata the smallest
  p is multiplied by eight, so a real effect in one stratum needs more tasks
  to show. The report says the family size.
- The policy's ceilings (false completion, retries, cost) read only the
  candidate arm, as `check` does; an old model that breaks the same ceiling
  is visible in the behaviour section, not in the decision.
- `migrate` runs real models when the agent and stack have keys: the
  forecast and `-budget-usd` are the guard, and nothing runs without `-yes`.
