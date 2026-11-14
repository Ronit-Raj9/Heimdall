# demo

A Heimdall suite written by `bifrost-bench suite init -template python-pytest`.

Each task's hidden pytest files run with the `hidden_tests` grader. pytest runs in the
helper image (`images.helper` in deploy/bench.yaml), so keep it and base/Dockerfile in step.
Required test ids are `path/test_file.py::test_name`.

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
