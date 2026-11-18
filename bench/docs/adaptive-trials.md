# Adaptive trials and buying more evidence

Status: built 18 Nov 2026 (W7). The fixed design stays the default: an
experiment without `trials:`, or with `trials: {mode: fixed}`, plans, runs and
hashes exactly as before.

Repeated sandboxed runs are what make a verdict honest, and they are what
make it expensive (HAL spent about $40,000 on 21,730 rollouts; lane A, unmet
need 8). A gate that cries wolf gets muted (lane E, risk 4), so nothing here
may raise the false-alarm rate. This page describes three ways to spend fewer
runs without weakening the verdict's false-alarm guarantee, what each one
costs in power (measured, not assumed), and what none of them allows.

| Feature | What it saves | What it costs |
|---|---|---|
| `rule: task` | trials on tasks where both arms agree every time | power, measured below: less than a fixed design of the same cost |
| `rule: sequential` | whole groups of tasks once a look is clear, or (with `futility: nonbinding`) once a look shows no trend | power, measured below; with fewer than about 6 tasks per look it rarely stops early |
| `extend` with a pre-declared rule | a second experiment only when the first was INCONCLUSIVE | half of alpha is reserved for it before the first run |

## The model: a frozen maximum design

The manifest freezes the **maximum design**: every slot the experiment could
ever run (task x arm x condition x trial), with its arm order, run inputs and
hash, exactly as a fixed experiment does. Slots beyond the minimum are marked
`optional: true` and the frozen plan (`manifest.adaptive`) records the rule,
its parameters, and for the sequential rule the task groups and the alpha of
each look. All of it is hashed before the first run. Changing the rule, the
minimum, the looks or the alphas makes a new experiment.

A slot is in one of three states:

| State | Meaning | In the verdict |
|---|---|---|
| `required` | always runs (the minimum design) | a missing run is an unknown, as for every fixed slot |
| `released` | optional, and the rule asked for it | a missing run is an unknown |
| `not_required` | optional, and the rule never asked for it | not missing: no placeholder, no unknown, counted as a run saved |

Which optional slots were released is **never stored**. It is a pure
function of the sealed runs (`verdict.ReplayAdaptive`). The scheduler replays
the rule each time its queue drains and queues what the rule now releases; the
verdict replays the same rule over the verified runs at analysis time. So the
analysis always follows the pre-registered rule applied to the evidence, and a
scheduler bug can only waste or miss runs, never change what is analysed. A
counted run of a slot the replay says was not required (possible only after a
regrade changed an outcome the rule read) is left out of the analysis and
named in the `adaptive_allocation` instrument check.

"No slot is ever dropped" still holds in the only sense that matters: every
slot the frozen rule requires enters the verdict, with or without a run.

## Rule `task`

```yaml
conditions:
  - {id: clean, tasks: all, trials: 4}   # with adaptive trials, a condition's trials is the maximum
trials: {mode: adaptive, rule: task, min: 2, max: 4}
```

Every task runs `min` trials per arm. After trial k (k >= min) the task gets
trial k+1, in both arms, unless every run of trials 1..k, in every arm, has
an observed value and all those values are equal: both arms unanimous and
agreeing (both always pass, or both always fail). It stops at `max`. An
unknown outcome never lets a task stop. The rule applies to the decision
conditions (the primary and every `no_regression` guard, or the conditions
listed in `trials.conditions`); other conditions run every planned trial.
Trials per arm stay equal within every (task, condition), so the
unequal-trials gate never fires.

The primary test is unchanged: the exact two-sided sign-flip test over
per-task differences of mean success (DESIGN.md 9.3).

**Why the test stays exact.** The sign-flip test is exact when, under the null
hypothesis, swapping the arm labels of a task leaves the distribution of
everything observed unchanged and turns d_i into -d_i, independently across
tasks (DESIGN.md 9.3, "Why a task-level flip is exact"). The rule decides how
many trials a task gets from the unordered collection of that task's values
only: "all equal and observed" is the same statement after the arm labels are
swapped. So the number of trials N_i is invariant under the swap, the swap
still maps each possible record of task i to another record with the same
null probability and the same N_i, and d_i still maps to -d_i. Conditional on
|d_1|, ..., |d_n| the signs remain independent fair coins, which is all the
test needs. The rule reads no other task and never asks which arm is ahead.
An asymmetric rule ("more trials while B is behind") breaks exactly this, and
the simulation below shows the inflation it causes.

## Rule `sequential`

```yaml
conditions:
  - {id: clean, tasks: all, trials: 2}
trials: {mode: adaptive, rule: sequential, looks: 2, alpha_split: obf, futility: curtail}
```

