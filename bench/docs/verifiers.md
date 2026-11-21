# Verifiers in any language

A suite grades every run with one verifier command. The `verifier_command` grader runs that command on the suite's own base image, so the verifier can be pytest, `node --test`, `go test`, a shell script or anything else that runs in a container, and it reports in one of three formats: JUnit XML, a reward file or the exit code. `hidden_tests` is the original Python grader; it keeps working unchanged for existing suites such as `coding-core`.

This page is the reference for suite authors. The design contract is `docs/bring-your-own-design.md` ("Suites in any language"); the code is `graders/verifiercommand.go` and the clean room it shares with `hidden_tests`, `graders/cleanroom.go`.

## What the verifier sees

Each grade runs in containers created for that grade only and removed before it returns:

1. A trusted helper (`images.helper` in `deploy/bench.yaml`; it needs `sh`, `cp` and `git`) copies the pristine task repo into a fresh volume, applies the run's exported diff with `git apply` and resets every path in `verifier.restore` from the pristine repo. A diff that does not apply, including one whose paths leave the repo (`../...`), is a fail with reason `patch does not apply`.
2. The verifier container starts from the suite base image (`base_image.tag`) with the command as its entrypoint and arguments (no shell unless the command starts one).

| Path or setting | What it is |
|---|---|
| suite `workdir` (default `/workspace`) | the graded workspace, read-only, and the working directory |
| `/grader/tests` | the task's hidden verifier folder (`task.yaml` `verifier.tests`), read-only |
| `/grader/out` | an empty folder created by the grader for this grade, writable; the result file goes here |
| `/tmp` (512 MiB), `/home/agent` (64 MiB, `HOME`) | tmpfs, writable, executable |
| user | uid and gid 1000 |
| network | none |
| limits | all capabilities dropped, no new privileges, read-only root filesystem, 2 CPUs, 2 GiB memory, 512 processes |
| `timeout_s` | default 300; the container is killed when it expires |
| `/grader/trajectory/steps.jsonl`, `/grader/trajectory/tools.jsonl` | the run's step timeline and tool proxy ledger, read-only, when the run has them, so a verifier can score the path as well as the end state (return `dimensions` in a reward file) |

Anything the command prints goes to a 64 KiB buffer kept only for the debug log. Grading never depends on it.

## Process scores (report only)

A task can name the tool path a good run takes. The process metrics compare
each run's tool calls with it and are shown beside the score, never inside
it: they do not change `verified_success`, a gate or a verdict.

```yaml
process:
  match: strict              # strict (same calls, same order), unordered, subset (no call outside the path), superset (every path call made)
  expected_tools:
    - name: lookup_order
      args: {order_id: A-2001}  # optional; compared as a subset unless args_match: exact
    - name: refund_order
  # or: reference: reference.tools.jsonl  (a tools.jsonl copied from a good run, relative to the task folder)
  reference_steps: 4         # optional; default the length of the path
  max_steps_ratio: 1.5       # optional; flags a run with more calls than this multiple
```

| Metric | What it is |
|---|---|
| `process_tool_match` | 1 when the run's tool calls match the path under `match`, else 0 |
| `process_step_ratio` | the run's tool calls over the reference's (1 is the same, 2 twice as many) |
| `process_redundant_calls` | calls repeating an earlier call's name and arguments |

The run's calls come from the tool proxy ledger (names and arguments) or,
without one, from the tool calls the model requested through the gateway
(names only: arguments are not compared and `process_redundant_calls` is
unknown). A run with no observed tool calls is unknown, never a mismatch.
The report card shows them per benchmark as "Process (report only)".

## The three result formats

`verifier.result` picks the format. `hidden_tests` reads JUnit only, at `/grader/out/junit.xml`, and refuses the other result fields.

| `result` | The command must | Default `result_path` | Pass rule |
|---|---|---|---|
| `junit` (default) | write JUnit XML | `/grader/out/junit.xml` | exit 0, at least one test, no failures, errors or skips, every `required` test ran and passed |
| `reward` | write a number, or `{"reward": n, "dimensions": {"name": n}}` | `/grader/out/reward.json` | `reward >= pass_threshold` (default 1) |
| `exit_code` | exit 0 to pass | none | exit code 0 |

`result_path` must name a file directly in `/grader/out` (`^/grader/out/[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`). The workspace, `/grader/tests` and subfolders are refused, so no file the agent wrote can stand in for the verifier's result.

### junit

The same rules as `hidden_tests`. Reports with `<testsuites>` or `<testsuite>` roots are read, and a count is never taken below what the `<testcase>` elements show. Testcases directly under `<testsuites>`, which Node's reporter writes for top-level tests, count too.

