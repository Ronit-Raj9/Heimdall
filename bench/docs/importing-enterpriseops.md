# Importing EnterpriseOps-Gym

`bifrost-bench import enterpriseops` turns the public tasks of
[EnterpriseOps-Gym](https://github.com/ServiceNow/EnterpriseOps-Gym)
(ServiceNow, Apache-2.0; tasks from the Hugging Face dataset
`ServiceNow-AI/EnterpriseOps-Gym`, Apache-2.0) into a suite of track `mcp`.

## What upstream is

Checked at repo commit `271f2c3` (19 Aug 2026), dataset sha `c8e538e`.

- 649 tasks in the `oracle` config (calendar 61, csm 103, drive 64, email 67,
  hr 102, hybrid 88, itsm 103, teams 61). The `plus_<n>_tools` configs are the
  same tasks with n distractor tools; not imported.
- Each task names one or two MCP servers (`gym_servers_config`), each with a
  database snapshot from `gym_dbs.zip` and context headers (user token, user
  id) that upstream's client sends on every call.
- Grading: `database_state` verifiers only (3496 in all): a SQL query through
  the server's `/api/sql-runner` and an expected value (`equals`,
  `greater_than`, `less_than`, `contains`). A task passes when every check
  passes (upstream `overall_success`).
- The MCP servers are Docker images on a personal Docker Hub account,
  `shivakrishnareddyma225/enterpriseops-gym-mcp-<domain>:latest`, linked from
  the Apache-2.0 README. Their source is not in the repo.
- No reference solutions.

## Getting the data

```
git clone https://github.com/ServiceNow/EnterpriseOps-Gym <ds>/repo
unzip -q <ds>/repo/gym_dbs.zip -d <ds>/dbs
# oracle/<domain>-00000-of-00001.parquet from the dataset, as JSON lines:
python -c 'import pyarrow.parquet as pq,json,sys; [print(json.dumps(r)) for r in pq.read_table(sys.argv[1]).to_pylist()]' \
  oracle/email-00000-of-00001.parquet > <ds>/jsonl/email.jsonl   # per domain
```

## Importing

```
bifrost-bench import enterpriseops -from <ds> -suite enterpriseops-gym
for d in calendar csm drive email hr itsm teams; do
  docker build -t bench/eog-$d:1 benchmarks/enterpriseops-gym/services/eog-$d
done
bifrost-bench suite lock enterpriseops-gym
```

Settings: `domains` (comma list), `revision`, `references` (folder of
`<task id>.sh` reference controls; `bench/importers/enterpriseops/references`
holds nine written by hand from the tasks' checks, one or two per domain,
checked end to end). Task ids are
`eog-<domain>-<upstream id without task_>`.

## What a task looks like

- One service per MCP server, id = domain (`email`, `hr`, ...), role `mcp`,
  port 8080, image `bench/eog-<domain>:1`: upstream's image plus
  `bench_front.py`, which starts the upstream server inside the container,
  seeds the task's snapshot (`/api/seed-database`, database id `bench`), and
  serves only `/mcp` on :8080, adding `x-database-id` and the task's context
  headers the way upstream's client does, offering only the task's
  `selected_tools` (upstream's oracle setting) and refusing others. `/api/*`
  is not reachable from the agent.
- Export `checks`: `bench_front.py export` runs the task's SQL checks on the
  final database and prints the raw results.
- Verifier `runverify.py` (offline, `python:3.12-slim`): upstream's value
  extraction and comparison, except that a numeric string expected value
  (144 checks in 43 tasks, which upstream's `==` can never match) is compared
  as a number; reward 1 when all checks pass, the check pass
  rate as dimension `verifier_pass_rate`. No export: no reward (undecided).
- Instruction: a line naming the servers, upstream's system prompt (the
  domain policy), the user info when upstream gives one, then the user prompt.
- Controls: `empty.sh` (no calls, must fail), `exploit.sh`, and a
  `reference.sh` that refuses: upstream has no solutions, so a task is
  admitted only after someone writes its reference.

The servers are per task services, not suite services: each task seeds its
own snapshot, and suite services take no per-task files.

## Not comparable with the leaderboard

Upstream runs its own ReAct/planner orchestrators with its own prompts. Here
the agent is yours; tasks, servers, snapshots and checks are upstream's.