The primary condition's tasks are shuffled once from the experiment seed and
dealt into `looks` groups (2 to 5) of near-equal size, frozen in the manifest.
Look 1's tasks always run; every slot of a later look's task (every
condition) is optional. After look k the primary effect over looks 1..k is
tested, at both missingness bounds, at that look's alpha:

- **efficacy**: IMPROVED or REGRESSED at both bounds at alpha_k: stop; the
  verdict decides at alpha_k.
- **futility**: stop with no directional decision (see below).
- otherwise release look k+1. At the last look the verdict decides at its
  alpha.

**Alpha spending.** The looks share alpha by a Bonferroni split: the chance of
any false directional result over all looks is at most the sum of the look
alphas, whatever the dependence between looks, because each look's sign-flip
p-value is exact on its own. `alpha_split: obf` (default) gives each look the
increment of the Lan-DeMets O'Brien-Fleming spending function
alpha(t) = 2 - 2 Phi(z_{1-alpha/2} / sqrt(t)) at the cumulative task fraction
t; for two equal looks at alpha 0.05 that is 0.0056 then 0.0444. `equal` gives
alpha/looks to each; `alphas: [...]` lists them explicitly (sum at most
alpha). The split is conservative: a true group-sequential boundary would
spend less, but would need the joint null distribution across looks, which
the discrete sign-flip test does not give cheaply. The final decision uses the
look alpha it stopped at, never the nominal alpha.

**Futility `curtail` (default) is exact.** It stops only when no completion of
the remaining tasks could make any later look directional, so it never changes
a decision: it only skips runs whose results could not matter. Per-task
differences of a rate metric lie in [-1, 1], and for the sign-flip test the
smallest p-value over every completion of m more tasks is reached with all m
at +1 or all m at -1. Lemma: if sum(d) > 0, raising one d_j by delta never
raises p. Pair each sign vector with its negation and take the member with
s_j = +1; its statistic moves from T to T + delta while the observed sum moves
from S to S + delta. For T >= 0, |T + delta| >= S + delta is T >= S,
unchanged; for T < 0 it implies |T| >= S + 2 delta, so the vector was already
counted. A completion with a positive total is therefore dominated by all +1,
one with a negative total by all -1, and one with total zero has p = 1. So
two sign-flip evaluations per bound decide futility exactly
(`verdict.BestCaseP`, checked against brute force over a grid of completions in
`TestBestCasePExact`). A directional result needs both bounds, so one bound
that cannot get there is enough. Curtailment needs a metric in [0, 1]; on any
other metric it is switched off with a warning at plan time. Above 22
discordant tasks the sign-flip test is Monte Carlo (stats.SignFlip), and so is
the futility computation.

**Futility `nonbinding`** also stops when a look shows no trend at all: the
sign-flip p at both bounds is at least 0.5. Any futility stop only removes
chances to reject, so it can never raise the false-alarm rate; it does cost
power, measured below. It is where the sequential rule saves runs when the
change makes no difference, which is the common case of a PR gate.

**What early stopping does to guards.** Guards (`no_regression` on clean, the
integrity guard) are evaluated on the tasks that ran. A stop at look 1 gives
the regression detector half the tasks. The verdict page says how many tasks
the stop used.

## Simulation

Seeded simulations in `verdict/sequential_sim_test.go` and
`verdict/sequential_model_test.go` (`go test ./verdict -run Sim -v` prints
these tables; `-short` cuts the replications). Each draws whole experiments:
per-task success probabilities spread from 0 to 1, Bernoulli runs, the real
rule code (`taskTrials`, `replaySequential`), and the exact sign-flip test,
compared with fixed designs on the same draws. "Null" means both arms share
each task's success probability; alpha is 0.05; the type-I assertion is
alpha + 3 standard errors.

**Rule task** (4000 replications per row; power against +50 pp on half the
tasks; "regression suite" rows: most tasks always pass or always fail, power
against a 50 pp drop on half the tasks):

| tasks | min-max | type I: adaptive | type I: fixed max | type I: asymmetric control | power: adaptive | fixed max | fixed max-1 | fixed min | runs adaptive / fixed max, null | alt |
|---|---|---|---|---|---|---|---|---|---|---|
| 10 | 2-5 | 0.0030 | 0.0110 | 0.0355 | 0.224 | 0.356 | 0.295 | 0.122 | 69% | 80% |
| 16 | 2-4 | 0.0152 | 0.0240 | 0.0695 | 0.345 | 0.461 | 0.360 | 0.239 | 75% | 79% |
| 20 | 1-3 | 0.0067 | 0.0267 | 0.1047 | 0.186 | 0.434 | 0.302 | 0.128 | 53% | 57% |
| 16, regression suite | 2-4 | 0.0000 | 0.0000 | 0.0000 | 0.273 | 0.565 | 0.432 | 0.234 | 57% | 68% |
| 20, regression suite | 2-4 | 0.0000 | 0.0000 | 0.0000 | 0.592 | 0.824 | 0.710 | 0.473 | 58% | 68% |

