# Importing OpenAI Evals (and Hugging Face datasets)

The hosted OpenAI Evals product shuts down on 30 Nov 2026. Evals written for the open-source framework (https://github.com/openai/evals, MIT) keep working as Heimdall question-and-answer suites: each sample becomes one answer task, checked with no model, or by the pinned judge for model-graded evals.

## What it reads

An evals registry folder: `evals/*.yaml` (each eval's name, id, class and arguments), `data/` (the samples files) and, for model-graded evals, `modelgraded/*.yaml`. `-from` may be the checkout, its `evals/` package, or the registry folder itself. An exported eval of your own works the same way: put `evals/<name>.yaml` and `data/<name>/samples.jsonl` in one folder.

The upstream repository keeps samples in Git LFS. Pull only the evals you import:

```sh
git clone https://github.com/openai/evals datasets/openai-evals
cd datasets/openai-evals && git lfs pull --include 'evals/registry/data/<name>/*'
```

A samples file that is still an LFS pointer is skipped with that reason.

## How samples map

| Eval class | Heimdall check |
| --- | --- |
| `basic.match:Match` | answer starts with an `ideal`: regex `^\s*<ideal>` |
| `basic.includes:Includes` | an `ideal` appears in the answer: regex `<ideal>`, `(?i)` when `ignore_case: true` |
| `basic.fuzzy_match:FuzzyMatch` | case-insensitive regex `(?i)<ideal>` (one direction only: the ideal inside the answer) |
| `modelgraded.classify:ModelBasedClassify` | judge rubric: the spec's prompt with the sample filled in and `{completion}` left for the answer, plus the passing choices |
| anything else | skipped with the class name |

- `input` as a string is the question; chat messages become one question, each message written with its role (a lone user message is written as is).
- Several `ideal` values are alternatives: any one passes. The first is the reference answer admission checks.
- Few-shot files (`few_shot_jsonl`) are not imported.
- Each task id is `<eval name>-<line>`; the eval name is the task's category.

## Import

```sh
bifrost-bench import openai-evals -from datasets/openai-evals -suite my-evals -set evals=capitals,arithmetic
bifrost-bench import openai-evals -from datasets/openai-evals -suite my-graded -set grader=judge \
  -set judge_provider=openai -set judge_model=<model> -set evals=<model-graded eval>
bifrost-bench suite lint my-evals && bifrost-bench suite lock my-evals && bifrost-bench suite admit my-evals
```

Settings: `evals` (names or ids, default all), `grader` (`answer_match` default, or `judge`), `judge_provider` and `judge_model` (required with `grader=judge`), `image` (default `bench/coding-core-base:1`), `license` (default MIT), `source`. `-ids` selects task ids, `-limit` caps the count. An existing suite is extended only when it has the same grader. Model-graded evals are skipped in an `answer_match` suite with the reason.

## Hugging Face datasets

`import fetch hf` reads a split through the dataset viewer API (`/splits`, `/info`, `/rows`, 100 rows a page; `HF_TOKEN` for gated sets), writes `<config>-<split>.jsonl` and `SOURCE.md`, and names the importer that reads it from the columns:

```sh
bifrost-bench import fetch hf princeton-nlp/SWE-bench_Verified -to datasets/swe-verified -suite my-swe   # swebench, imported
bifrost-bench import fetch hf https://huggingface.co/datasets/<org>/<qa-set> -to datasets/qa -split test
```

| Columns | Route |
| --- | --- |
| `instance_id`, `FAIL_TO_PASS`, `PASS_TO_PASS` | `import swebench` (run directly with `-suite`) |
| `question_id`, `haystack_sessions` | `import longmemeval` |
| `task_id`, `Question`, `Final answer` | `import gaia` |
| a question-like and an answer-like column | `task import-rows` into a qa suite |
| anything else | none: map the columns by hand |

Default split: `test`, else `validation`, else the first. `-max n` caps the rows. Public datasets are likely in model training data: `SOURCE.md` says so, and they belong in a public split, never held out.
