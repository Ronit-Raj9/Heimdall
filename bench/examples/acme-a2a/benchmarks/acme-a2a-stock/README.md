# acme-a2a-stock

A Heimdall suite written by `bifrost-bench suite init -template tool-use`,
with one complete sample task (`tasks/restock`). The user guide is
`docs/scenarios.md` in the bench repository.

Each task starts an HTTP tool server beside the agent, seeded with the task's data. The agent
reaches it through the per-run tool proxy (every call lands in the run's tools.jsonl), and the
hidden verifier checks the server's final state, exported after the agent exits.

## What to edit

1. `services/inventory/server.py`: replace the inventory tools with your own
   (`ROUTES` and their functions); the rest needs no change.
   Rename the folder and the service id in every task.yaml when you rename
   the service.
2. `tasks/<task>/seed/db.json`: the data the server starts with in that task.
3. `tasks/<task>/instruction.md`: the request, as a user would write it.
4. `tasks/<task>/verifier/tests/checks.py`: what the server's final state
   must hold (`verify.py` beside it documents every check; it needs no
   change).
5. `tasks/<task>/solution/reference.sh`: the calls a correct agent makes.
   Admission runs it in the agent's place and the verifier must pass it.

`controls/` holds what admission requires to fail: the empty control, an
exploit and plausible wrong actions (mutant scripts).

## Layout

```
suite.yaml                       the suite: image, grader, output, tasks, probes
base/Dockerfile                  the toolchain image the workspace and verifier run on
services/inventory/              the tool server: Dockerfile and server.py
probes/tool-error.yaml           a tool fault: the first call to the server fails
probes/model-outage.yaml         a gateway fault: the model answers 503 from call 3
tasks/<task>/task.yaml           one task: services, hidden verifier, controls
tasks/<task>/instruction.md      what the agent is asked to do
tasks/<task>/repo/               the workspace the agent starts from (no .git)
tasks/<task>/seed/db.json        the data the tool server starts with
tasks/<task>/verifier/tests/     hidden: verify.py and checks.py, at /grader/tests
tasks/<task>/solution/           the reference script that must pass
tasks/<task>/controls/           empty, exploit and mutant scripts that must fail
```

## Commands

Run from the project root.

    # base image bench/coding-core-base:1: uid 1000 user, git, /workspace and /grader/out owned by it
    docker build -t bench/acme-a2a-stock-inventory:1 benchmarks/acme-a2a-stock/services/inventory
    bifrost-bench task validate acme-a2a-stock/restock
    bifrost-bench task admit acme-a2a-stock/restock
    bifrost-bench task init -suite acme-a2a-stock -id t02
    bifrost-bench suite lint acme-a2a-stock

`task admit` builds the base image and the service image when missing and
records the result in `suite.lock`. A task that was not admitted cannot be
planned. `task init` copies the first task's shape into `tasks/t02` with
EDIT ME markers; `task validate` fails until every marker is gone.

## Held-out tasks and canaries

Add `split: held_out` to a task's `task.yaml` to keep it out of `tasks: all`,
out of the public leaderboard and out of what the UI and API serve. To detect
later whether this suite leaked into a model's training data, give it a
contamination canary:

    bifrost-bench suite canary acme-a2a-stock          # sets canary: <guid> in suite.yaml
    bifrost-bench suite canary -stamp acme-a2a-stock   # also writes it into every instruction file
    bifrost-bench suite canary-check -text model-output.txt

Stamping changes instruction hashes, so run `suite lock` and `suite admit`
again afterwards; stamp before the first published results, not after. A
canary only catches verbatim copies of stamped text. The bench guide
`docs/held-out-and-canaries.md` has the details.
