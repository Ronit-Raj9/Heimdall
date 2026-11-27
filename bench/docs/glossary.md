# Glossary

Heimdall tests AI agents. This page explains every word the app uses, in plain language. Each entry gives the plain word you see on screen first, then what it means. The technical name (used in the command line, the API and older docs) is in brackets, for experts.

Tip: anywhere in the app, a word with a dotted underline has an explanation. Hover over it or move to it with the Tab key.

## Benchmarks and tasks

- **Benchmark** (technical: `suite`). A set of tasks the agent is tested on.
- **Task** (technical: `task`). One job the agent must do, checked automatically.
- **Ready** (technical: `admitted`). Heimdall checked the task is fair: a correct solution passes and an agent that does nothing fails.
- **Readiness check** (technical: `admission`). Heimdall checks each task is fair: a correct solution passes and an agent that does nothing fails.
- **Not ready** (technical: `draft`). Failed the readiness check, so it is not used in tests.
- **Frozen** (technical: `locked / suite.lock`). The tasks are pinned so every run sees exactly the same version.
- **Stress test** (technical: `probe`). A harder version of the tasks: a model outage, a trick instruction, a fake secret.
- **Difficulty level** (technical: `tier`). How hard the tasks are. Higher levels add stress tests on top of the normal tasks.
- **Skills tested** (technical: `capabilities`). The kinds of ability a benchmark checks, such as coding, browsing or tool use.
- **License** (technical: `license`). The terms under which the tasks may be used and shared.

## Evaluations and report cards

- **Evaluation** (technical: `evaluation`). One request to test your agent on many benchmarks at once. Heimdall runs one comparison per benchmark and puts the results together in a report card.
- **Report card** (technical: `report card`). The result of an evaluation for one agent version and model: a score per skill and per benchmark with a range of likely values, cost, tokens, steps, time, how much the score drops under faults, and safety numbers.
- **Pack** (technical: `pack`). A ready-made group of benchmarks for one skill, such as coding, support or memory. "All compatible" means every benchmark your agent can run.
- **Capability** (technical: `capability`). Something an agent can do, or a benchmark needs: shell, tools, MCP, sessions, browser, desktop, voice or GPU. A benchmark is greyed out when your agent lacks what it needs.
- **Version** (technical: `agent version`, `v<N>`). A saved snapshot of an agent's settings. Every change makes a new numbered version, so a report card always says exactly which setup earned it.
- **Baseline** (technical: `baseline`). The setup you compare against. In an evaluation it can be your current version, or a well-known built-in agent (Codex, Claude Code, mini-swe-agent, OpenCode, Gemini CLI), which the agent leaderboard marks as a baseline.
- **Fault** (technical: `fault` / `condition`). Something going wrong on purpose during a run: the model goes down, a tool breaks, or the instructions mislead. Normal conditions always run too.
- **Score range** (technical: `confidence interval`, `ci_low` / `ci_high`). The range the true score most likely falls in. Few runs give a wide range.

## Comparisons and results

- **Comparison** (technical: `experiment`). Runs two setups (A and B) on the same tasks and tells you which is better.
- **Setup A (baseline)** (technical: `arm A`). The setup you have today. Setup B is compared against it.
- **Setup B (candidate)** (technical: `arm B`). The new setup you want to try. It is compared against Setup A.
- **Result** (technical: `verdict`). What the comparison concluded: B is better, B is worse, no clear winner yet, or the test was broken.
- **B is better** (technical: `improved`). Setup B solved more tasks than Setup A, and the difference is clear.
- **B is worse** (technical: `regressed`). Setup B solved fewer tasks than Setup A, and the difference is clear.
- **No clear winner yet** (technical: `INCONCLUSIVE`). Not enough evidence; run more tasks.
- **Test was broken** (technical: `INVALID`). Something went wrong while measuring (not the agent's fault). Fix and re-check.
- **Run** (technical: `run`). One attempt by one agent at one task.
- **Saved and tamper-proof** (technical: `sealed`). The results are stored with a fingerprint, so any later change would be detected.
- **Test recipe (fingerprint)** (technical: `manifest / manifest hash`). The exact settings of this comparison, fingerprinted.
- **Scores / Re-score** (technical: `grades original / regrade`). Scores are the grader's marks for each run. Re-score grades the same saved work again.
- **Measurements** (technical: `meters / metrics`). Numbers recorded for each run, such as cost, time, steps and tokens.
- **Details by condition** (technical: `diagnostics`). The same results split by condition (normal conditions and each stress test).
- **Normal conditions** (technical: `condition (clean)`). The tasks as written, with no stress test added.
- **Statistics (advanced)** (technical: `paired sign-flip p-value / MDE`). How sure we are that the difference between A and B is real, and the smallest difference this test can detect.

## Grading and safety

- **Grader / hidden checks** (technical: `verifier / hidden tests`). Tests the agent never sees that decide if it solved the task.
- **Unreliable grader** (technical: `flaky verifier`). The grader gave different answers for the same work.
- **Grader accuracy check** (technical: `calibration`). Compares the grader's marks with human marks to see how often it is right.
- **Safety check** (technical: `gate / veto gate`). A rule that fails a run that cheated or did something unsafe.
- **Safety rule** (technical: `guard`). A rule that watches what the agent does and stops or flags unsafe actions.
- **Cheating or unsafe action detected** (technical: `integrity violation`). The agent tried to cheat (for example, reading the hidden checks) or did something unsafe. The run fails.
- **Infrastructure error (not counted)** (technical: `aborted_infra`). The run broke before the agent could work; it is retried.
- **Perfect reference agent** (technical: `oracle`). An agent that applies the known correct solution. Used to check that a task can be solved.
- **Do-nothing agent** (technical: `noop agent`). An agent that does nothing. Used to check that a task cannot be passed for free.
- **Practice mode (no real model, free)** (technical: `synthetic / scripted model`). Runs with a scripted stand-in model instead of a real one. Free, good for trying things, not real results.

## Production and release

- **Drift monitor** (technical: `canary`). A small test re-run on a schedule to catch an agent or model silently getting worse.
- **Production failures** (technical: `inbox`). Failures collected from real use, waiting to be turned into new tasks.
- **Where failures come from** (technical: `trace source`). A system that sends failed agent sessions into the failure inbox.
- **Live quality scoring** (technical: `livescore`). Scores real agent sessions as they happen, so you see quality in production.
- **Release rule** (technical: `policy`). A rule a new agent version must pass before it can be released.
- **Release record** (technical: `release`). A saved record that an agent version shipped, with the evidence behind it.

## Machines

- **Worker machine / shared worker** (technical: `runner / shared runner`). A machine that actually runs the agents.
- **Reserved place on a worker** (technical: `lease / slot`). Internal bookkeeping for which worker runs which task. You can usually ignore it.

## How the pieces fit

1. You register your **agent** and start an **evaluation**: pick a **pack** or single **benchmarks** (sets of tasks). Benchmarks that need a **capability** your agent lacks are skipped.
2. Heimdall runs a **readiness check** on each task. Only **ready** tasks are used.
3. A **comparison** runs Setup A and Setup B on the same tasks. Each attempt at one task is a **run**.
4. A hidden **grader** scores every run. **Safety checks** fail runs that cheated or did something unsafe.
5. The **result** says B is better, B is worse, no clear winner yet, or the test was broken.
6. The **report card** puts every benchmark's numbers together, per skill, for one agent **version** and model.
7. In production, **drift monitors** re-run small tests on a schedule, and **release rules** decide whether a new version may ship.
