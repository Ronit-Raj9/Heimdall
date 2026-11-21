# Suites on the website

A suite is a benchmark pack: `benchmarks/<id>/` with `suite.yaml`, tasks,
hidden verifiers and a lock. Pages: **Build > Catalog** and **Build > Suites**.

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
tasks**. Admission proves each task: the reference solution passes and the
untouched repo fails. Hosted: admission runs as an experiment on a runner.

## Quarantine

**Ops** tab, **Quarantine** table: each task is Active or Quarantined.
**Quarantine** needs a reason (why it stays out of new plans); **Restore**
brings it back. **Quarantine flaky tasks** quarantines every task with
verifier flakiness evidence. Sealed results are never edited.

Other suite views: health, tiers, judge **Calibration** (for judged suites:
label runs, see agreement). Next: [web-experiments.md](web-experiments.md).
