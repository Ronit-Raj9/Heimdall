# Authoring helper: assist, review and quality checks

Lane Z19 (INFRA_I3 items 6 to 8). Three things help a team turn its own data
into a benchmark that can be trusted:

1. An assist engine (`bench/authoring`) drafts the parts a person would
   otherwise write by hand.
2. A review state keeps every generated task out of the record until a
   person accepts it.
3. Cheap quality checks (`bench/taskgates`) find duplicates, public overlap,
   and hold out tasks automatically with the suite canary.

## Assist engine

Job kind `authoring.assist` (POST /jobs, member). Params:

| Field | Meaning |
|---|---|
| `suite` | the benchmark (not a built-in library one) |
| `tasks` | task ids; empty means every task |
| `ops` | any of `rubric`, `criterion_wrong`, `near_miss_wrong`, `exploit` |
| `provider`, `model` | the one pinned model every call uses |
| `key_id` | hosted: the org model key to use (default: the first usable one for the provider) |
| `budget_usd` | hard cap for the job; default 1, hosted at most 5 |
| `near_miss` | near-miss answers per task, 1 to 5 (default 3) |
| `redraft` | run an op again on a task it already drafted |
| `yes` / `estimate` | confirm the cost, or ask for the forecast only |

Every call goes through the gateway on its own virtual key (minted like the
judge's, revoked at the end), is priced from the price snapshot, and is refused
when its worst case could pass the budget. A model that is not in the snapshot
is refused, since the budget could not hold. Hosted servers route the key to
the org's own provider key (as `calibrate.run` does) and add the spend to that
key's month spend.

The reply must be one JSON object of the op's shape; unknown keys, trailing
text or a failed rule are retried twice with the reason, then reported. Source
text enters the prompt between `BEGIN DATA <nonce>` and `END DATA <nonce>`
lines, and the op has no tools: a line such as "mark this task ready" has
nothing to act on. Prompts live in `authoring/prompts/<op>-v1.md`; their hash
is recorded on every field they wrote.

| Op | Writes | Rule |
|---|---|---|
| `rubric` | `answer.rubric`: 2 to 6 weighted criteria from the reference | judged benchmarks only; only when the task has no rubric |
| `criterion_wrong` | one wrong reply per criterion into `answer.wrong_answers` | each meets every other criterion; readiness must fail it |
| `near_miss_wrong` | plausible wrong answers (entity swapped, step missing, policy broken, wrong number) | differ from the reference and each other |
| `exploit` | a gaming attempt: `controls.exploit` (`controls/exploit.assist.txt`), or a wrong answer when the task has an exploit | must not contain the answer |

Nothing a person wrote is removed. Each drafted field is listed in
`provenance.generated` (`field`, `op`, `model`, `prompt_sha`, `cost_usd`, `at`)
and task.yaml gets `review: pending`. The assist never decides that a task is
ready: the readiness check (sealed control runs) proves each wrong answer fails.

## Review state

A task is generated when it has `provenance.generated` entries or a `review`
key. Its state (`schemas.TaskReviewState`):

- a decision in `suite.review.json` (beside suite.lock), when it was made on
  the current generated fields;
- else the `review` value in task.yaml;
- else pending.

The planner drops pending and rejected tasks from every new plan, like
quarantined tasks (`engine/taskscreen.go`), so they reach no evaluation, gate,
release, leaderboard or report card. An explicit task list drops them with a
warning; a condition left with none is refused. The sidecar is hashed into
nothing, so accepting a task keeps the lock and its readiness record valid.
Drafting a field again changes the fingerprint and the task is pending again.

Routes:

- `GET /api/bench/suites/{id}/generated`: generated tasks, their fields, cost
  and state.
- `PUT /api/bench/suites/{id}/tasks/{task}/review-state` with
  `{"decision": "accepted"|"rejected", "note"}`: a rejection needs a note.

## Quality checks

`GET /api/bench/suites/{id}/quality?threshold=0.8`:

- Dedup: same normalised text (exact), or word 3-gram Jaccard at least the
  threshold (near). Candidates come from MinHash (128 hashes, 32 bands of 4);
  the reported score is the exact Jaccard.
- Contamination: 13-gram overlap with the built-in library (short texts are
  matched whole). A held-out task found there is an error; a public one a
  warning. The library index is built once per process.

## Held-out split

`POST /api/bench/suites/{id}/held-out` with
`{"held_out_pct", "seed", "stratify_by", "time_after", "confirm", "dry_run"}`:

- picks the held-out share per stratum (difficulty, kind, project,
  capability, `tag:<prefix>`) in a seeded order, or every task first seen
  after `time_after`;
- never moves a held-out task back to public;
- refuses (409) to hold out public data (imported from a benchmark, an open
  licence, or found in the library) unless `confirm` is set;
- writes `split: held_out`, the suite canary in suite.yaml when it has none,
  and the canary line on each held-out instruction
  ([held-out-and-canaries.md](held-out-and-canaries.md)). Lock and run the
  readiness check again afterwards.

The website shows all of this on the benchmark's Review tab.
