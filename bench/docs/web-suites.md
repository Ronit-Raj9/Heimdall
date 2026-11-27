# Suites on the website

A suite is a benchmark pack: `benchmarks/<id>/` with `suite.yaml`, tasks,
hidden verifiers and a lock. Pages: [Benchmarks](/suites) in the sidebar, with its **Catalog** tab.

## Catalog

**Catalog** lists Examples, Bundled suites and Public benchmarks.

- **Add** copies a bundled suite and its agents into your project in one click.
- **Import** (public benchmarks: SWE, tau2, AgentDojo, MCPMark, Harbor and
  more) asks for the data path on the server, a suite id and a task count,
  then **Start import** runs a job. Its log shows below; the suite appears
  when it finishes.

## Create

**Suites > New suite**: pick a suite id and a template (python-pytest,
node-test, go-test, generic-reward, tool-use, mcp, browser, memory, qa,
external-api, artifact, computer-use, support, research, voice). Each comes
with a complete sample task. Optional existing toolchain image; empty builds
`base/Dockerfile` from the template. **Create suite**.

## Tasks and files

Open a suite, **Edit** tab:

- **Add task**: id, title, instruction (what a teammate would ask for, never
  how it is checked). **Create task**.
- **Files**: pick a file to edit and **Save**, **Add file** by path, **Upload
  file** (up to 1 MiB) or **Upload zip into folder** for anything larger.
- **Lint** (**Check now** inline, or **Lint (job)**), then **Lock** writes
  `suite.lock` from the files as they are.

From a repository (**Ops** tab, needs a GitHub connection or a local folder):

- **Task from a commit**: task id, base commit, reference commit, optional
  test globs. **Create task**.
- **Mine tasks from history**: since (commit, tag or date), tasks to cut,
  **Preview candidates** first, then **Mine tasks**. Only proven tasks are kept.

## Admit

**Ops** tab, **Checks and admission**: **Lint**, **Lock**, **Admit all
tasks**. Admission proves each task: the reference solution passes three
times; the empty answer, every near miss and every wrong answer fail; a
gaming attempt scores nothing; doing nothing and refusing fail. Hosted:
the same controls run as one experiment on the organization's worker, and
the record holds the same check names as a local `suite admit`. Two checks
only run locally (the honest solution of an impossible task, stale project
notes); hosted records them as "not run on this worker".

Judged benchmarks: the judge must grade the controls, so hosted readiness
checks call the pinned judge model on the organization's own key. The Build
tab shows about how many calls (three reference runs plus each near miss and
wrong answer that reaches the judge, times the votes) and asks for consent
first; the job refuses without it (`judge_consent`).

Judged rows need a wrong answer the judge actually sees: an empty answer, a
refusal or text aimed at the grader fails before the judge is asked. When a
row names no wrong answers, the import writes some from the row
(`generated_wrong_answers` in `verifier/answer.yaml`): the reference with a
number changed, a statement negated, yes and no flipped, another row's
answer, and for a rubric one reply per criterion that misses it. A task
written from your rows without one is a lint error; any other judged task
gets a warning, so library benchmarks keep their admissions.

## Quarantine

**Ops** tab, **Quarantine** table: each task is Active or Quarantined.
**Quarantine** needs a reason (why it stays out of new plans); **Restore**
brings it back. **Quarantine flaky tasks** quarantines every task with
verifier flakiness evidence. Sealed results are never edited.

Other suite views: health, tiers, judge **Calibration** (for judged suites:
label runs, see agreement). Next: [web-experiments.md](web-experiments.md).
