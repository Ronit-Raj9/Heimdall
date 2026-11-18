# Importing MultiAgentBench (MARBLE)

`bifrost-bench import marble` turns tasks of
[MultiAgentBench](https://arxiv.org/abs/2503.01935), the benchmark of the
[MARBLE](https://github.com/ulab-uiuc/MARBLE) repository, into team tasks:
every upstream agent becomes a member of the task's team (one container
each, on its own gateway key), the upstream relationship graph becomes who
may message whom through the team mailbox, and the upstream scoring becomes
a grader. It lets multi-agent claims rest on a public benchmark's tasks, not
only on this repository's own examples.

| Scenario | Tasks upstream | Imported as | Why |
|---|---:|---|---|
| database | 100 | verified suite (default) | PostgreSQL and a query tool run as services; the true root causes are in the task, so a script decides with no model |
| research | 100 | judged suite (`-set scenario=research`) | upstream scores proposals only with an LLM judge; there is no ground truth and no reference answer |
| coding | 100 | judged suite (`-set scenario=coding`) | upstream scores `solution.py` only with an LLM judge; no tests and no reference solution ship |
| bargaining | 100 | refused | competitive: buyer and seller are rated apart by a judge; no shared deliverable, no team outcome |
| minecraft | 100 | refused | needs a Minecraft Java 1.19.2 server and Node.js mineflayer bots |
| werewolf | not in the task files | refused | a competitive game with its own engine and entry point |

So 100 of the 500 task records can be verified, 199 more import as judged
tasks (one research task is skipped, see [Checked here](#checked-here)), and
200 are refused; werewolf has no task records at all. `import`
prints these counts for the clone it reads.

## What upstream is

Checked on 18 Nov 2026 against commit
`eae9f15e5b9f1e808f8ff61800b129d5538c1b47` of `ulab-uiuc/MARBLE` (main, 16
Nov 2026, "Fix reported bugs and make MultiAgentBench runnable out of the
box (#247)"), read through the GitHub API and raw files, and against the
paper (arXiv 2503.01935v1, submitted 3 Mar 2025; the README calls it ACL
2025 Main).

- **Licence.** MIT (`LICENSE`: "Copyright (c) 2024 Haofei Yu"; the GitHub
  API reports `MIT`). Every imported suite gets `LICENSE.marble` (the
  clone's own `LICENSE`) and `NOTICE.md` saying what was taken and what was
  written here.
- **Scenarios.** The paper (section 3.2) has four collaborative scenarios,
  research, Minecraft, database and coding, and two competitive ones,
  werewolf and bargaining. The repository's `multiagentbench/` folder holds
  `bargaining`, `coding`, `database`, `minecraft` and `research`, each a
  `<scenario>_main.jsonl` with 100 records (counted). Werewolf has its own
  entry point (`scripts/werewolf/`, `marble/environments/werewolf_env.py`).
- **Task format.** One JSON object per line: `scenario`, `task_id`,
  `coordinate_mode`, `relationships` (`[agent, agent, relationship]`
  triples), `llm`, `environment` (`type`, `name`, `max_iterations`; for
  database also `init_sql` and `anomalies`, each `{anomaly, threads,
  ncolumn, nrow, colsize}`), `task` (`content`, `output_format`; for
  database also `labels`, `root_causes`, `number_of_labels_pred`), `agents`
  (`type`, `agent_id`, `profile`; bargaining also `role`, `llm`), `memory`,
  `metrics`, `engine_planner`, `output`. Blank fields (`llm`, `type`,
  `max_iterations`, `coordinate_mode`) are filled by
  `scripts/run_benchmark.py`: coordinate mode `graph`, 5 iterations for
  research, coding and database, 3 for bargaining, 20 for Minecraft.
- **Agents, roles, graph.** Agents are `agent1` to `agentN`, each with a
  free-text profile. Team sizes: coding 3 in every task, database 5 in every
  task, bargaining 4, Minecraft 3, research 1 to 22 (2 tasks with 1 agent, 7
  with 2, 23 with 3, 18 with 4, 15 with 5, 35 with 6 or more). Relationships
  are `collaborate with` or `collaborates with` (bargaining: `parent`). In
  every coding, database and research record the graph is complete (every
  pair related; research lists each pair once, coding lists both
  directions). In `marble/agent/base_agent.py` an agent may open a
  communication session with any agent a relationship connects it to, in
  either direction, and the tool's target is an enum of exactly those
  agents; a session is a conversation of up to 5 turns between the two.
- **Orchestration.** `marble/engine/engine.py` runs `graph`, `star`,
  `tree` or `chain` coordination for `max_iterations` rounds; an LLM engine
  planner assigns each round's tasks and summarises.
- **Scoring.** The paper (section 3.3): a task score (TS) from an LLM rubric
  for research and bargaining and rule-based metrics for Minecraft,
  werewolf, database and coding; milestone KPIs from an LLM-based detector;
  a coordination score (CS), the mean of a communication and a planning
  score, each 1 to 5 from an LLM. The code at the checked commit
  (`marble/evaluator/evaluator.py`, `scripts/run_benchmark.py`): database
  accuracy is the share of true root causes among the labels a judge model
  extracts from the team's free-text answer; coding is judged on four 1 to 5
  criteria (instruction following, executability, consistency, quality);
  research is judged on innovation, safety and feasibility, 1 to 5;
  Minecraft is a block hit rate. The paper calls coding rule-based, the code
  judges it; this importer follows the code.
- **External software.** README "Scenario requirements": database needs
  Docker with the compose plugin, which starts PostgreSQL and Prometheus
  (`marble/environments/db_env_docker/docker-compose.yml` pins
  `postgres:17`); Minecraft
  needs a Minecraft Java 1.19.2 server in offline mode and Node.js for the
  mineflayer bots. Research's tools fetch papers from arXiv and the web
  (`research_env.py` uses `requests`).
- **Database environment.** Only the `query_db` action is registered
  (`db_env.py`; the alert and metric actions are commented out). Before the
  agents start, `anomaly_trigger/main.py` runs each anomaly with the task's
  `threads`, `ncolumn` and `colsize`; `nrow` is not passed, so the default of
  100 rows applies, for a 60 second default duration. The records' anomalies
  are `INSERT_LARGE_DATA`, `LOCK_CONTENTION`, `VACUUM`, `REDUNDANT_INDEX`
  and `FETCH_LARGE_DATA` (30 each, 150 in 100 tasks), all with threads 100,
  ncolumn 20, colsize 100; half the tasks have one true root cause and allow
  2 labels, half have two and allow 3.
- **Not verified.** The paper's appendices on Minecraft, database and coding
  were not read in full. The `communication: false` field of every database
  record is not read by `engine.py`, `base_agent.py`, `db_env.py` or
  `run_benchmark.py` (searched); other files were not searched.

## Getting the data

```
git clone https://github.com/ulab-uiuc/MARBLE.git datasets/marble   # about 215 MB; only multiagentbench/ (5.4 MB) is read
```

`-from` may also name one `<scenario>_main.jsonl` file; then pass
`-set revision=<commit>`, or provenance says the revision is unknown.

## Importing

```
bifrost-bench import marble -from datasets/marble -suite marble-db -ids 1,31,51,71
bifrost-bench suite lint marble-db && bifrost-bench suite lock marble-db && bifrost-bench suite admit marble-db
bifrost-bench import marble -from datasets/marble -suite marble-research -set scenario=research   # judged
```

Settings (`-set key=value`): `scenario` (database, research, coding;
default database), `lead` (the member that writes the deliverable, default
the first agent), `revision`, `version` (suite version), `agent_s` (agent
wall time, default 1800), `postgres_image` (default `postgres:18`, the image
the bench stack already has; `postgres:17` matches upstream's pin) and, for
the judged scenarios, `references` (a folder of `<task id>.answer`
reference answers). One suite holds one scenario.

A task is skipped, with the reason, when an agent id is not a valid member
id, a relationship names an undeclared agent or is not a triple, a member
cannot reach the lead through the graph, a database root cause is not one of
its labels, `number_of_labels_pred` is below the number of true causes, or
an anomaly has no replica (upstream's `MISSING_INDEXES` and the two combined
anomalies; no record uses them).

## The mapping

### Agents and the graph

Each agent becomes a member of `team:` in `task.yaml`, in upstream order,
with its upstream id; the lead (`-set lead`, default the first agent) gets
role `lead`, the others `worker`. No member sub-budget is set, so the arm's
total cap (`budgets.usd`) bounds the whole team. The task declares the team,
so every arm runs it with its own agent config on every member; an arm with
`topology: single` or `best_of_n` runs alone on the same tasks (the
baselines below).

The relationships become routes: the allowed pairs are undirected, as in
MARBLE (both ends of a relationship may talk), and a self-loop gives no
route. The instruction lists every member's profile and who may message
whom; `marble-team.json` in the workspace holds the same. The mailbox
itself accepts any recipient, so the graph is enforced at grading: every
mailbox message from a member to a member it has no relationship with (the
proven sender of the tool proxy route when known, else the declared
`from`; a broadcast counts once per recipient) is a route violation and
fails the run. Every graph of the imported scenarios is complete at the
checked commit, so no real task can violate it yet; the rule is tested on a
hand-made chain graph.

### Database tasks (verified)

- **Services.** `db`: stock PostgreSQL (`postgres:18` by default) with
  `pg_stat_statements` preloaded, user `test`, database `sysbench`, hidden
  from the agents. `dbtool`: the helper image's `python3` running
  `service/dbtool.py`, reached only through the tool proxy, so every query
  is a ledger row attributed to its member. It runs the task's `init_sql`
  statement by statement, creates `pg_stat_statements`, replays the
  anomalies, and only then answers ready, so the agents start on a database
  that already carries the anomaly's traces. `POST /query {"sql": ...}`
  answers like upstream's `query_db` (`status`, `explanation`, plus
  `columns` and `rows`).
- **Anomaly replica.** The upstream trigger's statements, with the task's
  column count and size, run in sequence instead of from 100 threads for 60
  seconds: 200 inserts of 100 rows, 200 updates, 20 `VACUUM FULL`, 10
  redundant indexes plus updates, 10,000 orders and 200 `SELECT * FROM
  orders LIMIT 100`; for lock contention one session holds a row lock for 3
  seconds against 8 updaters. Unlike upstream it leaves its tables and
  indexes in place, so the views the profiles point to (`pg_stat_user_tables`,
  `pg_stat_user_indexes`, `pg_indexes`) still show them beside
  `pg_stat_statements`.
- **Deliverable.** The lead writes `diagnosis.md` with one line
  `ROOT_CAUSES: <label>, <label>` (an artifact output, copied by the trusted
  helper). The hidden `verify.py` reads the last such line with no model:
  reward = upstream's accuracy (true causes named over true causes), 0 when
  more labels are named than `number_of_labels_pred` allows or when any
  route was violated; the task passes at reward 1. Dimensions: `accuracy`,
  `over_limit`, `labels_named`, `unknown_labels`, `root_cause_line`,
  `route_violations`, and `messages` and `senders` (reported only).
- **Controls.** The reference (`solution/reference.sh`) queries the tool
  once and writes the true causes: alone at admission and for a single-agent
  arm; as every member of the task's team for the oracle on a team arm,
  where each worker sends the finding its profile assigns it to the lead
  along the shortest path of the graph (relaying what is not for it), the
  lead waits for every finding, writes the diagnosis and sends `done` back
  down. The empty control writes nothing, the mutant swaps one true cause
  for a wrong label, the exploit names every label (over the limit), and the
  null controls do nothing; all must fail.

### Research and coding tasks (judged)

Upstream ships no tests, no reference answers and no ground truth for
these, and scores them only with an LLM judge. They import into a suite with
`grading: judged`, the judge grader and its `qa-v1` rubric prompt; the
rubric in `verifier/answer.yaml` is upstream's criteria, and the pass rule
(every rating at least 3 of 5) is this importer's, because a bench outcome
is pass or fail. The deliverable is `proposal.md` or `solution.py`, read as
the answer. Judged suites are labelled in reports and on the leaderboard and
never share a comparison with a verified suite. The reference answer says
that upstream has none, so the judge must reject it and the task is not
admitted until a person writes one (`-set references`). A one-agent research
task imports without a team. Route violations are not checked here: the
judge reads only the answer.

A judged grade is the weakest evidence the bench has: in these suites the
judge's vote is the outcome. Reports and the leaderboard label it, it is
never compared with a verified result, and no verdict from a judged MARBLE
suite should be read as a finding until its judge has been calibrated at
admission against a reference a person wrote and the split or unparsable
votes have been reviewed (docs/human-review.md).

## Equal-budget baselines

Every import writes `experiments/equal-budget.yaml` into the suite folder,
following docs/multi-agent.md: `fairness: equal_budget`, every arm at the
same `budgets.usd`, arm A the single-agent baseline (`topology: single`), arm
B the team (`topology: team`), `bon` the best-of-N baseline (`topology:
best_of_n`) and `broken` a team where every member does nothing. The
verdict compares A and B. It is written with the scripted controls
(`agents/oracle.yaml`, `agents/noop.yaml`), so it runs at no cost; swap in
real agent configs for a real comparison. The bench has no generic best-of-N
runner: a `best_of_n` arm's agent must sample and pick by itself; in the
template it is one oracle attempt with the label.

A team of N takes N gateway key slots at once. Every database task has 5
agents, so a stack with the default 4 slots refuses the plan, naming the
task, its team size and `gateway.key_slots` (`engine/taskteam.go`).

## What differs from the MARBLE harness

Scores from these suites are not comparable with the paper's, and should
not be quoted next to them:

- **Orchestration.** MARBLE runs its own engine: rounds up to
  `max_iterations`, an LLM engine planner that assigns tasks and
  summarises, agents that act through MARBLE's tool calls, and two-agent
  communication sessions of up to five turns. Here each member is an
  independent agent of the arm's framework in its own container, all
  members run at once for the wall budget, there is no planner and no
  rounds, members talk through the mailbox whenever they like within the
  graph, and the lead writes the deliverable. The coordination modes (star,
  tree, chain) are recorded in `marble-team.json` but not enforced.
- **Scoring.** Database: upstream asks a judge to pick labels from free text
  and averages accuracy; here a fixed line is read with no model and a task
  passes only at accuracy 1 within the label limit. The milestone KPIs and
  the planning and communication scores are LLM-judged upstream and are not
  computed; the bench's own multi-agent metrics (`messages`,
  `orchestration_share`, `duplicate_work_ratio`, attribution, and so on)
  stand beside verified success instead.
- **Database environment.** PostgreSQL 18 by default (upstream pins 17),
  trust authentication on an internal network, no Prometheus or exporters
  (upstream starts them, but its agents' only registered action is
  `query_db`), and a scaled, sequential anomaly replica that keeps its
  tables.
- **Research.** The tools that fetch papers from arXiv and the web are not
  provided: runs have no route out.
- **Side channel.** Members share the workspace, so they could pass messages
  through files; the instruction forbids it, and only the mailbox gives
  per-message evidence and a route check.
- **Models.** Upstream calls models through LiteLLM directly; here every
  call goes through the run's Bifrost keys, one per member.

## Checked here

All on 18 Nov 2026, at no cost: scripted controls only, no model call. The
team experiment ran on a throwaway stack of its own (Bifrost v2.5.1, its
Postgres, a fault proxy with six key slots and no provider key), because a
database team needs 5 slots and the shared stack has 4.

- **Import.** Every record of the checked commit imports and lints clean:
  100 of 100 database tasks, 100 of 100 coding tasks, 99 of 100 research
  tasks (research 9 is skipped: a profile says "expert human graders", and
  suite lint refuses the word in an instruction; upstream text is not
  rewritten). `TestImportRealClone` with `MARBLE_DIR` set.
- **Admission.** Database tasks 1, 31, 51 and 71 (anomalies
  `INSERT_LARGE_DATA`, `REDUNDANT_INDEX`, `VACUUM` + `FETCH_LARGE_DATA`,
  `INSERT_LARGE_DATA` + `LOCK_CONTENTION`, so every replica) admitted with
  `suite admit` in Docker: 32 of 32 controls as expected (per task the
  reference passed 3 of 3; the empty control, the mutant, the exploit and
  both null controls failed), plus the static, instruction, secret and
  verifier-network checks. Each control started PostgreSQL and the database
  tool beside it.
- **Team experiment.** `benchmarks/marble-db/experiments/equal-budget.yaml`
  through `bifrost-bench run`, experiment `e_marble_db_equal_budget_i3tp`,
  analysis `a_dhp4jknw`, 16 sealed runs:

  | Arm | Topology | Verified success | Mailbox messages per run |
  |---|---|---:|---:|
  | A, oracle alone | single | 4 of 4 | 0 |
  | B, oracle as every member of the 5-agent team | team | 4 of 4 | 8 |
  | bon, oracle alone | best_of_n | 4 of 4 | 0 |
  | broken, noop as every member | team | 0 of 4 | 0 |

  The verdict is INCONCLUSIVE, no decision (A and B tie on all four tasks).
  The `fairness_equal_budget` check is UNKNOWN and `claim_allowed` is false:
  no run made a model call, so no run has cost evidence against its cap.
  Both baselines were recognised. In the team run on task 71
  (`r_qkqjpmc0b0`) each member ran on its own key and slot (`fp-slot-1` to
  `fp-slot-5`), every database query and mailbox call in `tools.jsonl` is
  stamped with its member, the 8 messages (4 findings to the lead, 4 `done`
  back) have proven senders, the verifier read them from the mailbox record
  (`messages` 8, `senders` 5, `route_violations` 0) and passed the
  diagnosis at accuracy 1; `agents_spawned` 4, `attribution_coverage` 1,
  `serial_collapse` 0. The other team runs: `r_dsus4mblec`, `r_bwhgi2fnfn`,
  `r_kdoymp24v2`; the broken team: `r_tmuq152xk0`, `r_nyq2fcxff8`,
  `r_fq496634da`, `r_985mumhchi` (required artifact missing).
- **Plan refusal.** The same experiment planned against 4 key slots is
  refused for arms B and broken on every task: "its team of 5 members needs
  5 gateway key slots, the gateway has 4 (gateway.key_slots) for provider
  "anthropic"". Arms A and bon are not refused: they run alone.
- **Anomaly traces.** With the services started by hand (the tool was ready
  5 to 24 seconds after start), the tool showed:
  task 71, 201 calls of the large insert (20,100 rows) and updates waiting
  about 3 seconds each on the held row lock; task 51, 20 `VACUUM FULL`
  (about 20 seconds in all) and 200 calls of `SELECT * FROM orders LIMIT
  100`; task 31, the redundant indexes with `idx_scan` 0.
- **Unit tests** (`importers/marble`): the graph to routes mapping
  (undirected pairs, self-loops, partial graphs, paths to the lead,
  unreachable members, refusals), the import of a hand-made clone (skips with
  reasons, refused scenarios with counts, provenance, licence files, the
  experiment file decoded against its schema, judged suites, a kept pinned
  judge), the verifier on 17 cases (accuracy, label limit, the label holding
  a comma, missing line or file, route violations by proven sender, declared
  sender, broadcast, planted messages), the database tool's SQL splitter,
  and the reference team run as three processes against the real mailbox
  server on a chain graph, where the far member's finding reaches the lead
  through the middle one and the verifier passes with no violation.
- **Judged path, smoke only.** Research tasks 1 to 3 imported with
  placeholder references written for this check (`-set references`; each
  says it is not a quality reference), the judge pinned to `anthropic` /
  `claude-sonnet-4-5`, and the throwaway fault proxy's upstream pointed at a
  scripted judge (W8's judgescript, which says correct only for an answer
  holding the placeholder's marker sentence). A `smoke: true` experiment
  (`e_marble_research_judged_smoke_3ylu`, 9 runs) through the real
  pipeline: the oracle alone 3 of 3 and as every member of the task's team
  (5, 5 and 3 members) 3 of 3, each graded by one judge vote through
  Bifrost (`correct`, prompt `qa-v1-rubric`); a noop team 0 of 3 (no
  answer, so no judge call). This shows the judged import runs end to end;
  it says nothing about a real judge or about the placeholder's quality.
- **Not checked.** No real model ran any task, as agent or judge. No judged
  task was admitted: `suite admit` on a judged suite picks the unwired judge
  grader in this copy (every control errors "judge grader is not wired to
  the gateway"; reported to the lead), and upstream ships no references
  anyway.

## Limits

- Only the database scenario is verified. Research and coding are judged
  imports that no one has admitted: upstream ships no reference, a judge
  cannot be calibrated at no cost (a scripted judge proves only the
  plumbing), and `suite admit` cannot admit a judged suite in this copy until
  its grader lookup is fixed.
- No real model has run any imported MARBLE task; every run below used
  scripted controls.
- The route check reads the mailbox only; the shared workspace is not
  watched for messages.
- The anomaly replica is not upstream's workload: its traces are smaller
  and sequential, so a team that finds the cause here might not find it
  under upstream's 100-thread load, and the other way round.
- `-set lead` picks one lead for the whole import; MARBLE has none, its
  engine planner plays that part.
