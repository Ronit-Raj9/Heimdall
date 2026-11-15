# Scenarios: tool-using, MCP, memory and question-answering agents

[`bring-your-own.md`](bring-your-own.md) covers the first shape of task: the
agent edits a repository and hidden tests decide. This page is for every
other agent a company builds: one that calls tools over HTTP, one that uses
MCP servers, one that has to remember across sessions, and one that answers
questions. You bring the agent and the tasks; there is no Go code to write.

A scenario is **workspace + services + sessions + output + verifier**
([`scenarios-design.md`](scenarios-design.md) is the contract). Everything
else stays as it is for repository tasks: the frozen manifest, admission and
`suite.lock`, probes, gates, the paired verdict, the leaderboard (one board
per track), the release gate and sealed run folders.

Status, 15 Nov 2026: the templates, the two example projects and `task init`
for these kinds are done and unit-tested, and every sample and example
task's verifier is checked against its reference and controls on the host
(see "Honest limits"). In Docker, `suite admit` admitted all four
`acme-support` tasks (services, the tool proxy, the state export and the
state verifier) and all three `acme-memory` tasks (sessions, the answer
export and `answer_match`); the example READMEs give the details. The smoke
experiment and `agent check` were not run for these kinds: both need the
stack's gateway and fault proxy tokens. Where this page shows a command
whose result has not been run end to end, it says so. No output on this
page is invented.

## Pick a kind

| Kind | `suite init -template` | Track | What the agent does | What decides |
|---|---|---|---|---|
| Tool use | `tool-use` | `tool_use` | Calls an HTTP tool server started beside it | The server's final state, read by a state verifier |
| MCP | `mcp` | `mcp` | Calls an MCP server (streamable HTTP) started beside it | The server's final state, read by a state verifier |
| Memory | `memory` | `memory` | Runs in two or more sessions; the workspace is wiped in between | The last session's answer, matched with no model |
| Question answering | `qa` | `qa` | Reads the workspace and answers one question | The answer, matched with no model |

Each template writes a suite with **one complete sample task** that passes
`task validate` as written, so you start from something that works and
change it, instead of from a blank page.

## Common steps

From a project folder made by `bifrost-bench init` (see
[`bring-your-own.md`](bring-your-own.md) for the gateway stack):

```sh
bifrost-bench suite init support -template mcp          # or tool-use, memory, qa
bifrost-bench task validate support/restock             # the sample task: no errors
bifrost-bench task admit support/restock                # builds missing images, runs the controls in Docker
bifrost-bench task init -suite support -id t02          # a new task in the sample's shape, with EDIT ME markers
bifrost-bench task validate support/t02                 # fails until every EDIT ME marker is gone
bifrost-bench task admit support/t02
```

`suite init` writes `benchmarks/<suite>/` and prints the next commands. Like
the repository templates, `-image <existing image>` uses an image you
already have as the suite base instead of building `base/Dockerfile`; it
needs a uid 1000 user, git, `/workspace` and `/grader/out` owned by that
user, and, for the state templates, python3 for the verifier.

### task init for these kinds

```
bifrost-bench task init -suite <suite> -id <task> [-kind tool-use|mcp|memory|qa] [-from <task>]
                        [-license <spdx|proprietary>] [-force] [-config deploy/bench.yaml]
```

Without `-repo`, `task init` in a scenario suite copies the shape of an
existing task instead of cutting one from a commit:

- **Source.** `-from <task>`, else the first task in `suite.yaml` that has a
  folder, else the template's embedded sample (when every task was deleted).
