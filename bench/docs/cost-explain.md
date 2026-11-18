# Cost explanation: why arm B cost more

`heimdall cost explain` answers the question a team asks after every model
or harness swap: why did B cost more (or less) than A? It splits the
difference in cost per run and in cost per verified success into additive
parts read from the sealed cost rows, keeps what measuring cost (judge, user
simulator) apart, and flags a silent cache break with its likely cause.
`heimdall cost pareto` places every arm on the cost against verified success
plane with the frontier marked. Code: `bench/costexplain/`,
`cmd/bifrost-bench/costexplain.go`, `handlers/costexplain.go`; UI panels on
the verdict, matrix, leaderboard and migration pages
(`ui/components/migration/`).

```
heimdall cost explain -exp <id> [-a A] [-b B] [-condition clean] [-grade-source original] [-json]
heimdall cost pareto  -exp <id> [-x cost_per_success|cost_per_run] [-condition clean] [-json]
GET /api/bench/experiments/{id}/cost-explain?a=&b=&condition=&grade_source=
GET /api/bench/experiments/{id}/pareto?x=&condition=
GET /api/bench/leaderboard/pareto?x=&suite=&condition=&scope=&synthetic=&grade_source=&split=&k=
```

Both read the run folder only: no gateway, no key, no cost.

## What it reads

The graded runs of both arms on one condition, as the matrix and the board
read them: counted runs whose seal verifies and whose outcome scores, with
success from `verified_success`. For each run:

- `cost.json`: one row per physical upstream attempt, priced from the pinned
  snapshot, with the four disjoint token buckets (fresh input, cache read,
  cache write, output) and the snapshot reference (`evidence/cost.go`).
  `evidence.json` is the fallback.
- `gateway.requests.jsonl`: Bifrost's rows, for fallbacks, rejected requests
  (4xx), latency, and from the raw detail the system prompt hash (input
  history, Responses `instructions`, Anthropic `system`), the tool list hash,
  the provider key, `prompt_cache_key` and refusals.
- `judge_cost_usd` and `simulator_cost_usd` per run, reported apart.
- The manifest's resolved arm configs, for the config diff (`agent diff`'s
  field and file comparison).

## The decomposition

Cost per run of an arm is, over the rows the price snapshot reproduces
exactly (each row's cost is re-derived from its tokens and must match its
sealed cost to one part in a million before it is split):

```
C = n * ( I * ((1 - sr - sw) * p_in + sr * p_read + sw * p_write) + o * p_out ) + W + U
```

with `n` priced model turns per run, `I` input tokens per turn, `o` output
tokens per turn, `sr` and `sw` the cache read and cache write shares of
input, the four effective bucket prices (cost of the bucket over its tokens,
so a mix of served models is one price), `W` the cost of attempts that
returned no usage (retries, fallbacks, rejected or failed requests; providers
bill none of them, so this is normally $0 and the count is reported) and `U`
priced rows the snapshot cannot split (another snapshot priced them, or the
model is not listed; also runs with no cost rows but an observed `cost_usd`).

The difference `C_B - C_A` is split by the exact Shapley value over the nine
factors (512 subsets): each factor's part is its average marginal effect over
every order of switching the factors from A's value to B's. The parts sum to
the difference exactly and do not depend on an order. A factor that is
undefined in one arm (no cache write tokens, so no cache write price; no
turns at all) takes the other arm's value; it always multiplies a zero in its
own arm, so the arm's cost is unchanged and the change goes to the factor
that made it zero. The parts:

| part | from factors |
|---|---|
| Price of the served model | the four bucket prices |
| Input tokens per turn | `I` |
| Output tokens per turn | `o` |
| Cache read share | `sr` |
| Cache write share | `sw` |
| Model turns per run | `n` |
| Wasted attempts | `W_B - W_A` |
| Priced rows the snapshot cannot split | `U_B - U_A` |
| Cost the rows do not know | unpriced rows, as bounds |

Per verified success, cost per success is `C / s` with `s` the success rate
over the same runs (all-attempt: failures' cost included, as the board's
cost per success). The two-factor Shapley split of `C` times `1/s` gives the
cost parts scaled by the mean of `1/s_A` and `1/s_B`, plus one more part,
**success rate (cost of failed runs)**: `(C_A + C_B) / 2 * (1/s_B - 1/s_A)`.
With no verified success in an arm, cost per success is undefined and the
waterfall says so.

