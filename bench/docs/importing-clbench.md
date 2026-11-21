# Importing CL-Bench (Continual Learning Bench)

`bifrost-bench import clbench` turns [Continual Learning Bench](https://github.com/pgasawa/continual-learning-bench) (Snorkel AI, UC Berkeley Sky Computing Lab, UW-Madison; arXiv 2606.05661; Apache-2.0) into a suite of track `memory`. The code is `importers/clbench/`.

Checked on 22 Nov 2026 at commit `5f8c50eb1e84b2eda2ef4faff757dfc812a0ea26` (main, 18 Jul 2026). Upstream needs Python 3.13 and its own CLI (`clbench run <task> --schedule <id> --system <system>`).

## What upstream measures, and how it maps

Upstream runs a "system" through a stream of instances that share a learnable structure (a poker opponent's habits, a database schema that drifts, a spectrum band's channel plan) and scores how much the system improves over the stream. A task family run on one named schedule is one stream; a schedule has stages (a new opponent, a migrated schema, a new study).

The bench runs each task independently, so one stream becomes ONE bench task with one agent session per stage:

- A service `clenv` (role `mcp`, streamable HTTP at `/mcp`, port 8000) runs the upstream task object, built as `clbench run <family> --schedule <id>` builds it for run index 0 (the canonical order), behind a small MCP server (`importers/clbench/image/server.py`, fastmcp 2.11.3). Tools: `get_brief`, `start_session`, `get_query` (prompt, the JSON schema the response must follow, feedback on the previous response, progress) and `submit`. Every response goes through upstream `task.step`, so rewards are upstream's.
- Sessions are fresh agent processes. The service, and with it the stream's state, lives for the whole run; the workspace is reset between sessions, so only the agent's memory (`$BENCH_MEMORY_DIR`, `/memory`, and its memory paths) carries what it learned from one stage to the next. This is what upstream calls a stateful system, with the agent's own memory as the state.
- Session gating is soft: once a session's instances are complete, `submit` refuses until the next session is started with `start_session(n)`. An agent could start a later session early; each outcome records the session it was played in and the grade reports `sessions_used`.
- After the agent exits, the service's outcomes are exported (`POST /finalize` inside the service, as `state/clenv/result`) and `runverify.py` writes the reward offline: the sum of instance rewards over (instances in the schedule x upstream `r_max`), clipped to 0..1. Instances never reached count as zero. Dimensions: `normalized_reward`, `completion`, `mean_instance_reward`, `sessions_used`, `invalid_submissions`. The suite passes at 0.5.

Upstream's headline metric is `mean_gain`, the stateful run's reward minus a stateless baseline's. The bench reports the stateful reward; the gain needs a second run of the same agent with memory off, which is an experiment, not a task.

## Families

| Family | Streams (schedules) | Here |
|---|---|---|
| exploitable_poker | default (5 stages, 120 hands), calling_station_then_fit_or_fold (2 stages, 40 hands) | runs; reference = upstream `oracle.py` `expert_exploit_action` |
| blind_spectrum_monitoring | default (3 stages, 90 scans) | runs; no upstream reference policy |
| cohort_studies | default (5 stages, 20 studies) | runs; no upstream reference policy |
| database_exploration | default (2 stages, 20 + 20 questions) | runs with `products.db` and `products_drifted.db`; no upstream reference policy |
| codebase_adaptation | default | draft: per-instance Docker containers (mini-swe-agent over SWE-bench images); a service has no Docker daemon |
| sales_prediction | default | draft: the agent's bash runs in a Docker container upstream starts; a service has no Docker daemon |

`quick_test` (poker, 5 hands) is a smoke schedule and is skipped unless `-set smoke=true`.

## Fetch and import

```sh
git clone https://github.com/pgasawa/continual-learning-bench datasets/cl-bench
mkdir -p datasets/cl-bench-db
R=a0cc57eeb9a54f01c0490a1b46cb705b4e05aa19   # dataset revision checked on 22 Nov 2026
for f in products.db products_drifted.db; do
  curl -L -o datasets/cl-bench-db/$f https://huggingface.co/datasets/continual-learning-bench/database-exploration/resolve/$R/$f
done
bifrost-bench import clbench -from datasets/cl-bench -suite cl-bench -set db_dir=datasets/cl-bench-db
bifrost-bench suite lint cl-bench && bifrost-bench suite lock cl-bench && bifrost-bench suite admit cl-bench
```

Settings (`-set key=value`): `db_dir` (default `<from>/data/database_exploration`), `ref` (upstream commit when `-from` is not a clone), `schedules` (`all` or `default`), `smoke` (`true` imports `quick_test`), `version` (suite version). `-ids` takes bench ids (`clb-poker-default`) or `family/schedule`.

## What a task looks like

```
tasks/clb-poker-default/
  task.yaml  instruction.md  IMPORT.md  repo/README.md
  sessions/session-<n>.md        one per stage
  service/Dockerfile              python:3.13-slim + the pinned upstream dependencies
  service/server.py               the MCP wrapper
  service/clbench/                upstream src/, LICENSE, data/<family>/ (and the two databases)
  verifier/tests/runverify.py
  controls/reference.sh  controls/empty.sh  controls/exploit.sh
```

The service image is built at the first run with network (`pip install` of the versions upstream's `uv.lock` resolves, plus fastmcp, mcp and treys). Its environment carries the family, the schedule, the stages and a random reference token written at import.

## Reference controls

CL-Bench ships a reference policy only for exploitable_poker. Its reference control plays the stream through the MCP endpoint and submits `{"__reference__": <token>}` each turn; the service answers that token, and only that token, with upstream's `expert_exploit_action`. The token is in the control and the service's environment, never in the agent's container. Checked end to end on `calling_station_then_fit_or_fold` through the real MCP transport: 40/40 hands over 2 sessions, reward 1.0 (mean 12.7 big blinds per hand against `r_max` 9.49).

For the other runnable families the reference control refuses, so they stay drafts until someone writes one (a script that plays the stream through `$BENCH_MCP_SERVERS`). For calibration: answering every blind_spectrum_monitoring scan with an empty transmitter list scores 0.196.
