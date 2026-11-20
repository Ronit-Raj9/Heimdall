# Importing memory benchmarks: LongMemEval and LoCoMo

Two importers turn public long-term memory benchmarks into multi-session answer suites:

| Format | Benchmark | License | Upstream scoring | Default grading here |
|---|---|---|---|---|
| `longmemeval` | [LongMemEval](https://github.com/xiaowu0162/LongMemEval), cleaned release on [Hugging Face](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned) | MIT (code and dataset card) | a GPT-4o judge, one prompt per question type | judged, prompt `longmemeval-v1` |
| `locomo` | [LoCoMo](https://github.com/snap-research/locomo) (`data/locomo10.json`) | **CC BY-NC 4.0, non-commercial** | token F1; adversarial questions by phrase | judged, prompt `qa-v1` |

Both follow the bench's rules for imports: the importer reads a local copy and never the network, the suite folder gets a `.gitignore` that keeps the data out of git, and an imported task is trusted only after it is admitted here. How answers are graded, what "judged" means and how the judge is calibrated is in `docs/answer-grading.md`.

## Getting the data

Downloads go to `bench/datasets/` (git-ignored). Run every command below from `bench/`; none of them changes directory (`--output-dir` needs curl 7.73 or later).

```sh
# LongMemEval: the repository has the code and prompts, the data is on Hugging Face.
git clone --depth 1 https://github.com/xiaowu0162/LongMemEval.git datasets/longmemeval
mkdir -p datasets/longmemeval/data
R=98d7416c24c778c2fee6e6f3006e7a073259d48f   # dataset revision checked on 15 Nov 2026
curl -L --output-dir datasets/longmemeval/data -O https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/$R/longmemeval_oracle.json      # 15 MB
curl -L --output-dir datasets/longmemeval/data -O https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/$R/longmemeval_s_cleaned.json   # 277 MB

# LoCoMo: the data file is in the repository.
git clone --depth 1 https://github.com/snap-research/locomo.git datasets/locomo
```

`longmemeval_m_cleaned.json` (2.7 GB, about 500 sessions per question) exists upstream but is not offered: one question would replay millions of tokens.

## Import

```sh
bifrost-bench import longmemeval -from datasets/longmemeval -suite lme-oracle -limit 50
bifrost-bench import longmemeval -from datasets/longmemeval -suite lme-s -set variant=s -limit 20
bifrost-bench import locomo -from datasets/locomo -suite locomo -ids conv-26
```

`-from` is the clone, its `data/` folder, or the data file itself. Then:

1. When the suite is judged, pin the judge in `benchmarks/<suite>/suite.yaml`: `judge.provider` and `judge.model` are written as `pin-me`. Upstream LongMemEval judges with `gpt-4o-2024-08-06`; set `judge.votes: 3` for a majority. Importing more tasks into the same suite keeps the judge you pinned.
2. `bifrost-bench suite lint <suite>`, `suite lock <suite>`, `suite admit <suite>`. Admission is the judge's calibration on each task (`docs/answer-grading.md`).
3. Run experiments as usual. Results are per category too (`answer_<category>`), and the judge's spend is `judge_cost_usd`.

### Settings (`-set key=value`)

| Key | Values | Default | What it does |
|---|---|---|---|
| `variant` | `oracle`, `s` | `oracle` | LongMemEval only: which file. `oracle` holds only the evidence sessions (1 to 6 per question); `s` holds about 40 to 50 sessions per question |
| `ingest_sessions` | 1 to 20 | 3 | how many sessions hand over the history |
| `grading` | `judged`, `match` | `judged` | `match` writes an `answer_match` suite with only the questions exact matching can grade soundly |
| `version` | semver | `1.0.0` | suite version written |
| `revision` | text | none | upstream revision recorded in `imported.revision` |

An unknown key is an error. Importing into an existing suite adds tasks to its list; an existing task folder is skipped unless `-force` replaces it. A suite of the other grading, or written by another importer, is refused: judged and verified tasks never share a suite.

## What a task looks like

Each upstream question becomes one task:

```
tasks/<id>/
  task.yaml                       output.kind answer, sessions s1..sN then question
  sessions/s1.md, s1-files/history/conversation-001.md ...   ingest sessions
  sessions/question.md            the last session: the question
  repo/README.md                  the workspace starts empty in every session
  verifier/answer.yaml            hidden expected answer (category = question type)
  solution/answer.txt             reference answer
  controls/empty.txt, exploit.sh  an empty answer and an exploit that must fail
```

- **History.** The history sessions are sorted by date (LongMemEval's oracle file is not sorted; LoCoMo's `session_<n>` numbers are its order) and cut into `ingest_sessions` contiguous parts of nearly equal size, one per session. Each transcript is a markdown file with its date and every turn. Nothing that marks the evidence reaches the agent: LongMemEval's `has_answer` flags and session ids (which start with `answer_` on evidence sessions) and LoCoMo's dialog ids and image URLs are dropped. LoCoMo image turns keep their caption as `[shares a photo: ...]`.
- **Ingest sessions** tell the agent to read the transcripts and keep what it will need in its memory folder (`BENCH_MEMORY_DIR`), because the workspace is wiped after each session.
- **The question session** starts from a reset workspace, gives the date (LongMemEval's `question_date`), the question, and asks for the answer in `answer.txt`, saying it does not know when it cannot tell. LoCoMo temporal questions get upstream's hint to answer with an approximate date from the conversation dates.
- **Controls.** The reference answer is upstream's answer (for an abstention question, a plain "I don't know. That was never mentioned in the conversations."). Wrong answers are derived where that is sound: "I don't know" for every answerable question; for LongMemEval "which first, A or B?" questions whose answer names one option, the other option; for a plain-number answer, that number plus 5; for LoCoMo adversarial questions, upstream's `adversarial_answer`. Wrong answers stay in `wrong_answers` only (admission grades each as a mutant, so a mutant file with the same text would pay for every judge call twice), which leaves `controls.mutants` empty and earns a lint warning. The exploit (`controls/exploit.sh`, run in the agent's place) writes an answer that answers nothing and asks to be marked correct.

## LongMemEval specifics

Checked against the files on 15 Nov 2026 (dataset revision `98d7416c`, code commit `9e0b455`): each file is a JSON array of 500 questions with `question_id`, `question_type`, `question`, `answer` (text, or a number for 32 counting questions), `question_date`, `haystack_dates`, `haystack_session_ids`, `haystack_sessions` (turns `{role, content, has_answer?}`) and `answer_session_ids`. A question whose id ends in `_abs` is an abstention question (30 of 500): the correct answer is that the information was never given, and `answer` holds the explanation.

| Question type (category) | Count | Judge template | Match mode |
|---|---|---|---|
| temporal-reasoning | 133 | temporal (off-by-one days tolerated) | numeric when the answer is one plain number, else skipped |
| multi-session | 133 | standard | as above |
| knowledge-update | 78 | knowledge-update | as above |
| single-session-user | 70 | standard | as above |
| single-session-assistant | 56 | standard | as above |
| single-session-preference | 30 | preference (the answer is a rubric) | skipped |
| abstention (`_abs`, any type) | 30 | abstention | abstain (fixed not-known phrases) |

With `grading=match` most questions are skipped with the reason: their answers are free text ("GPS system not functioning correctly"), and matching them without a model would fail correct answers worded differently.

## LoCoMo specifics

Checked against the repository on 15 Nov 2026 (commit `3eb6f2c`): `data/locomo10.json` holds 10 conversations between two people (19 to 32 sessions each, `session_<n>` with `session_<n>_date_time`) and 1,986 questions with `question`, `answer`, `evidence` and a numeric `category`. Task ids are `<sample id>-q<NNN>`, the question's 1-based position; `-ids` takes task ids or whole sample ids (`conv-26`).

The repository does not name its categories; the names here follow its scoring code (`task_eval/evaluation.py`, `gpt_utils.py`):

| Category | Name here | Count | Why |
|---|---|---|---|
| 1 | multi-hop | 282 | scored per comma-separated sub-answer |
| 2 | temporal | 321 | asked with the hint to answer with a date |
| 3 | open-domain | 96 | answers carry `;`-separated rationales |
| 4 | single-hop | 841 | the remaining, largest category |
| 5 | adversarial | 446 | asks about something never said; the right answer is "not mentioned" |

Adversarial questions carry `adversarial_answer` (the tempting wrong answer) instead of `answer`. Two of them carry both (`conv-26` questions 168 and 179, answer "No"); upstream does not say which is expected, so they are skipped with that reason.

**License.** LoCoMo is CC BY-NC 4.0: non-commercial use only, with attribution (Maharana et al., 2024). The importer records it in `imported.license` and in every task's provenance and warns in its notes. Use imported LoCoMo tasks only where non-commercial use is allowed, and never commit them.

**Comparability.** Upstream scores LoCoMo with token F1, so a judged pass rate is not comparable with its published numbers. The generic `qa-v1` prompt is used.

## Cost

Every question replays its whole history through the agent, once per arm, condition and trial, before the agent's own work and the judge's calls. The importer prints the estimate (characters / 4) for what it wrote. Measured on the real files:

| Import | History per question |
|---|---|
| LongMemEval oracle | about 8,000 tokens (first 40 questions) |
| LongMemEval s | about 125,000 tokens (first 40; upstream says about 115k with the Llama 3 tokenizer) |
| LoCoMo | about 18,500 tokens (`conv-26`, 197 questions); a conversation has 105 to 260 questions, so all of LoCoMo replays its 10 conversations 1,986 times in all |

Start with `-limit` or `-ids` and a small trial count. The judge adds roughly `votes` times (prompt plus answer) per graded run, reported as `judge_cost_usd`; the spend cap counts only the agent.

## What a run needs

- Sessions (several agent runs per task, `/memory` kept between them) and the answer export, from the run and environment kernels.
- An agent that keeps notes across sessions in `BENCH_MEMORY_DIR` or its own `memory.paths`.
- For judged suites: the judge wired to the gateway and a free key slot of the judge's provider at grading time (`docs/answer-grading.md`).
