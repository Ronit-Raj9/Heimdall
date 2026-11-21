# Trends

A release verdict compares two arms in one experiment. A slow slide across many experiments, a few points per week, can pass every single verdict. Trends line up the results of each agent config lineage over time, so that slide shows up, and flag the newest point when it is significantly worse than the one before it.

Trends read the derived results index (`docs/results-index.md`), never the sealed folders directly, and nothing that decides a release reads trends. A trend is a monitoring signal, not a gate.

## Where it lives

| Part | Path |
|---|---|
| Compute | `trends/` (pure: `trends.Build(reader, query, now)` over a `resultsindex.Reader`) |
| API | `GET /api/bench/trends?suite=&agent=&metric=&condition=&synthetic=`, `handlers/trends.go`, returns `trends.Response` plus `mode` |
| UI | `/trends`: one line chart, filters, and a table of every point under it |
| Contract | `trends/types.go`, `handlers/testdata/contract/Trend*.keys`, `ui/lib/types/bench.ts` |

Absent parameters take the defaults: every suite, every agent, `verified_success`, `clean`, and `synthetic=exclude` (live runs only). `metric` is one of `verified_success`, `cost_per_success`, `tokens`, `wall_time_s`, `false_completion`; `synthetic` is `exclude`, `include` or `only`. Anything else is a 400. Each request first brings the index up to date incrementally (only experiments whose seals or analyses changed are read again).

## Lineages and points

A **lineage** is one agent config id and model, across experiments: `codex` with `claude-sonnet-4-5` is one lineage however its prompt, budgets or image change. A **point** is one config hash of that lineage in one experiment, dated by the experiment's creation time. Points are ordered by date, then experiment, then arm. Where a point's config hash differs from the previous point's, `config_changed` is true: the chart marks it, so a change in the line can be read against a change in the config. An experiment that ran two arms of the same lineage (A and B with different prompts) gives two points on the same date.

Which runs count follows the leaderboard: counted, sealed, verified runs whose outcome scores (intention to treat: a timeout or a crash is a failure), under the chosen condition. Runs of an experiment whose newest verdict is INVALID are left out, as is any run the index keeps with an evidence problem (for example a slot with two counted runs, which the verdict would call INVALID): those are counted in `invalid`, never charted, as are runs that called real external hosts (`external: live` or `record`) and, by default, synthetic runs.

Per point:

| Field | Meaning |
|---|---|
| `success` | Mean over tasks of the per-task verified success rate |
| `success_low`, `success_high` | Descriptive 95% t interval over the task rates (as the leaderboard's), null under 2 tasks |
| `cost_per_success` | Observed cost of every scored run over the verified successes; null with no success, and null when any run's cost is unknown (a partial sum would flatter) |
| `tokens`, `wall_time_s` | Median per run over runs that observed them |
| `false_completion` | Share of runs whose claim and outcome are known that claimed success and failed (a passing run counts as 0) |
| `value` | The charted metric |
| `decision`, `policy` | The experiment's newest analysis: verdict decision and policy decision |
| `release` | `regressed` when that verdict was REGRESSED, else `blocked` when its release policy blocked, else empty. The chart marks these points |

A number nobody observed is null, never zero.

## The regression flag

Each point after the first is compared with the newest earlier point of its lineage on the same suite (`id@version`) and suite lock, on the tasks both ran. Task ids are paired only inside one locked suite: two suites, or two versions of one, can reuse an id such as `t01` for unrelated tasks, so a point with no earlier point of its suite is `cannot_tell`. The comparison is with the sign-flip test (`stats.SignFlip`, the test behind the verdict). Exact means full enumeration of the 2^n sign vectors, which `stats.SignFlip` does for up to 22 nonzero task differences; with 23 or more it estimates p by Monte Carlo (1,000,000 random sign vectors, `Exact=false` in the result), so a p near alpha on a large suite carries sampling error. In it the per-task differences are the newer task success rate minus the older one. `lineages[].regression` is the newest point's result.

| Status | When |
|---|---|
| `regressed` | p < 0.05 and success went down |
| `improved` | p < 0.05 and success went up |
| `no_change` | At least 6 common tasks and either none changed or p is 0.05 or more |
| `cannot_tell` | Fewer than 6 common tasks, or fewer than 6 tasks whose rate changed: the two-sided test cannot reach p < 0.05 with fewer than 6 discordant tasks (`stats.MinPossibleP(5)` is 0.0625), so a small sample is never called a regression, nor "no change" |
| `first` | The first point of its lineage |

There is no naive threshold: a drop of 20 points on 4 tasks is `cannot_tell`, a drop of 10 points spread over 12 tasks can be `regressed`. The reason field says which, in plain words, with the p value and the counts. The test compares success only; the other metrics are charted for reading, not flagged.

## The page

`/trends` has filters for suite, agent, metric, condition and the synthetic choice, one line per lineage, distinct markers (not only colors) for a config change and for a regressed or blocked release, the lineage's regression status, and a table under the chart with every point and its numbers. With fewer than 2 points there is nothing to draw, and the page says so.