Asymmetric control (20 tasks, min 1, max 6, more trials only while B is not
ahead): type-I error 0.334 against 0.008 for the symmetric rule on the same
draws. The simulation sees inflation when there is some.

Reading: the task rule holds alpha in every setting (at or below the fixed
design, which is itself conservative at these sizes because the sign-flip
test is discrete). It saves 20 to 45% of the runs. **It is not more powerful
than a fixed design of the same cost**: a fixed design with one trial fewer
(max-1, 67 to 80% of the runs) had more power in every row. Stopping a task
that looked concordant after `min` trials fixes its difference at 0, and on
tasks with a true but noisy effect that loses information an even spread of
trials keeps. Use the task rule when the point is a hard cap on runs with a
guaranteed minimum on every task (a PR gate); when power per dollar is the
point, a fixed design with fewer trials is the better buy.

**Rule sequential**, real replay (1000 replications; power against +60 pp on
half the tasks; 2 trials per arm):

| tasks | looks | split | futility | type I: sequential | type I: fixed | power: sequential | power: fixed | stopped early (alt) | futility stops (null) | runs / fixed, null | alt |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 20 | 2 | obf | curtail | 0.0210 | 0.0230 | 0.406 | 0.435 | 0.000 | 0.000 | 100% | 100% |
| 20 | 2 | equal | curtail | 0.0040 | 0.0120 | 0.294 | 0.424 | 0.011 | 0.000 | 100% | 99% |
| 18 | 3 | obf | curtail | 0.0230 | 0.0260 | 0.297 | 0.351 | 0.047 | 0.150 | 95% | 96% |
| 20 | 2 | obf | nonbinding | 0.0130 | 0.0180 | 0.338 | 0.413 | 0.000 | 0.805 | 60% | 79% |

Larger suites, through a lattice model of the same rule (the exact sign-flip
distribution of differences k/T by convolution; `TestSequentialModelMatchesReplay`
checks that the model and the real replay stop at the same look for the same
reason on 400 draws per futility mode; 2000 replications; power against +30 pp
on half the tasks):

| tasks | looks | split | futility | type I: sequential | type I: fixed | power: sequential | power: fixed | stopped early (alt) | futility stops (null) | tasks run, null | alt |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 40 | 2 | obf | curtail | 0.0300 | 0.0335 | 0.340 | 0.351 | 0.027 | 0.000 | 100% | 99% |
| 40 | 2 | equal | curtail | 0.0180 | 0.0280 | 0.292 | 0.364 | 0.098 | 0.000 | 100% | 95% |
| 60 | 3 | obf | curtail | 0.0260 | 0.0330 | 0.460 | 0.517 | 0.196 | 0.000 | 100% | 93% |
| 20 | 2 | obf | nonbinding | 0.0150 | 0.0195 | 0.117 | 0.149 | 0.000 | 0.806 | 60% | 69% |
| 40 | 2 | obf | nonbinding | 0.0240 | 0.0320 | 0.315 | 0.352 | 0.019 | 0.677 | 66% | 83% |
| 60 | 3 | obf | nonbinding | 0.0215 | 0.0325 | 0.394 | 0.513 | 0.179 | 0.814 | 50% | 71% |

Futility exactness, by simulation as well as by the lemma: in 800 draws of an
18-task, 3-look design, 111 stopped for curtailment futility, and on none of
them would any later look have been directional (`TestSimFutilityNeverLosesARejection`).

Reading: the sequential rule holds alpha in every setting. With curtailment
alone it costs little power (OBF) but rarely saves anything: an exact
futility stop needs a hopeless first look, and agent suites are small. OBF
keeps more power than an equal split. `futility: nonbinding` is what saves
runs when nothing changed (a third to a half of them under the null) at a
measurable power cost, larger with three looks. Prefer two looks.

## Buy more evidence

An INCONCLUSIVE verdict carries `more_evidence` and the verdict page and
report say: "to detect X pp you need about N more tasks, estimated cost $Y".
X is |d_bar| (10 pp when d_bar is 0); N comes from the minimal-detectable-effect
formula of DESIGN.md 9.3 with the measured SD of an A/A arm when there is one,
else the assumed SD (labelled); the cost is N times the design's runs per
task times the mean observed `cost_usd` per run of this experiment (the same
config-hash history the forecast uses first). Unknown cost is shown as
unknown, never as zero.

