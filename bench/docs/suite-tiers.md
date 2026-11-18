# Suite tiers

Status: built 18 Nov 2026 (W7).

Teams run a benchmark in tiers (docs: lane A lifecycle research): a small,
fast regression set on every pull request (Dropbox ran about 150 canonical
queries per PR in under 10 minutes), the full suite nightly or post-merge, and
a headroom set where the agent still fails often (Notion aims for about 30%
pass). A tier names a task subset in the suite, and an experiment picks one.

## Defining tiers

In `suite.yaml`:

```yaml
tiers:
  pr: [t01, t03, t04, t07]   # fast, discriminating
  nightly: all               # every task (the default meaning when undefined)
  headroom: [t05, t08]       # observed pass rate under about 0.5
```

A value is `all` or a list of the suite's task ids. `nightly` and `all` mean
every task when the suite does not define them; any other undefined tier is an
error that points at `suite tiers suggest`. Editing `suite.yaml` may make the
lock's fingerprints stale: re-lock as for any suite change.

## Using a tier

```yaml
tier: pr
conditions:
  - {id: clean, tasks: all, trials: 2}          # all = the tier's tasks
  - {id: model-outage, tasks: [t01, t03], trials: 1}  # an explicit list must lie inside the tier
```

The planner narrows `tasks: all` to the tier's tasks before anything else
(held-out split and quarantine still apply after), refuses an explicit task
outside the tier, and freezes the tier and its resolved task list in the
manifest (`manifest.tier`). Changing a tier later never changes a planned
experiment. The tier shows in `plan`, on the verdict (`verdict.tier`), in
`report.md` and on the Trials and tier panel.

## Comparing across tiers

Two experiments of different tiers ran different tasks: comparing their
aggregates compares different suites. `suite.CompareTiers` refuses it unless
the comparison is restricted to the tasks both ran, and returns those.
`extend status` uses it for a `-trials` follow-up (the same tasks, the same
tier). Trends already compare consecutive points only on their common tasks.
The leaderboard's tiers (tier 1, tier 2 against the leader) are computed on
common tasks too, but its success column with `scope=all` pools every run in
scope, whatever tier its experiment ran: use `scope=common` to rank entries
from different tiers on the same tasks. A tier filter on the leaderboard is
not built.

## Suggesting tiers

```
heimdall suite tiers suggest <suite> [-max-minutes 10] [-max-tasks 10] [-trials 1] [-workers 4] [-headroom 0.5] [-json]
```

It reads task health (`suite health`: pass rate, discrimination, verifier
flakiness, quarantine) and the median observed `wall_time_s` per task over
every sealed run of the suite, and prints a `tiers:` snippet to paste, with a
reason per task:

- `pr`: the most discriminating tasks first (by spread between configs, then
  pass rates near 0.5), never a flaky-verifier or quarantined task, never a
  task that always passes or always fails for every config (it carries no
  information in a gate), never a task with no observed wall time, until the
  estimated wall clock (tasks x trials x 2 arms x median wall time over the
  workers) would pass `-max-minutes`, at most `-max-tasks` tasks.
- `nightly`: every task.
- `headroom`: every task with an observed pass rate below `-headroom`.

Example, on a copy of four real experiments of coding-core (codewins batch,
Codex and mixed pilots, seeded regression; scripted and smoke runs left out of
both health and wall time):

```
tiers:
  headroom: [t04, t05, t08]
  nightly: all
  pr: [t01, t03, t04, t05, t06, t07, t08]
# pr estimated wall clock: 1.2 minutes
#   t02: not discriminating (always passes or always fails for every config): carries no information in a PR gate
#   t04: pr: discriminating, pass rate 0.29, median 24 s per run
#   t08: pr: discriminating, pass rate 0.07, median 24 s per run
```

With fewer than 6 tasks in `pr` it warns: fewer than 6 discordant tasks can
never reach p < 0.05, so such a gate can only catch broken runs and guard
failures, and says INCONCLUSIVE otherwise. That is the honest trade of a
10-minute gate, and why the pr tier should stay near 10 tasks.

## Presets

`experiments/presets/`:

| Preset | Tier | Design |
|---|---|---|
| `pr-smoke.yaml` | `pr` | adaptive rule task, min 1, max 2; $5 cap |
| `nightly.yaml` | `nightly` | fixed, 3 trials on clean, 1 on stale-notes; pre-declares the extension rule |
| `headroom.yaml` | `headroom` | fixed, 3 trials |

Copy one, replace the arms with your change, and plan it. The task rule's
cost and power are in [adaptive-trials.md](adaptive-trials.md).
