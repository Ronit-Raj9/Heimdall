# Bring your own agent and tasks: design contract

Status: build contract for the parallel build of 14 Nov 2026. The user guide
is `docs/bring-your-own.md` (written after the build); this file fixes the
names and shapes every part must agree on.

## Goal

A company keeps a small folder in its own repository and, without writing Go
or touching this repository, can:

1. describe its own agent (a CLI, a script, or a service) in one YAML file,
2. turn its own repositories and commits into benchmark tasks with hidden
   verifiers, in any language,
3. run the same experiments, faults, gates, verdicts, leaderboard and CI gate
   the built-in suite gets.

Nothing leaves the company's machine except model calls through Bifrost.

## The project folder

`bifrost-bench init <dir>` scaffolds a project. Every command already takes
`-config <dir>/deploy/bench.yaml`; the bench root is that file's grandparent.

```
<project>/
  deploy/bench.yaml                 # paths, gateway URL, networks, images
  deploy/docker-compose.yml         # Bifrost + Postgres + fault proxy
  deploy/.env.example
  agents/<agent>.yaml               # one file per agent
  agents/images/<agent>/Dockerfile  # optional: how to build the agent image
  benchmarks/<suite>/suite.yaml
  benchmarks/<suite>/base/Dockerfile        # the suite's toolchain image
  benchmarks/<suite>/tasks/<task>/...       # task.yaml, instruction.md, repo/, verifier/, solution/, controls/
  experiments/<name>.yaml
  policies/default.yaml
  runs/                             # sealed run folders (git-ignored)
```

## Suites in any language

A suite names a base image (its toolchain) and a verifier command. The
verifier runs in a fresh container from the base image with no network, on a
pristine copy of the task repo with the agent's diff applied and the hidden
verifier files mounted at `/grader`.

`SuiteVerifier` (schemas/suite.go) reports in one of three ways:

| `verifier.result` | The command must | Pass rule |
|---|---|---|
| `junit` (default) | write JUnit XML at `result_path` (default `/grader/out/junit.xml`) | no failures or errors, at least one test, every `required` test ran and passed |
| `reward` | write a number, or `{"reward": n, "dimensions": {"name": n}}`, at `result_path` (default `/grader/out/reward.json`) | `reward >= pass_threshold` (default 1) |
| `exit_code` | exit 0 to pass | exit code 0 |

`result_path` must name a file directly inside `/grader/out`, a folder the
grader creates fresh for every grade, so the agent cannot plant a result.

A missing or unreadable result, or a timeout, is a fail with reason
`test_run_broken` (never "unknown"), because the reference solution passed
admission. `Grade` (schemas/evidence.go) carries `result_format`, `reward`
and `dimensions`; each dimension becomes a report-only metric
`reward_<name>` and is never blended into `verified_success`.

Grader part name: `verifier_command`. `hidden_tests` stays as it is and keeps
working for existing suites; `verifier_command` handles all three formats.

`bifrost-bench suite init <id> -template <name>` writes `suite.yaml` and
`base/Dockerfile` from a template: `python-pytest`, `node-test` (Node's
built-in runner with `--test-reporter=junit`, no dependencies), `go-test`,
`generic-reward` (a `tests/test.sh` that writes the reward file; the Harbor
task shape).

## Tasks from your own repository

```
bifrost-bench task init -suite <suite> -id <task> \
    -repo <git repo path> -base <commit> -reference <commit> \
    [-tests <glob,...>] [-license <spdx|proprietary>] [-capabilities a,b]
bifrost-bench task validate <suite>/<task>
bifrost-bench task admit <suite>/<task>        # one task; `suite admit <suite>` does all
```

`task init`:

- `repo/` is the tree at `-base` (no `.git`), minus files matched by `-tests`
  that the reference commit adds.
- `solution/reference.diff` is `git diff <base> <reference>` limited to
  non-test files.
- `verifier/tests/` holds the test files the reference commit added or
  changed (the tests that fail at base and pass at reference). Test files
  that exist at base stay visible in `repo/` in their base version and are
  listed under `protected`.
- `instruction.md` starts from the reference commit's message and must be
  edited by a person; `task validate` fails while it still holds the marker
  line `<!-- EDIT ME -->`.
- `controls/empty.diff` is generated (no change). Mutants are optional.
- `task.yaml` records `provenance` (source, base_commit, reference_commit,
  license, created_by: "task init").

The hidden tests come from the reference commit, so `task validate` prints a
warning asking the author to review them: tests written beside the fix can
encode the implementation too closely.

Admission (`task admit`, `suite admit`) must prove, and records in
`suite.lock`:

1. the reference solution passes, 3 times in a row (determinism),
2. the empty solution fails,
3. every mutant fails (when mutants exist),
4. the null agents (noop, refuse) fail and the exploit control fails,
5. no file in the task matches the secret patterns (`-----BEGIN .* PRIVATE
   KEY`, `sk-`, `AKIA`, `ghp_`, `xox[bap]-`, high-entropy `*_TOKEN=`/`*_KEY=`
   assignments),
6. the verifier ran with no network.

## Your own agent

```
bifrost-bench agent init <id> -template <command|python|http-service>
bifrost-bench agent build <agents/id.yaml> -suite <suite>
bifrost-bench agent check <agents/id.yaml> -suite <suite> [-task <task>]
```

- An agent is anything that can be started in a container, reads the task
  from a file or argument, edits the workspace, and sends its model calls to
  the base URL and key it is given (`{{gateway.url}}`, `{{run.key}}`). No SDK.
- `build.base_arg` (schemas `ImageSpec.BaseArg`): the agent's Dockerfile
  declares that ARG and starts `FROM ${<arg>}`; the image is built once per
  suite on that suite's base image and tagged `<tag>-<suite id>`. This is how
  one agent works inside every suite's toolchain. Without `base_arg` the one
  image is used for every suite, as today.
- Template `http-service` is for agents that are servers (LangGraph,
  FastAPI, an Agents SDK app): the generated agent YAML starts the server in
  the run container, waits for its health URL, POSTs the task
  (`{instruction, workspace, base_url, api_key, model, session_id}`), waits
  for the response and exits. It uses the `command` adapter and a launcher
  script delivered through `files`; no engine change.
- `agent check` is a conformance test that costs nothing: it runs one task
  with the fault proxy answering from its scripted model for that run
  (fault plan mode `scripted`, whatever provider the agent calls and whether
  or not a real key is configured), and reports, as PASS/WARN/FAIL rows:
  image builds; container starts as the unprivileged user; a model call
  reached Bifrost with the run key; the session id was sent; the agent
  exited by itself inside the budget; a diff was exported; no egress outside
  the gateway; which evidence level its runs get (gateway only, or native
  parser); whether a final message (completion claim) was captured.

## What stays out

- No hosted service, sign-on or roles.
- No remote agents that cannot be started in a container on the bench host:
  a coding task needs the agent next to the workspace.
- Importers for public benchmark formats (Harbor) reuse the `reward`
  verifier contract and are a separate piece of work.