```
heimdall extend -exp <id> -tasks N      # N tasks the experiment did not run, drawn from its seed
heimdall extend -exp <id> -trials K     # K more trials of its tasks
heimdall run experiments/<name>-ext.yaml
heimdall extend status -exp <follow-up id>
```

`extend` refuses a verdict that is not INCONCLUSIVE (`-force` overrides),
writes a new experiment (its own manifest and hash) with
`extends: {exp_id, manifest_hash, kind, rule}`, and keeps only the decision
conditions. New tasks are drawn by the parent's seed, so nobody picks them.

**A follow-up is analysed together with the first experiment only under a
combination rule the first experiment pre-declared** before its first run:

```yaml
trials:
  mode: fixed
  extension: {rule: inverse_normal, alpha_first: 0.025, weight_first: 0.5}
```

With it, the experiment's own verdict decides at `alpha_first` (default
alpha/2). `extend status` combines the two stages' one-sided exact sign-flip
p-values, each stage on its own runs only:
Z = w1 Phi^-1(1 - p1) + w2 Phi^-1(1 - p2), w1^2 = `weight_first`, w1^2 + w2^2 = 1,
and decides each direction at (alpha - alpha_first)/2, at both missingness
bounds. Why it holds: the follow-up's p-value is exact given everything the
first stage showed, however its size was chosen, so the combination keeps its
level even when N depends on the first result (an analyst who orders a small
follow-up when B leads and a large one otherwise); Bonferroni over the two
analyses keeps the whole at alpha. One-sided p-values are half the two-sided
one when the sum points that way and 1 otherwise, which is valid and
conservative. The follow-up's own verdict is a stage, not a decision: a
directional primary result there is shown as INCONCLUSIVE with an
`extension_stage` check pointing at `extend status`; a failed guard still makes
it REGRESSED, since holding is always allowed. Either stage INVALID makes the
combination INVALID.

**Without a pre-declared rule** the follow-up decides on its own (it is a new
experiment on new evidence), and `extend status` shows the pooled d_bar
labelled exploratory, with no decision. That is DESIGN.md 9.3's rule ("a
re-analysis that pools old and new runs is labelled exploratory") made
explicit.

Simulation (`TestSimExtensionTypeI`, 3000 replications, null, 10-task parent,
follow-up of 6 tasks when B leads and 12 otherwise): pre-declared combination
0.0010 overall; naive pooling (parent at 0.05, then the pooled tasks at 0.05)
0.0203. Both are under alpha at these sizes because the discrete test is
conservative; only the combination has a guarantee. The combination is
conservative and has little power with small stages.

## Scheduler

The queue starts with the required slots. Each time it drains, the scheduler
reads the slots' counted runs (from the run folders), replays the rule with
the slots it has already attempted counted as settled, and queues the newly
released slots; a slot not yet attempted makes the rule wait at its trial or
look, so trial 4 is never released before trial 3's results are in. It stops
when nothing new is released, the spend cap is reached, or the context ends.
`run --resume` replays the same way, so a resumed adaptive experiment never
reruns a slot and never releases a slot the rule would not. Remote workers
lease from the same queue; between stages they find nothing to lease. The
summary carries `not_required` (runs saved) and `stop_reason`.

A spend cap that stops a stage leaves released slots without runs: they are
missing (unknowns) in the verdict, as in a fixed design.

## Reports and UI

The verdict JSON carries `verdict.adaptive` (rule, slots planned, required,
run, saved, stop reason and look, decision alpha, per-task trials or per-look
results, runs left out), `verdict.tier`, and on INCONCLUSIVE
`verdict.more_evidence`. `report.md` gets a "Trials and tier" section; `plan`
prints the required and optional slot counts and the looks. The verdict page
shows the Trials and tier panel (`GET /api/bench/experiments/{id}/adaptive`,
`ui/components/adaptive/trialsPanel.tsx`).

## What it does not allow

- Changing the rule, `min`, `max`, the looks or their alphas after the
  manifest is frozen: that is a new experiment.
- Looking at results to decide whether to run more outside a declared rule.
  `extend` without a pre-declared combination rule is a new experiment, and
  its pooling with the first is exploratory.
- An allocation that depends on which arm is ahead, on other tasks' results
  (rule task), or on anything but the frozen rule.
- Adaptive designs with a verdict policy other than `paired_signflip`, without
  arms A and B, or a sequential design that also declares an extension rule.
