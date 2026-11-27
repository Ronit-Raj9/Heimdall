# Experiments on the website

An experiment runs arms (agent and model pairs) on the same tasks, clean and
under probes, then writes a paired verdict. Pages: **Benchmark > Experiments**
and the experiment page.

## QuickRun

The **Run** button (welcome wizard, Home, experiment pages) opens **Quick
run**: suite, agent, model and run size. It fills in whatever is missing and
starts in one call. Without a second model it runs one arm and the
experiment page shows its score with a 95% interval (no A against B
verdict). With a second model it runs arm B too and gives a verdict.
**Start run**.

## Builder

**Experiments > New experiment**:

1. Pick the suite, the agents and the models. Every agent runs with every
   model; **Matrix preview** shows the arms in order. Arm A is the baseline.
2. **Extra conditions (probes)**: clean always runs; each probe (model outage, slow tools and more)
   adds a condition with the same tasks.
3. Run size: first N tasks or all, **Trials per task** (optionally
   **Adaptive trials**), **Budget (USD)**, concurrency. Optional **Runner pool**.
   In local mode paste the serve **Write token** first.
4. **Estimate** forecasts cost and time from sealed history. **Plan** checks
   the experiment and shows its power. Change the form and you must plan again.
5. **Run experiment**. It freezes the plan (manifest hash) and queues runs.

Runs execute on a runner, never in the web server
([web-runners.md](web-runners.md)). The Jobs drawer and the experiment page
show progress; **Cancel** and **Resume** are on the page.

## Follow-ups (experiment page)

- **Extend** (Actions tab): an inconclusive verdict needs more evidence. Add tasks the
  experiment did not run, or more trials. It starts a follow-up experiment
  analysed with its parent. Tick "Extend anyway" to extend a decided verdict.
- **Probe sweep** (Actions tab): run the suite's declared probes against the same arms.
  No probes declared: add them to `suite.yaml` first.
- **Compare models** (Actions tab, migrate): one agent across several models on a suite,
  the model migration matrix. The **Migration** tab shows the report.
- **Regrade** (experiment page header): re-run graders over sealed runs at
  current gate versions as a job. The original grades stay; the Verdict tab's
  grade picker reads the regrade.

All of these confirm first because they spend model budget.

## Compare

**Runs** (also `/compare`): **Compare agents or models** starts a new
two-arm comparison. Pick a matched pair from the task grid to see both arms'
trace, metrics, the agent's claim and the hidden result side by side.

Comparing two git refs of your own repo (`compare --baseline main --candidate
HEAD`) needs a checkout of that repo. On the website use the CI gate in
**live** mode, which runs baseline and candidate refs for every pull request
([web-integrations.md](web-integrations.md)), or run it with the CLI and
**Upload** the sealed runs.

## Checks: repeat runs and drift monitors

- **Checks > Repeat runs**: run an evaluation or comparison on a cron with a
  daily USD cap. **Run now** fires it once and always asks first, naming the
  daily limit.
- **Checks > Drift monitors**: a drift monitor re-runs a pinned benchmark against a
  known-good baseline. **Create drift monitor**, record its baseline, then run.
  Practice mode answers every model call from a scripted model: no model
  cost, and it cannot find real drift.

## Archive

Evaluations, comparisons, runs, auto-improve runs and findings have
**Archive** in their row menu or the page's **More** menu. An archived item
is hidden from its list; **Show archived** in the list's toolbar brings it
back into view with an "Archived" pill, and **Unarchive** returns it to the
list. Nothing is deleted: the item's page, its share links, leaderboards,
verdicts, report cards, usage and spend stay exactly as they were.

Next: [web-analysis.md](web-analysis.md).
