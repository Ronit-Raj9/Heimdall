# Tasks from your own repository

Heimdall can benchmark your agents on work from your own codebase. A task is
cut from one commit that fixed something and changed its tests: the agent
starts from the commit before it, the fix becomes the reference solution, and
the tests the fix added or changed become the hidden verifier. This page walks
through it in about ten minutes and then explains what makes a good task, how
to write hidden tests by hand, and what admission does and does not prove.

The design contract is `docs/bring-your-own-design.md` ("The project folder",
"Suites in any language", "Tasks from your own repository"). The verifier
reference is `docs/verifiers.md`. The code is `bench/scaffold` (the
generators and `task init`), `bench/suite` (lint, validation, admission, the
lock) and `cmd/bifrost-bench` (`init.go`, `suite.go`, `task.go`).

## Ten minutes, start to finish

You need the `bifrost-bench` binary, Docker and a git repository with a
commit that fixes a bug and changes a test. Nothing calls a model until you
run an experiment with a real agent.

### 1. Make a project (1 minute)

```sh
bifrost-bench init ~/acme-bench
cd ~/acme-bench
```

This writes the project folder the contract describes: `deploy/` (bench.yaml
with relative paths, the compose stack, `.env.example`, the pricing snapshot,
Bifrost's config and datasheets, and Dockerfiles for the bench image and the
helper image), the free control agents in `agents/` (oracle, noop, refuse,
exploit, toy), an empty `benchmarks/`, `experiments/smoke.yaml`,
`policies/default.yaml`, a `.gitignore` that keeps `runs/` and `deploy/.env`
out of git, and a README with the next commands. Every command takes
`-config deploy/bench.yaml`, which is the default when you run from the
project folder. Keep the folder in your own repository if you like:
`task init` leaves it out of task snapshots.

Admission needs only Docker and the helper image. `task admit` builds the
helper image from `deploy/helper/Dockerfile` and the suite's base image from
its `base/Dockerfile` when they are missing. The gateway stack
(`docker compose ... up`) is needed later, for runs.

### 2. Make a suite (1 minute)

```sh
bifrost-bench suite init web -template node-test
```

Templates:

| Template | Grader | Verifier | Base image |
|---|---|---|---|
| `python-pytest` | `hidden_tests` | pytest with rootdir `/grader`, JUnit | `python:3.12-slim` (pinned by digest) + git + pytest |
| `node-test` | `verifier_command` | `node --test --test-reporter=junit` on the hidden test files | `node:22-slim` + git |
| `go-test` | `verifier_command` | `go test -json ./...`, turned into JUnit by `go-junit` (built into the image) | `golang:1.26-bookworm` |
| `generic-reward` | `verifier_command` | the task's `verifier/tests/test.sh` writes `/grader/out/reward.json` | `debian:bookworm-slim` + git |

Pass `-image <tag>` to use a toolchain image you already have instead of
building `base/Dockerfile`. The image must have a uid 1000 user, `/workspace`
and `/grader/out` owned by it, and `sh`.

