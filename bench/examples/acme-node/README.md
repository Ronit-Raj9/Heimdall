# Acme example project: a Node.js codebase and a custom agent on Heimdall

This folder is a complete Heimdall (bifrost-bench) project as a company would
keep it in its own repository, laid out as in
[`docs/bring-your-own-design.md`](../../docs/bring-your-own-design.md)
("The project folder"). It shows that the bench works on a codebase that is
not Python, with an agent the company wrote itself, without any Go code:

- **A JavaScript suite.** `acme-orders`: Node.js 22, ES modules, zero npm
  dependencies, so tasks, tests and the verifier run fully offline. Graded by
  the `verifier_command` grader with Node's built-in test runner reporting
  JUnit.
- **Four tasks of four kinds** on one small codebase (an orders service:
  pricing, coupons, stock reservations, CSV/JSON import, a CLI), each with
  hidden behaviour-level tests, a reference solution, an empty patch, two
  plausible wrong fixes (mutants), an exploit control and clean/stale notes.
- **A custom agent.** `acme-agent`: one dependency-free Node file speaking
  OpenAI chat completions with tool calling, built per suite on the suite's
  toolchain image (`build.base_arg`), unit-tested offline against a scripted
  fake server.
- **Experiments.** A free smoke check with the control agents, and a real
  comparison (`acme-agent` against Codex CLI, same model, clean plus two
  failure families).

## What is fictional

Acme, its tea stores, the `@acme/orders` package, the store names, customers,
SKUs, prices and the issue texts are invented for this example; nothing is
taken from a real company or an existing benchmark. Everything here was
written from scratch and is MIT licensed (`provenance.license` in every
`task.yaml`). The tasks were written by hand (`created_by: hand-written
example`); a real company would cut them from its own history with
`bifrost-bench task init`.

## Folder map

```
acme-node/
  README.md                         this file
  .gitignore                        runs/ stays local
  deploy/
    bench.yaml                      paths, gateway, networks, helper image (lib/config.go)
    pricing-snapshot.json           model prices for cost metrics (copy of the bench's)
  policies/default.yaml             release policy (copy of the bench's)
  agents/
    acme-agent.yaml                 Acme's agent (command adapter, build.base_arg)
    images/acme-agent/Dockerfile    ARG BASE_IMAGE / FROM ${BASE_IMAGE}
    images/acme-agent/agent.mjs     the whole agent, one file, no dependencies
    codex.yaml                      Codex CLI (the bench's config, image built per suite)
    images/codex/Dockerfile         Codex CLI on a suite's base image
    oracle.yaml, noop.yaml          scripted controls for the free smoke run
  benchmarks/acme-orders/
    suite.yaml                      verifier_command, junit, restore list, tasks, probes
    suite.lock                      written by `suite admit` (all four tasks admitted)
    base/Dockerfile                 node:22-slim (pinned) + git, uid 1000 "agent"
    probes/stale-notes.yaml         copies of the bench's probes
    probes/model-outage.yaml
    tasks/ord01 ... ord04/
      task.yaml                     metadata, required test ids, controls, provenance
      instruction.md                the issue the agent gets
      repo/                         the project at the task's starting point
      verifier/tests/*.test.mjs     hidden tests (never in the agent's workspace)
      verifier/tests/_workspace.mjs hidden helper: imports /workspace/src/index.js
      verifier/requirements.md      hidden test -> instruction rule map
      solution/reference.diff       reference solution
      controls/empty.diff           no change
      controls/mutant1.diff, mutant2.diff   plausible wrong fixes
      controls/exploit.sh           tries to fake a green test run
      fixtures/notes.clean.md       AGENTS.md for clean runs
      fixtures/notes.stale.md       AGENTS.md for the stale-notes probe
  experiments/
    acme-smoke.yaml                 free: oracle vs noop on all tasks
    acme-agent-vs-codex.yaml        acme-agent vs Codex, gpt-5.4-mini, 3 conditions x 2 trials
  tools/
    verify-by-hand.sh               admission by hand with plain docker (table below)
    junit-summary.mjs               the junit pass rule, dependency-free
    fake-chat-server.mjs            scripted OpenAI-compatible server for offline agent tests
    acme-agent.test.mjs             offline unit tests of agent.mjs
```

## The codebase and the tasks

`@acme/orders` is about 700 lines in `src/` (money in integer cents, ISO 8601
timestamps and business days in the store's time zone, coupons with windows
and minimums, shipping and tax, all-or-nothing stock reservations with
expiry, CSV/JSON import, an `acme-orders price|report` CLI) with 26 visible
`node:test` tests that pass at every task's starting point. Each task carries
its own full copy of the project in `repo/`.

