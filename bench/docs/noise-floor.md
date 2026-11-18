# Noise floor: the A/A arm

Every verdict states a minimal detectable effect (MDE): the smallest true difference between the arms that the experiment would detect 80% of the time. The MDE rests on SD_d, the standard deviation of the per-task paired differences (DESIGN.md 9.3 item 7). Until an experiment measures it, SD_d is assumed to be 0.30 and labelled "assumed". The A/A arm measures it: arm A runs twice on the same tasks, and the spread of the differences between the two copies is the noise the harness, the model and the agent produce with nothing changed.

## Turning it on

```yaml
name: code-wins-sentence
suite: coding-core@1.0.0
base: agents/claude-code.yaml
arms:
  A: {label: baseline}
  B: {label: code-wins sentence, set: {notes.user_file: experiments/fixtures/codewins.md}}
aa: true
primary: {condition: stale-notes, metric: verified_success}
conditions:
  - {id: clean, tasks: all, trials: 3}
  - {id: stale-notes, tasks: all, trials: 3}
```

`aa: true` is part of the frozen experiment, so it is hashed into the manifest. An experiment without it encodes exactly as before (the key is omitted) and plans exactly as before: same arm order draws, same slot order, same manifest hash.

The planner refuses `aa: true`:

- without an arm `A` to copy;
- with a declared arm named `A2` (the id is reserved for the copy; rename the declared arm). Without `aa: true` a declared arm `A2` plans as before and is never read as an A/A arm, because a declared arm can never share arm A's config hash;
- without a `clean` condition, since the A/A arm runs only there;
- with a `matrix:` block. The matrix view ranks every arm against every other, and a copy of arm A would enter that ranking as one more contender. Run the A/A arm in a two-arm experiment of the configs you compare.

## What the planner adds

- **Arm `A2`**, a copy of the resolved arm A: same agent config, same config hash, same identity, budgets, image and per-task images, labelled "A/A copy of arm A (label of A)". The same-hash refusal that stops two declared arms from being identical does not apply to it, and still applies to every declared arm. Its file hashes list only the clean condition, the one it runs on.
- **One counted slot per clean task and trial**, on the same tasks and trials as arm A on clean. Probe conditions get no A/A slots.
- **Placement.** Each A/A slot has a pair id of its own, so a pair still holds exactly A and B and every reader of a pair (the pair page, agent diff) still sees A against B. It runs right before or right after the A/B pair of its block. Which one is drawn once per task from the experiment seed, on a PCG stream separate from the arm order, so the A/B arm order does not move and A2 runs before A on some tasks and after it on others. A per-task draw keeps the task-level sign flip of A2 against A exact, as the per-task arm order does for B against A (DESIGN.md 9.3).
- **Everything else follows**: budget parity and capability gaps see an arm identical to A, run inputs hash A2's slots exactly like A's (same config hash, so the same run input hash), the forecast prices the extra slots, and the leaderboard pools A2's runs with A's because they share the config hash.

## Cost

The A/A arm adds `clean tasks x clean trials` agent runs, each priced like a run of arm A. For `experiments/codewins.yaml` (8 tasks x 3 trials on clean) that is 24 runs on top of 120 counted runs, 20% more. An agent built per task (`build.base_arg`) is built for A2 under the same tags as for A, so the second build is a cache hit.

## How the SD is measured

The verdict reads arm A2 as the A/A arm when the experiment set `aa: true`, or when arm A2 has arm A's config hash. Then, on the clean condition and the primary metric:

1. Per task, d_i = mean over trials of A2 minus mean over trials of A, on observed runs only.
2. SD = the sample standard deviation (n - 1) of d_i over the tasks with observed runs in both arms.
3. It is used when there are at least 2 such tasks and it is above 0. An SD of exactly 0 (A2 agreed with A on every task, as deterministic scripted runs do) is recorded but not used: an MDE of 0 would claim any effect is detectable, which a sign-flip test over a few tasks cannot do. The assumed SD stands in, labelled.

## How the MDE changes