The Node and Go templates run the hidden tests from a copy of the workspace
with the hidden files laid over it at their repository paths, because tests
cut from your repository import the code by relative paths (and a Go test
must sit in its package's folder). The Python template runs pytest from
`/grader`, so nothing the agent writes (a `conftest.py`, a pytest setting) can
take effect; `task init` adds a bootstrap `conftest.py` that puts the
workspace on `sys.path` and copies the repository's own `conftest.py` files
the hidden tests rely on.

### 3. Cut a task (2 minutes)

```sh
bifrost-bench task init -suite web -id t01 \
    -repo ~/src/webapp -base 4f2c1e0 -reference 9a7d3b2 \
    -capabilities test_discipline -difficulty medium -license proprietary
```

`-base` is the commit before the change (where the agent starts) and
`-reference` the commit with the change. `-repo` may be a folder inside a
repository, which limits the task to that folder; a reference commit that
changes anything outside it is refused. `task init` only reads the
repository, with plumbing commands (`git ls-tree`, `git cat-file`,
`git diff`); it never checks anything out and never writes to it, so the
state of your work tree does not matter.

It writes `benchmarks/web/tasks/t01/`:

| Path | What it is |
|---|---|
| `repo/` | the tree at `-base`, without `.git`. Test files the reference adds are not in it. |
| `verifier/tests/` | the test files the reference added or changed, at their repository paths, in their reference version |
| `solution/reference.diff` | `git diff base reference` without the test files |
| `controls/empty.diff` | an empty diff, which must fail |
| `controls/exploit.sh` | a generic exploit for the template (it rewrites the visible tests), which must fail |
| `instruction.md` | a draft from the reference commit's message, under an `<!-- EDIT ME -->` line |
| `task.yaml` | the task, with `protected` (the visible test files that existed at base), `required` test ids, `capabilities`, `difficulty` and `provenance` |

and adds `t01` to the `tasks` list of `suite.yaml`, keeping its comments and
layout. Test files are found with `-tests`, a comma-separated list of globs;
each template has defaults:

| Template | Default `-tests` |
|---|---|
| `python-pytest` | `tests/**`, `test_*.py`, `*_test.py` |
| `node-test` | `test/**`, `**/*.test.js`, `**/*.test.mjs`, `**/*.test.ts` |
| `go-test` | `**/*_test.go` |
| `generic-reward` | `tests/**` |

A glob without a `/` matches the file name at any depth; one with a `/` is
anchored at the repository root and `**` matches any number of folders.

`required` lists every test function of the hidden files for Python
(`tests/test_x.py::test_name`, `tests/test_x.py::TestClass::test_name`) and
Go (`<import path>::TestName`). Node test names cannot be read reliably
without running them, so Node tasks start with `required: []`; add
`test::<test name>` ids by hand when some tests matter more than others.

`task init` refuses, before writing anything: a base that is not an ancestor
of the reference, a change outside the chosen folder, a change with no test
files (pass `-tests`, or write the verifier by hand, below), a change with
only test files, a symlink that points outside the repository, and a tree
that holds a `.git` folder. It warns about binary files in the reference diff
(the diff carries them as `GIT binary patch`), files over 5 MB, test files
the reference deletes (they stay visible at their base version) and
submodules (left out). It finishes with a secret scan of the new folder.

### 4. Edit the instruction and review the tests (5 minutes)

Rewrite `instruction.md` as the request a teammate would send: what is wrong
or missing and how to tell when it is done. Do not name the tests, the grader
or the files that check the work, and do not paste the fix. Delete the
`<!-- EDIT ME -->` line and the comment under it.

Then read `verifier/tests/`. These tests were written beside the fix, so they
can encode the implementation rather than the behaviour: a test that asserts
a private helper's name, a log line or an exact error string fails a correct
fix that is written differently. Loosen such assertions, or delete the test
and keep the ones that check behaviour the instruction asks for.

### 5. Validate and admit (1 minute)

```sh
bifrost-bench task validate web/t01
bifrost-bench task admit web/t01
```

`task validate` needs no Docker. It reports, with file and line: the schema
and lint checks (`suite lint` runs the same lint), the EDIT ME marker, a
secret scan (private keys, `sk-` keys, AWS access key ids, GitHub and Slack
tokens, and high-entropy `*_TOKEN=` or `*_KEY=` values; the value itself is
never printed), hidden test files that are also visible in `repo/`, git
metadata in `repo/`, the task missing from `suite.yaml`, whether the
reference diff and every mutant apply cleanly to a temporary copy of
`repo/`, and a warning to review hidden tests that came from a reference
commit.

`task admit` runs the admission controls in Docker and records the result in
`suite.lock` (`suite admit <suite>` admits every task). A task that is not
admitted cannot be planned. Then set `suite:` in `experiments/smoke.yaml` to
your suite and run the free smoke experiment (it needs the gateway stack and
the variables of `deploy/.env`):

```sh
set -a; . deploy/.env; set +a
BENCH_SERVER_MODE=synthetic bifrost-bench run experiments/smoke.yaml
```

## What makes a good task commit

- **One change, one reason.** The commit fixes one bug or adds one feature.
  A commit that also renames things, reformats files or bumps dependencies
  turns the reference diff into a guessing game.
- **The tests fail before and pass after.** At least one hidden test must
  fail at the base commit, or the empty diff passes and admission refuses the
  task. Tests that pass either way still run, as regression checks.
- **The tests check behaviour, not shape.** Prefer asserting on return
  values, outputs and files over asserting on private names, call counts or
  exact messages.
- **The instruction can be written without the tests.** If you cannot
  describe the task without pointing at the tests, the agent cannot either.
- **It builds offline.** The verifier has no network. Dependencies must be in
  the base image or vendored in the repository; a Go module that needs a
  download, or a Node package that needs `npm install`, fails every grade.
- **It is deterministic.** No wall-clock, random seeds or network in the
  tests. Admission grades the reference three times in a row and refuses a
  task that flips.
- **It is small enough to run often.** A few hundred files at most; large
  binaries slow every run.
- **It holds no secrets.** The whole task folder is scanned at validation and
  admission. Replace real credentials with fakes that match no secret
  pattern.

Visible test files that existed at the base commit are listed under
`protected`, so an agent that edits them trips the protected-path gate. When
the fix legitimately has to change an existing test, that test's new version
is a hidden test and its old version stays visible and protected; say in the
instruction that the old behaviour is wrong, or remove the file from
`protected` if editing it is part of the job.

## Writing hidden tests by hand

When the fix commit changed no tests, or you want to test something the
commit's tests do not, write the verifier yourself. The quickest way keeps
`task init` doing the bookkeeping: on a scratch branch of your repository,
commit the fix together with the tests you wrote (cherry-pick the fix, add
the tests, commit), and pass that commit as `-reference` with the commit
before the fix as `-base`. To edit the hidden tests of an existing task
instead, work in its folder directly:

1. Put the test files in `verifier/tests/` at the paths they would have in
   the repository. They are mounted read-only at `/grader/tests`.
2. Make them import the code under test from the workspace:
   - **Python**: keep the generated `verifier/tests/conftest.py`, which puts
     `/workspace` (and `src/`, and the `pythonpath` of the workspace's
     pytest settings) on `sys.path`. Fixtures from the repository's own
     `conftest.py` files must be copied next to the tests.
   - **Node**: relative imports work, because the tests run from a copy of
     the workspace with the hidden files laid over it. Import builtins with
     the `node:` prefix so nothing in the workspace can shadow them.
   - **Go**: put each `_test.go` file at its package's path; the copy makes it
     part of the package.
   - **Generic reward**: replace the check in the generated
     `verifier/tests/test.sh`, remove its EDIT ME line, and write a number or
     `{"reward": n, "dimensions": {...}}` to `/grader/out/reward.json`.
3. List the tests that must run in `verifier.required` (formats in
   `docs/verifiers.md`), so a run where they are skipped or missing fails.
4. Optionally add `verifier/requirements.md`, mapping every hidden test to
   the instruction sentence it checks (lint warns without it), and mutants:
   diffs under `controls/` that are almost right and must fail, listed in
   `controls.mutants`.
5. Make sure no hidden test file is also in `repo/` (validation refuses an
   exact copy) and that skipped tests are intentional: a JUnit report with a
   skipped test fails the grade.

Then `task validate` and `task admit` as above.

## What admission proves, and what it does not

Admission (`task admit`, `suite admit`) records every check in `suite.lock`,
with the grader that ran them and the outcome of each reference run:

| Check | Want | It proves |
|---|---|---|
| `reference_1` .. `reference_3` | pass | the reference solution passes the verifier three times in a row: the verifier is deterministic for a correct fix |
| `empty_patch` | fail | the verifier does not pass the untouched repository |
| `mutant_<n>` | fail | each near miss you wrote is caught |
| `exploit` | fail | the exploit script (generic, or yours) cannot make the tests report success |
| `reference_stale_notes` | pass | when the task has stale notes, the reference still passes with them laid over |
| `null_noop`, `null_refuse` | fail | an agent that changes nothing, or refuses, is graded a failure in a real workspace |
| `instruction_edited` | edited | the draft marker is gone |
| `secret_scan` | none | no task file matches a secret pattern |
| `verifier_no_network` | none | the verifier container runs with no network |

It does **not** prove:

- that the hidden tests check what the instruction asks. Only a person
  reading both can say that, which is why validation reminds you to review
  tests cut from a reference commit.
- that every correct fix passes. The reference is one correct fix; tests that
  encode its implementation fail others. Mutants and a careful review are the
  defence.
- that the verifier cannot be gamed by a determined agent. The exploit
  control tries one cheap trick; the clean room (pristine repo, restore list,
  read-only workspace, fresh result folder, no network) closes the common
  ones. See the security notes in `docs/verifiers.md`.
- that the task is hard, or worth measuring. Difficulty and capabilities in
  `task.yaml` are your labels.
- anything about a different version of the task. An admission holds for the
  exact hashes in the lock: change the instruction, the repo, the verifier or
  the controls and the task goes back to draft until it is admitted again.

## Mining tasks from history: `tasks mine`

Cutting tasks one commit at a time is the precise path. `heimdall tasks mine`
(also `task mine`; `bifrost-bench` works the same) is the fast one: it walks
your git history, picks the commits that changed both code and tests, cuts
each with the same `task init` machinery, and keeps only the tasks admission
proves. It is step 2 of `docs/quickstart.md`.

```sh
heimdall tasks mine -limit 10                 # newest first, until 10 are proven
heimdall tasks mine -since v1.4.0 -paths src/billing,src/tax -min-tests 1
heimdall tasks mine -dry-run                  # ranked candidates and skip reasons only
```

Run it from inside the repository after `heimdall init`; it finds
`.heimdall/deploy/bench.yaml` and the suite init wrote. Outside a
`.heimdall` project pass `-config` and `-suite`.

What it does for each commit, newest first (at most `-scan`, default 300):

| Step | Rule |
|---|---|
| Candidate | one parent; base = the parent, reference = the commit; hidden tests = the test files the commit added or changed (the suite template's test globs) |
| Skipped, with the reason printed | merge commit; root commit; no test file changed; only test files changed; fewer than `-min-tests` test files; nothing under `-paths`; a test file deleted; a binary file changed; more than `-max-files` (12) files or `-max-lines` (400) lines changed; a test that needs the network (an HTTP client or socket call in the test, or a remote URL in the test while the commit's code makes a network call); a test that reads a credential from the environment (`*_API_KEY`, `*TOKEN*`, `*SECRET*`, `*PASSWORD*`) |
| Ranked | small diffs, few files and few test functions first (fast to prove and to run), interleaved across top-level folders so the first picks cover different areas |
| Cut | `task init` from the parent to the commit into `benchmarks/<suite>/tasks/m-<commit>`; the `.heimdall` folder is left out of the snapshot |
| Drafted | `instruction.md` from the commit subject and body and the names of the hidden tests, never from the diff; body sentences that say how the fix was made ("use", "instead", "replace", "the fix", ...) are dropped, and test names become plain phrases ("test_no_edge_hyphens" becomes "no edge hyphens") |
| Marked | `provenance.created_by: tasks mine` and `provenance.instruction_reviewed: false` (an additive field); no EDIT ME marker, so the task can be proved and run at once |
| Secret scan | the same scan as `task init`; a finding drops the task |
| Proved | the existing admission, run in the suite's verifier container: the reference passes 3 times, the empty patch fails (the hidden tests fail at the parent), the exploit and the null agents fail, the verifier has no network. An admitted task is recorded in `suite.lock` |
| Dropped | a task that is not admitted is removed again and `suite.yaml` restored, with the failed checks printed |

It stops after `-limit` proven tasks. A task already in the suite (the same
commit mined before) counts as proven and is left alone. The repository is
only read, with the same plumbing as `task init` (`ls-tree`, `cat-file`,
`diff`, `log`): no checkout, no worktree, no lock files, no hooks.

### Unreviewed instructions

A drafted instruction is a draft. Every report (`report.md` of `run`, `check`
and `compare`) and the verdict card say "N of M tasks have unreviewed
instructions" while any task of the experiment still has
`instruction_reviewed: false`. Read each `instruction.md`, rewrite what a
teammate would not say, then:

```sh
heimdall task review minitext/m-56d8921cd6     # or just the task id inside a .heimdall project
```

`task review` sets `instruction_reviewed: true` in task.yaml. The whole task
folder, task.yaml included, is part of the task's lock fingerprint, so the
mark (and any edit to `instruction.md`) clears the task's admission; `task
review` therefore proves the task again with the same admission ($0, Docker
only) and records it. `-no-admit` only marks it, and you run `task admit`
before the next experiment.

`tasks mine` writes `suite.lock` once, at the end. Adding task ids to
suite.yaml changes the suite fingerprint, which alone would clear every
earlier admission, so the miner carries a record (this run's proofs and the
previous lock's admissions) only when the task's own fingerprint and the
suite's image identities are the ones it was admitted against and suite.yaml
differs from before only in its tasks list; any other record is dropped with
a warning naming the task to admit again.

### Limits of mining

- The draft can still say too much: a commit body that names the exact line
  to change survives the filter when it does not use a "how" word. Review
  before you trust a verdict.
- The skip rules for network and secrets read the test files and the changed
  code; a test that reaches the network through a helper in another file is
  not caught statically. Admission catches it anyway (the verifier has no
  network, so the reference fails), at the cost of one proof.
- The default test globs are the suite template's. A repository that keeps
  tests elsewhere needs its globs in the suite (`task init -tests` takes
  them; `tasks mine` uses the template's).
- Proof time is the admission's: about 3 minutes per task on an 8-core
  machine under heavy load (load average above 40), mostly the 3 reference
  gradings and the 2 null-agent workspaces.
