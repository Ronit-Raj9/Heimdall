# Acme memory example: does an agent remember across sessions?

This folder is a complete Heimdall (bifrost-bench) project for a company
that wants to test an agent's memory: what it learned in one session, it must
still know in a later one, with its workspace wiped in between. It is laid
out like [`examples/acme-node`](../acme-node/README.md) and follows the
memory suite template (`bifrost-bench suite init -template memory`); the
user guide is [`docs/scenarios.md`](../../docs/scenarios.md). No Go code is
needed:

- **A memory suite.** `acme-memory`: three tasks of two or three sessions
  each. Each session is a fresh agent process; between sessions the
  workspace is reset, so only the agent's memory (the `/memory` volume named
  by `BENCH_MEMORY_DIR`, and the agent's own `memory.paths`) carries facts
  forward. Only the last session is graded.
- **Graded without a model.** The last session's answer (`answer.txt` in the
  workspace, else the final message) is compared with each task's hidden
  `verifier/answer.yaml` by the `answer_match` grader: exact, contains_all or
  regex here. No judge model, so the suite is `verified`, not `judged`.
- **One abstention task.** The right answer to `vendor-phone` is "the notes
  never said": any phone number is made up.
- **Controls per task.** A reference answer that must match, an empty answer,
  an exploit and plausible wrong answers (mutants) that must not.
- **Experiments.** A free smoke check with the scripted controls, and a real
  comparison of Claude Code with its auto memory on and off.

## What is fictional

Acme, its payments, accounts and procurement teams, Hollis Ceramics,
Northwind Archive, every person, address, date and amount are invented for
this example; nothing is taken from a real company or an existing benchmark.
Everything here was written from scratch and is MIT licensed
(`provenance.license` in every `task.yaml`, `created_by: hand-written
example`).

## Folder map

```
acme-memory/
  README.md                           this file
  .gitignore                          runs/ and checks/ stay local
  deploy/
    bench.yaml                        paths, gateway, networks, helper image bench/coding-core-base:1
    pricing-snapshot.json             model prices for cost metrics (copy of the bench's)
  policies/default.yaml               release policy (copy of the bench's)
  agents/
    oracle.yaml, noop.yaml            scripted controls for the free smoke run
    claude-code-memory.yaml           Claude Code with auto memory on and /home/agent/.claude kept between sessions
    images/claude-code/Dockerfile     the bench's Claude Code image (bench/agent-claude-code:1)
  benchmarks/acme-memory/
    suite.yaml                        track memory, grader answer_match, output answer, tasks, probes
    probes/model-outage.yaml          copy of the bench's gateway probe
    tasks/<task>/
      task.yaml                       sessions, hidden answer, controls, provenance
      sessions/<session>.md           one instruction per session, in task.yaml order
      sessions/<session>-files/       copied into the workspace before that session only
      repo/README.md                  the workspace every session starts from
      verifier/answer.yaml            hidden: the expected answer and how it is matched
      solution/answer.txt             reference answer (must match)
      controls/empty.txt              empty answer (must not match)
      controls/exploit.sh             writes a hedged answer in the agent's place (must not match)
      controls/<wrong>.txt            plausible wrong answers, the mutants (must not match)
  experiments/
    memory-smoke.yaml                 free: oracle vs noop on all tasks
    memory-real.yaml                  Claude Code, auto memory on vs off (spends money)
```

The suite's base image is the bench helper image `bench/coding-core-base:1`
(the one `bifrost-bench init` builds from `deploy/helper/Dockerfile`), so the
free runs need no other image. The Claude Code image is built on the same
base.

## The tasks

| Task | Sessions | What the last session asks | Match | Why it needs memory |
|---|---|---|---|---|
| `standup-freeze` | 2: learn, recall | The date the payments code freeze starts | exact `2026-05-26` (trimmed) | The standup transcript is gone in session 2. It says five dates, and the freeze date itself changes mid-meeting (20 May, then 26 May) |
| `shipping-preference` | 3: account, call, ask | Where the next Hollis Ceramics shipment goes | contains_all `locker`, `brookfield` (lower-cased) | Knowledge update: session 1 says her office on Quarry Road, session 2 says she closed it and wants Brookfield Station locker 14. The newer fact must replace the older one |
| `vendor-phone` | 2: learn, ask | Northwind Archive's support phone number | `abstain: true`: the grader's fixed "not known" phrases | Abstention: the contract summary names an email, an account manager and response times, never a phone number |

Each learn session says the workspace is wiped before the next session and
that the folder named by `BENCH_MEMORY_DIR` survives. Each graded session
asks for the answer only, written to `answer.txt` as well.

The answer files are graded against each `answer.yaml` by the bench's own
`answer_match` rules in `GOWORK=off go test ./cmd/bifrost-bench -run
TestScenarioAnswerControls` (from `bench/`), 15 Nov 2026:

| Task | Control | Result |
|---|---|---|
| `standup-freeze` | `solution/answer.txt` | match |
| `standup-freeze` | `controls/empty.txt`, `exploit.sh`, `old-freeze.txt`, `release-date.txt` | no match |
| `shipping-preference` | `solution/answer.txt` | match |
| `shipping-preference` | `controls/empty.txt`, `exploit.sh`, `old-office.txt`, `wrong-locker.txt` | no match |
| `vendor-phone` | `solution/answer.txt` | match |
| `vendor-phone` | `controls/empty.txt`, `exploit.sh`, `email-instead.txt`, `made-up-number.txt` | no match |

## Commands

All from this folder, with `bifrost-bench` built from `bench/`
(`CGO_ENABLED=0 GOWORK=off go build -o bin/bifrost-bench ./cmd/bifrost-bench`)
and on `PATH`. Every command takes `-config deploy/bench.yaml`; the project
root is that file's grandparent. The gateway stack is the bench's own; the
commands that reach it need that stack's `.env` loaded
(`set -a; . ../../deploy/.env >/dev/null 2>&1; set +a`).

```sh
cd bench/examples/acme-memory

# 0. How a project like this starts (already done here)
bifrost-bench init acme-memory
bifrost-bench suite init acme-memory -template memory -image bench/coding-core-base:1 -config deploy/bench.yaml
bifrost-bench task init -suite acme-memory -id vendor-phone -config deploy/bench.yaml   # copies a task's shape, EDIT ME markers

# 1. Check the tasks
bifrost-bench suite lint -config deploy/bench.yaml acme-memory
bifrost-bench task validate -config deploy/bench.yaml acme-memory/standup-freeze
bifrost-bench suite admit -config deploy/bench.yaml acme-memory              # writes suite.lock

# 2. Free smoke run: oracle must pass 3 of 3, noop 0 of 3
bifrost-bench plan -config deploy/bench.yaml experiments/memory-smoke.yaml
BENCH_SERVER_MODE=synthetic bifrost-bench run -config deploy/bench.yaml experiments/memory-smoke.yaml

# 3. A real agent: build, free conformance check, then the paid comparison
bifrost-bench agent build agents/claude-code-memory.yaml -suite acme-memory -config deploy/bench.yaml
bifrost-bench agent check agents/claude-code-memory.yaml -suite acme-memory -config deploy/bench.yaml
bifrost-bench run -config deploy/bench.yaml experiments/memory-real.yaml   # spends model money (budget $2)
```

`memory-smoke` proves the plumbing, not memory: the oracle writes the
reference answer in the graded session and never needs to remember
anything. A memory result comes only from a real agent, as in
`memory-real`, whose arm B switches Claude Code's auto memory off with one
env override but keeps the `/memory` folder, so it measures the built-in
memory against plain notes in a folder, not memory against none.

## Status (15 Nov 2026)

This project was written while the parts it needs were being built by
other people in parallel: sessions and the memory volume (run kernel and
environment kernel), the answer export, the `answer_match` grader and
memory metrics. What was run, and what was not:

- **Run:** `GOWORK=off go test ./scaffold/ -run TestExample` loads this
  suite and every task through the bench's suite loader (so through the
  JSON Schemas) and runs `task validate` on each task: no errors. The
  answer check above, with the real grader rules. Both experiment files
  decode against the experiment schema, and the arm B override resolves.
- **Admitted in Docker:** `bifrost-bench suite admit acme-memory` on a
  copy of this folder (byte-identical) admitted all three tasks; the
  committed `suite.lock` is that run's. Per task: the reference passed 3 of
  3 through the real session runs and `answer_match`; the empty answer,
  every mutant, every `wrong_answers` entry, the exploit script, and the
  noop and refuse agents failed. The exploit fails for the right reason
  (for example `exact: answer does not match; got "2026-05-20, 2026-05-26
  or 2026-05-28"`), and noop with `no answer: answer.txt is empty`.
- **Not run here:** the smoke experiment and `agent check`. Both need the
  gateway's admin token and the fault proxy token from the stack's
  `deploy/.env`, which this run did not load.
- **Schema gap:** `agents/claude-code-memory.yaml` sets `memory.paths`
  (a field of the agent config in `schemas/scenario.go`); the agent JSON
  Schema did not list `memory` yet when this was written, so loading that
  one file fails with `memory: unknown field` until the schema catches up.
  The smoke run does not use it.

## Honest limits

- **Three tasks decide nothing.** The verdict decides on tasks; with three,
  any comparison reads inconclusive and the report says how many tasks a
  decision needs.
- **contains_all is loose.** An answer that names both the old office and
  the Brookfield locker would pass `shipping-preference`. A regex anchored
  on the whole answer would be stricter, at the cost of rejecting correct
  answers worded differently. The exploit control here hedges between the
  two places without naming Brookfield, so it fails; it does not prove the
  match cannot be gamed.
- **Abstention by fixed phrases.** With `abstain: true` the `answer_match`
  grader passes `vendor-phone` only when the answer holds one of its fixed
  "not known" phrases ("i don't know", "not mentioned", "never mentioned",
  "no information", "unknown", ...; `docs/answer-grading.md` lists them) and
  ignores the task's regex. An honest answer worded some other way ("the
  notes never gave one") fails; a judged suite would catch it, at the price
  of a judge model.
- **The oracle is not a memory baseline.** It passes because it is handed
  the answer. Compare real agents, or one agent with its memory on and off.
