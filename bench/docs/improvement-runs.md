# Improvement runs

An improvement run lets your agent improve its own prompt, standing instructions, notes and skills over several rounds, and then tests the result on held-out tasks the loop never used, so the gain can be trusted. Code: `bench/improve/`, API `/api/bench/improvements`, page `/improvements/$id`.

## Spec

```yaml
# improvements/coder-notes.yaml
kind: improvement
name: coder-notes
agent: agents/coder.yaml          # version 0; a plain file is snapshotted first
suite: coding-core@1.0.0          # the task pool
preset: medium                    # small, medium, large or custom; explicit fields win
seed: 41
editable:                         # what the improver may change
  files: [/home/agent/AGENTS.md]
  dirs: [/home/agent/.claude/skills/]
  instructions: true              # the agent's standing instructions
splits: {dev: 0.4, audit: 0.3, sealed: 0.3, honeypots: [t09-impossible]}
budget: {total_usd: 60}           # the spending limit of the whole run
claim: {alpha: 0.05, extension: true, extension_alpha: 0.02, target_gain: 0.05}
controls: [null_improver, retries_v0]
```

The three splits are the practice tasks (dev), the gate tasks (audit) and the held-out tasks (sealed). The preset fills rounds, ideas per round, the keep set, trials per phase, the gate alpha (0.10), whether a second look is reserved and the controls. `custom` fills only the gate alpha 0.10 and the two controls, and needs `budget.total_usd`.

## One round

1. **Ideas.** The improver proposes several candidates from the keep set (mutations and, when the preset turns them on, merges and restarts). It sees only practice results.
2. **Checks** on each candidate's changes: answer keys of gate or held-out tasks, the benchmark's canary, fields it may not change, the benchmark's own cues, and after the screen whether it stopped emitting an event its parent emits. A fired check pauses the run for a person's approval; an approval is recorded and the result can then not be claimed.
3. **Screen** on the practice tasks: the ideas are narrowed by halving until one is left. It goes to the gate only when it beat the current version there.
4. **Gate** on the gate tasks: a one-sided exact sign-flip test of the per-task differences, at a level from an error budget spread over the run's gate looks. Over the whole run the chance that any kept version is no better than the one before it is at most the gate alpha.

The loop stops when the rounds or gate looks are spent, when the improver found nothing more to change or no idea beat the current version for 2 rounds in a row, or at the spending limit.

## The claim

After the last round, one experiment on the held-out tasks compares version 0 with the final version (and the null improver's final version). Its per-task differences give the verdict, the first rule that holds:

| State | When |
|---|---|
| `no_change` | no round kept a new version |
| `not_claimable` | a person approved a paused check |
| `supported` | the one-sided p is at or below the claim level and the gain is positive |
| `worse` | the same test the other way |
| `insufficient_sample` | too few held-out tasks differ for any gain to be confirmed |
| `likely_better` | a positive gain with p at or below 0.20 |
| `no_evidence` | otherwise |

The claim level is `claim.alpha` minus `claim.extension_alpha` when a second look is reserved (0.03 by default). The page shows a headline per state, then lines for the second look offer, the null improver, the retries control, a teacher model, the gate and a single loop, each only when it applies.

Controls never change the state. The null improver makes edits without looking at failures over the same rounds and budget. The retries control retries version 0 when a run visibly failed (timeout, budget, crash or refusal), with up to the same money as the loop.

## Second look (continue)

A finished run that is `likely_better` or `insufficient_sample`, and that kept a reserve at creation, may buy one second look: more tries on the same held-out tasks, new held-out tasks from the benchmark (not in the pool, at most 200), or both, with a higher spending limit. It is tested at `claim.extension_alpha`, so the two looks together keep the claim's bound. The verdict then reads "First look: ... Second look, with {n} tasks: ...". A run can be continued only once.

`POST /improvements/{id}/continue/estimate` with `{"extra_trials": 2, "new_tasks": true}` answers what the second look would run and cost and the gain it could confirm. `POST /improvements/{id}/continue` with the same body plus `extra_budget_usd` starts it (202).

## Resume and restarts

A run whose experiment failed is `failed`; a run a server restart left behind is `interrupted`, with the reason "The server restarted during this run. Press Resume to continue; nothing already paid for runs again." `POST /improvements/{id}/resume` continues a `planned`, `failed` or `interrupted` run from its saved stage (202). A running run answers 409 "This run is already running."; any other state answers 409 "This run is {status}; it cannot be resumed."

On a local server, runs left running are driven again at start. On a hosted server they are marked interrupted and wait for an org admin to press Resume, since the spend goes on the organization's keys. An organization's runtime is never unloaded while one of its improvement loops runs.

## Studies

One run of the loop says little about whether the loop works. A study runs the whole loop 2 to 5 times from version 0 (`replicate: {loops: 3, repartition: true}`), one after another, on one shared held-out split; each loop draws its own practice and gate tasks when `repartition` is on. No loop runs its own final experiment. The study ends with one experiment on the held-out tasks holding version 0 and each loop's final version, and a pooled test at 0.05: per task, the mean over loops of final minus version 0, where a loop that kept nothing counts as version 0. It reads "Across {L} runs of the loop from version 0: {gain} points on {n} held-out tasks (95% interval {lo} to {hi}, p {p}). {k} of {L} runs ended better than version 0."

Routes: `POST /improvement-studies` (the spec with `replicate.loops` 2 to 5, 202), `GET /improvement-studies`, `GET /improvement-studies/{id}`, `POST /improvement-studies/{id}/stop`.

## Notifications

`improvement.finished` (done, stopped or failed) and `improvement.paused` (waiting for approval) go to the organization's hooks like every other event (see notifications.md), with `{improvement_id, name, agent, suite, status, state, headline, gain_points, spent_usd, url}`.

## What is never served

The report never serves gate or held-out task ids, the task pool or the agent YAML. Only the practice screen's experiment id is served; the experiment ids of the gate, held-out, control, final and second look experiments are not. A fired answer-key check shows "a gate or held-out task" in place of the id it matched. Those experiments still appear in the organization's experiment list under names like `<improvement>-audit-r1`; the improvement page never links them.

## Storage

`<runs root>/.improvements/<id>/improvement.json` (frozen spec and hash, partition and hash, rounds, controls, verdict), `protected.json` (0600, never served), `lineage.jsonl` (hash-chained). Studies: `<runs root>/.improvements/studies/<id>/study.json`.

## API and roles

`GET /improvements`, `POST /improvements` (spec as YAML or JSON, 202), `GET /improvements/{id}`, `GET /improvements/{id}/rounds/{k}`, `GET /improvements/{id}/lineage`, `POST /improvements/{id}/rounds/{k}/approve` (`{"reason": "..."}`), `POST /improvements/{id}/stop`, and the resume, continue and study routes above. On a hosted server create, approve, resume, continue and starting a study are for org admins (they spend the organization's keys); stop and the continue estimate are open to any member; reads to every member. Every experiment of the run goes through the organization's launcher like any other run: it needs a working key of the organization for each provider, counts against the daily caps when a server admin set them, and runs on the organization's default worker.

## From a terminal

`bifrost-bench improve create -f improvements/x.yaml -run` creates and runs one; `bifrost-bench improve run <id>` resumes it from the saved stage.
