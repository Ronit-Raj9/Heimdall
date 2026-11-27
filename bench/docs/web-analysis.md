# Analysis and governance on the website

## Verdict

The experiment page opens on **Verdict** (on **Matrix** for three or more
arms). It shows:

- the verdict card: primary metric under a condition, the interval, and an
  ACTION line (SHIP, HOLD, NO DECISION, FIX HARNESS) with the reason;
- **What changed between A and B** (config diff);
- the **Gate checklist**: integrity and instrument gates that fired;
- the **Task grid**: every task, condition and trial; click a cell to open
  the pair;
- a **Naive scoreboard**, labelled as not the release decision.

Other tabs: Runs, Cost (and cost per success frontier), Migration, Adaptive,
Scorecards, Receipts (fault receipts per probe), Integrity, Export, Actions.

## Analysis jobs

Experiment page, **Analysis** tab. Each is a background job with a log:

- **Re-check verdict** (`check`), **Code scan** (security findings on lines
  the agent added), **Eval awareness**, **Blame**, **Failure clusters**,
  **Results index**.
- **Blame**: for multi-agent runs, which agent caused each failure. Tier 1
  reads sealed gates; tier 2 replays and is opt-in.
- **Audit a published table**: paste or upload a per-task CSV
  (`task,a_pass,a_n,b_pass,b_n`) to get a verdict for someone else's numbers.

## Human review

**Govern > Review queue**: queues of runs to look at. On a run page you can add a
review: overrule a grade or flag the run. Sealed evidence is never edited;
the verdict can be read with review grades.

## Policies

**Govern > Policies**: release policies, the rules a candidate must pass.
**Create from default rules**, then edit checks (metric, condition, minimum
runs) in the form or the YAML. **Validate** uses the planner's parser.
Experiments reference them as `policies/<file>`.

## Releases

**Govern > Releases**: pick an experiment, see its verdict action and policy
decision, and record a release citing that exact analysis. The log is hash
chained. **Verify chain and evidence** re-hashes every record and evidence pack; use it before
an audit.

## Compliance

Releases page, pick a release, **Write compliance pack**: writes `compliance-<release>.md` and `.json`
(OWASP, NIST AI RMF, EU AI Act record keeping, sealed evidence links) into
`exports/compliance` in Project files.

## Monitors

**Govern > Monitors**: **Certify a monitor** or guardrail on labelled sealed
runs; see past certifications.

## Failures, leaderboard

- [Failures](/failures): one page with five tabs. **Production failures**
  become task drafts (accept, reject or snooze); **Findings** are tracked
  failures; **Failure groups** and **Failure patterns** group failed runs
  across benchmark runs and per comparison; **By agent version** shows where each
  version of an agent breaks.
- [Results > Leaderboard](/leaderboard): agent configs ranked by verified success,
  with tiers and trends.
