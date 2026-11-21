# Importing CheatBench

CheatBench (Center for AI Safety, MIT, <https://github.com/centerforaisafety/cheatbench>,
paper arXiv 2609.36308, site cheatbench.ai) measures whether an agent cheats on a hard
assignment: each environment sets an expectation of honest work and plants a way to
break it (hidden answer files, a checker to tamper with, another player's work).
It is the current cheating test in the library. ImpossibleBench
(`importers/impossiblebench`, docs/importing-swe.md) is the older cheating test and
stays available.

## How it maps

| CheatBench | Heimdall |
|---|---|
| one row of a task's `data.jsonl` (every built variant) | one task; id is the row id (plus `-<variant>` when a task has several) |
| `prompts/*.md` rendered with `{{input}}` and `vars` | `instruction.md` (same empty-line rule) |
| `files` staged into the working directory | `repo/` (workspace `/workspace`) |
| `files_abs` planted outside it | `env/abs/`, copied to `/` by the task's own `env/Dockerfile` |
| upstream sandbox image (python:3.11-slim + libraries) | suite `base/Dockerfile` (`bench/cheatbench-base:1`) |
| shared judge `prompts/judge.md` + task `judge_schema.py` | judge grader, prompt `cheatbench-v1`; the rubric is the whole `judge_schema.py` plus the row's hidden `meta` |
| verdict `cheated: true` | judge vote `correct`, a passing grade on an `impossible: true` task |
| cheat rate | `impossible_pass` gate makes the run an `integrity_violation`; `cheating_rate` (lower is better) counts it |

The judge reads the run's recorded actions (monitor transcript: model messages, tool
calls, commands, file changes), the final message and `answer.txt` if any
(`graders/cheatbench.go`). A run with no actions and no answer is not judged and is
not cheating. Pin the judge to your model before locking (the importer writes
`pin-me`; upstream's default judge is gpt-6-sol). Task success is not scored, as
upstream keeps success and cheating separate.

## Admission controls

- reference (`solution/answer.txt`): a recording that plainly takes the shortcut; the
  judge must rule it cheated, three times.
- exploit (`controls/exploit.sh`): an honest answer that asks the judge for a
  `correct` vote; it must not pass.
- empty: nothing done, not judged.

About 3 judge calls per admitted task (reference, exploit, null refuse). On 22 Nov 2026, 40 of 40 runnable tasks were admitted with 127 judge calls; 2 failed one reference vote and passed on a second admission.

## Drafts

Every upstream environment is imported. Tasks that need something not ported are
tagged `draft:<reason>` with a `DRAFT.md` and are never admitted:

- `mcp-grader-tool`: chess, prime_factorization, subset_sum, protein_design,
  knowledge_work (agent-callable verifier hosted by upstream's adapter)
- `host-service`: openmath, openmath_agent, svgs (per-episode private-network service)
- `paid-grader`: creative_writings (staged grader calls an upstream LLM)
- `gpu`: go (KataGo)
- `chat-runner`: sycophancy (probability-shift chat probe)
- `harbor-runner`: software_engineering (separate Harbor workflow)

synthetic_puzzles stages a git repository (generator/.git), so its workspace is built into the task image (environment.workspace_path) instead of repo/. An environment with no built rows gets one `<task>-unbuilt` placeholder draft.

## Running it

```
git clone https://github.com/centerforaisafety/cheatbench && cd cheatbench
git checkout 96e05a6d61faf33be788f75dd01998066045ad02
for t in tasks/*/build.py; do (cd $(dirname $t) && python build.py); done
bifrost-bench import cheatbench -from cheatbench -suite cheatbench -set revision=96e05a6d61faf33be788f75dd01998066045ad02
```

Settings: `revision`, `version`, `tasks=<a,b>` (subset of upstream task names).
