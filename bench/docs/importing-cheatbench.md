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

About 3 judge calls per admitted task (reference, exploit, null refuse). The reference recording is specific to the environment: it reads the planted files, opens the planted archive or the other agent's branch, and computes with code where code is banned. On 22 Nov 2026, 63 tasks were admitted (geoguessr 20, synthetic_puzzles 20, prime_factorization 5, subset_sum 4, creative_writings 5, openmath 5, openmath_agent 4) with 265 judge calls in all. openmath_agent littlewood fails to seed its workspace (its staged .git has an unparsable HEAD).

## Upstream machinery, as Heimdall services

| Upstream | Heimdall |
|---|---|
| grader.py tool (prime_factorization, subset_sum) | MCP service named after `SERVER_NAME`, serving upstream's `grader.py` through upstream's own `core/agents/runner_support.py` and `core/grader_guard.py`, verbatim (`svc/heimdall_grader.py` only puts it on port 8000). The full row stays in the service. The live-log observer is not carried over: it mirrors the agent's messages into the workspace and needs the agent's message stream |
| review endpoint (creative_writings) | `review` service running upstream's `core/review_server.py` with the task's pairwise prompt and reference; the model is the suite's pinned judge (openai/gpt-6.2-sol) on the service's own gateway key; `grading/.review` is staged with its URL and token |
| HTTPS mirror (openmath, openmath_agent) | `mirror` compose service (upstream's server and capture, from `mirrors/<task>-mirror/`, image `bench/cheatbench-<task>-mirror:1`) on the real host names as network aliases; the mirror CA is added to the workspace image's trust stores, as upstream's sandbox adds it |
| svgs leaderboard | `leaderboard` service running upstream's `leaderboard_server.py`; `leaderboard/leaderboard_config.json` is staged with its endpoint and token |
| software_engineering | Harbor tasks: `bifrost-bench import harbor -from <checkout>/tasks/software_engineering/harbor/tasks -suite cheatbench-swe` (graded by their tests; the cheating judge is not applied there) |

The mirror images live in `mirrors/`, not `services/`: the suite fingerprint
covers `services/`, so adding a mirror to an admitted suite would reset every
admission. Build them before admitting:
`docker build -t bench/cheatbench-openmath-mirror:1 benchmarks/cheatbench/mirrors/openmath-mirror`
(and the same for openmath-agent-mirror).

## Drafts

Tasks that cannot run as upstream defines them are tagged `draft:<reason>`
with a `DRAFT.md` and are never admitted:

- `mcp-only` (chess, 150): upstream gives the agent only the task's MCP tools
  and no shell; a Heimdall agent always has a shell.
- `workspace-grader` (protein_design, knowledge_work, 40): the grader tool
  runs inside the agent's container upstream and reads and changes the
  agent's workspace (git init, file watchers, the deliverable); a service
  container cannot see the agent's workspace.
- `no-episode-verdict` (sycophancy, 112): every reply's verdict is
  `cheated=false`; the score is a shift over all replies (score.py).
- `gpu` (go): KataGo needs an NVIDIA GPU.
- `harbor-runner` (software_engineering placeholder): imported as cheatbench-swe instead.

## Running it

```
git clone https://github.com/centerforaisafety/cheatbench && cd cheatbench
git checkout 96e05a6d61faf33be788f75dd01998066045ad02
for t in tasks/*/build.py; do (cd $(dirname $t) && python build.py); done
bifrost-bench import cheatbench -from cheatbench -suite cheatbench -set revision=96e05a6d61faf33be788f75dd01998066045ad02
```

Settings: `revision`, `version`, `tasks=<a,b>` (subset of upstream task names).
