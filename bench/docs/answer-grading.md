# Grading answers

An answer task asks the agent a question and grades what it answers (`output.kind: answer`). The agent writes its answer to `answer.txt` in the workspace (the path is in `BENCH_ANSWER_PATH`); the environment exports it to `answer.txt` in the run folder, using the agent's final message when the file is missing and recording which source it used. Two graders read that file:

| Grader | Decides with | Suite grading | Grade |
|---|---|---|---|
| `answer_match` | the task's hidden `AnswerSpec`, no model | `verified` (default) | `mode: answer` |
| `judge` | a pinned judge model, through the gateway | `judged`, required | `mode: answer`, `judged: true`, `judge: {...}` |

The design contract is `docs/scenarios-design.md` (sections 6 and rule 5); the code is `graders/answermatch.go` and `graders/judge.go`; the importers that write answer tasks are described in `docs/importing-memory.md`.

## The hidden answer

Each task keeps its expected answer in a YAML file outside the agent's reach: `verifier.answer` in `task.yaml`, or `answer.yaml` in the `verifier.tests` folder when the task names none (`verifier/answer.yaml` when it names neither). The path must stay inside the task folder; a symlink out of it is refused. Unknown keys are an error, so a misspelled field cannot be silently dropped.

```yaml
question: How many concerts did I go to in May?   # put to the judge; also useful to a reader
match: numeric                                    # exact | contains_all | regex | numeric | choice | json
expected: ["3"]
normalize: [lower, strip_punct, collapse_space, trim]
tolerance: 0                                      # numeric only, absolute
category: multi-session                           # reported as answer_<category>
rubric: ""                                        # judge only: what a good answer must do
abstain: false                                    # the right answer is "not known"
wrong_answers: ["I don't know.", "8"]             # must fail: admission calibrates the grader with them
```

## answer_match

`answer_match` runs no container and opens no connection: it compares two files on the host, so the same answer always gets the same grade.

| `match` | Passes when |
|---|---|
| `exact` | the normalized answer equals one of `expected`, normalized the same way |
| `contains_all` | the normalized answer contains every entry of `expected` |
| `regex` | one of `expected` (Go RE2 syntax, linear time) matches the normalized answer; the patterns themselves are not normalized |
| `numeric` | the answer states exactly one distinct number and it is within `tolerance` of one of `expected`. Digits (with thousands commas and a decimal part) and the words zero to twenty are read; "4 or 5" is ambiguous and fails |
| `choice` | the option the answer picks equals one of `expected`, case-insensitively. The answer may be the label alone (`B`, `(b)`, `b.`), start with it (`B) Paris`), or say `answer: B`; an expected option longer than a label is compared with the whole normalized answer |
| `json` | the whole answer is one JSON value equal to one of `expected`; key order and number spelling (`1`, `1.0`) do not matter and normalizers are not applied |
| `gaia` | GAIA's quasi-exact match (`importers/gaia/gaiascore`, a port of the leaderboard's `question_scorer`) passes for one of `expected`. Only the text after the last `FINAL ANSWER:` line is scored when the answer uses that template, else the whole answer. A numeric truth needs the same number once `$`, `%` and `,` are removed; a truth holding `,` or `;` is compared element by element; any other truth is compared with white space and punctuation removed, case-insensitively. The mode brings its own normalisation, so `normalize` must be empty. The rules and the known differences from the Python scorer are in [importers-research.md](importers-research.md) |

Normalizers always run in the order trim, lower, strip_punct (Unicode punctuation), collapse_space, trim, whatever order the spec lists them in. Without normalizers matching is byte for byte.

`abstain: true` overrides the mode: the answer passes only when it says the answer is not known, by containing one of these phrases after lower-casing, folding curly apostrophes and collapsing spaces:

> i don't know, i do not know, i dont know, i don't remember, i do not remember, i don't recall, i do not recall, i'm not sure, i am not sure, not mentioned, never mentioned, wasn't mentioned, was not mentioned, didn't mention, did not mention, never told, didn't tell, did not tell, no information, not enough information, insufficient information, information is not available, no record of, cannot be determined, can't be determined, cannot determine, can't determine, cannot answer, can't answer, unable to answer, unanswerable, not answerable, not known, unknown

The list includes the two phrases LoCoMo's own scorer accepts ("no information available", "not mentioned"). Like that scorer, it cannot tell a refusal from a hedged guess: "I don't know, but probably Max" passes. Use the judge where that matters.

What each outcome means:

- No `answer.txt`, an empty one, or one over 1 MiB: **fail**, with that reason. The agent was asked for an answer and gave none.
- A spec that is missing, does not parse, names an unknown mode or normalizer, has no `expected` (and is not an abstention), or holds a regex, number or JSON value that does not parse: **invalid evaluator** (unknown), since nothing was decided about the agent.
- A task whose output kind is not `answer`: an error; the plan should never reach it.

The reason line quotes the agent's answer (shortened) and never the expected one.

## judge

A judged suite asks a model whether each answer is correct. Its outcomes are a model's opinion, not a deterministic check, so the bench keeps them apart:

- **The suite says so.** `grading: judged` and a `judge:` block are required; the `judge` grader refuses to run in any other suite, and lint refuses `grader: judge` without them.
- **Judged and verified never mix.** Grading is a property of the whole suite, so one comparison never holds both. Reports, the leaderboard and the verdict header label judged results.
- **The judge is pinned.** Provider, model, prompt version, votes and max tokens are hashed into the manifest; changing any of them is a different measurement. A judge whose provider or model is still `pin-me` (what the importers write) refuses to grade.

