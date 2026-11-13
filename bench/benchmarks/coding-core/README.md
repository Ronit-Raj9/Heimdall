# coding-core benchmark pack

Eight self-authored Python tasks in two small projects. The Python standard library and pytest are the only dependencies, so one base image (`base/Dockerfile`, tag `bench/coding-core-base:1`) serves every task, the grader and every agent image.

| Tasks | Project | Folder |
|---|---|---|
| t01-t04 | `ledger`: invoices, discounts, tax, CSV import and export | `tasks/t01` ... `tasks/t04` |
| t05-t08 | `gatekeeper`: rate limiting, configuration, deduplication, a small public API | `tasks/t05` ... `tasks/t08` |

Every task carries its own full copy of its project in `repo/`, so a task never depends on another task's folder.

## Task folder

```
tasks/t01/
  task.yaml                 # metadata, see below
  instruction.md            # what the agent is asked to do. What, never how
  repo/                     # the project as the agent first sees it. No CLAUDE.md or AGENTS.md here
    ledger/ ...             # the package
    tests/ ...              # visible tests. They pass at baseline
  fixtures/
    notes.clean.md          # accurate project notes, installed as CLAUDE.md or AGENTS.md in clean runs
    notes.stale.md          # the stale-notes probe: same notes with at least one claim that contradicts the code
  solution/patch.diff       # reference fix, `git apply -p1` against repo/
  controls/
    empty.diff              # empty file
    mutant1.diff            # a plausible wrong fix: passes the visible tests, fails the hidden tests
    exploit.sh              # tries to cheat: forces the visible tests to pass without fixing anything
  verifier/
    tests/                  # hidden tests. Never copied into the agent's workspace
      conftest.py           # puts /workspace on sys.path (the grader runs python -I)
      test_*.py
    requirements.md         # maps every hidden test to the sentence of instruction.md it checks
```

## task.yaml

```yaml
id: t01
version: 1.0.0
title: Fix rounding in invoice totals
project: ledger
kind: bugfix                 # bugfix | feature | refactor | performance
instruction: instruction.md
repo_dir: repo
notes:
  clean: fixtures/notes.clean.md
  stale: fixtures/notes.stale.md
  stale_claims:              # what the stale notes get wrong, for the report
    - "says amounts are float dollars; the code stores integer cents"
verifier:
  tests: verifier/tests
  required: ["test_hidden_rounding.py::test_half_cent_rounds_up"]   # test ids that must run and pass
protected: ["tests/**"]       # existing files whose hashes must not change (gate G1)
controls:
  reference: solution/patch.diff
  empty: controls/empty.diff
  mutants: [controls/mutant1.diff]
  exploit: controls/exploit.sh
timeouts:
  agent_s: 900
```

The suite file (`suite.yaml`) sets the grader command, the restore list and the workdir for every task.

## Grading, exactly

1. Copy `repo/` into a fresh workspace.
2. Apply the agent's diff (or a control patch).
3. Restore the suite's `verifier.restore` paths from `repo/`.
4. Run, with no network, workspace mounted read-only at `/workspace` and hidden tests at `/grader/tests`:
   `python -I -B -m pytest -q -p no:cacheprovider --rootdir=/grader --junitxml=/grader/out/junit.xml /grader/tests`
5. Pass only if the exit code is 0 and the JUnit file shows more than 0 tests with 0 failures, 0 errors and 0 skipped.

`tools/checktask.sh tasks/<id>` (or `bash tools/checktask.sh ...`) runs this procedure for the reference, the empty patch, every mutant, the exploit and both notes files.

## Rules for task authors

- **Every hidden assertion maps to a sentence in `instruction.md`.** Never test a behavior the instruction does not ask for. Record the mapping in `verifier/requirements.md`. This is the most common defect in public benchmarks.
- The instruction says what is needed, never how to do it, and never mentions notes files, hidden tests or graders.
- Visible tests in `repo/tests/` pass at baseline and do not reveal the hidden assertions.
- Hidden tests check behavior through the public API or CLI, not private names, so any correct implementation passes.
- The stale notes are goal-preserving: the reference fix still passes with them present. Following a stale claim must lead a careful agent toward a wrong fix or a wasted step (for example a test command that collects nothing).
- Pick difficulty so a capable coding agent solves the task about half the time: more than a one-line fix, less than an afternoon.
- Nothing is copied from any existing benchmark, company or dataset. Everything here is written from scratch.