`required` in `task.yaml` lists test ids that must run and pass, as `module::test` or `module::Class::test`:

| Runner | Classname in the report | Required id |
|---|---|---|
| pytest | `tests.test_calc` | `test_calc.py::test_add` or `tests/test_calc.py::test_add` |
| node --test | `test` | `test::adds` |
| gotestsum, go-junit-report | `example.com/calc` | `example.com/calc::TestAdd` or `calc::TestAdd` |

`required` applies only to `junit`. A malformed required id makes the evaluator invalid: it is a task bug, not the agent's.

### reward

The file holds either a bare decimal number (Harbor's `reward.txt`: `1`, `0.75`, `0`) or one JSON object:

```json
{"reward": 0.75, "dimensions": {"correctness": 0.75, "style": 1}}
```

- `reward` is required and must be a finite number. NaN, infinities, hex floats, strings and `null` are refused.
- `dimensions` is optional: at most 32 entries, names matching `[a-z0-9_]{1,40}`, values finite numbers (not `null`).
- Any other top-level key, or data after the object, is refused, so a typo never passes for a score.
- The file may be at most 64 KiB.
- `pass_threshold` is between 0 (exclusive) and 1e9; it defaults to 1. It cannot be 0: the Go field treats 0 as unset.
- The exit code is recorded but not required to be 0: a test script may exit non-zero after writing a partial reward.

The grade records `reward` and `dimensions` (`grade.json`). The metric `reward` reports the reward per run and each dimension becomes the report-only per-run value `reward_<name>` in `metrics.json` (for example `reward_correctness`). Neither is blended into `verified_success`, which the pass rule alone decides, and veto gates do not change them. A grade in another format has no reward (`not_applicable`); a reward run without a usable number is `unknown`, never 0.

### exit_code

Exit 0 passes, anything else fails with reason `exit <n>`. The format trusts the command completely: if the command imports or runs the agent's code, that code can exit 0 itself. Prefer `junit` (which also needs a report with at least one passing test) or `reward` whenever the agent's code runs inside the command.

## Outcomes

| What happened | Grade |
|---|---|
| diff does not apply | fail, `patch does not apply` |
| verifier ran past `timeout_s` | fail, `test run broken by the patch: verifier timed out after ...` |
| result file missing, a symlink or a folder, empty, unparseable or over its size limit (16 MiB JUnit, 64 KiB reward) | fail, `test run broken by the patch: ...` |
| JUnit with zero tests | fail, `test run broken by the patch: no tests ran` |
| malformed `required` id | invalid evaluator (unknown) |
| docker itself fails | error; the run's grade stage records the failure |

The reference solution passed this verifier three times at admission, so a broken test run after a cleanly applied patch is the patch's doing: a fail, never unknown. Otherwise an agent could turn a 0 into an unknown by crashing the verifier.

`Grade.result_format` is always set by `verifier_command` (`junit`, `reward` or `exit_code`).

## Minimal suites

Every suite needs the fields `spec/suite.schema.json` requires. The examples show `suite.yaml` and the hidden verifier files under `tasks/<task>/verifier/tests/` (`verifier.tests: verifier/tests` in `task.yaml`).

### Python, pytest JUnit

```yaml
id: py-core
version: 1.0.0
environment: docker_workspace
grader: verifier_command
base_image: {dockerfile: base/Dockerfile, tag: acme/py-core-base:1}
workdir: /workspace
verifier:
  command: ["python", "-I", "-B", "-m", "pytest", "-q", "-p", "no:cacheprovider",
            "--rootdir=/grader", "--junitxml=/grader/out/junit.xml", "/grader/tests"]
  restore: ["tests", "conftest.py", "pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini", "sitecustomize.py"]
  timeout_s: 300
tasks: [t01]
probes: []
```

`-I` keeps the workspace off `sys.path` and ignores `PYTHON*` variables; `--rootdir=/grader` and the restore list keep a planted `conftest.py` inert. Put `sys.path.insert(0, "/workspace")` in `/grader/tests/conftest.py` so the tests import the workspace code.

### Node, the built-in test runner

```yaml
id: node-core
version: 1.0.0
environment: docker_workspace
grader: verifier_command
base_image: {dockerfile: base/Dockerfile, tag: acme/node-core-base:1}   # FROM node:22-slim
workdir: /workspace
verifier:
  command: ["node", "--test", "--test-reporter=junit",
            "--test-reporter-destination=/grader/out/junit.xml", "/grader/tests/**/*.test.js"]
  restore: ["package.json"]
  timeout_s: 300
tasks: [t01]
probes: []
```

Node 22 expands the glob itself (no shell is involved). A hidden test requires the workspace code by absolute path and builtins with the `node:` prefix, so nothing in the workspace can shadow them:

```js
const test = require('node:test');
const assert = require('node:assert');
const { add } = require('/workspace/add.js');

test('adds', () => {
	assert.strictEqual(add(2, 3), 5);
});
```

`node:22-slim` has no `git`; the helper image prepares the workspace, so the base image does not need it.

### Go

Go needs test files inside the package folder and a writable build cache, so the hidden folder carries a script that copies the read-only workspace to `/tmp` first. With `exit_code`:

```yaml
id: go-core
version: 1.0.0
environment: docker_workspace
grader: verifier_command
base_image: {dockerfile: base/Dockerfile, tag: acme/go-core-base:1}     # FROM golang:1.26-bookworm, modules pre-downloaded
workdir: /workspace
verifier:
  command: ["sh", "/grader/tests/run.sh"]
  result: exit_code
  timeout_s: 600
tasks: [t01]
probes: []
```

```sh
#!/bin/sh
# tasks/t01/verifier/tests/run.sh
set -eu
export GOCACHE=/tmp/go-cache GOPROXY=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local
cp -a /workspace /tmp/src
# Agent test files go: a TestMain that exits 0 would pass everything.
find /tmp/src -name '*_test.go' -delete
cp /grader/tests/calc_test.go /tmp/src/calc/
cd /tmp/src
go test -count=1 ./...
```

For per-test results use `junit` instead: bake a converter into the base image (`go install gotest.tools/gotestsum@<version>` in the Dockerfile; there is no network while grading) and end the script with `gotestsum --junitfile /grader/out/junit.xml -- -count=1 ./...`. JUnit also fails a run where no test ran.

### Harbor-style reward script

A Harbor task's `tests/test.sh` writes its reward to `/logs/verifier/reward.txt`. Here it writes to `/grader/out` instead:

```yaml
id: reward-core
version: 1.0.0
environment: docker_workspace
grader: verifier_command
base_image: {dockerfile: base/Dockerfile, tag: acme/reward-core-base:1}
workdir: /workspace
verifier:
  command: ["sh", "/grader/tests/test.sh"]
  result: reward
  result_path: /grader/out/reward.txt     # or leave the default /grader/out/reward.json
  pass_threshold: 1
  timeout_s: 900
tasks: [t01]
probes: []
```

```sh
#!/bin/sh
# tasks/t01/verifier/tests/test.sh, PID 1 of the verifier container.
# The scoring step imports the agent's code, so it runs as a child. Then
# every process it left behind is killed and the reward is written last.
passed=$(python -I -B /grader/tests/score.py | tail -n 1)
kill -9 -1 2>/dev/null
case "$passed" in
0 | 1 | 2 | 3 | 4) ;;
*) passed=0 ;;
esac
python -I -B -c 'import sys; print(int(sys.argv[1]) / 4)' "$passed" > /grader/out/reward.txt
```

To report dimensions, write `reward.json` instead of `reward.txt`, for example `{"reward": 0.75, "dimensions": {"correctness": 0.75, "cases_passed": 3}}`. `graders/testdata/task/verifier/reward/` is a working example.

## Security notes

The verifier's output is untrusted: it ran on a workspace the agent wrote.

- **No planted result.** `/grader/out` is a volume the grader creates empty for each grade. The diff and the restore step only write inside the workspace, and `result_path` can only name a file directly in `/grader/out`. A `reward.json` the agent commits to its workspace, under any path, is never read.
- **Bounded reads.** The result file is copied out of the stopped container as a tar stream and read in memory: only the first entry, only a regular file, at most 16 MiB (JUnit) or 64 KiB (reward). A symlink or a folder at the result path reads as no file. Nothing is unpacked, so entry names cannot reach the host.
- **Bounded runs.** `timeout_s`, the CPU, memory and process limits, tmpfs sizes and the 64 KiB output buffer cap what a runaway verifier costs. `/grader/out` is a disk-backed volume; it is removed with the grade.
- **The verifier runs agent code.** Whatever the command imports or executes from the workspace runs as the same user and can write `/grader/out` too. A file it writes is overwritten if the trusted part of the verifier writes the result last, after killing leftover processes (the `kill -9 -1` above, run by PID 1). No grader can stop agent code from faking a measurement inside the process that measures it (patching `assert`, exiting 0 early): keep the agent's code in a child process where possible, reset configuration it could plant with `restore`, prefer `junit` or `reward` over `exit_code`, and let admission's exploit control and mutants prove the verifier cannot be gamed cheaply.
- **No network.** The verifier has no network at all; dependencies must be in the base image.
