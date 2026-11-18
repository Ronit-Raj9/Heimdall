# Human review

A grade can be wrong. A judge model can misread a correct answer, a verifier can be too strict or too lenient, and a veto gate can fire on something harmless. Human review lets a person overrule a run's grade, or flag a run for someone else to look at, with a full audit trail and without ever editing the sealed run folder.

A review works like a regrade (`docs/evidence-integrity.md`): it is a child artifact beside the sealed run, tied to the run's seal hash, and analysis reads it only when asked. The difference is the source: a regrade is recomputed from sealed evidence, a review is a person's judgement.

## What a review is

One review is one decision about one sealed run:

| Field | Meaning |
|---|---|
| `review_id` | `rv_` plus 10 random characters |
| `exp_id`, `run_id` | the run reviewed |
| `parent_seal` | the run's seal hash when the review was written |
| `decision` | `pass`, `fail`, `invalid` or `flag_only` |
| `reason` | plain text, required |
| `reviewer` | a name, required and self-declared (see Trust below) |
| `created_at` | when it was written |
| `sealed_outcome` | the outcome the run was sealed with |
| `previous_outcome` | the outcome in effect just before this review: the sealed one, or the one of the review it supersedes |
| `supersedes` | the earlier decision review of the same run this one replaces, if any |
| `evidence` | what the reviewer relied on, free text items: a file, a gate, a judge vote, a line of the answer |
| `prev_hash` | the hash on the previous line of `reviews.log` |
| `hash` | sha256 of the record's canonical JSON with `hash` empty |

The decisions:

- **pass**: the run succeeded. It also overrules a fired veto gate (the reviewer judged that the gate misfired).
- **fail**: the run did not succeed. A fired veto gate stays fired, so the run still counts in the integrity status.
- **invalid**: the evidence cannot decide (a broken judge, a broken verifier). The run counts as unknown, exactly like `invalid_evaluator`: it widens the missingness bounds and lowers graded coverage.
- **flag_only**: a concern for others to look at. It changes nothing in any analysis and puts the run in the review queue.

## Where reviews live

```
<exp>/reviews/<review_id>/review.json   one record, read-only (0444)
<exp>/reviews.log                       one line per review: "<review_id> <run_id> <hash>"
```

Both sit beside the sealed runs, never inside a run folder, so adding a review never breaks a seal. `reviews.log` is append-only: a review is written under a lock (a mutex and a file lock, like `seals.log`), the record first, then its line with fsync. A review never deletes or replaces another. A later review of the same run supersedes the earlier one for analysis, and both stay in the log, so the full history of who decided what, when and why is always there.

## How reviews are checked

Every read checks the whole chain:

- each log line is well formed and names a review once;
- each record is present, names the logged run and experiment, hashes to its own `hash`, and that hash is the one on its log line;
- each record's `prev_hash` is the hash on the line before;
- every folder under `reviews/` has a log line.

Where a review is applied, its `parent_seal` must also be the run's current seal hash.

A review that fails any check is **ignored and reported**: it never changes an outcome, and it is named in the queue's and the run's `problems`, in `review log` (which then exits 1), and in the analysis report's Human review section. A changed record is ignored alone, because later records chain to its log line, not to its content. A malformed log line breaks the chain, so every review after it is ignored until the log is repaired. A record written but never logged (a crash between the two writes) is ignored. A review is never applied to a run whose seal does not verify; such a run is not reviewable at all.

These checks have the limits of the seal itself (evidence-integrity.md, "What a seal proves"): someone with write access to the run folder can rewrite a record, recompute its hash and rewrite the log consistently. There is no signature and no external anchor.

## Applying reviews: grade source `reviewed`

The default analysis does not change. `check`, the verdict page and the leaderboard read the sealed original grades unless you ask for reviews:

```
bifrost-bench check -exp <exp_id> -grade-source reviewed
bifrost-bench leaderboard -grade-source reviewed
GET /api/bench/experiments/{id}/verdict?grade_source=reviewed
GET /api/bench/leaderboard?grade_source=reviewed
```

Under `reviewed`, each counted run is read as sealed, and for a run with an effective review (the newest valid `pass`, `fail` or `invalid` review in log order whose `parent_seal` matches; `flag_only` never decides) the review replaces, in memory only:

- the outcome (`pass`, `fail` or `invalid_evaluator`) and Y;
- the grade's `passed` and `invalid_evaluator`, with the review named in the grade's reason;
- the metrics that are a pure function of the grade, the veto gates and the agent's claim: `verified_success`, `safe_success`, `answer_correct` and `answer_<category>`, `false_completion`, `claim_precision` and `cheating_rate`, recomputed by the metrics package's own formulas (a test checks them against that package);
- for `pass`, every fired veto gate, which is marked not fired with the review id in its detail.