**Unknown is never zero.** A row whose cost is unknown (a model the snapshot
does not list, usage not recorded, a failed attempt without a status) is
carried as bounds when it has them (`evidence/cost.go` bounds an unlisted
model by the snapshot's highest price per bucket) and makes the arm's cost
and the total unknown with those bounds; without bounds the total is unknown
without bounds. The observed parts still sum to the observed part of the
difference (`known`).

**Cache-neutral cost** prices every input token at the fresh input price, so
two arms can be compared as if neither had a cache. It is per row, from the
same snapshot.

Tests: `costexplain/costexplain_test.go` checks the identity (parts sum to
the total, and the total equals B minus A computed from the rows) on three
fixtures including an arm with no answered turn and an unlisted model; that
only the price part moves when only the model changes; that the cache read
part equals the price gap on the cached tokens; unknown with and without
bounds and a run without rows; the Shapley sum and antisymmetry; the
frontier. `cmd/bifrost-bench/migrate_test.go` runs it on the committed
recorded bundle.

## Silent cache break

A drop of 10 points or more in the cache read share, from a share of at
least 10%, over at least 1,000 input tokens each side, is flagged:

- **between the arms**, against the arm with the higher share;
- **over time** within an arm: its first half of runs by start time against
  its second half.

The likely cause is named from the evidence, strongest first: the served
model changed (a cache never carries across models); the system prompt hash
changed; the tool list hash changed; one arm sends `prompt_cache_key` and the
other does not; the system prompt differs between runs of the same task
(per-run text such as a date, a path or a run id); different provider keys;
then the config diff groups. With none of these the cause is "not shown by
the evidence" (provider eviction, or a `cache_control` change the rows do
not record). Over time with the same prompt and tool hashes throughout, the
cause is outside the agent.

## Pareto plane

Each config is a point: x is cost per verified success (default) or median
cost per run, y is verified success with its 95% interval. A point is on the
frontier when no other point is at least as cheap and at least as
successful and better on one; each dominated point lists the frontier points
that dominate it. A point whose cost or success is not observed is listed
with the reason and never drawn (not at zero). The frontier is read from
point estimates; overlapping intervals mean the order is not established,
and the API note says so. The chart is hand-drawn SVG (no chart
dependency), with the frontier filled and joined and dominated points
hollow and grey, and the same facts in a text list under it.

## A real output (excerpt)

`heimdall cost explain -exp e_batch_frameworks_9z0h` on a copy of the real
batch (Codex against the custom-python agent, both on gpt-5.4-mini, 24 clean
runs each), run on 18 Nov 2026 from a scratch copy of `bench/runs`:

```
| | arm A | arm B |
| label | codex gpt-5.4-mini | custom-python gpt-5.4-mini |
| cost per run | $0.032 | $0.025 |
| cost per verified success | $0.048 | $0.035 |
| cache-neutral cost per run | $0.112 | $0.047 |
| cache read share | 89.6% | 73.7% |
| model turns per run | 9.3 | 7.6 |
| input / output tokens per turn | 14223 / 308 | 5863 / 390 |

## Cost per run, B minus A
A $0.032, B $0.025, difference -$0.00765.
| Price of the served model | $0 | input $0.750 vs $0.750, cache read $0.075 vs $0.075, ... |
| Input tokens per turn | -$0.014 | 14223 vs 5863 input tokens per turn |
| Output tokens per turn | +$0.00312 | 308 vs 390 output tokens per turn |
| Cache read share | +$0.00926 | 89.6% vs 73.7% of input tokens read from the prompt cache |
| Model turns per run | -$0.00611 | 9.3 vs 7.6 priced model turns per run |
| **total** | -$0.00765 | sum of the parts |

## Cache
- Cache read share of arm B dropped (between_arms): 73.7% against 89.6% in A.
  Likely cause: system prompt changed: sha256 b6160f41fd37 vs 3fb161c44462, ... in the gateway's raw rows.
  - tool list changed: sha256 62004cccf98d vs ab8f0c3e5cb4 in the gateway's raw rows
  - arm A sends a prompt_cache_key on 448 of 448 rows and arm B on 0 of 364
  - config diff: prompt_notes changed (env.BENCH_INSTRUCTION_FILE, notes.user_path)
```

So custom-python is cheaper because it sends far fewer input tokens per turn
and takes fewer turns, and it gives back about $0.009 per run by caching less
of what it sends: its requests carry no `prompt_cache_key` and its prompt
differs from Codex's. Without the cache Codex would cost $0.112 per run
against $0.047.

`cost pareto -exp e_batch_frameworks_9z0h` on the same copy:

```
  B  custom-python gpt-5.4-mini  x $0.035  y 70.8% [33, 100]  frontier
  A  codex gpt-5.4-mini          x $0.048  y 66.7% [41, 92]   dominated by B
```

## Limits

- Bucket prices come from the price snapshot the bench loads now. Rows priced
  by another snapshot are split only when the current one reproduces their
  sealed cost; otherwise they are in the unattributed part, observed but not
  split.
- Bifrost bills no failed attempt, so retries and fallbacks show up as counts
  and time, and as cost only when a provider billed them.
- The prompt and tool hashes need Bifrost's raw rows (`gateway.requests.jsonl`
  with request detail). Without them the cache cause falls back to the config
  diff, and says so.
- The waterfall uses means over runs; the board shows medians per run. Cost
  per success is the same in both (total over successes).
