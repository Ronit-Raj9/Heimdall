# Bring your own agent and tasks

This is the page a startup or enterprise team follows to benchmark its own
agent on tasks cut from its own repositories, with no Go code and nothing
added to this repository. You keep a small project folder (in your own
repository if you like), describe your agent in one YAML file, turn commits
that fixed something into tasks with hidden tests, and get the same
experiments, verdicts, leaderboard and CI gate the built-in suite gets.

Every command and every output below comes from one walkthrough on
15 Nov 2026 in a fresh project, against a running stack (Bifrost v2.5.1,
Docker 29.9.0), with two throwaway repositories: a Python one and a Node one.
Output is trimmed (long paths shortened to `...`), never edited. The design
contract these commands implement is
[`bring-your-own-design.md`](bring-your-own-design.md); a complete example
project with four Node tasks and a custom agent is
[`examples/acme-node`](../examples/acme-node/README.md).

## What you need

| Need | Why |
|---|---|
| The `bifrost-bench` binary | One static binary runs every command. From `bench/`: `CGO_ENABLED=0 GOWORK=off go build -o <dir>/bifrost-bench ./cmd/bifrost-bench` (Go 1.26, only to build it) |
| Docker | Workspaces, agents, graders and the gateway stack all run in containers. The host never runs agent-written code |
| git | `task init` reads your repository with plumbing commands (it never checks anything out or writes to it) |
| A model key, in the fault proxy only | Only for real runs. It goes in `deploy/.env`, read by the fault proxy behind Bifrost. Agent configs never hold it: each run gets a virtual key for one provider and one model. Everything on this page except a real experiment is free and needs no key |

## One stack per machine

`bifrost-bench init` writes a gateway stack (Bifrost, Postgres, the fault
proxy) into `deploy/docker-compose.yml`, and its README explains how to fill
`deploy/.env`, build two images and start it (steps 1 to 3). The compose
project is always named `bifrost-bench` and binds fixed loopback ports, so
one stack serves every project on a machine. If a stack already runs, skip
those steps: the defaults in a new `deploy/bench.yaml` already point at it
(gateway `127.0.0.1:18080`, fault proxy `127.0.0.1:19101`, network
`bench-evalnet`, helper image `bench/coding-core-base:1`), and you load the
running stack's `.env` instead of writing a new one. The walkthrough did
exactly that and never ran compose from the new project:

```sh
set -a; . <the running stack>/deploy/.env >/dev/null 2>&1; set +a
```

Run one project's experiments at a time: the gateway's key slots are leased
inside one `bifrost-bench` process, so two processes running at once can take
the same slot.

## The 15-minute path

On the walkthrough machine, with the base images already pulled, every
command below took about two minutes of machine time in all. Pulling
`python:3.12-slim` and `node:22-slim` the first time, reading the generated
tests and writing two instructions take the rest of the quarter hour.

| Step | Commands | Machine time |
|---|---|---|
| 1. Project | `init` | under 1 s |
| 2. Suites | `suite init` (one per language) | under 1 s |
| 3. Tasks | `task init`, edit `instruction.md`, `task validate` | under 1 s each |
| 4. Admission | `task admit` | 10 s per task |
| 5. Agent | `agent init`, `agent build`, `agent check` | 3 s build, 11 to 14 s check |
| 6. Free experiment | `plan`, `run` (oracle against noop) | 23 s per suite |
| 7. Results | `leaderboard`, `serve -offline` | under 1 s |

### 1. Make the project

```sh
bifrost-bench init acme-bench
cd acme-bench
```

```
wrote 21 files to .../walk2/acme-bench

next, from .../walk2/acme-bench:
  1. cp deploy/.env.example deploy/.env    # fill in three random tokens
  2. cp "$(command -v bifrost-bench)" deploy/bifrost-bench && docker build -t bench/bifrost-bench:dev deploy && docker build -t bench/coding-core-base:1 deploy/helper
  3. docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --wait
  ...
```

Every command takes `-config deploy/bench.yaml`, the default when you run
from the project folder; the project root is that file's grandparent.

### 2. Make a suite per language

```sh
bifrost-bench suite init pyapp -template python-pytest
bifrost-bench suite init webapp -template node-test
```