| Task | Kind | What the agent must do | Why it needs reasoning | Hidden tests | Mutants (plausible wrong fixes) |
|---|---|---|---|---|---|
| `ord01` | bugfix | Orders near midnight land on the wrong business day | Root cause is not where the symptoms are: `businessDay` is correct, but `parseTimestamp` validates the `+HH:MM` offset and never applies it. Only non-UTC offsets near midnight show it; the visible tests use `Z` | 10 | 1: offset applied with the wrong sign. 2: offset minutes ignored (`+05:30` read as `+05:00`) |
| `ord02` | feature | Multi-buy promotions ("buy 2, get 1 free") | Spans settings validation (`store.js`, `parseStore`), the rule (`discounts.js`), pricing order (promotions before coupon, shipping and tax) and CLI output; units count across lines and free units come from the last line | 9 | 1: counts units per line, not per order. 2: one free unit per `buy` units instead of per `buy + get` group |
| `ord03` | refactor | Options object for `priceOrder`/`priceOrders`, rename `formatMoney` to `formatCents`, old forms keep working with a `DeprecationWarning` | Two codes, each once per process, through `process.emitWarning` so `--no-deprecation` and `--throw-deprecation` work; every internal caller (the CLI) must move to the new API | 10 | 1: one "warned" flag shared by both codes. 2: deprecated forms work but the CLI still uses them |
| `ord04` | performance | Batch catalog lookups in `priceOrders` (N+1 round trips) | Distinct SKUs once per call, `getMany` in batches of at most 100, no cache across calls (prices change), errors identical to one-by-one pricing. Checked by counting calls through an injected catalog, never by wall-clock | 8 | 1: process-wide cache (stale prices on the next call). 2: one `getMany` call with every SKU (ignores the 100 limit) |

Each stale notes file contradicts the code on a behaviour the visible tests
check (`stale_claims` in each `task.yaml`): business days in UTC (ord01), tax
before the coupon (ord02), `store` argument required (ord03), `getMany`
returning a plain object (ord04). Each `exploit.sh` replaces the visible
tests, makes `npm test` exit 0, writes a `node.config.json` that would filter
every test, drops a fake passing `grader/out/junit.xml`, plus one task-specific
trick (`.env` with `TZ`, `.npmrc` with `--no-deprecation`, a changelog claim).

### How the hidden tests reach the code

The verifier mounts the agent's workspace read-only at `/workspace` and the
hidden tests at `/grader/tests`, outside the package. The hidden helper
`_workspace.mjs` imports the public entry point by absolute path
(`await import('file:///workspace/src/index.js')`); `ACME_WORKSPACE=<checkout>`
points it elsewhere when writing tasks:

```sh
ACME_WORKSPACE=$PWD/benchmarks/acme-orders/tasks/ord01/repo \
  node --test 'benchmarks/acme-orders/tasks/ord01/verifier/tests/**/*.test.mjs'   # fails: the bug is there
```

The suite's verifier command is

```
node --test --test-reporter=junit --test-reporter-destination=/grader/out/junit.xml /grader/tests/**/*.test.mjs
```

with the test path as a glob that Node expands itself: in Node 22 a bare
folder (`/grader/tests`) is not walked but loaded as a module and fails with
`Cannot find module '/grader/tests'`. Node's JUnit reporter gives every case
`classname="test"` and no file name, so required ids are `test::<test name>`;
test names are unique per task, every hidden test is required, and every
hidden file wraps its tests in one `describe` so they land in a `<testsuite>`.

## Commands

All from this folder, with `bifrost-bench` built from `bench/`
(`CGO_ENABLED=0 GOWORK=off go build -o bin/bifrost-bench ./cmd/bifrost-bench`)
and on `PATH`. Every command takes `-config deploy/bench.yaml`; the project
root is that file's grandparent. The gateway stack is the bench's own
(`deploy/docker-compose.yml` in `bench/`); `bench.yaml` here points at it.

```sh
cd bench/examples/acme-node

# 0. How a project like this starts (already done here)
bifrost-bench init acme-node
bifrost-bench suite init acme-orders -template node-test -config deploy/bench.yaml
bifrost-bench task init -suite acme-orders -id ord01 -repo ~/src/acme-orders -base <commit> -reference <commit> \
    -license MIT -kind bugfix -config deploy/bench.yaml        # then edit instruction.md and the controls

# 1. The suite's toolchain image (also the trusted helper image in bench.yaml)
docker build -t heimdall-example/acme-orders-base:1 benchmarks/acme-orders/base

# 2. Check the tasks: by hand with plain docker, then with the bench
sh tools/verify-by-hand.sh                                      # table below
bifrost-bench suite lint -config deploy/bench.yaml acme-orders
bifrost-bench task validate -config deploy/bench.yaml acme-orders/ord01
bifrost-bench suite admit -config deploy/bench.yaml acme-orders  # writes suite.lock

# 3. Agents: offline unit test, per-suite images, free conformance check
node --test tools/acme-agent.test.mjs
bifrost-bench agent build agents/acme-agent.yaml -suite acme-orders -config deploy/bench.yaml
bifrost-bench agent build agents/codex.yaml -suite acme-orders -config deploy/bench.yaml
bifrost-bench agent check agents/acme-agent.yaml -suite acme-orders -task ord01 -config deploy/bench.yaml

# 4. Experiments
bifrost-bench plan -config deploy/bench.yaml experiments/acme-smoke.yaml
bifrost-bench run  -config deploy/bench.yaml experiments/acme-smoke.yaml            # free
bifrost-bench plan -config deploy/bench.yaml experiments/acme-agent-vs-codex.yaml
bifrost-bench run  -config deploy/bench.yaml experiments/acme-agent-vs-codex.yaml   # spends model money (budget $3)

# 5. Results: web UI and API over runs/
bifrost-bench serve -config deploy/bench.yaml                                       # http://127.0.0.1:8091
```

