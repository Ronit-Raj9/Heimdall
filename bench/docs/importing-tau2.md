# Importing tau2-bench

`bifrost-bench import tau2` turns the customer service domains of
[tau2-bench](https://github.com/sierra-research/tau2-bench) into a suite of
track `tool_use`. In each task the agent talks to a customer and calls the
domain's tools (look up a reservation, cancel it, issue a refund), and the
final state of the domain database decides the outcome.

The customer is scripted, not played by a model. Read
[Not comparable with the leaderboard](#not-comparable-with-the-leaderboard)
before you compare any number with a published one.

## What upstream is

Checked against commit `c06b07dd7c4d4ec61ba6ff9bcdf0b15dd7ef36d4` (10 Nov 2026,
package version 2.0.0). License: MIT.

- Domains live in `data/tau2/domains/<domain>/`: `db.json` (the database),
  `policy.md` (the agent's policy), `tasks.json`. The tools are Python, in
  `src/tau2/domains/<domain>/tools.py`, each marked READ, WRITE, THINK or
  GENERIC.
- A task has a `user_scenario` (what the simulated user wants, knows and does
  not know, and how to behave), an optional `initial_state`, and
  `evaluation_criteria`: expected `actions` (tool calls), `communicate_info`
  (facts the agent must tell the user), `nl_assertions` (statements a judge
  model checks) and a `reward_basis` that says which of these count.
- Upstream decides a task like this. The agent's tool calls are replayed on a
  fresh database and the expected actions on another; the reward's DB part is
  1 when the two databases hash the same. COMMUNICATE is 1 when every required
  fact appears in the agent's messages. NL_ASSERTION asks a judge model. The
  reward is the product of the parts in the task's `reward_basis`. A
  conversation that ended for any reason other than the agent or the user
  stopping scores 0.
- Since 2.0.0 the communicate check is LLM-judged by default (a substring
  scorer is still an option) and the NL judge is a pinned model. 2.0 scores are
  not comparable with 1.x.
- The released text benchmark (`data/tau2/benchmarks/text.json`, `text-v1`)
  runs `airline_50` (all 50 airline tasks) and `retail_50` (50 of the 114
  retail tasks, in the reviewed English versions from
  `data/tau2/multilingual/en/retail_tasks_en.json`), plus `telecom_50`.
- In the airline tasks the reward basis is DB and COMMUNICATE. In the retail
  tasks it is DB and NL_ASSERTION, so retail's `communicate_info` is not
  scored upstream either.

## Import

```sh
git clone --depth 1 https://github.com/sierra-research/tau2-bench.git datasets/tau2-bench
bifrost-bench import tau2 -from datasets/tau2-bench -suite tau2
bifrost-bench suite lint tau2
bifrost-bench suite lock tau2
bifrost-bench suite admit tau2
```

The clone is about 1.1 GB. The importer reads only local files and pins the
clone's commit into the service image.

| Setting (`-set k=v`) | Default | Meaning |
|---|---|---|
| `domains` | `airline,retail` | Domains to import. Only these two are supported. |
| `task_set` | `published` | `published`: the task subsets of the released text benchmark, with the English pack's reviewed tasks. `all`: every task in the domain's `tasks.json`. |
| `nl_assertions` | `skip` | `skip` leaves out tasks whose score needs a judge model. `unscored` imports them and scores only the database; their natural-language assertions are listed in the verifier's details, never scored. |
| `noop_tasks` | `skip` | `include` also imports tasks whose correct outcome is "change nothing, say nothing in particular". Their empty control passes, so admission refuses them. |
| `user` | `scripted` | The only user that is built. |
| `max_user_turns` | `30` | Replies before the scripted user leaves. |
| `ref` | | Upstream commit, when `-from` is not a git clone. |

`-ids` takes bench ids (`airline-7`) or upstream ids (`7`). Task ids are
`<domain>-<upstream id>`.

At commit `c06b07d` the default import writes 27 airline and 31 retail tasks.

| Left out | Airline | Retail (published) | Retail (all) |
|---|---|---|---|
| Correct outcome is no change and nothing to communicate | 23 | 3 | 6 |
| Needs a judge model (`nl_assertions=skip`) | 0 | 16 | 40 |

Telecom is not supported: its tasks need the user to run tools on their own
phone, which the scripted user cannot do. `banking_knowledge` needs a
retrieval index and judged answers. `intake` and `mock` are not imported.

## What a task is

```
tasks/airline-7/
  task.yaml               service, exports, output kind state, images
  instruction.md          the conversation rule and the domain policy
  repo/README.md          an empty workspace (the work happens over MCP)
  workspace/Dockerfile    the task image the agent runs in (no upstream data)
  service/                Dockerfile and tau2svc.py: the service and verifier image
  scenario/scenario.json  domain, starting state, the customer's scenario
  verifier/tests/         task.json (upstream's task, verbatim) and grading.json
  verifier/requirements.md  what each reward dimension checks
  controls/               reference.sh, empty.sh, mutant1.sh, exploit.sh
```

**Images.** The task's own image (`environment.dockerfile`,
`workspace/Dockerfile`) is a plain Python image: the agent runs in it and
sees no upstream file. The service image (`service/Dockerfile`) installs the
upstream package; the run builds it as a task service and tags it
`bench/svc-<suite>-tau2:<content hash>`, the same for every task of the
import, and the verifier runs in it (`environment.verifier_image` holds that
tag, computed at import the same way the run computes it). Both builds need
the network (`environment.build_network: true`); the runs do not.

**The service.** One task service `tau2`, role `mcp`, proxied, on port 8000.
Its image installs the upstream package at the pinned commit. It runs `tau2svc.py serve`, an MCP streamable HTTP server at `/mcp` that answers
with JSON (no event stream). Its tools are the domain's own tools, with their
upstream names, descriptions and argument schemas, working on a fresh copy of
the domain database seeded with the task's starting state, plus one more:

- `respond_to_user(message)`: sends the message to the customer and returns
  the customer's reply. The conversation is over when the reply contains
  `###STOP###`, `###TRANSFER###` or `###OUT-OF-SCOPE###`. Later messages are
  answered with "The customer has left the conversation" and do not count.

Tool calls go through upstream's own environment code, so a wrong argument
returns upstream's `Error: ...` text as an MCP tool error. Any MCP-capable
agent can run these tasks with no custom protocol. The agent never sees
`scenario.json`, the expected actions or the database file: they are inside the
service container, which the agent reaches only through MCP.

**Exports.** After every change the service writes `/state/db.json` (the
database, its upstream hash, and the user database when the domain has one)
and `/state/transcript.json` (every message and tool call in order, and which
stop token ended the conversation). Both are exported as `state/tau2/db.json`
and `state/tau2/transcript.json`.

**The scripted user.** Built from the task's user scenario. The first reply
states the whole request: the reason for the call, what the user knows, what
they do not know, and the scenario's instructions on how to handle it, in
upstream's own words (written about the user in the second person, which the
reply says). After that it answers "Yes, I confirm" to a request for
confirmation, answers a question with the sentences of its known information
that share words with the question, says it does not know otherwise, stops
with `###STOP###` when the agent reports without asking anything or asks
whether there is anything else, and answers `###TRANSFER###` when told it is
being transferred to a human agent. It is deterministic and free. It does not
play out the scenario's conditional instructions turn by turn ("if the agent
asks for confirmation a second time, ask for a return instead"); the agent
reads them in the first reply instead.

**The verifier.** Runs in the service image with no network:
`tau2svc.py verify` reads the hidden `task.json`, replays the expected actions
on a fresh database with upstream's code, and compares the hash with the
exported database, as upstream does. When COMMUNICATE is in the reward basis it
checks each required fact against the agent's `respond_to_user` messages with
upstream's substring rule (case folded, commas removed from the message). It
writes `/grader/out/reward.json`:

```json
{"reward": 1, "dimensions": {"db": 1, "communicate": 1}}
```

and `/grader/out/details.json` with each check, the stop token, and notes such
as unscored assertions. `communicate` appears only when the task's reward basis
has it. A missing export is an error with no reward file, never a zero.

**Controls.** Script controls, run in the agent's place with the services up.
Each is a self-contained shell script holding a small Python client and the
task's plan; the client speaks raw JSON-RPC over HTTP to the URL in
`BENCH_MCP_SERVERS` (no MCP library).

| Control | Does | Must |
|---|---|---|
| `reference.sh` | greets, performs the expected actions in order, tells the customer the required facts, asks if there is anything else | pass (reward 1) |
| `empty.sh` | nothing | fail |
| `mutant1.sh` | the reference without its last database change, or without the required facts when it changes nothing | fail |
| `exploit.sh` | greets, repeats the customer's own words back, claims the request is done, no tool call | fail |

## Not comparable with the leaderboard

Numbers from these suites measure the same skills on the same tasks and the
same databases, graded on the same database check, but they are not upstream
scores:

- The user is scripted. Upstream's user is a model that reveals information a
  little at a time and follows the scenario's conditional instructions. The
  scripted user states everything at once, which makes some tasks easier, and
  cannot react to the agent, which makes others harder.
- The communicate check is upstream's substring scorer, not the 2.0 default
  semantic judge.
- Tasks that need a judge model are left out, or (with
  `nl_assertions=unscored`) scored on the database only.
- Tasks whose correct outcome is to change nothing are left out (the bench
  admits a task only when doing nothing fails it). In airline that is 23 of
  the 50 tasks, mostly refusals.
- The agent's prompt is the bench instruction (upstream's agent instruction
  adapted to `respond_to_user`, plus the policy), not upstream's system prompt.
  Upstream's step and error limits are not applied; the run's wall budget is.
- A run that ends without the customer stopping is still graded. Upstream
  treats the agent's own stop the same way.

A model-played user (upstream's behaviour, through the gateway, costing money)
is not built.

## What the run needs

The tasks use scenario features the environment kernel provides: a task
image, task services built from a Dockerfile in the task folder with build
network, files copied into a service, state exports, the tool proxy for MCP
services, script controls run with the services up, and the task's
`verifier_image`. If the service Dockerfile or `tau2svc.py` is edited after
import, the service tag changes and `verifier_image` must follow: import again
with `-force`.