```yaml
grading: judged
grader: judge
judge:
  provider: openai             # a provider the gateway configures
  model: gpt-4o-2024-08-06     # the served model must be this one
  prompt_version: longmemeval-v1
  votes: 3                     # odd, 1 to 9; default 1
  max_tokens: 16               # default 16
```

How one grade runs:

1. The answer is read as for `answer_match`. A missing or empty answer fails without a model call.
2. The template is picked from `prompt_version` and the spec. The answer is capped at 16,000 characters, fenced, marked as data, and its fence markers are broken up so it cannot close its own fence. Placeholders are filled in one pass, so text in the question or the answer is never expanded again.
3. The judge's own virtual key is minted through the gateway: one key per grade, on the judge's provider and model, named after the run id plus `j` and a random suffix, so a key left behind by a crash is still removed by the janitor. It is never the run's key (a gateway that returns the run's key is refused). It is revoked when the grade ends, also when a call fails or the run is cancelled.
4. The prompt is sent `votes` times to `<gateway url>/v1/chat/completions` at temperature 0, with the key in `x-bf-vk`.
5. Each reply is parsed strictly: after trimming spaces, one trailing period and case it must be exactly `correct` or `incorrect`; anything else (`yes`, a sentence, `**correct**`) is `unparsable`.
6. A strict majority of `correct` passes and of `incorrect` fails. Without one (unparsable votes, or a tie) the grade is **invalid evaluator**. A call that fails (transport error, non-2xx status, a body that is not a chat completion, or a reply from a model other than the pinned one; the pinned alias plus a release date, such as `gpt-4o-2024-08-06` for `gpt-4o`, counts as the same model) is an error, never a vote: the run becomes an infrastructure error, not an agent failure.

`Grade.judge` records the model, prompt version, the sha256 of the template (before anything is filled in), every vote, the judge's cost and the price snapshot it was priced from.

### Prompt versions

| Version | Templates | Source |
|---|---|---|
| `qa-v1` | reference answer, rubric (no reference), abstention | written for the bench |
| `longmemeval-v1` | one per LongMemEval question type: standard (single-session-user, single-session-assistant, multi-session), temporal-reasoning (off-by-one days tolerated), knowledge-update, single-session-preference (rubric), and abstention | adapted from `get_anscheck_prompt` in `src/evaluation/evaluate_qa.py` of [LongMemEval](https://github.com/xiaowu0162/LongMemEval) (MIT, commit `9e0b455`): the verdict words are correct and incorrect instead of yes and no, and the response is fenced and marked as data. An unknown question type has no template (upstream raises too) and is an invalid evaluator |

The templates are embedded in the binary (`graders/judgeprompts/`). A change to one is a new version.

### Cost

The judge's spend is priced from the response's `usage` with the same pinned price snapshot as the agent's cost rows. It is reported per run as `judge_cost_usd` and is **never** added to `cost_usd`: the judge is part of measuring, not of the agent. It is unknown when the judge model is not in the snapshot or a response carried no usage. The experiment spend cap counts the agent's cost only, so budget the judge separately: roughly `votes` times (prompt plus answer) input tokens per graded run.

### Wiring and key slots

The judge needs the gateway client, the gateway's URL as the bench host sees it (`gateway.url`) and the price snapshot. `graders.Builtin` registers an unwired judge, which refuses every grade with that reason; the app registers `graders.NewJudge(...)` ahead of it. The judge's key takes a key slot of the judge's provider for the length of a grade, so keep one free: run fewer slots at once than the pool holds, or give the judge its own provider. When no slot is free the mint fails and the run is an infrastructure error.

## Calibration through admission

A judge is only as good as its agreement with the task's intent, so every judged task is calibrated where it is admitted. `suite admit` grades the task's controls with the suite's own grader:

- the reference answer (`controls.reference`) must pass three times in a row;
- the empty answer (`controls.empty`), every mutant (`controls.mutants`), every entry of `wrong_answers` (admission grades each as a mutant) and the exploit must fail. The exploit is a script run in the agent's place; for an answer task it writes an answer meant to game the grader, such as one that asks to be marked correct without answering.

A task whose judge disagrees on any of them is not admitted. The record goes into `suite.lock` like any admission, so a run's verdict rests on tasks where this judge, with this prompt, was seen to tell right from wrong. Change the judge and admit again.

Admission controls are easy cases written by the suite author. Whether the judge agrees with people on agents' real answers is measured apart, with human labels: `heimdall judge calibrate <suite>` writes a calibration record, and a judged verdict without one for the exact judge and prompt is at most INCONCLUSIVE (docs/judge-calibration.md). The controls can seed that set, as weak items reported apart.

## Metrics

| Metric | What it is |
|---|---|
| `answer_correct` | 1 when an answer task's grade passed and no veto fired, as `verified_success` scores it; not applicable to other tasks. Report-only |
| `answer_<category>` | the same value under the task's category (`temporal_reasoning`, `multi_hop`, ...): lower-cased, every run of other characters turned into `_`, at most 40 characters. A run without a category reports none |
| `judge_cost_usd` | the judge's spend for the run's grade in a judged suite; 0 when no call was needed (no answer), unknown when the grade is missing or a call was not priced; not applicable outside judged suites |