- Treating a not-required slot as missing, or a released slot without a run
  as not required.
- Anytime-valid inference (confidence sequences, e-values): the looks are a
  fixed, small, pre-registered number.
- Multiplicity across experiments: each experiment controls its own alpha.

## End to end at $0

Scripted agents through the real pipeline (Bifrost v2.5.1 on 127.0.0.1:18080
with the anthropic provider only, the fault proxy, Docker, the coding-core
suite's 8 tasks, hidden pytest verifiers), runs under a scratch
`BENCH_RUNS_ROOT`. No model call was made by any agent: the oracle applies the
reference patch, noop does nothing. Experiments `hd-w7-*`:

| Experiment | Arms | Design | Slots planned | Runs sealed | d_bar | p | Discordant | Verdict |
|---|---|---|---|---|---|---|---|---|
| concordant-task | oracle vs oracle + note | task rule, min 1, max 3 | 48 | **16** (32 not required) | 0 | 1 | 0 | INVALID* |
| concordant-fixed | same | fixed, 3 trials | 48 | 48 | 0 | 1 | 0 | INVALID* |
| discordant-task | noop vs oracle | task rule, min 1, max 2 | 32 | 32 (0 saved: every task disagrees) | +1.0 | 0.0078 | 8 | INVALID* |
| discordant-fixed | same | fixed, 2 trials | 32 | 32 | +1.0 | 0.0078 | 8 | IMPROVED |

The adaptive concordant run released no optional slot (every task passed in
both arms at trial 1) and sealed a third of the runs for the same primary
result; on the discordant pair the rule released trial 2 of every task and
saved nothing, as it should: it never cuts a task whose arms disagree.

\* INVALID on `usage_completeness`, not from the design: these runs were made
at concurrency 4 while other working copies leased the same fault-proxy key
slots, and 3 of 16, 9 of 48 and 1 of 32 runs received another process's model
calls on their slot (ledger rows with no Bifrost row), which the instrument
check rightly refuses. A concurrency-1 rerun of concordant-task
(`e_hd_w7_concordant_task_c1_csw8`) again sealed 16 of 48 slots with the same
primary result and was again INVALID on one cross-talk request; its fixed
counterpart was stopped after one run to free the shared machine. The design
is not what failed: the same release flow runs in-process with no shared
stack in `TestSchedulerReleasesOptionalSlots`, and the verdict over such runs
is tested in `TestDecideTaskRuleSkipsNotRequiredSlots`.

`extend`, end to end: `heimdall extend -exp e_hd_w7_concordant_task_c1_csw8
-trials 1 -force` (the parent was INVALID, hence `-force`) wrote a follow-up
linked by manifest hash; it ran 16 runs and its own verdict is INCONCLUSIVE
(no cross-talk this time) with "to detect 10.0 pp you need about 65 more tasks
(73 in all, SD 0.30 assumed), 130 runs, cost unknown (no observed cost in this
experiment)". `heimdall extend status` printed both stages, the pooled d_bar
(+0.0 pp over 8 tasks, descriptive) and "decision: inconclusive (exploratory:
no decision)", because the parent pre-declared no combination rule; exit
code 2. A combined decision under the inverse-normal rule is covered by
`TestCombineExtension`; no live run of it was made.

## Tests

- `verdict/sequential_test.go`: task-rule replay, swap invariance, unknowns,
  waiting slots, Decide with not-required slots (no placeholder, slot coverage
  passes, a stray run left out), sequential efficacy and futility, look
  alphas, the "buy more evidence" line.
- `verdict/sequential_sim_test.go`, `verdict/sequential_model_test.go`: the
  simulations above, `TestBestCasePExact`.
- `verdict/sequential_extend_test.go`: the combination rule and its
  simulation.
- `engine/adaptive_test.go`: every recorded pre-W7 manifest
  (`testdata/recorded`, `docs/results`) still verifies its hash; a fixed plan
  carries no W7 key; planning (optional slots, hash, refusals,
  sequential looks from the seed, tiers, presets), the scheduler releasing
  optional slots stage by stage and on resume (task rule) and stopping a
  sequential design for futility after look 1, `ExtendExperiment`.
- `cmd/bifrost-bench/extend_test.go`: the follow-up YAML round trip through
  the strict decoder and schema, the report section.
- `suite/tiers_test.go`: tier resolution, `CompareTiers`, `SuggestTiers`, the
  presets against the experiment schema.
- `handlers/adaptive_test.go` and `ui/components/adaptive/trialsPanel.test.tsx`:
  the route and the panel against the contract key lists.
