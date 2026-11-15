# demo

A Heimdall suite written by `bifrost-bench suite init -template node-test`.

Each task's hidden node:test files run with the `verifier_command` grader and report JUnit
XML. Node's JUnit reporter names every test case with classname `test`, so required test ids
are `test::<test name>`.

## Layout

```
suite.yaml                   the suite: image, verifier command, tasks, probes
base/Dockerfile              the toolchain image every task runs on
probes/model-outage.yaml     a gateway fault: the model answers 503 from call 3
tasks/<task>/task.yaml       one task: instruction, repo, hidden verifier, controls
tasks/<task>/instruction.md  what the agent is asked to do
tasks/<task>/repo/           the workspace the agent starts from (no .git)
tasks/<task>/verifier/tests/ hidden files, mounted at /grader/tests
tasks/<task>/solution/       the reference diff that must pass
tasks/<task>/controls/       the empty diff and the exploit that must fail
```

## Commands

Run from the project root.

    docker build -t bench/demo-base:1 benchmarks/demo/base
    bifrost-bench task init -suite demo -id t01 -repo <git repo> -base <commit> -reference <commit>
    bifrost-bench task validate demo/t01
    bifrost-bench task admit demo/t01
    bifrost-bench suite lint demo

`task admit` builds the base image when it is missing and records the
result in `suite.lock`. A task that was not admitted cannot be planned.

## Held-out tasks and canaries

Add `split: held_out` to a task's `task.yaml` to keep it out of `tasks: all`,
out of the public leaderboard and out of what the UI and API serve. To detect
later whether this suite leaked into a model's training data, give it a
contamination canary:

    bifrost-bench suite canary demo          # sets canary: <guid> in suite.yaml
    bifrost-bench suite canary -stamp demo   # also writes it into every instruction file
    bifrost-bench suite canary-check -text model-output.txt

Stamping changes instruction hashes, so run `suite lock` and `suite admit`
again afterwards; stamp before the first published results, not after. A
canary only catches verbatim copies of stamped text. The bench guide
`docs/held-out-and-canaries.md` has the details.