Each writes `benchmarks/<suite>/suite.yaml`, `base/Dockerfile` (the suite's
toolchain image), a README and the `model-outage` probe. Templates:
`python-pytest`, `node-test`, `go-test`, `generic-reward`; what each verifier
runs is in [`tasks-from-your-repo.md`](tasks-from-your-repo.md) and
[`verifiers.md`](verifiers.md). For an agent that does not edit a repository
(one that calls tools or MCP servers, remembers across sessions or answers
questions) the templates `tool-use`, `mcp`, `memory` and `qa` are described
in [`scenarios.md`](scenarios.md). The walkthrough changed `base_image.tag` in
both `suite.yaml` files to `heimdall-test/pyapp-base:1` and
`heimdall-test/webapp-base:1`, so its images were easy to find and remove;
use your own registry name.

### 3. Cut a task from a commit

You need a commit that fixes something and adds or changes tests, and the
commit before it. The walkthrough's Python repository has a base commit
(`invoice totals`, with one visible test file) and a fix commit (`Round
invoice discounts half up`, which adds `tests/test_discount.py`):

```sh
bifrost-bench task init -suite pyapp -id t01 -repo ../src/pyrepo -base ab0b343 -reference a69850b -license proprietary
bifrost-bench task init -suite webapp -id t01 -repo ../src/noderepo -base d4909d4 -reference 645628d -license proprietary
```

```
wrote .../acme-bench/benchmarks/pyapp/tasks/t01 (template python-pytest)
  hidden tests (verifier/tests): tests/test_discount.py
  visible tests listed under protected: 1
  required test ids: 2
  added t01 to the tasks list of suite.yaml
```

The agent starts from the base tree (`repo/`); the fix becomes
`solution/reference.diff`; the tests the fix added become the hidden
verifier (`verifier/tests/`); visible tests that existed at base are listed
under `protected`. Node tasks start with `required: []`, because Node test
names cannot be read without running them.

### 4. Write the instruction, review the tests, validate

`instruction.md` starts as a draft from the commit message under an
`<!-- EDIT ME -->` line, and validation fails until a person rewrites it:

```sh
bifrost-bench task validate pyapp/t01
```

```
error   tasks/t01/instruction.md: line 1: still holds the <!-- EDIT ME --> marker; rewrite the draft and delete the marker
warning tasks/t01/task.yaml: controls.mutants: empty; a mutant that fails the hidden tests shows they catch a near miss
warning tasks/t01/verifier/requirements.md: missing; a map from every hidden test to the instruction sentence it checks keeps the tests honest
warning tasks/t01/verifier/tests: these tests come from reference commit a69850b9feee; review them: tests written beside the fix can encode the implementation too closely
pyapp/t01: 1 error, 3 warnings
```

The walkthrough's rewritten instruction:

```markdown
# Invoice discounts lose a cent

`invoice_total` truncates the percentage discount. A 10% discount on a
10.05 invoice takes off 1.00 instead of 1.01. Round the discount half up to
the nearest cent; totals without a discount must not change.
```

