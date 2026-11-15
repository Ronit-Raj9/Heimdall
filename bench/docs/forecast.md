# Cost and time forecast

Before an experiment runs, the bench can say what it will probably cost and how long it will take, and whether that fits `budget.total_usd`. Every number comes from runs that already happened and were sealed. When there is no such history the forecast says "no history, cannot estimate" and stops there: it never fills the gap with a guess.

## Where to see it

| Surface | What it does |
|---|---|
| `bifrost-bench plan experiments/<file>.yaml` | Prints the slot counts as before, then the forecast. A forecast that cannot be read only warns; the plan still answers. |
| `bifrost-bench estimate [-json] [-allow-unpriced] experiments/<file>.yaml` | Plans the experiment exactly as `plan` does (same planner, same gateway preflight) and prints the forecast, or the JSON with `-json`. Nothing is frozen and nothing runs. |
| `POST /api/bench/estimate` | Same body and content types as `POST /api/bench/experiments/plan` (the experiment as YAML or JSON), write token required. Answers a `Forecast`. A refused plan comes back with HTTP 200, `fit: unknown` and the planner's error in `notes`, as plan does. |
| New experiment page | An estimate box under the plan result: total cost and time ranges, budget fit, the basis sentence, one row per arm, the judge line and the notes. |

## What the forecast rests on

For each arm of the planned manifest, the first basis that has runs wins:

1. **Same config** (`config_hash`): sealed runs whose arm resolved to the same agent config hash, on the same suite version.
2. **Same agent and model** (`agent_model`): sealed runs of another config with the same agent id and the same model, on the same suite version. A config change that does not touch the model (a new notes file, a new image) usually costs about the same; the basis text says "other configs" so nobody mistakes it for the first case.
3. **Budget bound** (`budget_bound`): no history, but the agent config sets `budgets.usd` and the pinned price snapshot lists its model. The forecast gives `budgets.usd` times the arm's runs as an upper bound on cost, and `budgets.wall_s` times runs as an upper bound on agent time (setup and grading not included). There is no low or median.
4. **None** (`none`): no history and no usable bound. Cost, tokens and time are unknown with the reason "no history, cannot estimate".

History is read through the store's verified sealed-run index (`SealedRuns`, original grades), the same reader the verdict and the leaderboard use. Left out, and counted in the notes:

- synthetic runs, recognised exactly as the live leaderboard does (`leaderboard.SyntheticArms`): a smoke experiment, a scripted control agent, or any run whose proxy ledger shows the scripted model;
- runs whose seal does not verify, or whose slot has more than one counted run;
- runs whose outcome does not score (`infra_error`, `invalid_evaluator`, `unknown`).

An experiment on another suite version is never opened past its manifest, and neither is one with no arm matching a planned arm (except on a judged suite, below).

## How the numbers are made

Per arm and per metric (`cost_usd`, `tokens`, `wall_time_s` from each run's `metrics.json`):

- Each planned slot is estimated from its own task's history when that task has 3 or more runs on the arm's basis, else from all of the basis's runs on the suite. `task_level` in the output says how many planned tasks used their own runs.
- From that distribution the slot takes the 10th, 50th and 90th percentile of one run (linear interpolation between order statistics). The arm's low, median and high are the sums over its slots.
- A run that did not observe a metric is left out of that metric, never counted as zero; the range's reason says how many.
- `per_run` gives the 10th, 50th and 90th percentile of one run over the whole basis.
- Fewer than 5 history runs are flagged as thin.

Low and high are sums of per-run percentiles. That is a planning range, not a confidence interval: summing percentiles overstates how far the total can swing, which is the safe side for a budget.

The total sums the arms. It is a history range when every arm has one, an upper bound when every arm has at least a bound, and unknown as soon as one arm has nothing.

Wall clock is agent time divided by the experiment's `concurrency` (or the scheduler's default of 2), assuming no queueing and no setup time. Treat it as a floor.

### Budget fit

`fit` compares the total cost with `budget.total_usd`:

| `fit` | Meaning |
|---|---|
| `fits` | Even the high end (or the upper bound) is within the budget. |
| `may_exceed` | The median fits, the high end does not. |
| `likely_exceeds` | Only the low end fits. |
| `exceeds` | Even the low end is over the budget. The scheduler stops leasing slots past the cap, so the experiment would end partly run. |
| `unknown` | Some arm has no estimate, or only an upper bound that is above the budget. |
| `no_budget` | `budget.total_usd` is not set. |

### Judge cost

For a judged suite (the manifest has a `suite.judge` input), the judge model's cost is forecast separately and never added to the agent cost or compared with the budget, the same way `judge_cost_usd` is reported apart. It rests on past judge records (`grade.judge.usd`) on the same suite version with the same judge config; a run graded without a judge call counts as zero, an unpriced judge record is left out. With no records the judge line says so.

## Example

The shape of the output, for `experiments/matrix-models.yaml` on a run folder where only `gpt-5.4-mini` has history with Codex on `coding-core@1.0.0` (values in angle brackets come from your history):

```
$ bifrost-bench estimate experiments/matrix-models.yaml
experiment e_matrix_models_<id>  manifest <hash>
  clean             48 slots  arm A 16, arm B 16, arm C 16
  counted 48, compatibility 0, total 48 agent episodes
forecast  History on this suite: 1 arm from sealed runs of the same agent and model, 2 arms from no history (cannot estimate). Each slot uses ...
  arm A      gpt-5.4-mini                  16 runs  cost <low> to <high> (median <median>)  agent time <low> to <high> (median <median>)
             basis agent_model: <n> sealed runs of agent codex with model gpt-5.4-mini (other configs) on coding-core@1.0.0, in <k> experiments; 8 of 8 planned tasks estimated from their own runs
  arm B      gpt-5.4-nano via openai       16 runs  cost unknown: no history, cannot estimate  agent time at most 4h
             basis none: no history, cannot estimate: no sealed run of this config or of this agent and model on this suite
  arm C      gpt-5.4                       16 runs  cost unknown: no history, cannot estimate  agent time at most 4h
             basis none: no history, cannot estimate: no sealed run of this config or of this agent and model on this suite
  total      cost unknown: no estimate for arm B, C  agent time at most <bound>  wall clock at most <bound> (concurrency 3)
  budget $3.00: unknown (some arm has no estimate: no estimate for arm B, C)
```

The agent time bound for B and C is `budgets.wall_s` (900 in `agents/codex.yaml`) times 16 runs. A pilot of those two arms, or `budgets.usd` in the agent config, turns their cost from unknown into a range or a bound.

## Limits

- The forecast is only as representative as the history: a new suite version, a new model or a new agent has none, and says so.
- Probe conditions can change cost (a model outage makes runs short, a stale note can make them long). History is pooled across conditions per task, so a plan with a very different condition mix than its history may land outside the range.
- Prices are the pinned snapshot's. History costs were computed when the runs were sealed; a snapshot change since then is not re-applied.
