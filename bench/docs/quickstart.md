# Quick start: will this change break my coding agent?

Four commands, run from inside your own git repository, take you from
nothing to a verdict on a change to your coding agent (its notes file, its
skills, its prompt, its model) measured on tasks cut from your own history:

```sh
heimdall init                       # detect tests, agent CLIs, Docker, ports; write .heimdall/
heimdall tasks mine -limit 10       # cut tasks from fix commits; keep only the proven ones
heimdall compare --baseline main --candidate HEAD --base agents/claude-code.yaml --trials 3 --fault model-outage --budget-usd 5
heimdall ci install github          # the same gate on every pull request
```

`heimdall` is the same program as `bifrost-bench` (`make heimdall` in
`bench/` builds `bin/heimdall`; `make bin` builds `bin/bifrost-bench`); usage
text prints the name you started it as, and every command below works under
either name. Nothing calls a model until `compare` runs a real agent, and
`compare` shows its forecast and asks first.

`compare` exits with `check`'s codes when it runs (0 ok, 1 regressed, 2
inconclusive, 3 invalid, each only when listed in `--fail-on`, 4
infrastructure, 5 policy block). When it does not run (you answer no, or
stdin is not a terminal and `--yes` is not given), it exits **6**: no
verdict exists, so a script never reads a skipped compare as a pass. Only
`--write-only` stops before running and exits 0, because you asked for it.

The rest of this page is a walk-through run for real at $0: a throwaway
Python library with a history of small fixes, the four commands, the output
they printed and how long each step took. Then what a real user still does by
hand, and the limits.

## The walk-through

Run on 18 Nov 2026 on a shared 8-core Linux host (load average 35 to 45 from
other work the whole time), with the gateway stack that was already running
on 127.0.0.1:18080 / 19101 and the images already built. Only scripted agents
on the anthropic provider ran; the stack has no Anthropic key, so the fault
proxy's scripted model would have answered any model call. Total spend $0.

