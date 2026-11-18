# acme-a2a-files

A Heimdall suite written by `bifrost-bench suite init -template artifact`,
with one complete sample task (`tasks/sales-report`). The user guide is
`docs/scenarios.md` in the bench repository.

Each task's result is files the agent writes in its workspace. After the agent exits the
bench copies the files the task lists under output.artifacts into the run folder, and the
hidden verifier reads them read-only at /grader/artifacts/<id>/<file name>. A required
artifact that is missing fails the run before the verifier runs.

## What to edit

1. `tasks/<task>/repo/`: the input files the agent works from.
2. `tasks/<task>/instruction.md`: the request, naming every file the agent
   must write.
3. `output.artifacts` in task.yaml: each file the agent must leave in the
   workspace (an id, a path relative to the workspace, `required`, and
   `max_bytes`).
4. `tasks/<task>/verifier/tests/verify.py`: reads each artifact at
   `/grader/artifacts/<id>/<file name>` and writes the reward, one
   dimension per artifact.
5. `tasks/<task>/solution/reference.sh`: writes the files a correct agent
   writes. Admission runs it in the agent's place and the verifier must pass
   it.

`controls/` holds what admission requires to fail: the empty control, an
exploit and plausible wrong files (mutant scripts). The exploit
copies the input to the output names and plants fake reward files.

## Layout

```
suite.yaml                       the suite: image, grader, output, tasks, probes
base/Dockerfile                  the toolchain image the workspace and verifier run on
probes/model-outage.yaml         a gateway fault: the model answers 503 from call 3
tasks/<task>/task.yaml           one task: artifacts, hidden verifier, controls
tasks/<task>/instruction.md      what the agent is asked to do
tasks/<task>/repo/               the workspace the agent starts from (no .git)
tasks/<task>/verifier/tests/     hidden: verify.py, at /grader/tests
tasks/<task>/solution/           the reference script that writes the files
tasks/<task>/controls/           empty, exploit and mutant scripts that must fail
```

## Commands

Run from the project root.

    # base image bench/coding-core-base:1: uid 1000 user, git, /workspace and /grader/out owned by it
    bifrost-bench task validate acme-a2a-files/sales-report
    bifrost-bench task admit acme-a2a-files/sales-report
    bifrost-bench task init -suite acme-a2a-files -id t02
    bifrost-bench suite lint acme-a2a-files

`task admit` builds the base image when missing and
records the result in `suite.lock`. A task that was not admitted cannot be
planned. `task init` copies the first task's shape into `tasks/t02` with
EDIT ME markers; `task validate` fails until every marker is gone.

## Held-out tasks and canaries

Add `split: held_out` to a task's `task.yaml` to keep it out of `tasks: all`,
out of the public leaderboard and out of what the UI and API serve. To detect
later whether this suite leaked into a model's training data, give it a
contamination canary:

    bifrost-bench suite canary acme-a2a-files          # sets canary: <guid> in suite.yaml
    bifrost-bench suite canary -stamp acme-a2a-files   # also writes it into every instruction file
    bifrost-bench suite canary-check -text model-output.txt

Stamping changes instruction hashes, so run `suite lock` and `suite admit`
again afterwards; stamp before the first published results, not after. A
canary only catches verbatim copies of stamped text. The bench guide
`docs/held-out-and-canaries.md` has the details.