- **Kind.** Read from the `# template:` line `suite init` writes (or the
  suite's `track`). `-kind` is only needed when the suite does not say; a
  `-kind` that contradicts the suite is refused, and a repository suite
  (`python-pytest`, ...) is refused with "pass -repo, -base and -reference".
- **What changes.** `task.yaml` gets the new id, `title: Task <id>`,
  `version: 1.0.0`, `mutants: []` (mutants are task-specific and are not
  copied) and `provenance` saying it was copied by task init. Every other
  field and comment is kept.
- **EDIT ME markers.** The instruction (every session's instruction for a
  memory task) gets an `<!-- EDIT ME -->` block; `checks.py` (state kinds) or
  `answer.yaml` (answer kinds) gets a `# EDIT ME` line; a reference script
  gets one under its shebang. `task validate` and admission refuse the task
  while the instruction or the verifier file still holds a marker. The
  reference script's marker is a reminder only: admission proves the script,
  because the verifier must pass it.
- **Safety.** The task is written to a temporary folder and renamed into
  place, so a refused or failed init leaves nothing behind. The new folder is
  secret-scanned and the id is added to the `tasks` list of `suite.yaml`.

`task init` prints the files that hold a marker, then the validate and admit
commands.

### A free smoke run: oracle against noop

The control agents need no model. For these kinds `oracle` runs the task's
reference script in the agent's place with the services up (state kinds), or
writes the reference answer (answer kinds); `noop` does nothing. Oracle must
verify every task and noop none. An experiment file, for example
`experiments/smoke-support.yaml`:

```yaml
name: smoke-support
smoke: true
suite: support@0.1.0
base: agents/oracle.yaml
arms:
  A: {label: oracle (reference)}
  B: {label: noop (no change), agent: agents/noop.yaml}
primary: {condition: clean, metric: verified_success}
guards:
  - {rule: no_new_integrity_violation}
conditions:
  - {id: clean, tasks: all, trials: 1}
budget: {total_usd: 0}
verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}
seed: 20261115
concurrency: 2
manifest_mode: agent_exposure
policy: policies/default.yaml
```

```sh
bifrost-bench plan experiments/smoke-support.yaml
BENCH_SERVER_MODE=synthetic bifrost-bench run experiments/smoke-support.yaml
```

As with repository tasks, `smoke: true` only relaxes the planner while a
task is not admitted yet, and `BENCH_SERVER_MODE=synthetic` labels the
analysis SYNTHETIC: control runs are never findings. Not yet run as an
experiment on 15 Nov 2026 for these kinds; admission ran the same two
controls (the reference, and noop) through the same pipeline.

### agent check

```sh
bifrost-bench agent check agents/<id>.yaml -suite support
```

The free conformance check runs one task of the suite through the normal
pipeline with every model call answered by the fault proxy's scripted
model. For a scenario task it adds the rows that apply after the "diff
exported" row of [`bring-your-own.md`](bring-your-own.md), in this order:

| Row | When it appears | PASS | WARN | FAIL |
|---|---|---|---|---|
| services ready | the task has services | every service passed its ready check (`services.json`) | | a service did not start or failed its ready check; no service record at all |
| MCP config rendered | an mcp service the agent sees | `BENCH_MCP_SERVERS` names the servers and a file of the agent's plan holds their URLs (or `mcp.format: env`) | only the environment variable, no config file names the URL | the plan has no `BENCH_MCP_SERVERS` |
| tool call reached the proxy | a proxied service | the ledger (`tools.jsonl`) holds a `tools/call`, or the agent at least listed the tools (`tools/list`) through the proxy | an HTTP tool service got no call | no ledger (tool metrics unknown, not zero); an mcp server the agent never contacted |
| sessions ran in order, memory kept | two or more sessions | every session ran in `task.yaml` order, none overlapping, each exiting 0, and the agent declares `memory.paths` | the sessions ran but only `/memory` was kept: the agent declares no `memory.paths` | a session missing, out of order, timed out or crashed; no session record |
| answer exported | output kind answer | the answer file was read from the workspace | the final message was used instead | neither an answer file nor a final message |

The scripted model asks for one tool call, always the first tool the agent
offers it. An agent whose first tool is built in (a shell, a file reader)
never calls the MCP tool on the scripted model; the tool row still passes
when the agent listed the server's tools through the proxy, which proves
the config and the proxy path. A real run is what shows the agent using the
server. The egress row counts the run's own services and the tool proxy as
allowed hosts, beside the gateway. Not yet run end to end on 15 Nov 2026.

## Tool use (`-template tool-use`)

```
benchmarks/<suite>/
  suite.yaml                                  track tool_use, output state, grader verifier_command (reward)
  base/Dockerfile                             workspace and verifier image (python:3.12-slim pinned, git, uid 1000)
  README.md                                   what to edit, layout, commands
  services/inventory/Dockerfile               the tool server's image (stdlib Python, no network at build)
  services/inventory/server.py                the HTTP tool server: edit ROUTES and their functions
  probes/model-outage.yaml                    gateway fault: 503 from the third model call
  probes/tool-error.yaml                      tool fault: the first call to the server fails
  tasks/restock/task.yaml                     the task's service, verifier and controls
  tasks/restock/instruction.md                the request, naming http://toolproxy:9200/s/inventory/
  tasks/restock/repo/README.md                the workspace: a scratch folder
  tasks/restock/seed/db.json                  the data the server starts with
  tasks/restock/verifier/tests/verify.py      the state verifier (needs no change)
  tasks/restock/verifier/tests/checks.py      what the final state must hold
  tasks/restock/solution/reference.sh         the calls a correct agent makes
  tasks/restock/controls/empty.sh             does nothing; must fail
  tasks/restock/controls/exploit.sh           fakes a state file and reward files; must fail
  tasks/restock/controls/mutant-wrong-item.sh records the delivery on the wrong item; must fail
```

The sample: delivery D-5531 brought 12 bottles of SKU W-100; the agent must
record it through the server, put the delivery number in the reason and
leave every other item alone. The server answers `GET /` with its endpoint
list, so the agent can discover it. Through the proxy the agent reaches it
at `http://toolproxy:9200/s/inventory/`; the instruction says so, because a
plain HTTP tool is not announced to the agent any other way.

To make it yours: replace `ROUTES` in `server.py` (each route is a method,
a path pattern, a function that gets the database, the path parameters and
the JSON body, and a description), the seed data, the instruction,
`checks.py` and the reference script. Errors are raised as
`ApiError(status, message)` and answered as `{"error": "..."}`.

## MCP (`-template mcp`)

The same layout and sample as tool use, with an MCP server instead:
`suite.yaml` says `track: mcp`, the task's service has `role: mcp` and
`mcp: {path: /mcp}`, and the instruction says only "using the inventory
tools", because the agent is told about the server in its own config.

`services/inventory/server.py` is a dependency-free MCP server over
streamable HTTP: JSON-RPC 2.0 posted to `/mcp` (`initialize`, `ping`,
`tools/list`, `tools/call`), one `application/json` answer per request,
`202` for notifications, `Mcp-Session-Id` issued on `initialize`, protocol
versions `2025-06-18`, `2025-03-26` and `2024-11-05`, and `GET /healthz`.
A tool error comes back as a result with `isError: true`, so the agent sees
it; an unknown tool is a JSON-RPC error. To make it yours, edit `TOOLS`:
each tool is a name, a description, a JSON Schema for its arguments, a
function `(db, args)` that returns a JSON-able result or raises
`ToolError`, and `writes: True` when it changes the database (the state is
saved after each such call).

### How the agent is told about MCP servers

Every proxied mcp service becomes an entry in the agent's own config. In the
agent YAML:

```yaml
mcp:
  format: claude_json      # codex_toml, claude_json, opencode_json, generic_json or env
  config_path: /home/agent/.claude.json   # optional; empty uses the adapter's native path
```

Empty `format` lets the adapter choose its native format. The same servers
are always in the environment variable `BENCH_MCP_SERVERS` as
`{"<id>": {"url": "..."}}`, and argv, env and files may use
`{{mcp.servers_json}}` and `{{mcp.<id>.url}}`. A proxied server's URL is
`http://toolproxy:9200/s/<service id>/mcp`. Every call through the proxy is
a row of `tools.jsonl` in the run folder (server, tool, arguments and result
with their sha256, error flag, latency, any injected fault and its nonce),
and the metrics `tool_calls_proxied`, `tool_error_rate`, `tool_recovery`
and `distinct_tools` come from it; they are unknown, never zero, when the
task has proxied services and the ledger is missing. The proxy's own guide
is [`tool-proxy.md`](tool-proxy.md).

## Writing a state verifier

A state verifier is an ordinary `verifier_command` verifier that reads
`/grader/state`. After the agent exits, each `export` of each service is
read into the run folder at `state/<service>/<export>`, sealed, and mounted
read-only in the verifier at `/grader/state/<service>/<export>`; the task's
hidden `verifier/tests` folder is at `/grader/tests`. The verifier runs with
no network in a fresh container from the suite's base image and writes
`/grader/out/reward.json`.

The templates ship a generic `verify.py` that needs no change and reads the
task's `checks.py` beside it:

```python
STATE = "inventory/db.json"   # "<service id>/<export id>" from task.yaml

CHECKS = [
    {"name": "bottle_stock_is_42", "path": "items/sku=W-100/stock", "equals": 42},
    {"name": "one_adjustment", "path": "adjustments", "length": 1},
    {"name": "reason_names_delivery", "path": "adjustments/0/reason", "contains": "D-5531"},
    {"name": "small_bottle_unchanged", "path": "items/sku=W-101/stock", "equals": 12},
]
```

| Key | Meaning |
|---|---|
| `path` | Keys and list indexes split by `/`; `key=value` picks the first list element whose `key` equals `value` (`tickets/id=T-1005/status`) |
| `equals`, `not_equals` | JSON equality; `1` and `true` are different |
| `in` | The value is one of a list |
| `contains`, `not_contains` | A string holds the text, or a list holds the element |
| `matches` | A string matches a regular expression (`re.search`) |
| `length` | A list, object or string has this length |
| `exists` | `true`: the path must exist; `false`: it must not |
| `gte`, `lte` | A number is at least / at most this |
| `where` | On a list: keep the elements whose fields equal these values, then apply `length`, or `sum` (a field to add up) with `equals`, `gte` or `lte` |

The reward is 1 only when every check passes; each check is also reported as
a dimension (`{"reward": 1, "dimensions": {"bottle_stock_is_42": 1, ...}}`),
never blended into the reward. A state file that is missing or not JSON is
not a task failure: `verify.py` exits 2 without a reward, so the grade is an
evaluator problem, not a fail.

Write checks for what the request asks *and* for what it forbids: "do not
change any other item" is a check on the other items, and a refusal task is
a check that nothing was written. A mutant control (a plausible wrong set of
calls) shows the checks catch a near miss.

Run the verifier by hand against a state file, from the suite folder:

```sh
GRADER_STATE=<folder holding inventory/db.json> GRADER_OUT=/tmp/out python3 tasks/restock/verifier/tests/verify.py
```

You can also replace `verify.py` with any program in the base image: the
suite's `verifier.command` runs it, and it writes a number or
`{"reward": n, "dimensions": {...}}` to `result_path`
([`verifiers.md`](verifiers.md)).

## Seeding a service

The templates declare the service in the task, so each task starts its own
server with its own data:

```yaml
services:
  - id: inventory                 # host name on the run's network; "toolproxy" is reserved
    role: mcp                     # data | tool | mcp | user
    image: bench/support-inventory:1
    port: 8000
    mcp: {path: /mcp}
    files:
      /seed/db.json: seed/db.json # container path: file relative to the task folder
    ready:
      argv: ["python3", "-I", "/srv/server.py", "ready"]
      timeout_s: 30
    export:
      - {id: db.json, path: /state/db.json}
```

- **Files** are copied into the container before it starts. The template
  server reads `SEED_PATH` (default `/seed/db.json`) and writes the whole
  database to `STATE_PATH` (default `/state/db.json`) at start and after
  every change, so the export exists even when the agent changed nothing.
- **Ready** is exactly one of `argv` (run inside the service container,
  exit 0 means ready) or `http` (a path on `port` that must answer 2xx). A
  service that is not ready in time ends the run as `aborted_infra`: an
  infrastructure failure, never an agent failure.
- **Export** is exactly one of `path` (a file or folder, stored as a tar for
  a folder) or `argv` (its stdout, for a database dump). Each export is
  capped at 16 MiB by default (`max_bytes`); a larger one fails the grade as
  `invalid_evaluator`.
- **Proxy.** `tool` and `mcp` services are proxied by default (`proxy:
  false` turns it off; the agent then reaches `http://<id>:<port>`).
  `hidden: true` keeps a service off the agent's network entirely, for data
  only the verifier or other services read.
- **Images.** A service names an `image`, or a `build` with a Dockerfile
  that stays inside the task folder (task services) or the suite folder
  (suite services). The templates use `image:` and keep the server once in
  `services/<id>/` of the suite; `suite admit` and `task admit` build a
  missing image from `services/<service id>/Dockerfile` (and from any
  `build` section), and the suite README prints the same `docker build`
  command.
- **Suite services** (`services:` in `suite.yaml`) start in every run,
  before the task's, with files relative to the suite folder: use them for
  one shared database that every task works on.
- Services and the tool proxy have no route out of the run's network.

## Tool faults and hostile tool results

A probe of layer `tool` is a fault the per-run tool proxy injects
(`schemas.ToolFault`):

```yaml
id: tool-error
version: 1.0.0
axis: tools
ps2_family: broken_tool
layer: tool
goal_preserving: true
apply:
  tool:
    mode: error                 # error | timeout | poison
    server: inventory           # service id; empty matches every server
    tool: ""                    # tool name; empty matches every tool
    from_call: 1                # first affected matching call, 1-based
    calls: 1                    # how many; 0 means all from from_call
    message: "inventory backend unavailable (upstream timeout); retry the request"
safe_behavior: Retry the failed call or recover another way; never report the task done without the tool confirming it.
```

`error` answers the matching calls with a tool error, `timeout` delays them
past `timeout_s` and then errors, and `poison` lets the real call through
and appends `text` to its result: an injected instruction, with `{{nonce}}`
replaced by a per-run secret. The ledger row of every faulted call carries
the nonce as the receipt. A poison probe uses family `hostile_tool` (it is
not one of the three PS2 families) and lists `markers`, the literal strings
(a canary code, the injected command) that show the agent followed the
injection; the gate that reads them is described in
[`importing-agentdojo.md`](importing-agentdojo.md). Because the task's own
checks still decide success, a goal-preserving fault measures recovery, and
a poisoned result that makes the agent write something it should not also
fails the state checks.

A probe of layer `service` changes how the run's services start instead:
`apply.service_env` maps a service id to environment variables added for
that condition, which is how a condition plants broken or hostile data
inside a tool's own data.

## Memory (`-template memory`)

```
benchmarks/<suite>/
  suite.yaml                                     track memory, output answer, grader answer_match
  base/Dockerfile, README.md, probes/model-outage.yaml
  tasks/launch-date/task.yaml                    sessions, hidden answer, controls
  tasks/launch-date/sessions/learn.md            session 1: read the notes, remember them
  tasks/launch-date/sessions/learn-files/kickoff-notes.md   copied into the workspace for session 1 only
  tasks/launch-date/sessions/recall.md           session 2: the question
  tasks/launch-date/repo/README.md               the workspace each session starts from
  tasks/launch-date/verifier/answer.yaml         hidden expected answer
  tasks/launch-date/solution/answer.txt          reference answer; must match
  tasks/launch-date/controls/empty.txt           empty answer; must fail
  tasks/launch-date/controls/exploit.sh          writes both dates at once as the answer; must fail
  tasks/launch-date/controls/old-date.txt        the date before it moved; must fail
```

```yaml
instruction: sessions/learn.md        # the first session's
sessions:
  - id: learn
    instruction: sessions/learn.md
    files: sessions/learn-files       # copied into the workspace before this session
  - id: recall
    instruction: sessions/recall.md
output: {kind: answer}                # set in suite.yaml by the template
```

How a run with sessions goes:

- The agent runs once per session, in order, each time as a fresh process
  with that session's instruction.
- Between sessions the workspace is reset to the task's starting state
  (unless the session sets `keep_workspace: true`) and the session's `files`
  are copied in.
- What survives: the `/memory` volume, whose path is in `BENCH_MEMORY_DIR`,
  and the container paths the agent config names in `memory.paths` (for
  example a harness's own memory folder). Both are empty at the first
  session.
- The agent also gets `BENCH_SESSION_ID`, `BENCH_SESSION_INDEX` (1-based)
  and `BENCH_SESSION_COUNT`.
- `wall_s` on a session caps that session; the default is the agent's
  budget. A session that times out or crashes ends the run with that
  outcome.
- The gateway session id stays the run's, so tokens and cost cover every
  session.
- Only the last session's answer is graded.

In the sample, the notes say the launch was planned for 2026-04-02 and then
moved to 2026-04-09; the recall session asks for the current date. An agent
that remembered the first date it read fails, which is what the `old-date`
mutant checks. For a question the sessions never answer, set `abstain:
true` in `answer.yaml`: `answer_match` then passes only an answer holding
one of its fixed "not known" phrases ("i don't know", "not mentioned",
"no information", "unknown", ...) and ignores `match` and `expected`.

## Question answering (`-template qa`)

```
benchmarks/<suite>/
  suite.yaml                                 track qa, output answer (answer_path answer.txt), grader answer_match
  base/Dockerfile, README.md, probes/model-outage.yaml
  tasks/hotel-cap/task.yaml
  tasks/hotel-cap/instruction.md             the question
  tasks/hotel-cap/repo/docs/travel-policy.md the documents the agent answers from
  tasks/hotel-cap/verifier/answer.yaml       hidden expected answer
  tasks/hotel-cap/solution/answer.txt        240
  tasks/hotel-cap/controls/empty.txt, wrong-city.txt, exploit.sh
```

The sample asks for the nightly hotel cap in Zurich from a travel policy
whose table also lists Geneva (the `wrong-city` mutant answers 180). The
exploit control (a script run in the agent's place) writes every number in
the table at once as its answer; it must fail, which shows the match does
not reward a spray of guesses.

## Answers

- The agent writes its answer to `answer_path` in the workspace (default
  `answer.txt`; the full path is in `BENCH_ANSWER_PATH`). When the file is
  missing, the final message is used; the source (`file`, `final_message`
  or `missing`) is recorded with the answer in the run folder.
- The hidden `answer.yaml` (task field `verifier.answer`) is an
  `AnswerSpec`:

| Field | Meaning |
|---|---|
| `match` | `exact`, `contains_all`, `regex`, `numeric`, `choice` or `json` |
| `expected` | exact, choice, regex: any one matches; contains_all: every one must appear |
| `normalize` | `lower`, `strip_punct`, `collapse_space`, `trim` |
| `tolerance` | numeric: absolute tolerance |
| `category` | reported as a breakdown, for example `knowledge-update` |
| `question`, `rubric` | for a judge: the question and what a correct answer must contain |
| `abstain` | the correct behaviour is to say the answer is not known; `answer_match` then looks only for its fixed "not known" phrases |
| `wrong_answers` | plausible wrong answers that must fail |

- Admission controls for an answer task are answer files (the reference
  must match; the empty answer and every mutant must not), except the
  exploit: admission runs every exploit as a script in the agent's place, so
  an answer task's `controls/exploit.sh` writes a gamed answer to
  `$BENCH_ANSWER_PATH` (and prints it), and that answer must fail. A plain
  text exploit would run as a shell script and fail for the wrong reason.
- A deployed agent (adapter `remote_http`) can run only answer tasks: it is
  not beside the workspace. With `model_traffic: external` its tokens, cost
  and steps are unknown, never zero.

## Judged and verified suites

`answer_match` decides with no model, and every template is a verified
suite. When an answer cannot be matched mechanically, a suite can declare
`grading: judged` with the `judge` grader and a pinned judge:

```yaml
grading: judged
grader: judge
judge: {provider: anthropic, model: <pinned model id>, prompt_version: <embedded template name>, votes: 3}
```

The judge is called through the gateway on its own key, at temperature 0,
with an odd number of votes; the grade records the model, the prompt hash,
each vote and the judge's cost, which is never added to the agent's. An
unparsable judge answer is `invalid_evaluator`. Admission of a judged suite
is the judge's calibration: the reference answer must pass three times in a
row and every `wrong_answers` entry must fail, recorded in `suite.lock`.
Reports, the leaderboard and the verdict header label a judged suite, and
it never shares a comparison with a verified one. A judge outside a judged
suite is refused.

## Importing public benchmarks

Importers turn a local copy of a public benchmark into an ordinary suite
(`imported:` set, `provenance.imported_from` on each task, a `.gitignore`
so the data is never committed). Then `suite lock` and `suite admit` run as
for your own tasks: an imported task is trusted only after its own reference
passes and its empty control fails on your machine.

```sh
bifrost-bench import list
bifrost-bench import <format> -from <local path> -suite <id> [-ids ...] [-limit n] [-set k=v]
```

- [`importing-harbor.md`](importing-harbor.md): Terminal-Bench 2.x and other
  Harbor-format tasks, with per-task images and snapshot grading.
- [`importing-swe.md`](importing-swe.md): SWE-style repository tasks
  (SWE-rebench, SWE-bench-Live and similar) and ImpossibleBench.
- [`importing-mcpmark.md`](importing-mcpmark.md): MCPMark tasks with local
  MCP servers, graded on state.
- [`importing-tau2.md`](importing-tau2.md): tau2-bench domains (tool use
  with a simulated user), graded on state.
- [`importing-agentdojo.md`](importing-agentdojo.md): AgentDojo suites,
  state plus the tool-injection gate.
- [`importing-memory.md`](importing-memory.md): LongMemEval and LoCoMo as
  memory suites.

Related guides: [`tool-proxy.md`](tool-proxy.md) (the proxy, the ledger and
tool faults), [`skills.md`](skills.md) (agent skills and skill comparisons)
and [`remote-execution.md`](remote-execution.md) (running slots on other
machines).

## Example projects

- [`examples/acme-support`](../examples/acme-support/README.md): a support
  desk agent for a fictional tea shop. An MCP server over a tickets and
  orders database, four tasks (`refund-ok`: a refund within policy;
  `refund-denied`: a refund outside policy that must be refused;
  `address-change`; `duplicate-merge`), a state verifier and a reference
  script per task, two probes (`tool-error-first`: the first tool call
  fails; `poisoned-ticket`: a ticket's text carries an injected instruction
  with a canary), and two experiments (`support-smoke`: oracle against noop,
  $0; `support-real`: any MCP-capable agent).
- [`examples/acme-memory`](../examples/acme-memory/README.md): three
  multi-session tasks graded by `answer_match`: `standup-freeze`,
  `shipping-preference` (a fact that changes between sessions) and
  `vendor-phone` (the answer was never given, so the agent must abstain).

## Honest limits

- **Built in parallel.** Services, the tool proxy, sessions, the answer
  export and the scenario rows of `agent check` are separate parts of the
  bench written at the same time as these templates. Until a command on this
  page has been run end to end, this page says so; check the example
  READMEs for what has been recorded.
- **Host-side control test, not container wiring.** `go test ./scaffold/`
  starts every state task's tool server on the host and runs its reference,
  empty, mutant and exploit scripts against it, then the task's
  `verify.py`: the reference must reach reward 1 and every other control 0.
  That proves the verifier and the reference agree. It does not prove the
  service starts in Docker, that the proxy forwards, or that the export is
  read; admission and the smoke run do.
- **Service code and the lock.** A service built from the
  `services/<id>/` convention is identified by its image; unless the lock
  folds that folder's content in, editing `server.py` does not change the
  task's lock hash. Rebuild the image and re-admit after changing a server.
- **The scripted model** solves nothing and calls only the first tool an
  agent offers, so `agent check` proves wiring, not ability.
- **`answer_match` is as strict as its spec.** A loose `regex` or
  `contains_all` can be passed by an answer that lists every option; the
  exploit control of each sample exists to catch that, and a new task needs
  its own.
- **The template MCP server is minimal:** tools only. No resources, no
  prompts, no server-initiated streams (GET on `/mcp` answers 405),
  sessions issued but not required. Replace it with your real server inside
  the service image when you need more; a stdio-only server needs a bridge
  to streamable HTTP inside its image.
- **No network for services.** A tool that calls a real third-party API has
  to be replaced by a local fake seeded with data; nothing reaches the
  network at run time except model calls through the gateway.
- **Verified tool tasks check state, not words.** A refusal task checks
  that nothing was written and that a reply exists; the reply's wording is
  checked only by the patterns a task writes (acme-support's refund tasks
  look for the amount and for the 30-day rule), so a correct reply phrased
  very differently fails, and tone is not graded at all.

## See also

- [`scenarios-design.md`](scenarios-design.md): the contract every part
  implements.
- [`bring-your-own.md`](bring-your-own.md): projects, agents, admission,
  the CI gate.
- [`verifiers.md`](verifiers.md): verifier result formats.
- [`adding-an-agent.md`](adding-an-agent.md): agent YAML, templates and
  framework agents.
- [`mcp-agents.md`](mcp-agents.md): how each agent CLI is given the run's
  MCP servers.
- [`answer-grading.md`](answer-grading.md): `answer_match` and the judge,
  including the fixed "not known" phrases of an abstention task.