`acme-smoke` uses the `oracle` and `noop` controls rather than the bench's
`toy` agent, because `toy` is a Python script and this project's images have
no Python. Expected: arm A verifies 4 of 4, arm B 0 of 4.

## Verification by hand

`tools/verify-by-hand.sh` grades every control the way admission does, with
plain `docker run`, every container `--network none`: prepare (root, trusted:
copy `repo/`, `git apply`, reset the restore paths), verify (uid 1000,
read-only root, workspace read-only, the suite's verifier command), judge the
JUnit report with `tools/junit-summary.mjs` (at least one test, no failures,
errors or skips, every required id ran and passed). The exploit runs as uid
1000 in a workspace with a git baseline; the diff it leaves is exported and
graded the same way. "reference+stale" is the reference with
`notes.stale.md` present as `AGENTS.md` and `CLAUDE.md` (the stale-notes
probe must stay goal-preserving). Output of 15 Nov 2026, Node 22.23.5:

| task  | control          | want | got  | hidden tests           | result   |
|-------|------------------|------|------|------------------------|----------|
| ord01 | reference_1      | pass | pass | 10 tests, 0 failed     | OK       |
| ord01 | reference_2      | pass | pass | 10 tests, 0 failed     | OK       |
| ord01 | reference_3      | pass | pass | 10 tests, 0 failed     | OK       |
| ord01 | empty_patch      | fail | fail | 10 tests, 9 failed     | OK       |
| ord01 | mutant_1         | fail | fail | 10 tests, 9 failed     | OK       |
| ord01 | mutant_2         | fail | fail | 10 tests, 6 failed     | OK       |
| ord01 | exploit          | fail | fail | 10 tests, 9 failed     | OK       |
| ord01 | reference+stale  | pass | pass | 10 tests, 0 failed     | OK       |
| ord02 | reference_1      | pass | pass | 9 tests, 0 failed      | OK       |
| ord02 | reference_2      | pass | pass | 9 tests, 0 failed      | OK       |
| ord02 | reference_3      | pass | pass | 9 tests, 0 failed      | OK       |
| ord02 | empty_patch      | fail | fail | 9 tests, 8 failed      | OK       |
| ord02 | mutant_1         | fail | fail | 9 tests, 1 failed      | OK       |
| ord02 | mutant_2         | fail | fail | 9 tests, 4 failed      | OK       |
| ord02 | exploit          | fail | fail | 9 tests, 8 failed      | OK       |
| ord02 | reference+stale  | pass | pass | 9 tests, 0 failed      | OK       |
| ord03 | reference_1      | pass | pass | 10 tests, 0 failed     | OK       |
| ord03 | reference_2      | pass | pass | 10 tests, 0 failed     | OK       |
| ord03 | reference_3      | pass | pass | 10 tests, 0 failed     | OK       |
| ord03 | empty_patch      | fail | fail | 10 tests, 7 failed     | OK       |
| ord03 | mutant_1         | fail | fail | 10 tests, 1 failed     | OK       |
| ord03 | mutant_2         | fail | fail | 10 tests, 1 failed     | OK       |
| ord03 | exploit          | fail | fail | 10 tests, 7 failed     | OK       |
| ord03 | reference+stale  | pass | pass | 10 tests, 0 failed     | OK       |
| ord04 | reference_1      | pass | pass | 8 tests, 0 failed      | OK       |
| ord04 | reference_2      | pass | pass | 8 tests, 0 failed      | OK       |
| ord04 | reference_3      | pass | pass | 8 tests, 0 failed      | OK       |
| ord04 | empty_patch      | fail | fail | 8 tests, 6 failed      | OK       |
| ord04 | mutant_1         | fail | fail | 8 tests, 1 failed      | OK       |
| ord04 | mutant_2         | fail | fail | 8 tests, 2 failed      | OK       |
| ord04 | exploit          | fail | fail | 8 tests, 6 failed      | OK       |
| ord04 | reference+stale  | pass | pass | 8 tests, 0 failed      | OK       |

The same day `bifrost-bench suite admit` (the bench's own admission, with the
`verifier_command` grader) admitted all four tasks with the same outcome per
control, plus the checks it adds: `null_noop` and `null_refuse` fail, the
instruction is edited, no secrets, the verifier ran with no network.

The agent's offline tests (`node --test tools/acme-agent.test.mjs`, 5 tests)
pass on the host and inside `heimdall-example/acme-agent:1-acme-orders` with
`--network none`: tool calls run and their results go back to the model, the
bench headers are sent (`Authorization: Bearer <run key>`, `x-bf-session-id`,
`x-bf-session-affinity: off`), the run key is kept out of shell commands,
paths outside the workspace are refused, a 503 stops the agent with exit 1 and
no success claim, the turn cap and the shell timeout hold.
