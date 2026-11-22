# Judge calibration

A judged suite (`grading: judged`) lets a pinned model decide whether an
answer is correct. That judge is the least trusted part of any evaluation: in
published re-tests, web-agent judges reach about 70% precision, a single
token can induce up to 35% false positives, and one team's production judge
started at a Cohen's kappa of 0.02 against its own experts (research lane B
1.7, lane A). Heimdall therefore treats a judge as an instrument that must be
measured against people before its verdicts are trusted, and says plainly
when it has not been.

In short (`heimdall` and `bifrost-bench` are the same program):

- People label answers as correct or incorrect. The labels live with the
  suite, in `benchmarks/<suite>/calibration/labels.jsonl`.
- `heimdall judge calibrate <suite>` runs the suite's pinned judge on every
  labelled answer and writes a calibration record: agreement, Cohen's kappa,
  true positive and true negative rates with exact intervals, an upper bound
  on the false-positive rate, per category, how stable the judge is, and how
  easily form fools it.
- Every verdict of a judged experiment carries a `judge_calibration`
  instrument. No record for the exact judge and prompt: the verdict is at
  most INCONCLUSIVE. A record showing the judge unreliable: INVALID.
- Runs whose judge verdict is in dispute are marked `contested` and listed in
  the review queue. A judge never overrules a person, and a second judge never
  overrules the first.

Verified suites are untouched: nothing in this page applies to them, and no
manifest, lock, seal or recorded run changes its hash.

## The calibration set

```
benchmarks/<suite>/calibration/
  labels.jsonl                 the working set: one labelled answer per line, append-only
  sets/<sha256>.jsonl          content-addressed snapshot of labels.jsonl, one per calibration (read-only)
  records/<record_id>/record.json   one calibration record (read-only)
  records.log                  one line per record: "<record_id> <hash>"
```

One line of `labels.jsonl`:

| Field | Meaning |
|---|---|
| `id` | content address of the task and the answer text: the first 16 hex of sha256(task NUL answer). The same answer labelled twice is one item |
| `task` | a task id of the suite |
| `answer` | the answer text, exactly as the agent wrote it |
| `label` | `correct` or `incorrect` |
| `labeller` | a name, self-declared (there is no login) |
| `note` | optional, why |
| `source` | `human_review`, `label`, `import` or `admission_control` |
| `ref` | where it came from: a review id, a run id, a file |
| `at` | when |

Sources:

- **Human reviews of sealed runs**: `heimdall judge calibrate <suite>
  -from-reviews -exp <exp_id> [-exp ...]` turns every run whose newest
  decision review (written against the run's current seal) is `pass` or
  `fail` into a label, with the answer the run sealed. `invalid` and
  `flag_only` reviews say nothing about correctness and are skipped. Reviews
  are hash-chained child records (docs/human-review.md), so each label names
  the review it came from.
- **Direct labels**: `heimdall judge label <suite> -task <id> -answer <text>
  -label correct -labeller <name>` (or `-answer-file`, or `-run <run_id>` to
  take the task and answer from a sealed run), the labelling form on the
  `/calibration` page, or `POST /api/bench/suites/{id}/calibration/labels`.
- **Import**: `heimdall judge label <suite> -import labels.csv` (header
  `task,answer,label,labeller` and optional `note,ref`, any order) or a JSONL
  file with the fields above (`id`, `source` and `at` may be left out).
- **Weak seed**: `heimdall judge label <suite> -seed-controls` adds the
  suite's own admission controls: each task's expected answers as correct and
  its `wrong_answers` as incorrect, labelled "suite admission controls". These
  are easy cases written by the suite author, not agents' answers read by a
  person. They are reported apart (`seed`) and **never** count toward the
  label minimum or the thresholds.

Rows are only appended. Importing the same reviews or file twice adds
nothing (a row equal in id, label, labeller, source and ref is skipped).
When people disagree about an item, it is **disputed**: left out of every
statistic and counted, because a judge cannot be measured against labels
people do not agree on. A person's label of an item replaces a seed label of
the same item.

**Lock fingerprint.** When `labels.jsonl` exists, its sha256 is recorded in
the suite lock's fingerprints as `calibration`, so the lock and every
manifest planned under it name the calibration set in force. A suite without
labels fingerprints exactly as before. Records and snapshots are not
fingerprinted, so writing a record never makes a lock drift. Labels added
after the lock give a warning (`calibration labels changed since the lock:
run suite lock to bind the current set`), never a refusal, and never send a
task back to draft.

## Running the judge on the labels

```
heimdall judge calibrate <suite> [-yes] [-from-reviews -exp <exp_id> ...] [-seed-controls] [-policy <file>]
```

1. The labels are resolved (human items, seeds, disputed) and each item's
   task is resolved to the template the grader would use for it (an item of
   an unknown task, or of a task without a usable answer spec, is skipped and
   named).
2. The forecast is printed: the number of judge calls, input and output
   tokens, and the cost from the price snapshot.
3. Nothing is spent without consent: `-yes`, or `y` at the prompt. Without a
   terminal and without `-yes` it prints the forecast and stops.
4. The judge runs exactly as it grades: through the gateway, on its own
   virtual key minted for the calibration (never a run's key) and revoked at
   the end, temperature 0, the suite's number of votes, the pinned model
   (a response from another model is an error). For each item it is asked
   four times over: the first pass, an identical second pass, and two
   perturbed passes. A failed call stops the calibration and no record is
   written.
5. The set is snapshotted to `sets/<sha256>.jsonl` and the record is written
   and chained.

Calls per item = votes x 4. 50 labels with 3 votes cost 600 judge calls.

## What a record holds

The record names the judge (`provider`, `model`, `prompt_version`), the
prompt hash (`prompt_sha256`, over every template of the version, and
`templates`: each template's sha256, the same hash a judged grade records),
the votes, the model ids the gateway served, the set hash and row count, the
labellers, the cost, the date, and:

| Statistic | Definition |
|---|---|
| agreement | items where the judge's majority verdict equals the label, over all items |
| TPR | judged correct among items labelled correct |
| TNR | judged incorrect among items labelled incorrect |
| FPR | judged correct among items labelled incorrect: the judge passing an answer a person failed |
| kappa | Cohen's kappa (below) |
| per category | the same, per answer category |
| unanimous items | with more than one vote, items whose votes were all the same |
| repeat agreement | items whose verdict was the same in the first and the second pass |
| perturbation flips | items whose verdict changed when the answer was perturbed |

"Positive" means "correct". The judge's verdict on an item is the majority
of its votes, as in grading; with no majority (an unparsable reply, a tie)
it is **undecided**. An undecided verdict counts against agreement, TPR and
TNR, and is never a false positive. So FPR is not always 1 - TNR.

**Intervals.** Every rate has an exact two-sided 95% Clopper-Pearson
interval: lower end `1 - U(n-k, n)` and upper end `U(k, n)`, where `U(x, n)`
is the 0.975 quantile of Beta(x+1, n-x) (0 and 1 at the edges). The
false-positive bound shown on the card is the upper end of the FPR interval.
Exact intervals are conservative: they hold their coverage at any sample
size, which matters at the 30 to 100 labels teams actually have.

**Kappa.** The judge is a rater with three categories (correct, incorrect,
undecided) and the person one with two. p_o is the agreement and
p_e = P(label correct) P(judge correct) + P(label incorrect) P(judge
incorrect); kappa = (p_o - p_e) / (1 - p_e). It is not defined when p_e is 1
(every item in one category on both sides). No interval is given for kappa.

**Perturbations.** Two deterministic changes that do not change what the
answer says: `whitespace` (every space run between words doubled, blank lines
around the text) and `preamble` ("Sure! I have looked into this carefully.
Here is my answer:" before the answer). A judge whose verdict moves is
reacting to form. Both are reported; neither enters the thresholds.

**Seeds** get their own statistics (`seed`), reported apart.

The record is `records/<id>/record.json`, read-only, with `hash` the sha256
of its canonical JSON with `hash` empty and `prev_hash` the hash on the log
line before. Every read checks the chain the way reviews are checked: a
changed record is ignored alone, a malformed log line breaks the chain from
there on, a folder without a log line is reported. Ignored records are
listed as problems on the card and in `judge status`. The same limit as
seals applies: someone with write access can rewrite consistently; there is
no signature.

## The `judge_calibration` instrument

Every analysis of a judged experiment (one that froze a judge, or whose runs
carry judge records) gets one more instrument check, `judge_calibration`,
and `judge_calibration` on the analysis and on `GET
/experiments/{id}/verdict`. The record used is the newest valid record of
the suite for the **exact** judge model that also covers **every** prompt
template hash the judged runs recorded. Its state:

| State | When | Effect |
|---|---|---|
| `uncalibrated` | no such record, or a record with seeds only | instrument `unknown`; an IMPROVED or REGRESSED decision becomes INCONCLUSIVE (`capped_from` says which; a capped regression still holds the release) |
| `allowed_uncalibrated` | as above, and the experiment sets `verdict: {allow_uncalibrated_judge: true}` | instrument `warn`; the decision stands, labelled |
| `few_labels` | fewer human labels than `min_labels` | instrument `warn` with the intervals; when the bounds do not pass yet, capped like `uncalibrated` ("too few labels to show the judge reliable") |
| `unreliable` | at least `min_labels` human labels, and the lower bound of agreement under `min_agreement` or the lower bound of TNR under `min_tnr` (or TNR not measured: no label is incorrect) | instrument `fail`; the verdict is INVALID with the reason "judge unreliable: ..." |
| `calibrated` | otherwise | instrument `pass`, or `warn` with drift reminders |

`allow_uncalibrated_judge` is in the experiment file, so it is frozen into
the manifest before the first run and cannot be switched on after the
results are in.

The instrument also says, whatever the state:

- **contested runs**: how many judged runs are contested (below);
- **judge error over effect**: when one minus the lower bound of agreement is
  larger than the absolute effect the verdict reports, "the judge's error
  rate (up to x%) exceeds the effect claimed (+y pp): misjudged answers alone
  could produce it". A judge that misreads that many answers could produce
  the difference by itself if its errors fell unevenly between the arms;
- **bias-adjusted rate** per arm (below).

The report gets a "Judge calibration" section with all of it.

### Thresholds

In the release policy file (`policies/<name>.yaml`), all optional:

```yaml
judge_calibration:
  min_agreement: 0.75   # lowest allowed lower bound of agreement
  min_tnr: 0.60         # lowest allowed lower bound of the true negative rate
  min_labels: 30        # fewer human labels warn
  max_age_days: 90      # older records get a reminder
```

The defaults are these values. The policy the experiment froze is used, or
the one given to `check -policy`. The thresholds are on lower bounds, so they
need enough labels: with every label matched, agreement's lower bound passes
0.75 from 13 labels and TNR's passes 0.60 from 8 negative labels. 30 human
labels with 27 matched (0.90) give an agreement interval of about [0.73,
0.98]: just under 0.75, so the judge is not yet shown reliable. Label more
items rather than lowering a threshold.

### Calibrated on controls

Before anyone labels answers, a judge can be **calibrated on controls**: the admission-control items of a calibration record (reference answers labelled correct; wrong answers, the injection answer and the refusal labelled incorrect) are measured like human labels. When there are at least `min_controls` of them (default 20), both labels are present, every template the verdicts use is covered and the bounds pass `min_agreement` and `min_tnr`, the state is `calibrated_on_controls`. Verdicts and report cards may use it, always with the visible label "calibrated on controls". Human labels that pass upgrade it to "human-calibrated" (`calibrated`). On an abstention task (`qa-v1-abstain`) a refusal is the right answer, so the refusal control is not required to fail there.

```yaml
judge_calibration:
  min_controls: 20      # fewest admission-control items for "calibrated on controls"
```

### Gates on judge-graded benchmarks

The report card and the CI comment show which benchmarks a model judge grades and its calibration state. A/B verdicts on a judge that is neither human-calibrated nor calibrated on controls are capped (shown as unclear), and a `min_score` gate on such a benchmark is inconclusive instead of passing or failing, unless the gate sets `allow_uncalibrated_judge`.

`max_regression` is decided on the paired tasks only (tasks both versions ran, on the same benchmark version): a 95% paired bootstrap interval of the change, failing only when the drop is beyond the limit and the interval excludes zero. A drop over the limit that could be noise is inconclusive, and the check shows the smallest change its number of tasks can detect. Tasks only one side ran, or a baseline on another benchmark version, are listed in the check's notes and never pooled. `fail_on_inconclusive` makes an inconclusive gate block a release.

### Drift reminders

A reminder is a warning; it never changes a decision.

- the record is older than `max_age_days`;
- the gateway served the judge alias as a model id the calibration never saw
  (each judged grade now records `served_model`; a provider that moves an
  alias to a new snapshot shows up here);
- the labels changed since the record (its set hash is not the current one);
- calibration records were ignored because the chain does not verify.

`heimdall judge status [<suite>] [-policy <file>] [-json]` shows the state of
every judged suite (or one): the label counts, the newest record for the
pinned judge and prompt, and the same reminders except the served model,
which only judged runs reveal. It reads files only and makes no call.

## Contested runs and the auditor

A judged run is **contested** when:

- its judge votes split between correct and incorrect;
- the auditor disagreed (below); or
- the newest decision review written against its seal says the opposite of
  the judge's majority (a `pass` review of a run the judge failed, or a
  `fail` review of a run it passed).

Contested runs are listed in the review queue under the reason `contested`
(`review list -exp <id> -reason contested`, `GET
/experiments/{id}/reviews/queue?reason=contested`, the `/review` page) and
counted in the verdict. Being contested never changes an outcome. Under grade
source `reviewed` a human review decides the run, as always.

**Auditor.** A judged suite may name a second judge from another provider:

```yaml
judge:
  provider: openai
  model: gpt-4o-2024-08-06
  prompt_version: qa-v1
  votes: 3
  auditor: {provider: anthropic, model: claude-sonnet-4-5, votes: 1}
```

After the judge has decided, the auditor gets the same prompt on its own
virtual key. Its votes, majority, whether it agrees, its served model and its
cost are recorded in the grade (`judge.audit`). A disagreement marks the run
contested; it never changes the grade. An auditor from the judge's own
provider, or with an even number of votes, is refused by `suite lint` and
at grading. A failed audit is recorded with its error and the grade
stands. The auditor's provider needs a free key slot in the gateway like the
judge's. Its cost is in `judge.audit.usd`; the `judge_cost_usd` metric counts
the judge only.

## Bias-adjusted success (report only)

A judge with TPR below 1 misses correct answers and one with TNR below 1
passes wrong ones, so the raw judged success rate is biased. The
Rogan-Gladen estimator corrects it:

    corrected = (raw + TNR - 1) / (TPR + TNR - 1), clipped to [0, 1]

defined only when TPR + TNR > 1 (a judge at or below chance carries no
information to correct with). With TPR = TNR = 1 it equals the raw rate.

The interval is conservative: each of the three inputs (the raw rate, TPR,
TNR) gets its exact interval at 1 - 0.05/3, so all three hold together with
at least 95% (Bonferroni), and the estimate is evaluated at every corner of
that box (it is monotone in each input, so its extremes are at corners).
When TPR + TNR could be 1 or less inside the box, the interval is not
defined and the note says to label more items.

The corrected rate is shown beside the raw judged rate of each arm, on the
verdict and in the report. It is for reading only and never replaces the
verdict, which stays the paired test on the judged outcomes, so a team can
see how much the judge's measured error could move the numbers.

## Grades name their calibration

Each judged grade now also records `served_model` (the model id the gateway
reported) and `calibration` (`<record_id>@<hash>` of the newest valid record
covering its model and template when it was graded; empty when none). Both
are additive: older grades without them read as before.

## API and page

| Route | What |
|---|---|
| `GET /api/bench/suites/{id}/calibration` | `CalibrationStatus`: state, detail, label counts, newest record (without per-item results), thresholds, reminders, problems |
| `GET /api/bench/suites/{id}/calibration/labels` | `CalibrationLabels`: every row, oldest first |
| `POST /api/bench/suites/{id}/calibration/labels` | body `CalibrationLabelRequest` (`task`, `answer`, `label`, `labeller`, optional `note`, `ref`); 201 with the item. Needs the write token; 403 outside live mode; 400 for a bad label or an unknown task |

Running the judge costs money and is only on the command line, behind its
forecast and confirmation.

The `/calibration` page (navigation: Judge calibration) shows the card of a
judged suite (state, agreement, kappa, false-positive bound, set size, date,
thresholds, reminders), the labelling form (write token, live mode only) and
the labels, seeds marked weak. The review queue shows the `contested` reason.

## A full run at $0

`calibration/testdata/judgescript/` is a scripted judge model: an Anthropic
Messages and OpenAI Chat Completions endpoint whose verdict comes from a
JSON script (by prompt sha256, by graded answer, by answer substring, or
`reference`: correct when the answer holds the reference answer). With
`compose.example.yml` beside it, a throwaway stack (Bifrost, the fault proxy,
Postgres, judgescript) runs the whole path for real while no provider can be
reached: the proxy's anthropic upstream is judgescript, its key is a dummy,
and the proxy sits only on an internal network.

One such run (18 Nov 2026, the acme-memory example made judged with an
anthropic judge, 32 human-style labels from a fixture file, 16 correct and
16 incorrect, 288 judge calls, all answered by judgescript):

| Labels | Script | Agreement | TPR | TNR | FPR | kappa | State |
|---|---|---|---|---|---|---|---|
| 8 | right except one false positive and one false negative | 0.750 [0.349, 0.968] (6/8) | 0.800 [0.284, 0.995] | 0.667 [0.094, 0.992] | 0.333 [0.008, 0.906] | 0.47 | `few_labels` (capped) |
| 32 | the same | 0.938 [0.792, 0.992] (30/32) | 0.938 [0.698, 0.998] | 0.938 [0.698, 0.998] | 0.062 [0.002, 0.302] | 0.88 | `calibrated` |
| 32 | ten false positives, two false negatives | 0.625 [0.437, 0.789] (20/32) | 0.875 [0.617, 0.984] | 0.375 [0.152, 0.646] | 0.625 [0.354, 0.848] | 0.25 | `unreliable` (INVALID) |

The script decided by exact answer text, so the preamble perturbation fell
back to its `reference` rule and flipped 9 of 32 verdicts: the sensitivity
measure picks up a judge that reacts to form. The repeat pass agreed on 32 of
32 (the script is deterministic).

## Limits

- No real judge model has answered. Every statistic, state, chain and route
  is tested with a fake gateway and a fake judge at $0; the statistics are
  checked against hand-computed values and closed forms. Real-pipeline runs
  used judgescript (above) or the live stack's scripted model, whose fixed
  reply the judge cannot read.
- Labels are only as good as the people who give them. Self-declared names,
  no authentication (see docs/human-review.md, Trust), and no measure of
  agreement between people beyond leaving disputed items out.
- Labels from reviews are not a random sample: people review the runs the
  queue sends them (split votes, contested runs), which are harder than
  average. The judge's measured error is then pessimistic for the typical
  run, and the bias-adjusted rate assumes TPR and TNR are the same on the
  experiment's answers as on the labelled ones.
- A record covers the templates of the items it judged. If the labels have no
  abstention item, a run graded with the abstention template is not covered
  and its experiment stays uncalibrated.
- Served-model drift is seen only when the provider echoes a dated model id.
- The perturbations are two simple, fixed ones; they show sensitivity to
  form, not robustness in general.
- Kappa has no interval; read agreement, TPR and TNR with their intervals
  instead.