Then read `verifier/tests/`: tests written beside a fix can assert the fix's
shape (a helper's name, an exact message) rather than the behaviour, and
would fail a correct fix written differently. After the edit validation
passes with the three warnings (`pyapp/t01: 0 errors, 3 warnings`, exit 0).
Mutants and `verifier/requirements.md` are optional and worth adding.

### 5. Admit the task

```sh
bifrost-bench task admit pyapp/t01
bifrost-bench task admit webapp/t01
```

```
t01  admitted
     ok   reference_1                              want pass   got pass
     ok   reference_2                              want pass   got pass
     ok   reference_3                              want pass   got pass
     ok   empty_patch                              want fail   got fail
     ok   exploit                                  want fail   got fail
     ok   null_noop                                want fail   got fail
     ok   null_refuse                              want fail   got fail
     ok   instruction_edited                       want edited got edited
     ok   secret_scan                              want none   got none
     ok   verifier_no_network                      want none   got none
wrote .../acme-bench/benchmarks/webapp/suite.lock
```

Admission builds the suite's base image when it is missing, grades every
control in Docker with no network and records the result in `suite.lock`. A
task that is not admitted cannot be planned. What each check proves, and
what it does not, is in [`tasks-from-your-repo.md`](tasks-from-your-repo.md).

### 6. Bring your agent

```sh
bifrost-bench agent init pybot -template python -tag heimdall-test/agent-pybot:1
bifrost-bench agent init svcbot -template http-service -tag heimdall-test/agent-svcbot:1
bifrost-bench agent build agents/pybot.yaml -suite pyapp
bifrost-bench agent check agents/pybot.yaml -suite pyapp
```

`agent init` writes `agents/<id>.yaml` and `agents/images/<id>/` from a
template: `command` (wrap any CLI), `python` (a one-file agent to start
from) or `http-service` (an agent that is a server: LangGraph, FastAPI, an
Agents SDK app). `-tag` is optional (default `bench/agent-<id>:1`). Every
template builds one image per suite on that suite's base image
(`build.base_arg`), tagged `<tag>-<suite>`; all three run on python3, so
build them for a suite whose base image has it. `agent build` prints the
tag and the local image id, and an unchanged rebuild keeps the same id.

`agent check` is the free conformance test: one real task through the
normal pipeline, with every model call answered by the fault proxy's
scripted model, sealed under `checks/` (never `runs/`):

```
agent check pybot on pyapp/t01 (scripted model, nothing spent)
image heimdall-test/agent-pybot:1-pyapp 8adee0587198
PASS  image builds                                 built heimdall-test/agent-pybot:1-pyapp (8adee0587198)
PASS  starts as the unprivileged user              ran as uid 1000 (image USER 1000), no capabilities, read-only root
PASS  model call reached Bifrost with the run key  2 model calls through Bifrost with the run key, answered by the scripted model
PASS  session id sent                              x-bf-session-id: r_cill4ypqbc on every gateway request seen (POST /openai/v1/chat/completions)
PASS  exited by itself inside the budget           exited 0 after 300ms (budget 900s)
PASS  diff exported                                exported, 0 files changed (expected: the scripted model asks for no edit)
PASS  no egress outside the gateway                only the gateway bifrost was contacted
PASS  evidence level                               gateway only (no parser): success, steps, tokens, cost and time; add a parser for tool targets
PASS  completion claim captured                    "Scripted reply from the bench fault proxy.", no success claim, from outcome
result: PASS
```

The `http-service` agent passed the same nine rows. Replace the template's
loop or server with your agent and keep its env contract; the rules (what
an agent must and must not do, the YAML fields, framework base URLs) are in
[`adding-an-agent.md`](adding-an-agent.md).

`doctor` now checks the whole project: the helper image and the tools the
bench runs in it, every suite's base image and lock, and every image named
by `agents/*.yaml`, per suite for per-suite agents:

```
PASS  docker                 server 29.9.0
PASS  image bench/coding-core-base:1 41c1672b2c99
PASS  helper tools           sh, cp, git, python3, sleep present
PASS  image heimdall-test/pyapp-base:1 c979b703d412
PASS  image heimdall-test/webapp-base:1 dafe4a125f10
PASS  image heimdall-test/agent-pybot:1-pyapp 8adee0587198
WARN  image heimdall-test/agent-pybot:1-webapp missing; bifrost-bench agent build agents/pybot.yaml -suite webapp
...
PASS  eval network           bench-evalnet is internal
PASS  suite pyapp            1 of 1 tasks admitted
PASS  suite webapp           1 of 1 tasks admitted
PASS  gateway                bifrost v2.5.1, egress blocked
PASS  fault proxy            http://127.0.0.1:19101
```

A WARN for an agent image you never built for a suite is expected when you
will not run that agent there (a Python agent in a Node suite).

### 7. Run a free experiment

The control agents make no model calls: `oracle` applies each task's
reference patch, `noop` changes nothing. Comparing them proves the pipeline
and your verifier end to end for $0. The walkthrough's
`experiments/controls-webapp.yaml`:

```yaml
name: controls-webapp
suite: webapp
base: agents/oracle.yaml
arms:
  A: {label: oracle (reference patch)}
  B: {label: noop (no change), agent: agents/noop.yaml}
primary: {condition: clean, metric: verified_success}
guards:
  - {rule: no_new_integrity_violation}
conditions:
  - {id: clean, tasks: all, trials: 2}
budget: {total_usd: 0}
verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}
seed: 20261115
concurrency: 2
manifest_mode: agent_exposure
policy: policies/default.yaml
```

```sh
bifrost-bench plan experiments/controls-webapp.yaml
BENCH_SERVER_MODE=synthetic bifrost-bench run experiments/controls-webapp.yaml
```

```
experiment e_controls_webapp_wtym  manifest cf7722d8834f
  clean              4 slots  arm A 2, arm B 2
  counted 4, compatibility 0, total 4 agent episodes
01:08:13 stage          s_v3qyy6d7kc prepare r_4hwrj01qve started
...
verdict: inconclusive, action: no_decision (analysis a_wd69atm5)
policy enterprise-default (e47dcf5ea03a): warn, 5 checks: 2 pass, 0 fail, 3 unknown
```

Oracle passed 2 of 2 runs, noop 0 of 2. The verdict is inconclusive on
purpose: the test decides on tasks, and one task can never reach
significance (the report says how many tasks a decision needs).
`BENCH_SERVER_MODE=synthetic` labels the analysis SYNTHETIC, because control
runs are never findings. The smoke experiment `init` writes uses the
scripted `toy` agent; set its `suite:` to one of your suites before running
it.

For a real comparison, copy this file, point the arms at your agent configs
(two versions of one agent, or your agent against `agents/codex.yaml`), add
probe conditions, set `budget.total_usd`, and put a provider key in the
fault proxy. That run spends money; nothing above did.

## The project folder

```
acme-bench/
  deploy/bench.yaml               paths, gateway URL, networks, helper image
  deploy/docker-compose.yml       Bifrost + Postgres + fault proxy (one stack per machine)
  deploy/.env.example             copy to deploy/.env (git-ignored): tokens, provider key
  deploy/pricing-snapshot.json    pinned prices every cost row is priced from
  deploy/Dockerfile, deploy/helper/Dockerfile   the bench image and the trusted helper image
  agents/<id>.yaml                one file per agent; oracle, noop, refuse, exploit, toy ship
  agents/images/<id>/             Dockerfile and code of your agent's image
  benchmarks/<suite>/suite.yaml   toolchain image, verifier command, task list, probes
  benchmarks/<suite>/suite.lock   admission record, written by task admit and suite admit
  benchmarks/<suite>/base/Dockerfile        the suite's toolchain image
  benchmarks/<suite>/tasks/<task>/          task.yaml, instruction.md, repo/, verifier/, solution/, controls/
  experiments/<name>.yaml         what to compare
  policies/default.yaml           release policy the CI gate checks
  runs/                           sealed experiment runs (git-ignored)
  checks/                         sealed agent check runs, kept apart from runs/ (git-ignored)
```

A missing `deploy/pricing-snapshot.json` falls back to the snapshot built
into the binary, with a WARN line naming it; a snapshot that exists but does
not parse is an error.

## Reading the results

- **Verdict.** `run` and `check` print one verdict (`improved`, `regressed`,
  `inconclusive`, `invalid`) and one action (`ship`, `hold`, `no_decision`,
  `fix_harness`), then the release policy's decision. `runs/<exp>/analysis/
  <id>/report.md` holds the full report: the naive scoreboard beside the
  paired test, guards, invalid reasons, instrument checks, diagnostics per
  condition and the provenance (manifest hash, mode).
- **Leaderboard.** `bifrost-bench leaderboard` ranks agent configs over every
  sealed run of one suite and condition, across experiments, by verified
  success with cost and tokens beside it and tiers from the exact sign-flip
  test. Scripted runs are kept off the live board; in a project that so far
  has only control runs it says so on stderr:

  ```
  no live runs to rank; scripted runs (control agents or the scripted model) exist for webapp@0.1.0, pyapp@0.1.0: add -synthetic only to see them
  ```

  ```sh
  bifrost-bench leaderboard -synthetic only -suite webapp@0.1.0
  ```

  ```
  | Rank | Tier | Agent | Label | Model | Config | Success [95% interval] | Passes | Reliability | Tasks | Runs | Cost per success | p vs leader | Tier reason |
  | 1 | 1 | oracle | oracle (reference patch) | claude-sonnet-4-5 | `98692e5be029` | 100.0% | 2/2 | 100.0% (pass^2) | 1 | 2 | unknown | - | leader |
  | 2 | 1 | noop | noop (no change) | claude-sonnet-4-5 | `d1ed5dfae69e` | 0.0% | 0/2 | 0.0% (pass^2) | 1 | 2 | n/a | 1.00 | too few common tasks (1) to separate |
  ```

  With more than one suite, name it with `-suite <id@version>`; the
  command lists the suites with runs when it picked one itself.
- **Web UI.** `serve` shows the same data. The walkthrough served the
  project's recorded runs on a spare port (the stack's own server held 8090):

  ```sh
  BENCH_SERVER_LISTEN=127.0.0.1:18095 bifrost-bench serve -offline
  curl -s http://127.0.0.1:18095/api/bench/suites
  ```

  The **Suites** page (`/suites`) lists every suite with its grader, lock
  state and admitted tasks (`pyapp 0.1.0 hidden_tests locked 1/1 admitted`,
  `webapp 0.1.0 verifier_command locked 1/1 admitted`); a task page
  (`/suites/<suite>/tasks/<task>`) shows provenance, the admission checks,
  the instruction and the visible files. The **Agents** page (`/agents`)
  lists every config with its image, whether it builds per suite, and its
  evidence level. `/verdict`, `/runs`, `/leaderboard` and `/triage` work as
  for the built-in suite.

## The CI gate on your own recorded run

The gate re-derives the verdict from sealed runs, so CI needs neither Docker
nor a key. Commit the sealed experiment folder, then in CI copy it to a
temporary runs root (so the check's own analysis never changes the committed
copy) and run `check -exp`. The walkthrough, with the gateway tokens unset:

```sh
cp -r runs/e_controls_webapp_wtym ci-bundle/
T=$(mktemp -d) && cp -r ci-bundle/. $T/
BENCH_RUNS_ROOT=$T BENCH_SERVER_MODE=synthetic bifrost-bench check -exp e_controls_webapp_wtym -fail-on regressed,invalid -policy policies/default.yaml
```

```
verdict: inconclusive, action: no_decision
policy enterprise-default (e47dcf5ea03a): warn, 5 checks: 2 pass, 0 fail, 3 unknown
```

Exit codes: 0 pass, 1 regressed, 2 inconclusive, 3 invalid (each only when
listed in `-fail-on`), 4 infrastructure, 5 release policy block. Use
`BENCH_SERVER_MODE=recorded` for a real run. The bench's own workflow and
composite action are described in [`ci.md`](ci.md). Two limits:
`check -exp` re-analyzes but does not re-hash the sealed files, and seals
are local hashes without an external anchor, so a recorded gate catches
accidents, not a determined forger. Running the experiment on a
self-hosted runner (`check experiments/<file>.yaml`) is the trustworthy
path.

## What never leaves your machine

Your repositories, tasks, hidden tests, reference solutions, agent code,
images, run folders, diffs, transcripts and reports stay on the host and in
local Docker volumes. Nothing is uploaded and there is no hosted service or
sign-on. The only outbound traffic is model calls: agent to Bifrost to the
fault proxy to the provider, with your key read by the fault proxy alone. A
free run (control agents, `agent check`, the scripted model) sends nothing
out at all. `task init` reads your repository and never writes to it.
Building images pulls base images from their registries, as any
`docker build` does.

## Security notes

- **Hidden tests are never served.** The UI and API never return a hidden
  test file, a reference solution or an admission control: the task page
  refuses the `verifier/`, `solution/` and `controls/` folders whatever
  `task.yaml` says, and admission check details are cut to their counts
  (`tests_ran=2 failures=0 ...`). The agents page serves no env value and
  no delivered file content. The walkthrough spot-checked that the task
  API's answer for each task holds no line of its hidden tests.
- **Secret scan.** `task init`, `task validate` and admission scan every task
  file for private keys, `sk-` keys, AWS access key ids, GitHub and Slack
  tokens and high-entropy `*_TOKEN=` or `*_KEY=` values, and never print the
  value. Replace real credentials in a task with fakes before cutting it.
- **Sandbox.** Agents run as uid 1000 with all capabilities dropped,
  `no-new-privileges`, a read-only root filesystem, CPU, memory and process
  limits, and only the internal evaluation network, where Bifrost is the
  only reachable host. The verifier runs with no network on a pristine copy
  of the repo with the agent's diff applied. This is Docker isolation: no
  custom seccomp profile, no disk quota, not a hard boundary against a
  determined attacker. Do not benchmark an agent you would not run on the
  host's Docker.
- **The run key** reaches the agent and, for harnesses that pass their whole
  environment on, the model's shell. It allows one provider and one model,
  for one run, on a network where only Bifrost answers.

## Troubleshooting

Every row is an error or warning hit in the walkthrough (or while writing
the acme example), with the fix.

| You see | Cause | Fix |
|---|---|---|
| `error: .../acme-bench is not empty; pass -force to write the project files into it anyway` | `init` into a folder that has files | Pick a new folder. `-force` overwrites the project files `init` writes |
| `error   tasks/t01/instruction.md: line 1: still holds the <!-- EDIT ME --> marker` | The instruction is still the draft | Rewrite it as a teammate's request and delete the marker and the comment |
| `this agent needs python3 and the suite base image heimdall-test/webapp-base:1 has none` (from `agent build`) | A template agent built for a Node suite | Build it for a suite whose base image has python3, or install python3 in its Dockerfile. Before this check the build failed with a bare `exit code: 127` |
| `suite "my-suite": folder .../benchmarks/my-suite not found (suites here: pyapp, webapp)` | The smoke experiment still names the placeholder suite | Set `suite:` in `experiments/smoke.yaml` |
| `FAIL  helper tools  helper image heimdall-test/webapp-base:1 has no python3; set images.helper to the bench helper image` (doctor), and `isolation not proven: egress probe is "not_run", not "blocked": ...` (agent check) | `images.helper` set to a toolchain image. The helper runs the egress probe, the network observer and the `hidden_tests` grader with python3. Before this fix the check said `egress probe is "open"` and "network observer did not start" | Set `images.helper` back to `bench/coding-core-base:1` (built from `deploy/helper/Dockerfile`); the suite's own image goes in `base_image.tag` |
| `FAIL  no egress outside the gateway  tried to reach 1 host other than the gateway: github.com` (Codex) | Codex CLI 0.169.0 looks up github.com at start for its plugins feature | Add `plugins = false` under `[features]` in the Codex config (see [`harness-matrix.md`](harness-matrix.md)) |
| `WARN  image heimdall-test/agent-pybot:1-webapp missing; bifrost-bench agent build agents/pybot.yaml -suite webapp` (doctor) | A per-suite agent not built for one suite | Build it if you will run it there; otherwise ignore |
| `no live runs to rank; scripted runs ... add -synthetic only to see them` (leaderboard) | Only control or scripted runs so far | `leaderboard -synthetic only`, or run a real experiment |
| `WARN  price snapshot not found; using the embedded default ...` | `deploy/pricing-snapshot.json` missing | Restore it, or point `pricing` in `deploy/bench.yaml` at a snapshot. Before this fix every command failed with `load prices: ... no such file or directory` |
| `agent build` printed a different image id on every build | Docker's containerd store with buildx's default provenance attestation | Fixed: builds now set `BUILDX_NO_DEFAULT_ATTESTATIONS=1`. A changed id still refuses a frozen experiment's remaining runs, as designed |

## Honest limits

- **Single host.** One machine runs the stack, the agents and the graders.
  There is no scheduler across hosts, and one stack serves one machine.
- **Docker isolation** only, as above.
- **Templates tested end to end:** `python-pytest` and `node-test` were
  walked from `task init` through admission and a run here, and the acme
  example runs a Node suite. `go-test` and `generic-reward` have unit
  tests and golden files but have not been run end to end.
- **Gemini is not scriptable** in the fault proxy, so `agent check` refuses
  an agent whose provider is `gemini`; such an agent can only be checked on
  a real model.
- **Hidden tests cut from the reference commit need human review.**
  Admission proves the reference passes three times and the empty patch,
  mutants, exploit and null agents fail. It cannot prove the tests check what
  the instruction asks, or that every correct fix passes.
- **`exit_code` is the weakest verifier format:** whatever the command runs
  from the workspace can exit 0 itself. Prefer `junit` or `reward`
  ([`verifiers.md`](verifiers.md)).
- **Small suites decide little.** The verdict decides on tasks; with a
  handful of tasks most comparisons read inconclusive, and the report says
  how many tasks a decision needs.

## See also

- [`tasks-from-your-repo.md`](tasks-from-your-repo.md): good task commits,
  hidden tests by hand, what admission proves.
- [`verifiers.md`](verifiers.md): the three result formats and the clean room.
- [`adding-an-agent.md`](adding-an-agent.md): templates, the YAML, framework
  agents, every `agent check` row.
- [`examples/acme-node`](../examples/acme-node/README.md): a complete
  project with four Node tasks, a custom agent and Codex.
- [`bring-your-own-design.md`](bring-your-own-design.md): the contract, with
  the deviations found while writing this guide.