| Step | Command | Wall time | Exit |
|---|---|---|---|
| 0 | build the repository (author's script, not a user step) | - | - |
| 1 | `heimdall init` | 3.6 s | 0 |
| 2a | `heimdall tasks mine -dry-run` | 0.6 s | 0 |
| 2 | `heimdall tasks mine -limit 10` | 28 min 9 s | 0 |
| 2b | `heimdall task review <task>` (marks it, proves it again) | 74.8 s | 0 |
| 3 | `heimdall compare --a agents/oracle.yaml --b agents/noop.yaml --trials 1 --fault model-outage` | 21 min 39 s | 5 |
| 3b | `heimdall compare --baseline HEAD~1 --candidate HEAD --base agents/claude-code.yaml --budget-usd 5 --write-only` | 0.1 s | 0 |
| 4 | `heimdall ci install github -dry-run` | 0.0 s | 0 |
| 4b | the workflow's gate locally: `ci/check.sh recorded` on the sealed run, then `heimdall ci card` | 0.9 s | 5 |

Command time in total: 51 min 8 s, almost all of it Docker work: 28 min
proving 10 tasks (109 to 229 s each) and 22 min for 40 scripted runs at
concurrency 2. On this loaded host the 30-minute target is not met; the
commands themselves need no waiting on a person except the review.

### 0. The repository

`minitext`, a small Python text library written for this page (by the
script `make_repo.py` of this walk-through, outside the repository): 19 commits,
of which 12 are small fixes or features with their tests (slugify edges,
truncate width, durations with hours, Roman numerals, word wrap, camel case
acronyms, word counts, title case on a merged branch, dedent with tabs,
accented slugs), plus one of each kind the miner must skip: the root commit,
a docs-only commit, a tests-only commit, a merge, a binary file, a test that
calls the network, and an agent-notes change. One fix commit adds a test
that already passed before it, so admission must refuse it. `python -m
pytest` passes at HEAD (22 tests).

### 1. `heimdall init`

```console
$ cd ~/minitext && heimdall init
repository  ~/minitext
tests       python: python -m pytest  [high confidence; pyproject.toml [tool.pytest.ini_options]]
suite       template python-pytest
agent CLI   claude   2.1.312 (Claude Code)  (~/.local/bin/claude) -> agents/claude-code.yaml
agent CLI   codex    codex-cli 0.169.1  (~/.local/bin/codex) -> agents/codex.yaml
agent CLI   opencode 1.18.21  (~/.opencode/bin/opencode) -> agents/opencode.yaml
docker      29.9.0, compose 5.6.0
port        18080 gateway (Bifrost): IN USE (a Heimdall stack may already be running there (then skip `docker compose up`); otherwise change the port in .heimdall/deploy/bench.yaml and docker-compose.yml)
port        19101 fault proxy control: IN USE (a Heimdall stack may already be running there (then skip `docker compose up`); otherwise change the port in .heimdall/deploy/bench.yaml and docker-compose.yml)
port        8090 bench server (heimdall serve): IN USE (a Heimdall stack may already be running there (then skip `docker compose up`); otherwise change the port in .heimdall/deploy/bench.yaml and docker-compose.yml)

wrote 32 files to ~/minitext/.heimdall
added .heimdall/runs/ to ~/minitext/.gitignore

next (from the repository root; every step is $0 until a real model runs):
  1. cp "$(command -v heimdall)" .heimdall/deploy/bifrost-bench && docker build -t bench/bifrost-bench:dev .heimdall/deploy && docker build -t bench/coding-core-base:1 .heimdall/deploy/helper
     cost: $0 (local Docker builds)
  2. docker build -t bench/agent-claude-code:1 .heimdall/agents/images/claude-code
     cost: $0 (local Docker build, no model call)
  3. docker build -t bench/agent-codex:1 .heimdall/agents/images/codex
     cost: $0 (local Docker build, no model call)
  4. docker build -t bench/agent-opencode:1 .heimdall/agents/images/opencode
     cost: $0 (local Docker build, no model call)
  5. docker compose -f .heimdall/deploy/docker-compose.yml --env-file .heimdall/deploy/.env up -d --wait
     cost: $0; a port is in use, so a stack may already be running: reuse it (copy its tokens into .heimdall/deploy/.env) or change the ports first
  6. heimdall tasks mine -limit 10
     cost: $0 (no model; runs your tests in containers to prove each task)
  7. heimdall compare --a agents/oracle.yaml --b agents/noop.yaml --trials 1   # then: --baseline main --candidate HEAD --base agents/<your agent>.yaml --budget-usd 5
     cost: $0 with the scripted agents shown; a real agent shows its forecast and asks before spending
  8. heimdall ci install github -dry-run
     cost: $0 (writes a file)

every command finds .heimdall/deploy/bench.yaml by itself; pass -config .heimdall/deploy/bench.yaml to be explicit.
```

The three ports were taken by the stack that was already running, so the
compose step was skipped and the bench used that stack.

### 2. `heimdall tasks mine`

The dry run lists what would be proved, and why the rest is skipped:

```console
$ heimdall tasks mine -dry-run
looked at 19 commits of ~/minitext: 12 candidates, 7 skipped (suite minitext, template python-pytest)
skipped:
  changes a binary file                            1
      b28c424716ae Ship the logo with the package [minitext/logo.png]
  changes no test file                             2
      254f9cd3f438 Agent notes: never edit tests
      489540ae90a1 Docs: list the helpers in the README
  changes only test files (no reference solution)  1
      6866376900bc More roman numeral tests
  merge commit                                     1
      91c35bd78388 Merge branch 'fix-title-case'
  root commit (no parent to start from)            1
      f9844909ec05 Initial minitext: slug, truncate, durations, roman numera...
  tests need the network (the verifier has none)   1
      123fc3113144 Add fetch_title [tests/test_fetch.py calls https://example.com through minitext/fetch.py: urllib.request]

ranked candidates (-dry-run: nothing written or proved):
   1. 55801bb55db5 minitext       5 lines 2 files  dedent treats a tab as one character
   2. 9d666c0907c8 minitext      11 lines 2 files  Add dedent
   3. 42d91dd59f63 minitext       6 lines 2 files  parse_duration ignores hours
   4. 0fed3b4ce365 minitext       6 lines 2 files  Fix slugify leaving hyphens at the ends
   5. 87e174eff70b minitext       7 lines 2 files  count_words counts empty strings between spaces
   6. 2c6ebd705069 minitext      13 lines 2 files  title_case capitalizes small words
   7. cbf7b5a5f59b minitext       8 lines 2 files  camel_to_snake splits acronyms letter by letter
   8. 8b120aa98076 minitext       8 lines 2 files  to_roman writes 4 as IIII
   9. 158a7bbf6e71 minitext       8 lines 2 files  truncate: keep the result within width
  10. 4d0e36a2d04d minitext       6 lines 2 files  slugify drops accented letters
  11. 6218a7a64197 minitext       6 lines 2 files  Document truncate on text that already fits
  12. d97a729b4ecf minitext      11 lines 2 files  wrap drops the last line
```

The real run cuts each candidate, drafts its instruction, proves it with
admission in the suite's verifier container and keeps only proven tasks:

```console
$ heimdall tasks mine -limit 10
looked at 19 commits of ~/minitext: 12 candidates, 7 skipped (suite minitext, template python-pytest)
skipped:
  changes a binary file                            1
      b28c424716ae Ship the logo with the package [minitext/logo.png]
  changes no test file                             2
      254f9cd3f438 Agent notes: never edit tests
      489540ae90a1 Docs: list the helpers in the README
  changes only test files (no reference solution)  1
      6866376900bc More roman numeral tests
  merge commit                                     1
      91c35bd78388 Merge branch 'fix-title-case'
  root commit (no parent to start from)            1
      f9844909ec05 Initial minitext: slug, truncate, durations, roman numera...
  tests need the network (the verifier has none)   1
      123fc3113144 Add fetch_title [tests/test_fetch.py calls https://example.com through minitext/fetch.py: urllib.request]
proving m-55801bb55d (55801bb55db5) dedent treats a tab as one character ...
proving m-9d666c0907 (9d666c0907c8) Add dedent ...
proving m-42d91dd59f (42d91dd59f63) parse_duration ignores hours ...
proving m-0fed3b4ce3 (0fed3b4ce365) Fix slugify leaving hyphens at the ends ...
proving m-87e174eff7 (87e174eff70b) count_words counts empty strings between spaces ...
proving m-2c6ebd7050 (2c6ebd705069) title_case capitalizes small words ...
proving m-cbf7b5a5f5 (cbf7b5a5f59b) camel_to_snake splits acronyms letter by letter ...
proving m-8b120aa980 (8b120aa98076) to_roman writes 4 as IIII ...
proving m-158a7bbf6e (158a7bbf6e71) truncate: keep the result within width ...
proving m-4d0e36a2d0 (4d0e36a2d04d) slugify drops accented letters ...

proved 10 new tasks (hidden tests fail at the parent, pass at the commit; admission recorded in suite.lock):
   1. m-55801bb55d   55801bb55db5   5 lines  2 files  2 tests  proof 174.4s  dedent treats a tab as one character
   2. m-9d666c0907   9d666c0907c8  11 lines  2 files  1 tests  proof 229.3s  Add dedent
   3. m-42d91dd59f   42d91dd59f63   6 lines  2 files  2 tests  proof 207.6s  parse_duration ignores hours
   4. m-0fed3b4ce3   0fed3b4ce365   6 lines  2 files  2 tests  proof 149.9s  Fix slugify leaving hyphens at the ends
   5. m-87e174eff7   87e174eff70b   7 lines  2 files  2 tests  proof 139.6s  count_words counts empty strings between spaces
   6. m-2c6ebd7050   2c6ebd705069  13 lines  2 files  1 tests  proof 134.2s  title_case capitalizes small words
   7. m-cbf7b5a5f5   cbf7b5a5f59b   8 lines  2 files  2 tests  proof 109.2s  camel_to_snake splits acronyms letter by letter
   8. m-8b120aa980   8b120aa98076   8 lines  2 files  2 tests  proof 160.1s  to_roman writes 4 as IIII
   9. m-158a7bbf6e   158a7bbf6e71   8 lines  2 files  2 tests  proof 159.7s  truncate: keep the result within width
  10. m-4d0e36a2d0   4d0e36a2d04d   6 lines  2 files  3 tests  proof 224.4s  slugify drops accented letters
took 28m9s
10 of 10 tasks have unreviewed instructions (drafted by tasks mine; read each instruction.md, then `heimdall task review <suite>/<task>`)
```

The commit whose test already passed ("Document truncate on text that
already fits") ranked 11th, so `-limit 10` stopped before it. In an earlier
run of the same history with `-limit 12` (36 min) it was cut, refused by
admission and removed again, with `suite.yaml` restored:

```console
dropped 1 unproven (removed again):
  m-f5b8e439ff   f5b8e439ff20 Document truncate on text that alread...: not proven: empty_patch (want fail, got pass), exploit (want fail, got pass), null_noop (want fail, got pass), null_refuse (want fail, got pass)
```

That earlier run also showed a bug fixed before this walk-through: each
proof rewrote `suite.lock`, and adding a task id to `suite.yaml` changes the
suite fingerprint, so only the last task stayed admitted. `tasks mine` now
writes the lock once at the end and carries each proof whose task
fingerprint is unchanged; here all 10 tasks are `admitted` in `suite.lock`.

One drafted instruction, as written (no EDIT ME marker; the task is marked
`instruction_reviewed: false` and counted as unreviewed everywhere):

```markdown
# Dedent treats a tab as one character

A tab-indented block was not dedented at all next to a space-indented one.

When you are done, this behaviour must hold:

- spaces
- tabs count as four spaces

Keep the existing behaviour and tests passing.
```

Reviewing one task marks it and proves it again (task.yaml is part of the
task's lock fingerprint):

```console
$ heimdall task review <task>
minitext/m-42d91dd59f: instruction marked reviewed
minitext/m-42d91dd59f: proved again and admitted (74.7s)
9 of 10 tasks have unreviewed instructions (drafted by tasks mine; read each instruction.md, then `heimdall task review <suite>/<task>`)
```

### 3. `heimdall compare`

Oracle (applies the reference fix) against noop (changes nothing), one
trial, clean plus the model-outage fault. Both arms are scripted, so the run
is labelled synthetic and needs no consent:

```console
$ heimdall compare --a agents/oracle.yaml --b agents/noop.yaml --trials 1 --fault model-outage --name walk-oracle-vs-noop
wrote ~/minitext/.heimdall/experiments/walk-oracle-vs-noop.yaml
  A = agents/oracle.yaml, B = agents/noop.yaml
experiment e_walk_oracle_vs_noop_047n  manifest d91de1e99ea9
  clean             20 slots  arm A 10, arm B 10
  model-outage      20 slots  arm A 10, arm B 10
  counted 40, compatibility 0, total 40 agent episodes
forecast  No history for any arm: cannot estimate.
  arm A      agents/oracle.yaml            20 runs  cost unknown: no history, cannot estimate  agent time at most 5h
             basis none: no history, cannot estimate: no sealed run of this config or of this agent and model on this suite
  arm B      agents/noop.yaml              20 runs  cost unknown: no history, cannot estimate  agent time at most 5h
             basis none: no history, cannot estimate: no sealed run of this config or of this agent and model on this suite
  total      cost unknown: no estimate for arm A, B  agent time at most 10h  wall clock at most 5h (concurrency 2)
  no budget: no budget (budget.total_usd is not set)
every arm is a scripted agent: $0, no consent needed; running 40 episodes
```

The verdict card it printed, as markdown (the same text the CI comment posts):

```markdown
<!-- heimdall-verdict -->
### Heimdall verdict: REGRESSED (hold)

Compared: A = agents/oracle.yaml, B = agents/noop.yaml

> **Synthetic data**: scripted agents or a scripted model. This shows the pipeline works; it is not a finding about any agent.
> 9 of 10 tasks have unreviewed instructions (drafted by `tasks mine`; review them with `heimdall task review`).

| | A: agents/oracle.yaml | B: agents/noop.yaml |
|---|---|---|
| verified success (clean) | 10/10 (100%) | 0/10 (0%) |
| cost per verified success | unknown (cost unknown in 20 of 20 runs, verified success unknown in 0) | unknown (undefined (0 of 20)) |

**Effect** of B against A on verified_success (clean): -100.0 pp, interval [-100.0 pp, -100.0 pp], sign-flip p = 0.00195 over 10 tasks (0 better, 10 worse, 0 tied)

**Policy** enterprise-default: block (3 of 5 checks not passing)  
**Integrity**: clean

**Tasks that differ most**

| task | A | B | B - A |
|---|---|---|---|
| `m-0fed3b4ce3` | 1.00 | 0.00 | -1.00 |
| `m-158a7bbf6e` | 1.00 | 0.00 | -1.00 |
| `m-2c6ebd7050` | 1.00 | 0.00 | -1.00 |

Experiment `e_walk_oracle_vs_noop_047n`, analysis `a_4xmyzwi0`; [verdict page](http://127.0.0.1:8090/verdict?exp=e_walk_oracle_vs_noop_047n); evidence: ~/minitext/.heimdall/runs/e_walk_oracle_vs_noop_047n
```

and then:

```console
report: ~/minitext/.heimdall/reports/e_walk_oracle_vs_noop_047n/report.md (took 21m39s)
verdict: regressed, action: hold
policy enterprise-default (e47dcf5ea03a): block, 5 checks: 2 pass, 1 fail, 2 unknown
  fail    block clean-no-regression: clean: regressed at both bounds, d_bar -100.0 pp (-100.0 / -100.0 pp at the missingness bounds), p 0.00195312, 10 of 10 tasks discordant
  unknown warn  retry-amplification-ceiling: 0 observed runs of 10, fewer than min_n 1 (0 unknown); needs review: run more tasks or trials
  unknown warn  cost-per-verified-success: cost_usd unknown in 10 of 10 runs
```

Exit 5: the release policy blocks (`clean-no-regression` fails), which
`check` reports before the verdict code 1. The verdict is what it should be
for a candidate that changes nothing: regressed on all 10 tasks, p = 0.002.
Cost per verified success is unknown because scripted agents make no priced
model call. An earlier run of the same comparison on 11 tasks came out
INVALID instead: 9 of its 44 runs timed out waiting for the shared
gateway's request log, the usage-completeness instrument failed, and the
card said so rather than report a verdict.

The refs form writes the experiment for a change to the agent's own files
(here the AGENTS.md commit) and, with `--write-only`, stops before planning:

```console
$ heimdall compare --baseline HEAD~1 --candidate HEAD --base agents/claude-code.yaml --trials 3 --fault model-outage --budget-usd 5 --name walk-notes --write-only
wrote ~/minitext/.heimdall/experiments/walk-notes.yaml
  agent files at `HEAD~1` (4d0e36a2d04d, 1 files) vs `HEAD` (254f9cd3f438, 1 files)
--write-only: nothing planned or run; run it with
  (cd ~/minitext/.heimdall && heimdall check experiments/walk-notes.yaml)
```

```yaml
# Written by `heimdall compare`; a normal experiment file. Rerun it from the
# project folder (.heimdall/) with
#   heimdall check experiments/walk-notes.yaml
# from: heimdall compare --baseline HEAD~1 --candidate HEAD --base agents/claude-code.yaml --trials 3 --fault model-outage --budget-usd 5 --name walk-notes --write-only
name: walk-notes
suite: minitext
base: agents/claude-code.yaml
arms:
  A:
    label: HEAD~1 (4d0e36a2d04d)
    set:
      notes.user_file: experiments/fixtures/walk-notes/A/AGENTS.md
  B:
    label: HEAD (254f9cd3f438)
    set:
      notes.user_file: experiments/fixtures/walk-notes/B/AGENTS.md
primary: {condition: clean, metric: verified_success}
guards:
  - {rule: no_new_integrity_violation}
conditions:
  - {id: clean, tasks: all, trials: 3}
  - {id: model-outage, tasks: all, trials: 3}
budget: {total_usd: 5}
verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}
seed: 20261118
concurrency: 2
manifest_mode: agent_exposure
policy: policies/default.yaml
```

### 4. `heimdall ci install github -dry-run`

```console
$ heimdall ci install github -dry-run
# Written by `heimdall ci install github -mode recorded`: the Heimdall agent gate.
# On every pull request that can change the coding agent (notes, skills,
# prompts, the .heimdall project) it runs `heimdall check` through Heimdall's
# composite action (bench/ci/action.yml), posts the verdict card as one PR
# comment that later runs update in place, uploads the evidence and fails on
# regressed,invalid or a release-policy block.
#
# RECORDED mode re-checks the sealed experiment checked in under
# .heimdall/recorded/ (copy one from .heimdall/runs/<exp_id>): no Docker, no
# gateway, no key, no secret; it runs on GitHub-hosted runners.
name: Heimdall agent gate

...
-dry-run: would write ~/minitext/.github/workflows/heimdall.yml (115 lines)
recorded mode: copy one sealed experiment into ~/minitext/.heimdall/recorded (mkdir -p ~/minitext/.heimdall/recorded && cp -R ~/minitext/.heimdall/runs/<exp_id> ~/minitext/.heimdall/recorded/) and commit it
```

The workflow's gate was then run locally the way the job runs it: the sealed
experiment copied into `.heimdall/recorded/`, the seal guard (`go test -run
TestRecordedBundleSeals ./ci/` with `BENCH_CI_BUNDLE` pointing at it: PASS),
`ci/check.sh recorded` with `BENCH_CI_BENCH_DIR=.heimdall` and the prebuilt
binary (`bench gate: verdict=regressed action=hold policy=block exit=5`),
then `heimdall ci card` on its `report.json`, which printed the same card.

The full workflow is golden-tested in `bench/ci/testdata/workflow-recorded.golden.yml`
(and `workflow-live.golden.yml` for `-mode live`).

## What a real user still does by hand

The walk-through above reused images and a gateway stack that were already
on the machine. On a fresh machine, and for a real agent, these steps are
still yours:

1. **Build the images once.** `init` prints the exact `docker build` lines:
   the bench image (from a static build of the binary), the helper image,
   one image per agent CLI it found (`agents/images/<agent>/Dockerfile`),
   and a toolchain image for every suite that is not Python. With
   `init -image <tag>` no `base/` folder is written, so instead of a build
   it prints `docker image inspect <tag>` to check that image is on the
   machine. Not measured
   here; the agent images download their CLI, so they need the network.
2. **Start the stack.** `docker compose -f .heimdall/deploy/docker-compose.yml
   --env-file .heimdall/deploy/.env up -d --wait`. `init` writes `.env` with
   fresh random tokens; it does not start containers and does not pick other
   ports when 18080, 19101 or 8090 are taken (it says so, and you change
   them in `deploy/bench.yaml` and the compose file, or reuse the running
   stack by copying its tokens into `.heimdall/deploy/.env`).
3. **Put a provider key in the stack** for a real agent
   (`ANTHROPIC_API_KEY` or `OPENAI_API_KEY` in `.heimdall/deploy/.env`, read
   only by the fault proxy). Without one the fault proxy answers from its
   scripted model, which only the scripted `toy` agent understands.
4. **Give the suite your dependencies.** The Python suite reuses the helper
   image (Python 3.12 and pytest only). A library with dependencies, and
   every Rust, Ruby, Java, Make or non-`node:test` Node repository, needs
   its toolchain and dependencies baked into
   `.heimdall/benchmarks/<suite>/base/Dockerfile` (or `init -image <tag>`),
   because the verifier runs with no network. Until then admission fails
   and `tasks mine` drops every task, with the failing check printed.
5. **Read the drafted instructions.** `tasks mine` writes each instruction
   from the commit message and the test names and marks it
   `instruction_reviewed: false`; every report and card counts them. Fix
   what a teammate would not say, then `heimdall task review <task>` (it
   proves the task again).
6. **Pick the agent and the change.** `compare --baseline/--candidate` needs
   `--base agents/<agent>.yaml` (the first agent CLI `init` found is the
   default) and reads the agent's own files at both refs: `AGENTS.md`,
   `CLAUDE.md`, `GEMINI.md`, `.claude/skills`, `skills`, `prompts` and a
   committed `.heimdall/agents/<agent>.yaml` (`--paths` changes the list).
   Only the notes file, skill folders and that committed config reach the
   runs. A difference anywhere else (a file under `prompts/`, another
   agent's config) is refused with the file named, since both arms would
   run the same inputs; reference a prompt from the committed config so it
   is part of the config change.
   A model swap is `--a model=<m1> --b model=<m2>`.
7. **Wire CI.** Commit `.heimdall/` (its `.gitignore` keeps `runs/` and
   `deploy/.env` out) and the workflow. Recorded mode needs one sealed
   experiment copied into `.heimdall/recorded/`. Live mode needs a
   self-hosted runner labelled `heimdall` with the stack up and the
   repository variable `HEIMDALL_ENV_FILE` naming the stack's env file on
   it. Pin `-source-ref` to a Heimdall commit.

## Limits

- **Time.** Proofs and runs are Docker work. On a shared 8-core host under
  a load average of 35 to 45 one task proof took 31 to 270 seconds (109 to
  229 in the walk-through) and the 40 scripted runs took 22 minutes at
  concurrency 2, so the four commands took 51 minutes, not 30. `tasks mine
  -limit 5` and `compare --trials 1` without a fault roughly halve it; a
  real agent adds its own run time and model latency.
- **Verdict strength.** With scripted agents the card says synthetic, never
  a finding. A real verdict needs a real agent, a provider key and enough
  tasks: ten tasks with three trials detect only large differences, and the
  card says inconclusive (with the tasks needed) rather than guess.
- **Mining** reads history only through the suite template's test globs,
  skips by static rules (merge, binary, huge, network, secrets) and proves
  the rest; it cannot tell a flaky test from a real one beyond the three
  reference repeats admission runs. Drafted instructions can still say
  too much (docs/tasks-from-your-repo.md, "Limits of mining").
- **CI** has not run on GitHub from this walk-through: the workflow is
  golden-tested and its YAML parsed in tests, and the comment step uses
  documented `gh api` endpoints (docs/ci.md).
- **No model was called** anywhere on this page.
