# Bifrost Bench

**Crash tests for coding agents, ending in a release verdict that knows when it cannot tell.**

Bifrost Bench answers one question for anyone who changes a coding agent (a new model, a new prompt, a new notes file, a new tool, a new gateway rule): *did this change make my agent better, worse, or can we not tell yet, where is it weak, and what does a verified success cost?*

It runs two versions of an agent on the same tasks, several times, clean and under deliberate failures, with every model call going through the [Bifrost](https://github.com/maximhq/bifrost) gateway. It ends in one verdict (improved, regressed, inconclusive or evaluation invalid), one action (ship, hold, no decision, fix the harness) and a diagnostics table, with the evidence for every number one click away.

Built for PS2 (agent benchmarking platform) of the Geek Room Global AI Hackathon, title partner Bifrost by Maxim AI.

## Why it is different

Most evaluation tools show traces, token charts and a pass rate. Teams already have those. What they cannot get is a trustworthy answer:

| Usual dashboard | Bifrost Bench |
|---|---|
| "B scored 19/24, A 17/24: B wins" | An exact paired test over tasks. Two points of difference on 8 tasks is **inconclusive**, and the page says how many tasks a new experiment needs |
| Pass/fail read from the agent's own container | A clean-room regrade in a fresh container with hidden tests. A 10-line `conftest.py` cannot fake a pass |
| The agent's own log is the trace | Bifrost's log is the trace of record. The agent cannot rewrite it |
| Faults are assumed to have happened | Every fault carries receipts: assigned, armed, fired, delivered, absorbed by which layer, visible to the agent, acknowledged, effect |
| Cost from the provider dashboard | An own ledger, one row per physical attempt, at pinned prices. Unknown is never shown as zero |
| One number | No blended score. Success, honesty, safety and cost sit side by side |

The verdict can also say **evaluation invalid**, computed from controls: low grading coverage, a served model different from the requested one, an unplanned fallback, missing usage data, or an agent that could reach the internet.

## What it measures

PS2's six metrics, per run, for any agent and with no agent-specific code:

| Metric | Definition |
|---|---|
| Success | The hidden verifier passes under the clean regrade and no integrity gate vetoed the run |
| Steps | Model turns (below) |
| Tool calls | Calls the model requested (below) |
| Tokens | Fresh input, cache read, cache write and output, kept apart |
| Cost | Every physical attempt at the pinned price snapshot |
| Time | Agent container start to exit |

Steps are model turns: logical model requests the model answered, so retries and fallbacks of one request are not extra steps. Tool calls are the calls the model requested, by tool-call id, from the gateway record (the agent's own stream stands in only when that record is incomplete, and the value says so). Whether a tool actually ran, and whether it failed, is confirmed only for agents with a native parser (Codex, Claude Code, OpenCode): Bifrost sees which tool results went back to the model, not what the tool did.

Plus false-completion rate (claimed success the verifier refuted), claim precision, retry amplification and safe success.

## Failure testing (PS2's three families)

Every probe perturbs the same admitted tasks, so its effect is a paired difference, not a different benchmark.

| Family | Probe | How |
|---|---|---|
| Unavailable model | `model-outage` | The fault proxy behind Bifrost returns 503 from the third model call on. Bifrost's own retries and attempt trail record it |
| Broken tool | `test-crash` (loud), `empty-test-run` (quiet) | A `pytest` shim crashes, or reports "collected 0 items" while exiting 0 |
| Misleading instruction | `stale-notes` | The repo's `CLAUDE.md`/`AGENTS.md` contradicts the code on a verified behaviour |

## PS2 requirements, where each is met

| PS2 requirement | Where it is built | Evidence |
|---|---|---|
| **Framework independence** (mandatory) | `adapters/command.go` runs any agent that accepts a gateway base URL from one YAML in `agents/`: Codex, Claude Code, OpenCode, Gemini CLI, mini-swe-agent, and a no-framework agent in one standard-library Python file (`agents/custom-python.yaml`). The metrics come from Bifrost's record (`gateways/bifrost/`, `evidence/gateway.go`), so no agent is rewritten. Guide: `docs/adding-an-agent.md`; what has actually run: `docs/harness-matrix.md` | <!-- EVIDENCE: batch-frameworks exp id; Codex vs custom-python on the same tasks and model; runs per arm and condition --> |
| **Consistent evaluation**: success, steps, tool calls, tokens, cost, time (mandatory) | Every arm runs the same admitted tasks (`benchmarks/coding-core/`, `suite.lock`). Metrics in `metrics/` (`verifiedsuccess.go`, `steps.go`, `toolcalls.go`, `tokens.go`, `costusd.go`, `walltime.go`); clean-room regrade in `graders/`; cost ledger at pinned prices in `evidence/cost.go`. Per arm on `/verdict`, per run on `/runs` | <!-- EVIDENCE: the six metrics per arm from one real experiment's diagnostics table --> |
| **Repeatability** (mandatory) | Trials per condition in the experiment file; manifest frozen and hashed before the first run (`engine/planner.go`); pinned images, prices (`deploy/pricing-snapshot.json`) and suite lock; sealed, hash-chained run folders (`store/seal.go`); `bifrost-bench check -exp <id>` re-analyzes sealed runs without rerunning; `bifrost-bench serve -offline` serves recorded runs; pass^k in `stats/passk.go` | <!-- EVIDENCE: trials per task in the real batch; per-task pass rates across trials; seal verification result --> |
| **Failure testing**: unavailable model, broken tool, misleading instruction (mandatory) | One YAML per probe in `benchmarks/coding-core/probes/`: `model-outage` (fault proxy behind Bifrost, `faultproxy/`, `probes/gateway.go`), `test-crash` and `empty-test-run` (`pytest` shims, `probes/toolshim.go`), `stale-notes` (`probes/fixture.go`). Every fault carries receipts, shown on `/receipts` | <!-- EVIDENCE: batch-frameworks success per probe vs clean; receipt counts (fired, delivered, visible) per family --> |
| **Inspection and comparison** with step-by-step records (mandatory) | `/runs` is the matched-run inspector: both arms of one pair side by side, each with its normalized events (`events.jsonl`, built by `evidence/` from Bifrost rows, the proxy ledger, the agent stream and shim logs) and its diff. `/verdict` compares the arms; `/triage` links patterns to the runs and events behind them | <!-- EVIDENCE: pair id of one real matched pair to open in /runs --> |
| Leaderboard (optional) | `leaderboard/`, `bifrost-bench leaderboard`, `/leaderboard`; see Features | <!-- EVIDENCE: leaderboard -scope common over the real batch: entries, tiers, exclusions --> |
| Repeatable task suites (optional) | `benchmarks/coding-core/` (8 tasks with hidden verifiers and a lock file); `bifrost-bench suite lint\|lock\|admit`, where `admit` runs the admission controls (the reference passes 3 of 3; the empty patch, every mutant and the exploit fail); YAML validated by `spec/` | <!-- EVIDENCE: suite admit result for the 8 tasks --> |
| Execution traces (optional) | Bifrost's log is the trace of record (`gateways/bifrost/`), joined with the fault proxy's hash-chained ledger (`faultproxy/`) and the agent's own stream (parsers in `adapters/`) into `events.jsonl` | <!-- EVIDENCE: one real run id with its event count by source --> |
| Comparative dashboards (optional) | `/verdict` (verdict, action, diagnostics table), `/leaderboard`, `/triage`, `/releases` | <!-- EVIDENCE: verdict and action of each real experiment --> |

## Features

| Feature | What it does | Guide |
|---|---|---|
| Release policy as code | YAML rules a candidate must also meet (`policies/default.yaml`, `policies/strict.yaml`, evaluated by `policy/`). A policy only adds requirements; `check -policy <file>` exits 5 when it blocks | `policies/README.md` |
| Release records | `bifrost-bench release create\|list\|verify\|export`: a hash-chained log of what was released on which analysis and sealed runs, with a zip evidence pack per release; `/releases` | `docs/releases.md` |
| CI gate | `.github/workflows/bench-check.yml` runs `bifrost-bench check` through the composite action in `ci/` on pull requests that touch an agent, an experiment, a policy, a benchmark or the bench; a bad verdict or a policy block fails the build | `docs/ci.md` |
| Failure triage | Counts per arm how many runs show each observed pattern (`pattern_loop`, `pattern_unverified_finish`, `pattern_ignored_error`), each count linked to its runs and event ids; `/triage`. Patterns, not root causes | `docs/triage.md` |
| Safety probes | `destructive-bait`, `protected-path` and `prompt-injection` (veto gates G10 to G12) and `secret-canary` (report-only metrics) plant a trap per run; `experiments/safety.yaml` | `docs/safety-probes.md` |
| Leaderboard | Ranks agent configs (one per resolved config hash) over every sealed run of one suite version and condition, across experiments, by verified success with cost, tokens and steps beside it and no blended score. Each entry is compared with the leader by the exact sign-flip test on shared tasks, Holm-adjusted (tier 1: cannot be told apart, tier 2: significantly behind). Invalid experiments, infra errors and unsealed runs are left out and counted; scripted agents and scripted-model runs appear only with `-synthetic only`, under a SYNTHETIC banner. `bifrost-bench leaderboard`, `GET /api/bench/leaderboard`, `/leaderboard` | `docs/leaderboard.md` |

## How it works

```
experiment.yaml -> plan -> manifest (frozen and hashed before the first run)
   |
   v  for each slot (task x arm x condition x trial), in randomized order:
 prepare  fresh workspace volume, per-run Bifrost virtual key
 arm      the probe (fixture, tool shim, or fault plan in the proxy)
 run      the agent in a hardened container whose only reachable host is Bifrost
 export   the diff, by a trusted container, path allowlist applied
 collect  Bifrost rows, proxy ledger, agent stream, shim log, cost rows
 grade    hidden tests in a fresh container with no network
 gate     protected paths, test-infra injection, regrade tampering, egress, model fidelity
 measure  metrics; receipts; cleanup; seal (sha256 of every file, chained)
   |
   v
 analysis: exact sign-flip test on the primary endpoint, guards, invalid gates,
           diagnostics, meters -> verdict + one action + report.md
```

```
[agent container] --evalnet (internal)--> [Bifrost v2.5.1] --upstream--> [fault proxy] --> model provider
                                              | logs (Postgres)              | ledger
[bifrost-bench]  <-- management API, logs ----+                              |
                 <-- fault plans, ledger ------------------------------------+
```

The fault proxy is registered as the base URL of Bifrost's provider, so faults go through Bifrost's real retry path and show up in its attempt trail. Bifrost's own Mocker plugin cannot be loaded on a stock gateway and its errors skip the retry loop, so it is not used.

## Quick start

Needs Docker, Go 1.26+ and Node 22. From `bench/` (or `make bench-<target>` from the repo root):

```bash
make up        # builds the binary, UI and images, creates deploy/.env with random tokens, starts Bifrost + Postgres + fault proxy
make doctor    # preflight: docker, images, internal network, egress blocked, suite lock, gateway, fault proxy
make smoke     # no API key needed: toy agent + scripted model through real Bifrost, clean and model-outage
make serve     # web UI and API on http://127.0.0.1:8090
make down      # stop the stack
```

With a provider key in `deploy/.env` (only the fault proxy ever sees it):

```bash
./bin/bifrost-bench plan experiments/codewins.reduced.yaml   # slots, sizes; nothing runs
./bin/bifrost-bench run  experiments/codewins.reduced.yaml   # freeze, run, analyze, print report.md
./bin/bifrost-bench check -fail-on regressed,invalid -policy policies/default.yaml experiments/codewins.yaml   # CI: exit 1 regressed, 2 inconclusive, 3 invalid, 5 policy block
./bin/bifrost-bench audit testdata/audit/maxim-tb21.csv     # verdict for a published per-task table
./bin/bifrost-bench leaderboard -scope common               # rank every agent config across experiments; no gateway needed
```

`audit` on Maxim's own Terminal-Bench 2.1 routing result (fixed model 74 of 77, router 73 of 77) gives **inconclusive, exact p = 1.0**: 7 discordant tasks out of 77, and detecting a 1.3-point gap needs more than 200 tasks.

## Repository layout

| Folder | What |
|---|---|
| `schemas/` | Every shared type and extension interface (the contract) |
| `spec/` | JSON Schemas for every YAML file: the source of truth |
| `engine/` | Planner, slot pipeline, scheduler with leases, resume and spend cap, janitor |
| `environments/`, `graders/` | Hardened Docker workspace, diff export, hidden-test grader |
| `adapters/`, `agents/` | Agent adapters and parsers; one YAML per agent |
| `probes/`, `gates/`, `metrics/`, `evidence/` | Probe layers, integrity gates, metrics, evidence sources and the cost ledger |
| `faultproxy/`, `gateways/bifrost/` | The fault proxy and the Bifrost client |
| `stats/`, `verdict/` | Exact sign-flip test, paired t, MDE, pass^k; verdict policy, diagnostics, audit |
| `policy/`, `policies/` | Release policy engine and the shipped policy files |
| `release/`, `leaderboard/`, `ci/` | Release log and evidence packs, leaderboard, CI gate |
| `suite/` | Suite loading, lint, lock and admission controls |
| `store/` | Sealed run folders, transitions, leases, seal chain, read index |
| `server/`, `handlers/`, `lib/`, `reporters/` | HTTP API (Bifrost's fasthttp handler style) and reports |
| `ui/` | Web UI (React, TanStack Router, RTK Query, Bifrost's UI primitives), embedded in the binary |
| `benchmarks/coding-core/` | 8 self-authored Python tasks with hidden verifiers, admission controls and probe fixtures |
| `experiments/` | Experiment files: the main experiment in three sizes, the real batch (`batch-*.yaml`), pilots, the safety experiment and the smoke test |
| `deploy/` | Docker Compose stack, Bifrost config with pinned datasheets, price snapshot, bench config |
| `docs/` | Design, spike results, sources, guides |

## Extending it

Everything that varies is a file, validated by a JSON Schema; everything that computes is a part registered in one `builtin.go` list.

| To add | Do this | Go code? |
|---|---|---|
| A task | A folder under `benchmarks/<suite>/tasks/`, then `bifrost-bench suite admit` | No |
| A benchmark | A folder with `suite.yaml` | No, if its environment and grader types exist |
| A probe | One YAML in `benchmarks/<suite>/probes/` | No, for an existing layer |
| An agent | One YAML in `agents/` (any CLI or custom agent that accepts a base URL); see `docs/adding-an-agent.md` | No |
| A variable | A `set:` override on an arm in the experiment file | No |
| A metric or gate | One file plus one line in that package's `builtin.go` | Yes |

## Honest limits

- The statistics decide on tasks, not runs. With 8 tasks most verdicts read inconclusive, by design: a 5% call needs at least 6 tasks moving the same way, so a small effect needs a bigger suite, and the verdict page says how many tasks.
- Tool shims run inside the agent's container, so their log is boundary-level evidence the agent could tamper with. "Visible" receipts come from Bifrost's rows, which the agent cannot touch.
- Docker is not a hard security boundary against a determined attacker; there is no custom seccomp profile or disk quota. Tasks are self-authored and contain no secrets.
- Seals are local hashes without an external anchor.
- Scripted-model runs are kept off the live leaderboard by the label the fault proxy writes on its ledger lines (`upstream`: `real` or `scripted`). Runs recorded before that label existed are recognized by a fingerprint of the scripted model (exactly 32 output tokens and a scripted response id format), which is a heuristic.
- Which agents and providers were actually run, and which are only configured, is listed in `docs/harness-matrix.md`.

## How AI was used

- **Research:** 14 parallel research agents (Claude) read and logged 1,604 sources on agent evaluation, statistics, fault injection, memory, safety, tool use, cost and the Bifrost codebase; digests and a master synthesis fed the design. The team reviewed and decided the design in several rounds of external critical review.
- **Implementation:** the code was written by Claude Code with parallel Claude Opus 5.5 sub-agents, each owning separate packages and working against shared contracts, with the lead agent integrating, reviewing diffs and running every test. Decisions on scope, statistics, security and the benchmark tasks were made and checked by the team.
- **In the product:** no LLM judges any result. Verdicts rest on deterministic evidence: hidden tests, file hashes, gateway records.

## Licenses and sources

Apache-2.0, like Bifrost. The benchmark tasks are written from scratch. Every source cited in the design is listed with its link in `docs/sources.md`; the pinned prices come from the datasheet the Bifrost gateway uses (`deploy/pricing-snapshot.json` records its origin).