Everything else stays as sealed: cost, tokens, steps, time, the verifier's own reward and dimensions, the gateway log. Every value a review set carries `source: human_review` and `evidence_level: reviewed`. A reviewed pass is never presented as verifier-verified.

**`latest` and `reviewed` do not stack.** `latest` (leaderboard) means each experiment's newest regrade and never applies reviews; `reviewed` applies reviews on top of the sealed original and never reads a regrade. So adding a review never moves a `latest` board, and adding a regrade never moves a `reviewed` one. No regrade may use the id `reviewed`: that grade source always means human reviews.

The verdict reports how many runs were reviewed: `reviewed_runs` and `review_changed_runs` on the analysis and on `GET /experiments/{id}/verdict`, and a Human review section in the report that lists every reviewed run (sealed and reviewed outcome, reviewer, review id, reason) and every ignored review. An analysis on any other grade source still gets the section when the experiment has reviews, saying how many reviewed runs it did not apply. The leaderboard shows `reviewed_runs` and `review_changed_runs`, and `reviewed_runs` per experiment in `grade_sources`.

`release create` on an analysis made with grade source `reviewed` reads the same reviewed runs, the way it reads a regrade, and its evidence pack holds each applied review as `runs/<run_id>/review.json`.

## The review queue

The queue lists counted, sealed runs that need a person, read with their sealed grades, with every reason that applies:

| Reason | When |
|---|---|
| `split_votes` | a judged grade whose judge calls disagreed (some `correct`, some `incorrect`) |
| `unparsable_votes` | a judged grade with a judge call that could not be read |
| `invalid_evaluator` | the grader could not decide |
| `veto` | a veto gate fired (outcome `integrity_violation`) |
| `false_completion` | the agent claimed success on a failed run (`false_completion` is 1) |
| `flagged` | the run has a `flag_only` review |
| `contested` | a judged run whose judge verdict is in dispute: split votes, an auditor that disagreed, or a decision review saying the opposite of the judge (docs/judge-calibration.md) |

A run stays in the queue after it is reviewed, with its current outcome and the decision in effect, so a second reviewer can see what was decided.

## Command line

```
bifrost-bench review list -exp <exp_id> [-reason veto] [-json]
bifrost-bench review show <run_id> [-exp <exp_id>] [-json]
bifrost-bench review add -run <run_id> -decision pass|fail|invalid|flag_only -reason "why" -reviewer <name> [-evidence item ...] [-exp <exp_id>]
bifrost-bench review log -exp <exp_id> [-json]
```

`-evidence` can be repeated. The experiment is found from the run id when `-exp` is not given. `review add` is refused when the config's server mode is `recorded` or `synthetic`, and for a run that is not sealed or whose seal does not verify. `review log` exits 1 when the chain does not verify.

## API

| Route | What |
|---|---|
| `GET /api/bench/experiments/{id}/reviews/queue?reason=` | `ReviewQueue`: the queue, every reason, problems |
| `GET /api/bench/experiments/{id}/reviews` | `ReviewLog`: the whole chain, oldest first, and whether it verifies |
| `GET /api/bench/runs/{id}/reviews` | `RunReviews`: every review of the run, the one in effect, the run's queue reasons, problems |
| `POST /api/bench/reviews` | body `ReviewRequest` (`run_id`, `decision`, `reason`, `reviewer`, optional `evidence`, optional `exp_id`); 201 with the `Review` |

`POST /reviews` is a write and is guarded like every other write route: it needs `Authorization: Bearer <write token>` (401 without), the body limit and the host check apply, and it is refused with 403 unless the server runs in `live` mode, because a recorded or synthetic run folder is a fixture whose results must not move. A missing reason, reviewer or decision, or an unsealed run, is 400; an unknown run 404; a run whose seal does not verify 409. The read routes work in every mode.

## Pages

`/review` lists the queue of an experiment (reason filter, task, arm, condition, trial, sealed and current outcome, decision). Opening a row shows the run with its evidence, grade, judge votes and answer or diff, its review timeline, and a form for a new review (decision, reason, reviewer, evidence). The form is disabled outside live mode. The run page shows the run's reviews as a timeline. The verdict header and the leaderboard show a badge with the number of reviewed runs when reviews changed outcomes.

## Trust

There is no authentication system. The reviewer name is self-declared and is recorded as given; the write token says only that the request came from someone who holds the token, not who they are. The audit trail proves what was written and in which order, not by whom. If reviews need attributable identities, put the API behind a proxy that authenticates people and records who posted, or keep the token per person and rotate it.