MDE in pp = (t(0.975, n-1) + t(0.8, n-1)) x SD / sqrt(n), with n the paired tasks of the primary effect. It scales with SD: at n = 8 the assumed 0.30 gives 34.6 pp, a measured 0.20 gives 23.1 pp, a measured 0.40 gives 46.1 pp. "Tasks needed to detect this d_bar" uses the same SD. `mde_assumed_sd` is false exactly when the SD came from the A/A arm.

## Why a directional A/A result is INVALID

The A/A effect is the exact paired sign-flip test of A2 against A on the clean condition and the primary metric, at both missingness bounds, at the experiment's alpha, the same rule as the primary effect. A2 and A are one config. When they differ significantly at both bounds, something other than the agent moved the number: a cache, a rate limit hitting one slot position, an order effect, a leaking fixture, a grader that is not deterministic. A verdict between B and A from the same harness cannot be trusted, so the experiment is INVALID with an invalid reason that starts `harness noise:` and names the arms, the metric, d_bar and p at each bound, and the action is "fix harness".

The `aa_noise_floor` instrument check shows the outcome beside the other instrument checks:

| Status | When | Effect |
|---|---|---|
| pass | the A/A effect is not significant at either bound | none |
| fail | significant at both bounds, in either direction | INVALID, `harness noise:` reason |
| unknown | significant at one bound only, unknowns with no bounds, or no task with runs in both arms | holds an IMPROVED verdict until known |
| not_applicable | the experiment ran no A/A arm | none |

The test is exact, so on a sound harness it still fails at a rate of at most alpha (5% by default), usually less because the sign-flip p-value is discrete. A failed A/A on a sound harness costs a rerun, a new experiment with a new manifest. That is the price of being able to call the harness broken at all.

## Where it shows

- `verdict.variance` in the analysis (and `report.json`): `source` (`measured_aa` or `assumed`), `sd` (the SD used), `assumed_sd`, `measured_sd`, `aa_arm`, `aa_tasks`, `aa_effect`, `aa_decision`, `mde` and a one-sentence `detail`. Every new analysis carries it; one written before it existed does not, and readers fall back to `verdict.sd` and `verdict.mde_assumed_sd`.
- The Markdown report's "Noise floor" section, right after the primary endpoint, and the `aa_noise_floor` row of "Instrument checks".
- The naive scoreboard, diagnostics, meters, arm metrics, slot coverage, usage completeness, upstream errors, the invalidate-gate rates and the equal-trials gate list arm A2 under its own id: they describe the harness, and A2's runs are harness runs. The report says under the noise floor section that A2 is the A/A copy.
- Arm A2 never enters the A-against-B effect, the guards, the graded coverage and task version gates, timeout parity, integrity, or a release policy rule (rules compare an arm against A, default B).
- `bifrost-bench audit` of a published table has no A/A arm. Its card's source is `observed_effect`: the SD of the table's own per-task differences, which includes any real effect, so it is an upper estimate and not a noise floor. With no usable SD it is `assumed`.

## Limits

- **Clean only.** The A/A arm runs on the clean condition. When the primary endpoint is a probe condition, the MDE still uses the clean SD, which can differ from the noise under that probe.
- **A/A noise is not all of SD_d.** A2 minus A holds run-to-run noise of one config. B minus A under a real change also holds task-by-change variation (the change helps some tasks and hurts others), so the measured SD can understate the SD of the real comparison and make the MDE optimistic.
- **An SD from few tasks is itself noisy.** For roughly normal differences the relative standard error of a sample SD is about 1 / sqrt(2 (n - 1)): about 27% at 8 tasks, 16% at 20, 13% at 30 and 10% at 50. Per-task means of 0/1 outcomes over 3 trials are far from normal, so treat these as a guide. Below about 20 tasks read the measured SD as a rough figure, not a constant.
- **One A/A per experiment.** The SD is measured inside each experiment and used only there. There is no cross-experiment noise model, no pooling of A/A runs across experiments, and no carrying of a measured SD into the planning of the next experiment.
- **No variance decomposition.** There is no ICC and no split of the noise into task, trial and harness parts.
- **Load.** The A/A arm adds runs of arm A's config next to each pair, which changes the load the harness carries during the experiment (rate limits, CPU) compared with the same experiment without it.
- **Matrix experiments** cannot run an A/A arm (see above).
