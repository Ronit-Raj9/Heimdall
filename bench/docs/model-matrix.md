# Model comparison matrix

An experiment compares arms with a paired verdict of arm B against arm A. A common question is wider: "same agent, which of these N models is best?" or "which of these N agents?". The matrix answers it without touching the verdict machinery: a short `matrix:` block expands into arms at plan time, and a matrix view compares every arm with every other from the experiment's sealed runs.

## Writing one

```yaml
name: matrix-models
suite: coding-core@1.0.0
base: agents/codex.yaml
matrix:
  models:
    - gpt-5.4-mini
    - {model: gpt-5.4-nano, provider: openai}
    - gpt-5.4
primary: {condition: clean, metric: verified_success}
conditions:
  - {id: clean, tasks: all, trials: 2}
budget: {total_usd: 3}
```

(`experiments/matrix-models.yaml` is this example in full, with the expected spend in a comment. It has not been run: it costs money.)

- `models`: model names, or `{model, provider}` when the gateway provider differs from the agent config's (`gateway.provider`).
- `agents`: optional agent config paths. Without it every arm uses `base`; with it, every agent is crossed with every model (agents first). Agents alone compare agent configs with their own models.
- `allow_unpriced`: plan a model the pinned price snapshot does not list. Without it, or the `-allow-unpriced` flag on `plan` and `estimate` (which sets it), the plan is refused for such a model: its cost would be unknown, so cost per success could never be compared. Only models listed under `matrix.models` are checked.

The planner expands the matrix into arms `A`, `B`, `C`... in that order, labelled by what varies ("gpt-5.4-nano via openai", "codex / gpt-5.4"), then plans them exactly like hand-written arms. A matrix and `arms:` together are refused, as are an empty or repeated entry, fewer than 2 or more than 26 combinations, and a model list with neither `agents` nor `base`.

The frozen manifest records both the matrix block and the expanded arms. An experiment without a matrix encodes exactly as before: the field is omitted when empty, and a test re-hashes every recorded manifest under `docs/results` to prove their hashes did not move.

The paired verdict still runs and still compares B with A on the primary endpoint; its guards and policy are unchanged. Read it as one pair among several, not as the answer to the matrix question.

## Reading it

| Surface | What it does |
|---|---|
| `bifrost-bench matrix -exp <id> [-condition <id>] [-grade-source original\|<regrade id>] [-k n] [-alpha 0.05] [-json]` | Prints the matrix table, the tier reasons, every pair and the note. Reads the run folder only. |
| `GET /api/bench/experiments/{id}/matrix?condition=&grade_source=&k=&alpha=` | Answers a `ModelMatrix`. |
| `/matrix?exp=<id>` in the UI | The matrix table, a pairwise grid of Holm-adjusted p values (hover for the reason), the note, and a condition switch. |

The condition defaults to the experiment's primary condition; `conditions` lists every condition with a graded run, for the switch.

### Rows

One row per arm, in rank order, with the same numbers the leaderboard computes for an entry, by the same code: verified success (mean over tasks of the per-task rate, 95% descriptive interval over tasks), passes, pass^k (k is min(3, trials per task) unless `-k` sets it), and the per-run medians of cost, tokens and wall time, the false completion share and cost per success (total cost over verified successes). Order: success, then lower cost per success, then more runs.

A run enters when it is a counted, sealed run whose outcome scores (intention to treat: a timeout or crash counts as a failure). Runs with a seal problem, a duplicate counted run, or an outcome that does not score are counted in `excluded` by reason. A synthetic experiment (smoke, scripted agent or scripted model) is marked `synthetic`, and its numbers are not findings.

### Pairs and tiers

Every pair of arms is compared by the exact sign-flip test on the per-task success rates of the tasks both ran (`stats.SignFlip`, as the verdict and the leaderboard use). The p values of all pairs are Holm-adjusted together, so three arms are a family of three tests and five arms of ten. A pair is significant only when its adjusted p is below alpha.

Tiers come from the leader's pairs: tier 1 cannot be told apart from the leader, tier 2 is significantly behind it. Rank alone is never a claim that one arm beats another.

### "Cannot separate"

A sign-flip test on n differing tasks cannot give a p below 2 / 2^n, and Holm multiplies the smallest p by the number of pairs. When even the most extreme result on the largest common task set cannot get below alpha after that, `separable` is false and the note says so in plain words, for example:

> Cannot separate any two arms on this suite: with at most 4 common tasks the smallest possible p is 0.12, and Holm across 3 pairs makes it 0.38, not below alpha 0.05 whatever the results. More tasks are needed; more trials per task do not help, since the test is over tasks.

With 3 arms, a pair can be separated once the two arms differ on at least 7 tasks, all in the same direction: 2/2^7 = 0.0156, and Holm across 3 pairs makes it 0.047, below 0.05. Tied tasks add no sign, so on 8 common tasks one tie still leaves a separable pair; 6 differing tasks cannot (2/64 times 3 = 0.094). The threshold always follows the actual Holm family and the smallest possible p (`stats.MinPossibleP`). Every pair also carries its own reason ("too few differing tasks (2 of 8 common)", "p 0.31 after Holm across 3 pairs").

## What it does not do

- It invents no statistics: the test, the interval, pass^k and Holm are the ones the verdict and the leaderboard already use.
- It does not pool across experiments; that is the leaderboard's job. A matrix is one experiment, one suite version, one condition at a time.
- It does not change how a two-arm experiment plans, hashes, or is decided.
