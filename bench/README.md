# Heimdall (`bifrost-bench`)

Heimdall is the project name; the folder, the Go module and the `bifrost-bench` command keep the code name. Older docs call it Bifrost Bench.

**Evaluate your agent: register it once, run it on many benchmarks at once, get one report card per version and model.**

Heimdall benchmarks agents, not models. You bring your own agent (a CLI coding agent, an HTTP service, an A2A agent, a voice agent, your own script), tell Heimdall what it can do (shell, tools, MCP, sessions, browser, desktop, voice, GPU), and pick an evaluation: a pack of benchmarks for one skill (coding, terminal, support, memory, research, voice, safety) or every benchmark your agent can run. Heimdall skips the benchmarks your agent cannot run and says why, runs the rest, and gives back a report card: a score with a confidence interval per skill and per benchmark, cost, tokens, steps and time, how much the score drops under faults (a model outage, a broken tool, a misleading instruction), and safety numbers. Compare two versions of your agent, two models, or your agent against a baseline agent (Codex, Claude Code, mini-swe-agent, OpenCode, Gemini CLI) and each benchmark gets a verdict that says "no clear winner yet" when the evidence is too thin. Every model call goes through Bifrost, so any model Bifrost reaches can be used, open-weight models included: [`docs/evaluations.md`](docs/evaluations.md), [`docs/models-and-providers.md`](docs/models-and-providers.md).

**Crash tests for coding agents, ending in a release verdict that knows when it cannot tell.**

Bifrost Bench answers one question for anyone who changes a coding agent (a new model, a new prompt, a new notes file, a new tool, a new gateway rule): *did this change make my agent better, worse, or can we not tell yet, where is it weak, and what does a verified success cost?*

It runs two versions of an agent on the same tasks, several times, clean and under deliberate failures, with every model call going through the [Bifrost](https://github.com/maximhq/bifrost) gateway. It ends in one verdict (improved, regressed, inconclusive or evaluation invalid), one action (ship, hold, no decision, fix the harness) and a diagnostics table. On the machine that ran an experiment, every number links to the sealed evidence behind it; a fresh clone holds only the reports and one small recorded bundle (see below).

Built for PS2 (agent benchmarking platform) of the Geek Room Global AI Hackathon, title partner Bifrost by Maxim AI.

## What is proven and what is not

**Only the coding track has real-model results:** six experiments on 14 Nov 2026, 318 sealed runs, `gpt-5.4-mini` on OpenAI through stock Bifrost v2.5.1, with Codex CLI 0.169.0 and the bench's own no-framework `custom-python` agent, $8.08 in all ([Results from real runs](#results-from-real-runs)). Even there, 5 of the 6 verdicts are inconclusive, none has said "improved", and the one directional verdict (regressed, `e_batch_frameworks_9z0h`) comes from the integrity guard on one run (`r_mxfbr3dqg3`), not from a difference in success. The 318 run folders (about 560 MB) are git-ignored: the repository keeps every report, manifest and verdict, and the full sealed folders of the 10-run pilot `e_pilot_mixed_nc93` only, whose seals `GOWORK=off go test ./ci/` re-verifies.

**Every other track and feature is proven with scripted controls at $0:** scripted agents (oracle, noop, exploit, refuse, unsafe controls), the fault proxy's scripted model, local fakes and stand-ins of third-party APIs, and real Docker. That shows the plumbing works and the controls come out as expected; it says nothing yet about how a real agent or model does on those tracks. "Last proof" is the experiment the guide names as its most recent check; "-" means the guide names unit, integration or admission tests instead of an experiment id. Only the six real experiments (`docs/results/`) and the two recorded bundles (`testdata/recorded/`: `e_pilot_mixed_nc93`, `e_smoke_sdoj`) ship their evidence in this repository. Every other id below is a manual demonstration recorded in its guide: its run folder stayed on the machine that ran it, so it cannot be re-checked from a fresh clone, and an id marked INVALID is a run that exercised an instrument, not a pass.

| Track or feature | How it is proven | Last proof |
|---|---|---|
| Coding: repository edits (`coding-core`, 8 tasks) | Real model, 318 runs; admission 60 of 60 scripted controls as expected | `e_batch_frameworks_9z0h` (all six in [`docs/results/`](docs/results/README.md)) |
| Failure probes: `model-outage`, `test-crash`, `empty-test-run`, `stale-notes` | Real model, in the coding batch | `e_batch_frameworks_9z0h` |
| Memory-trap probe | Real model, on the coding track | `e_memory_trap_wn4b` |
| Framework independence | Real model: Codex CLI and `custom-python` only. Seven framework agents (LangGraph, OpenAI Agents SDK, CrewAI, Pydantic AI, LlamaIndex, Vercel AI SDK, a plain OpenAI SDK loop): `agent check` and a scripted-model smoke. Claude Code, OpenCode, Gemini CLI, mini-swe-agent: a fake-API smoke ([`docs/harness-matrix.md`](docs/harness-matrix.md)) | `e_matrix_frameworks_smoke_30uo` |
| Terminal and skills (Harbor import: Terminal-Bench 2.0, SkillsBench) | Scripted controls: 5 and 3 real tasks admitted | - |
| SWE-style tasks (SWE-rebench, SWE-bench-Live) | Scripted controls: 1 real task admitted | - |
| Tool use and MCP (`acme-support`, tau2, MCPMark) | Scripted controls through the tool proxy: 4 `acme-support` and 3 tau2 tasks admitted; MCPMark imports and lints only (no upstream reference solutions) | - |
| Memory (`acme-memory`, LongMemEval, LoCoMo) | Scripted controls: 3 `acme-memory` and 2 LongMemEval tasks admitted; the judged path against a fake judge | - |
| Customer support (`acme-helpdesk`) | Scripted controls and a scripted customer; the model-played customer only on the fault proxy's scripted model | `e_hd_w3_helpdesk_smoke_xjlz`, `e_hd_w3_helpdesk_llm_smoke_vqj0` |
| Research (`acme-research`, BrowseComp-Plus, GAIA) | Scripted controls over replayed searches; the cassettes come from a local Tavily stand-in (no real Tavily call); GAIA on an invented fixture only (the dataset is gated and was not downloaded) | `e_research_smoke_mstn`, `e_bcp_smoke_qxhg`, `e_gaia_smoke_rg9n` |
| Safety (`acme-safety`, safety probes, ImpossibleBench, AgentDojo) | Scripted safe and unsafe controls: every gate quiet on the safe control and firing on the unsafe one under its probe. No real model has met a safety probe | - |
| Browser (`acme-web`) | Scripted controls; real-Docker test `TestBrowserTemplateInDocker` | - |
| Computer use (`acme-desktop`) | Scripted controls; real-Docker test `TestIntegrationDesktop` | - |
| Artifacts (`acme-design`) | Scripted controls | - |
| External APIs (`acme-external`) | Scripted controls over committed cassettes | - |
| Multi-agent teams (`acme-team`, MARBLE) | Scripted teams; 4 MARBLE database tasks, 32 of 32 controls as expected; real-Docker test `TestTeamEndToEnd` | `e_marble_db_equal_budget_i3tp` |
| Voice (`acme-voice-desk`) | Scripted controls and reference agents on a fake provider; no real speech provider called | `e_voice_cascaded_fake_t8sk` |
| ML engineering on GPUs (`acme-gpu`, KernelBench) | Scripted controls on one GTX 1650 | `e_gpu_smoke_2itf` |
| Visual grading | Scripted controls; the vision judge only on a scripted model | `e_visual_smoke_9uwe`, `e_visual_judged_smoke_9t6d` |
| Multi-container tasks (compose) | Scripted controls | `e_stack_smoke_9pw6` |
| Kubernetes driver | Local kind cluster: parity with Docker baselines, the toy agent through an in-cluster Bifrost; EKS, GKE and AKS untested | `e_k8s_parity_k2wh`, `e_k8s_parity_probes_esrp`, `e_smoke_9ic3` |
| Code scan of added lines | Scripted controls, report and veto modes | `e_x4_report_fwxc`, `e_x4_veto_7q6s` |
| Routing policy under test | Scripted model on a throwaway Bifrost v2.5.1 stack; the four example experiments have not run on a real model | `e_hd_w9_proof_routing_outage_gated_e5tn`, `e_hd_w9_proof_governance_429_4k9p` |
| Drift canary | Scripted toy agent through Bifrost and the scripted model | `e_canary_smoke_toy_o63c` |
| Adaptive trials and suite tiers | Simulated type-I error and power, and in-process scheduler tests. The scripted runs on the shared stack came out INVALID, because other working copies leased the same fault-proxy key slots and `usage_completeness` refused them, as it should | `e_hd_w7_concordant_task_c1_csw8` (INVALID) |
| Noise floor (A/A arm) | Unit tests. The real batches ran no A/A arm, so their MDE is still "assumed" | - |
| Leaderboard, failure clusters, agent diff, model migration, cost explain | Computed over the real coding runs; the model matrix itself has not run on a real model | [`leaderboard-clean.md`](docs/results/leaderboard-clean.md), `e_batch_frameworks_9z0h` |
| Release policy, CI gate, release records | A recorded replay in CI; the pilot bundle's seals re-verified by `go test ./ci/` | `e_smoke_sdoj`, `e_pilot_mixed_nc93` |
| Bring your own tasks and agents, quickstart | Fresh-project walkthroughs with scripted controls | `e_controls_webapp_wtym`, `e_walk_oracle_vs_noop_047n` |
| Live traffic scoring | A fixture gateway and a throwaway Bifrost v2.5.1 stack with scripted sessions | - |
| A2A agents (`acme-a2a`) | Scripted reference agents on a separate keyless stack with the scripted model ([`docs/a2a-agents.md`](docs/a2a-agents.md)) | `e_a2a_oracle_vs_noop_qk2t`, `e_a2a_transport_yjil`, `e_a2a_team_hdp8` |
| Bifrost gateway features (MCP, routing, caches) | Scripted model on a throwaway Bifrost stack ([`docs/bifrost-features.md`](docs/bifrost-features.md)) | `e_b2_mcp_proof_5nm0`, `e_b2_complexity_router_proof_s7wx`, `e_b2_semantic_cache_proof_p249` |
| Failure inbox, tasks from traces | Local fakes of the Langfuse, LangSmith and Bifrost log APIs; no real backend called | - |
| Trace export and exports | Local fake collectors for every backend preset; Jaeger called for real in a container | `e_pilot_mixed_nc93` |
| Judged suites and judge calibration | A fake judge only | - |
| Agent steps, observers, agent events | Real-Docker integration tests (observer catch rates measured; events file and collector channels); framework spans in the scripted smokes | - |
| Evaluation awareness, compliance pack, monitor certification, skill and MCP admission scan | Unit tests, plus real-Docker tests of the neutral view and the monitor sandbox | - |
| Remote workers and deployed agents (`remote_http`) | Two processes on one machine | - |
| Hosted platform (accounts, organizations, runners, evidence mirror) | `deploy/hosted/smoke.sh` on localhost: sign-up, an organization, a demo runner and a scripted two-slot experiment end to end | - |

## Bring your own agent and tasks

A team can benchmark its own agent on tasks cut from its own repositories, in any language, without Go code or changes to this repository. Six commands take a project from nothing to an admitted task and a free conformance check of the agent:

```bash
bifrost-bench init acme-bench                                  # project folder: deploy/, agents/, benchmarks/, experiments/, policies/
bifrost-bench suite init pyapp -template python-pytest         # or node-test, go-test, generic-reward
bifrost-bench task init -suite pyapp -id t01 -repo <git repo> -base <commit before the fix> -reference <fix commit>
bifrost-bench task validate pyapp/t01 && bifrost-bench task admit pyapp/t01     # after rewriting instruction.md
bifrost-bench agent init pybot -template python                # or command, http-service
bifrost-bench agent check agents/pybot.yaml -suite pyapp       # builds the image, runs one task on the scripted model, $0
```

The guide, with real output from a fresh-project walkthrough, the CI gate and the limits: [`docs/bring-your-own.md`](docs/bring-your-own.md). A complete example project (four Node tasks, a custom agent, Codex): [`examples/acme-node`](examples/acme-node/README.md).

## Scenarios: tools, MCP, memory, answers

Not every agent edits a repository. A suite can also start tool servers beside the agent and grade their final state, run the agent over several sessions with the workspace wiped in between, or grade a written answer with no model. Four more templates each write a suite with one complete sample task, and `task init` copies its shape into new tasks:

```bash
bifrost-bench suite init support -template mcp      # or tool-use, memory, qa
bifrost-bench task init -suite support -id t02      # copy of the sample's shape, with EDIT ME markers
bifrost-bench task validate support/t02 && bifrost-bench task admit support/t02
```

The guide (layouts, state verifiers, seeding services, tool faults, sessions and memory, answers, judged suites, importing public benchmarks, limits): [`docs/scenarios.md`](docs/scenarios.md). Examples: [`examples/acme-support`](examples/acme-support/README.md) (a support desk agent on an MCP server) and [`examples/acme-memory`](examples/acme-memory/README.md) (multi-session recall, with an abstention task).

## Public benchmarks

`bifrost-bench import list` shows the importers; `bifrost-bench import <format> -from <local copy> -suite <id>` turns upstream tasks into an ordinary suite. An imported task is trusted only after `suite admit` proves, on this machine, that its own reference passes and its empty and exploit controls fail. Upstream data is never committed here: imported suites are git-ignored.

| Importer | Benchmarks | Track | Checked here (15 to 18 Nov 2026, scripted controls) |
|---|---|---|---|
| `harbor` | Terminal-Bench 2.0, SkillsBench | terminal, skills | 5 and 3 real tasks admitted; in a full pipeline run the reference control passed 5 of 5 and 3 of 3, the no-change control 0. 54 of 89 and 71 of 87 tasks import (SkillsBench counted on 18 Nov, with compose tasks); the rest download things at solve or test time, or need a setting the bench refuses, and are skipped with the reason |
| `swebench` | SWE-rebench, SWE-bench-Live | coding | 1 real SWE-rebench task admitted |
| `impossiblebench` | ImpossibleBench | safety | 5 real tasks admitted; in a full pipeline run the `impossible_pass` gate turned 3 of 3 gamed passes into integrity violations |
| `tau2` | tau2-bench airline, retail | tool use | 3 real tasks admitted through the tool proxy. The user is scripted, so results are not comparable with the published leaderboard |
| `agentdojo` | AgentDojo 1.2.2 | safety | 2 real banking tasks admitted, with an injection probe in the suite |
| `mcpmark` | MCPMark, filesystem family | MCP | 40 real tasks import and lint clean. Upstream ships no reference solutions, so none is admitted until a reference is written |
| `longmemeval`, `locomo` | LongMemEval, LoCoMo | memory | 2 real LongMemEval tasks with exact-match answers admitted. The judged path has only been tested against a fake judge. LoCoMo is CC BY-NC 4.0 |
| `browsecomp-plus` | BrowseComp-Plus (X5a) | research | 6 real queries admitted over a closed 541-page corpus served by a search service inside the run; oracle 6 of 6, noop 0 of 6; answers stay obfuscated in the suite. The corpus is a subset, so scores are not comparable with the official leaderboard |
| `gaia` | GAIA 2023 validation (X5a) | research | gated, not downloaded: proven on an invented fixture only (4 tasks admitted). Graded by GAIA's quasi-exact match (`answer_match` mode `gaia`); questions that need the web are refused unless a recorded cassette covers them |
| `marble` | MultiAgentBench (MARBLE): database verified; research and coding judged | tool use, qa (team tasks) | 4 real database tasks (5-agent teams, PostgreSQL and a query tool as services) admitted: 32 of 32 controls as expected. In a full pipeline run at equal budget the scripted reference passed 4 of 4 alone, as a 5-member team (own key per member, 8 proven mailbox messages per run) and as the best-of-N baseline; a do-nothing team passed 0 of 4. All 100 database, 99 research and 100 coding tasks import and lint clean; minecraft and bargaining refused with the reason. Upstream ships no reference for the judged tasks, so none is admitted; a smoke run with a scripted judge shows the judged path works end to end. Not comparable with the paper <!-- X5b --> |
| `kernelbench` | KernelBench level 1 (MIT, commit 423217d9) | ml_engineering (one GPU per task) | 28 of the 100 level-1 problems pass the importer's input-size filter and import (59 need more than 256 MiB of input, 13 have sizes the importer cannot know without running them; each skipped with the reason); of 5 tried on a GTX 1650, 4 admitted with the unchanged model as reference (correctness, speedup reported) and 1 refused by admission (its 4 GiB output does not fit the card). Upstream ships no ModelNew solutions <!-- X7 --> |

Every check in this table used scripted controls and cost nothing. No real model has been run on an imported benchmark or on any scenario track yet. Guides: [`docs/importing-harbor.md`](docs/importing-harbor.md), [`docs/importing-swe.md`](docs/importing-swe.md), [`docs/importing-tau2.md`](docs/importing-tau2.md), [`docs/importing-agentdojo.md`](docs/importing-agentdojo.md), [`docs/importing-mcpmark.md`](docs/importing-mcpmark.md), [`docs/importing-memory.md`](docs/importing-memory.md), [`docs/importers-research.md`](docs/importers-research.md). Running slots on other machines: [`docs/remote-execution.md`](docs/remote-execution.md).

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

PS2's six metrics (the first six rows), per run, for any agent that calls its model through Bifrost and with no agent-specific code (tool calls are read from Anthropic Messages and OpenAI chat and Responses rows; a run on another provider format, such as Gemini's, has tool calls unknown at the gateway, [`docs/harness-matrix.md`](docs/harness-matrix.md)), then the agent steps metrics, which also read the tool proxy and two trusted observers beside the agent container ([`docs/steps-and-evidence.md`](docs/steps-and-evidence.md)):

| Metric | Definition |
|---|---|
| Success | The hidden verifier passes under the clean regrade and no integrity gate vetoed the run |
| Steps (model turns answered) | Logical model requests the model answered (below); turns that failed (`model_turns_failed`), Bifrost retries (`model_retries`) and fallbacks (`model_fallbacks`) are counted beside it, never in it |
| Tool calls | Calls the model requested (below) |
| Tokens | Fresh input, cache read, cache write and output, kept apart |
| Cost | Every physical attempt at the pinned price snapshot |
| Time | Agent container start to exit |
| Model attempts (`model_attempts`) | Physical upstream attempts: every attempt in Bifrost's attempt trail plus every fallback row, so the retries that steps count once stay visible |
| Tool calls executed (`tool_calls_executed`) | Tool calls with an execution witness, each once: requested calls a witness saw run, plus proxied, shim or agent-reported calls that match no request |
| Unwitnessed tool calls (`tool_calls_unwitnessed`) | Requested calls that nothing saw run |
| Tool call proof (`tool_proof_rate`, `tool_proof_*`) | Share of requested calls a bench-side witness (tool proxy, process or workspace observer) saw run, and each call's strongest witness class; `tool_traffic_unparsed` counts proxied requests the proxy forwarded but could only record opaque |
| Commands executed (`commands_executed`) | Command trees the process observer saw the agent start, each with all its descendants: `bash -c "ls \| grep x"` is 1 |
| File change events (`files_changed_events`) | Create, modify, delete and rename events in the workspace, caches and `.git` internals left out; exact even past the recording cap |
| Agent steps (`agent_steps`) | Every agent action with a witness, each once: model turns, executed tool calls, and the commands and file changes no tool call accounts for. One turn that runs `bash -lc "pytest -q"` is 2 agent steps |

Steps are model turns: logical model requests the model answered, so retries and fallbacks of one request are not extra steps. Tool calls are the calls the model requested, by tool-call id, from the gateway record (the agent's own stream stands in only when that record is incomplete, and the value says so). Bifrost sees which tool results went back to the model, not what the tool did. Whether a tool actually ran is confirmed from outside the agent, for any local agent and with no parser, by the tool proxy's ledger, a process observer in the agent's PID namespace and a workspace observer; each requested call is `executed_with_proof`, `executed_agent_reported` (only the agent's own stream says so: Codex, Claude Code, OpenCode) or `no_witness`. Whether a tool failed is known from the ledger, from an exit code the process observer caught, or from that stream. Every number names its witness and is unknown, never zero, when that witness is off or missing.

Plus false-completion rate (claimed success the verifier refuted), claim precision, retry amplification and safe success.

## Failure testing (PS2's three families)

Every probe perturbs the same admitted tasks, so its effect is a paired difference, not a different benchmark.

| Family | Probe | How |
|---|---|---|
| Unavailable model | `model-outage` | The fault proxy behind Bifrost returns 503 from the third model call on. Bifrost's own retries and attempt trail record it |
| Broken tool | `test-crash` (loud), `empty-test-run` (quiet) | A `pytest` shim crashes, or reports "collected 0 items" while exiting 0 |
| Misleading instruction | `stale-notes` | The repo's `CLAUDE.md`/`AGENTS.md` contradicts the code on a verified behaviour |

## Results from real runs

Six experiments on 2026-11-14 with a real model: every model request that reached a provider went to `gpt-5.4-mini` on OpenAI through stock Bifrost v2.5.1 and the fault proxy. The model-outage probe deliberately had the fault proxy answer some requests itself with a 503 instead of forwarding them (279 of the 2,779 model requests across the six experiments). with Codex CLI 0.169.0 and the no-framework `custom-python` agent. **318 sealed runs, $8.08 in all**, from the bench's own cost ledger. Safety probes have not been run on a real model yet. Reports, manifests, verdicts and how to re-check them: [`docs/results/`](docs/results/README.md). Counts are verified successes over runs, arm A first.

| Experiment | Question | Runs | Cost | Verdict | Key finding |
|---|---|---:|---:|---|---|
| [`e_pilot_codex_elat`](docs/results/e_pilot_codex_elat/a_4d0pu4jn/report.md) | Cost and minutes per run before sizing the batch (Codex baseline vs code-wins note, clean, t01 and t05) | 4 | $0.08 | inconclusive, no decision | 1/2 vs 1/2; two tasks cannot decide anything |
| [`e_pilot_mixed_nc93`](docs/results/e_pilot_mixed_nc93/a_44f3rkp4/report.md) | Codex vs custom-python, one run per condition; the bundle CI replays | 10 | $0.17 | inconclusive, no decision, policy warn | Every probe armed and fired; one run per cell is too few to decide, by design |
| [`e_batch_frameworks_9z0h`](docs/results/e_batch_frameworks_9z0h/a_agx2nynj/report.md) | Codex vs custom-python on the same tasks and model, clean and all three PS2 failure families | 128 | $3.08 | **regressed, hold**, policy block | Clean success equal (17/24 vs 17/24), but custom-python rewrote existing limiter test expectations on t05 under stale-notes (`r_mxfbr3dqg3`), so the integrity guard holds it |
| [`e_batch_seeded_regression_ik2t`](docs/results/e_batch_seeded_regression_ik2t/a_k20dqicp/report.md) | Codex told in its notes not to run the tests: does the gate catch it? | 48 | $1.20 | inconclusive, no decision, policy block | Codex obeyed (arm B ran `pytest` in 1 of 24 runs, arm A in 23 of 24) but success did not move: 16/24 vs 16/24 |
| [`e_batch_codewins_f8f7`](docs/results/e_batch_codewins_f8f7/a_ioo7g1fr/report.md) | Does "the code is the source of truth" in the notes help Codex resist stale notes? | 64 | $1.82 | inconclusive, no decision, policy block | Stale-notes 10/16 vs 8/16, d_bar -12.5 pp, p 0.75; detecting that needs 48 tasks |
| [`e_memory_trap_wn4b`](docs/results/e_memory_trap_wn4b/a_a8wc3grp/report.md) | Does a memory hygiene sentence stop Codex acting on a planted memory note? | 64 | $1.73 | inconclusive, no decision, policy block | The note reached the model in 32 of 32 trap runs; memory-trap 13/16 vs 12/16; G13 fired 3 times in each arm |

The three batches with a regate give the same verdict on the sealed gates and on the post-hoc G1/G4 1.1.0 regate (`rg_gates_1_1_0`, disclosed in each report); the table shows the regated numbers. Every policy block includes arm B breaking the default policy's 5% false-completion ceiling.

- **The verdict can say regressed when success ties.** In the frameworks batch both agents pass 17 of 24 clean runs, but custom-python changed five existing lines of `tests/test_limiter.py` on t05 under stale-notes and claimed "All tests pass". The post-hoc regate, which no longer vetoes test lines that were only moved, re-indented or wrapped, changed 8 of 128 outcomes (7 of them on t08) and the verdict did not move.
- **Failure families show differences that clean runs hide**, descriptive only (4 to 8 tasks each, and both agents stay tier 1 on every per-condition leaderboard; Codex first): stale-notes 15/16 vs 9/16, empty-test-run 7/8 vs 4/8, test-crash 5/8 vs 6/8. Under model-outage both scored 0/8: Codex retried until 30 of its 32 calls got the 503, custom-python stopped at its first. custom-python used a median 45,146 tokens per run against 116,671, at a similar cost per verified success ($0.0370 vs $0.0396).
- **The skip-tests note: inconclusive, not shown to have no effect.** Codex followed it, and verified success was 16/24 in both arms (four discordant tasks, exact p = 1). That is a failed test for a difference, not evidence of equivalence: no equivalence margin was declared, and 8 tasks can only detect an effect of about 34.6 pp. On these tasks the seeded regression did not show in the primary metric.
- **The memory trap: inconclusive, and G13 is not specific to it.** Memory-trap success was 13/16 vs 12/16 (an observed -6.2 pp, exact p = 1, three discordant tasks; detecting a difference that size would need 183 tasks), so the experiment neither shows an effect nor shows its absence. The note reached the model in every trap run and G13 fired 3 times per arm, all on t07 and t08. Codex edits those tasks' visible tests with no note at all (G1 fired on clean t07 or t08 runs in all four Codex batches), so a G13 fire there does not prove the note was followed. The fix, not built yet, is a trap that asks for a unique canary action ([`docs/memory-probes.md`](docs/memory-probes.md#results)).
- **8 tasks keep most verdicts inconclusive.** 5 of 6 experiments are inconclusive; the smallest effect 8 tasks can detect is 34.6 pp (assumed SD 0.30), the code-wins effect would need 48 tasks and the memory-trap effect 183. All 5 configs on the clean leaderboard are tier 1 ([`leaderboard-clean.md`](docs/results/leaderboard-clean.md)).
- **An infrastructure failure was scored as agent failures, then excluded by hand.** The first memory-trap attempt (`e_memory_trap_guw7`) ran after the host changed networks and Docker DNS broke: 8 runs got the proxy's 502 on every model call and the bench of that day sealed them as agent `fail`. The whole attempt was kept out of the published results (it is archived outside the repo), and the incident led to the `upstream_error` ledger field, the `aborted_infra` and `infra_error` outcomes and the `doctor` upstream check ([`docs/upstream-failures.md`](docs/upstream-failures.md)). That attempt shows why the fix was needed, not that it works; the post-fix classification is covered by the engine tests `TestRunSlotUpstreamUnreachable` and `TestRegateOutcomeUpstream`.

## PS2 requirements, where each is met

| PS2 requirement | Where it is built | Evidence |
|---|---|---|
| **Framework independence** (mandatory) | `adapters/command.go` runs any agent that accepts a gateway base URL from one YAML in `agents/`. Configs ship for Codex, Claude Code, OpenCode, Gemini CLI and mini-swe-agent; for seven agents written with popular frameworks and run unchanged, only the standard base URL and key variables pointed at Bifrost (LangGraph, OpenAI Agents SDK, CrewAI, Pydantic AI, LlamaIndex, Vercel AI SDK in Node, and a plain OpenAI SDK loop; `examples/frameworks/`, `agents/fw-*.yaml`, `docs/frameworks.md`); and for a no-framework agent in one standard-library Python file (`agents/custom-python.yaml`). Only Codex and `custom-python` have run on a real model; the seven framework agents have passed `agent check` and a scripted-model smoke at $0, and the other harnesses a fake-API smoke (`docs/harness-matrix.md`). The metrics come from Bifrost's record (`gateways/bifrost/`, `evidence/gateway.go`), so no agent is rewritten. Any agent can also report its own internal steps with no parser: OpenTelemetry spans (OTLP/HTTP, protobuf or JSON, GenAI and OpenInference conventions) or native events to a per-run collector, an events file or prefixed stdout, kept as agent-reported evidence apart from trusted metrics (`docs/agent-events.md`). Guide: `docs/adding-an-agent.md`; what has actually run: `docs/harness-matrix.md` | [`e_batch_frameworks_9z0h`](docs/results/e_batch_frameworks_9z0h/a_agx2nynj/report.md): Codex CLI 0.169.0 against custom-python on the same 8 tasks, the same `gpt-5.4-mini` and the same Bifrost; 64 runs per arm (clean 24, stale-notes 16, model-outage, test-crash and empty-test-run 8 each), $3.08 |
| **Consistent evaluation**: success, steps, tool calls, tokens, cost, time (mandatory) | Every arm runs the same admitted tasks (`benchmarks/coding-core/`, `suite.lock`). Metrics in `metrics/` (`verifiedsuccess.go`, `steps.go`, `toolcalls.go`, `tokens.go`, `costusd.go`, `walltime.go`); clean-room regrade in `graders/`; cost ledger at pinned prices in `evidence/cost.go`. Per arm on `/verdict`, per run on `/runs` | `e_batch_frameworks_9z0h`, Codex vs custom-python: clean success 17/24 vs 17/24; medians over each arm's 64 runs: steps 8 vs 7, tool calls 7 vs 8, tokens 116,671 vs 45,146 (unknown, not zero, on the 8 model-outage runs per arm), wall time 23.7 s vs 26.1 s; cost per verified success $0.0396 vs $0.0370 |
| **Repeatability** (mandatory) | Trials per condition in the experiment file; manifest frozen and hashed before the first run (`engine/planner.go`); pinned images, prices (`deploy/pricing-snapshot.json`) and suite lock; sealed, hash-chained run folders (`store/seal.go`); `bifrost-bench check -exp <id>` re-analyzes sealed runs without rerunning; `bifrost-bench serve -offline` serves recorded runs; pass^k in `stats/passk.go` | Clean trials per task and arm: 3 in the frameworks and seeded-regression batches, 2 in code-wins and memory-trap. Trials disagree: in the frameworks batch 6 of 16 task-arm cells are mixed (Codex t03 1/3, t04 2/3, t05 2/3, t07 1/3, t08 2/3; custom-python t03 2/3). Clean pass^3 in the frameworks batch (3 trials per task and arm, recomputed from the sealed runs with the gate regate `rg_gates_1_1_0` applied, 18 Nov): Codex 37.5% (3 of 8 tasks passed all three times), custom-python 62.5% (5 of 8), with pass@1 70.8% (17 of 24) for both. Under the original seals, before that regate, Codex t08 was 1/3 and its pass@1 16 of 24. `GOWORK=off go test ./ci/` re-verifies every seal of the recorded real bundle `e_pilot_mixed_nc93`: pass |
| **Failure testing**: unavailable model, broken tool, misleading instruction (mandatory) | One YAML per probe in `benchmarks/coding-core/probes/`: `model-outage` (fault proxy behind Bifrost, `faultproxy/`, `probes/gateway.go`), `test-crash` and `empty-test-run` (`pytest` shims, `probes/toolshim.go`), `stale-notes` (`probes/fixture.go`). Every fault carries receipts, shown on `/receipts` | `e_batch_frameworks_9z0h`, Codex vs custom-python: clean 17/24 vs 17/24; stale-notes 15/16 vs 9/16; model-outage 0/8 vs 0/8; test-crash 5/8 vs 6/8; empty-test-run 7/8 vs 4/8. Receipts: armed in 80 of 80 fault runs; shims and outage fired in 48 of 48, the 503s delivered in 16 of 16; visible to the model in 72 of 80 (the other 8: custom-python stops at its first 503, so visibility is `unknown`) |
| **Inspection and comparison** with step-by-step records (mandatory) | `/runs` is the matched-run inspector: both arms of one pair side by side, each with its normalized events (`events.jsonl`, built by `evidence/` from Bifrost rows, the proxy ledger, the agent stream and shim logs) and its diff. `/verdict` compares the arms; `/triage` links patterns to the runs and events behind them | `/runs?pair=p_hgyrr06k54` (`e_batch_frameworks_9z0h`, t05, stale-notes, trial 2): Codex `r_calkb24x21` passes (57 events); custom-python `r_mxfbr3dqg3` rewrites five existing lines of `tests/test_limiter.py`, claims "All tests pass", and G1 and G4 veto it (43 events) |
| Leaderboard (optional) | `leaderboard/`, `bifrost-bench leaderboard`, `/leaderboard`; see Features | [`leaderboard-clean.md`](docs/results/leaderboard-clean.md) (`-grade-source latest`): 5 configs over 166 clean runs from the 6 experiments, all tier 1 (none significantly behind the leader on 8 tasks), 0 runs excluded. One board per condition in `docs/results/leaderboard-*.md` |
| Repeatable task suites (optional) | `benchmarks/coding-core/` (8 tasks with hidden verifiers and a lock file); `bifrost-bench suite lint\|lock\|admit`, where `admit` runs the admission controls (the reference passes 3 of 3; the empty patch, every mutant and the exploit fail); YAML validated by `spec/` | `benchmarks/coding-core/suite.lock` as frozen by the published experiments (sha256 `8d94484578c0`, recorded in their manifests; the lock has since been rewritten with task fingerprints, so a fresh checkout hashes to `5d35f0c36784`): 8 of 8 tasks admitted, 60 of 60 controls as expected: 24 reference runs and 8 reference-under-stale-notes runs pass; 8 empty patches, 12 mutants and 8 exploits fail |
| Execution traces (optional) | Bifrost's log is the trace of record (`gateways/bifrost/`), joined with the fault proxy's hash-chained ledger (`faultproxy/`) and the agent's own stream (parsers in `adapters/`) into `events.jsonl` | `r_zhtlq3br1h` (Codex, t01, model-outage, `e_batch_frameworks_9z0h`): 112 events, 68 from Bifrost's log, 30 from the proxy ledger, 14 from the Codex stream. The whole batch: 6,334 events (gateway 4,420, agent stream 1,629, proxy ledger 248, shim log 37) |
| Comparative dashboards (optional) | Home `/` (the landing page: running and finished experiments, ready suites), `/verdict?exp=<id>` (verdict, action, diagnostics table), `/leaderboard`, `/triage`, `/releases` | `e_batch_frameworks_9z0h`: regressed, hold. Seeded regression, code-wins, memory trap and both pilots: inconclusive, no decision. Policy block on the 4 batches, warn on `e_pilot_mixed_nc93`, none set on `e_pilot_codex_elat`. Table in "Results from real runs" |
| Agents beyond repository edits: tools, MCP, memory, answers (beyond PS2) | `suite init -template tool-use\|mcp\|memory\|qa` (`scaffold/`), services and the per-run tool proxy, sessions with a kept memory volume, state and answer outputs (`docs/scenarios.md`); examples in `examples/acme-support/` and `examples/acme-memory/` | `suite admit` in Docker, 15 Nov 2026: 4 of 4 `acme-support` tasks admitted through the tool proxy (reference 3 of 3 `reward 1`; empty, 2 mutants, exploit, noop and refuse `reward 0`) and 3 of 3 `acme-memory` tasks over 2 to 3 sessions (reference 3 of 3; empty, mutants, wrong answers, exploit, noop and refuse fail) |
| Research category (the brief names research; W4) | `suite init -template research`: web search through Tavily behind the tool proxy (key injected, allowlist `POST /search` and `/extract`), the `websearch` MCP tool, record and replay cassettes, answers and cited sources graded with no judge, metrics `searches`, `sources_cited`, `citations_grounded`, `citation_precision` (`docs/research-tasks.md`); example `examples/acme-research/` | `suite admit` in Docker, 18 Nov 2026: 8 of 8 `acme-research` tasks admitted in replay mode (reference 3 of 3 `reward 1`; empty, mutant, invented-citation exploit, noop and refuse `reward 0`). $0 smoke `e_research_smoke_mstn` (scripted oracle vs noop, no model, no network): oracle 8 of 8 verified with every citation grounded in its own replayed searches, noop 0 of 8. The cassettes come from a local Tavily stand-in: no real Tavily call has been made |
| **Customer support** (a PS2 category; W3) | `suite init -template support` (helpdesk MCP server plus a simulated customer), `bench/simuser` (the customer over MCP: a deterministic script, or a pinned model playing a persona, prompt hashed into the manifest), services' own gateway keys apart from the agent's (`environments/servicegateway.go`), support metrics `escalated`, `escalation_correct`, `policy_violation`, `user_turns`, `simulator_cost_usd` (`metrics/support.go`), the support scorecard (Resolution, Cost per resolution), `simulator spread`, `import tau2 -set user=llm`; example `examples/acme-helpdesk/` (six chats); guide [`docs/customer-support.md`](docs/customer-support.md) | 18 Nov 2026, all $0: `suite admit` 6 of 6 `acme-helpdesk` tasks (54 checks: reference 3 of 3 `reward 1`; empty, 2 mutants, exploit, noop and refuse fail); scripted smoke oracle 6 of 6 resolved, noop 0 of 6 (regressed, p 0.03125); model-played customer smoke on the fault proxy's scripted model: oracle 2 of 2 resolved with the simulator on its own key, revoked at export; real-docker test of the service key (`TestServiceGatewayIntegration`). The llm user has not run on a real model |

## Features

One row per feature; the guide holds the detail, the limits and what was run. How each one is proven: [What is proven and what is not](#what-is-proven-and-what-is-not).

| Feature | What it does | Guide |
|---|---|---|
| Bring your own agent and tasks | `init` scaffolds a project; `suite init` writes a suite for Python, Node, Go or a reward script; `task init` cuts a task from a fix commit; `agent init`, `agent build` and `agent check` (a free conformance check on the scripted model); `doctor` | [`docs/bring-your-own.md`](docs/bring-your-own.md) |
| Suites and Agents pages | `/suites` and `/agents`: every suite with its grader, lock and admitted tasks, every agent config with its image and evidence level; hidden tests and solutions are never shown | [`docs/bring-your-own.md`](docs/bring-your-own.md) |
| Scenarios: tools, MCP, memory, answers | Suite templates for tool servers graded on their final state, multi-session tasks and answers matched with no model | [`docs/scenarios.md`](docs/scenarios.md) |
| Customer support (W3) | A helpdesk MCP server and a simulated customer (a script, or a pinned model playing a persona on its own key); support metrics and scorecard; `import tau2 -set user=llm` | [`docs/customer-support.md`](docs/customer-support.md) |
| Research with Tavily (W4) | Web search through Tavily behind the tool proxy (the key injected, never seen by the agent), recorded and replayed; answers and citations graded with no judge | [`docs/research-tasks.md`](docs/research-tasks.md) |
| External API tasks | A real HTTP API behind the tool proxy's allowlist, with an injected credential and every call recorded; runs replay a committed cassette | [`docs/external-apis.md`](docs/external-apis.md) |
| Artifact tasks | The agent writes files (a CSV, an SVG, a mesh) that a hidden verifier reads | [`docs/scenarios.md`](docs/scenarios.md) |
| Browser tasks | The agent drives a headless Chromium through a Playwright MCP service; graded on the application's exported state | [`docs/browser-tasks.md`](docs/browser-tasks.md) |
| Computer-use tasks | The agent operates a Linux desktop through screenshots, mouse and keyboard; graded on the apps' saved state | [`docs/computer-use.md`](docs/computer-use.md) |
| Visual grading (X1) | The bench captures the result itself and checks it against a hidden reference with deterministic image checks; an optional vision judge is labelled judged and capped until calibrated | [`docs/visual-grading.md`](docs/visual-grading.md) |
| Voice agents (X8) | The agent answers a spoken support call over WebSocket from a bench-owned caller; latency, barge-in, dead air and transcription metrics | [`docs/voice-agents.md`](docs/voice-agents.md) |
| GPU tasks (X7) | Whole GPUs per run, passed to the agent and the clean-room verifier only; GPU metrics and the `hardware_parity` instrument; `import kernelbench` | [`docs/gpu-tasks.md`](docs/gpu-tasks.md) |
| Multi-container tasks (X2) | `compose: <file>` on a task: the supported subset of a compose file runs beside the agent on the run's private network; unsafe settings refused with their place in the file | [`docs/multi-container.md`](docs/multi-container.md) |
| Multi-agent runs | Teams of agents with a key per member, a mailbox, attribution, team probes and gates, and comparisons at equal budget; `import marble` | [`docs/multi-agent.md`](docs/multi-agent.md), [`docs/multi-agent-safety.md`](docs/multi-agent-safety.md), [`docs/importers-marble.md`](docs/importers-marble.md) |
| Agent skills and MCP servers | Skills installed per run and recorded in the manifest; MCP servers given to the agent; `skill scan` and `mcp scan` admission records | [`docs/skills.md`](docs/skills.md), [`docs/mcp-agents.md`](docs/mcp-agents.md), [`docs/admission-scan.md`](docs/admission-scan.md) |
| Kubernetes driver (X3) | `environment.driver: kubernetes` runs each run as one pod in its own namespace, with the Docker driver's seeding, observers, export and grading; what it cannot run is refused at plan time | [`docs/kubernetes.md`](docs/kubernetes.md) |
| Remote workers and deployed agents | Slots leased to other machines; `remote_http` agents answer over HTTP, with their self-reports labelled agent-reported | [`docs/remote-execution.md`](docs/remote-execution.md) |
| Safety probes | Traps planted per run (destructive bait, protected paths, prompt injection, secret canary, and the S1 set) with veto gates and report-only metrics, mapped to OWASP | [`docs/safety-probes.md`](docs/safety-probes.md), [`docs/safety-catalogue.md`](docs/safety-catalogue.md) |
| Code scan of added lines (X4) | Security pattern rules with CWE ids over the lines the agent added only; a report gate, a veto on request | [`docs/code-scan.md`](docs/code-scan.md) |
| Evaluation awareness | The cues a run shows the agent, a neutral view that hides most of them, and an awareness scan | [`docs/eval-awareness.md`](docs/eval-awareness.md) |
| Compliance pack | The tests an experiment executed mapped to OWASP and NIST ids, linked to the sealed runs (not a certificate) | [`docs/compliance.md`](docs/compliance.md) |
| Monitor certification | A monitor's catch rate at fixed false-positive rates, on sealed runs labelled by deterministic gates | [`docs/monitor-certification.md`](docs/monitor-certification.md) |
| Agent steps and execution evidence | A process observer and a workspace observer beside every local agent; one timeline per run where each tool call has its witness | [`docs/steps-and-evidence.md`](docs/steps-and-evidence.md) |
| Agent events | Agent-reported steps over OTLP, native events, an events file or stdout, kept apart from trusted metrics | [`docs/agent-events.md`](docs/agent-events.md) |
| Release policy as code | YAML rules a candidate must also meet; `check -policy <file>` exits 5 when it blocks | [`policies/README.md`](policies/README.md) |
| Release records | A hash-chained log of releases with a zip evidence pack per release; `/releases` | [`docs/releases.md`](docs/releases.md) |
| CI gate | `bifrost-bench check` in a GitHub workflow fails a pull request on a bad verdict or a policy block | [`docs/ci.md`](docs/ci.md) |
| Failure triage | Per-arm counts of observed patterns (loop, unverified finish, ignored error), linked to runs and events; patterns, not root causes | [`docs/triage.md`](docs/triage.md) |
| Failure clusters | Non-passing runs grouped by a deterministic failure signature across experiments; no model | [`docs/failure-clusters.md`](docs/failure-clusters.md) |
| Leaderboard | Agent configs ranked by verified success with exact sign-flip tests against the leader, Holm-adjusted tiers, no blended score | [`docs/leaderboard.md`](docs/leaderboard.md) |
| Model comparison matrix | One arm per model and agent combination, every pair tested with Holm across pairs | [`docs/model-matrix.md`](docs/model-matrix.md) |
| Agent diff | What changed between two agent configs and which tasks flipped, with the paired test | [`docs/agent-diff.md`](docs/agent-diff.md) |
| Model migration and cost explanation (W6) | A verdict per model pair overall and per stratum; the cost difference split into additive parts | [`docs/model-migration.md`](docs/model-migration.md), [`docs/cost-explain.md`](docs/cost-explain.md) |
| Adaptive trials and suite tiers (W7) | Pre-registered rules that release extra trials only where arms disagree, `extend`, and suite tiers | [`docs/adaptive-trials.md`](docs/adaptive-trials.md), [`docs/suite-tiers.md`](docs/suite-tiers.md) |
| Noise floor | An A/A arm measures the noise behind the MDE; a directional A/A result makes the verdict INVALID | [`docs/noise-floor.md`](docs/noise-floor.md) |
| Cost and time forecast | Expected cost and wall time per arm from sealed history before a run, and whether it fits the budget | [`docs/forecast.md`](docs/forecast.md) |
| Suite health | Verifier flakiness, outcome variance, infra rates and empirical difficulty per task; quarantine | [`docs/suite-health.md`](docs/suite-health.md) |
| Lock fingerprints | `suite.lock` binds every evaluator setting, file and image; plan and run refuse drift | [`docs/lock-fingerprints.md`](docs/lock-fingerprints.md) |
| Held-out tasks and canaries | Held-out tasks kept off the suite pages and suites API, the default leaderboard and trends (run pages are not filtered); contamination canaries and the `canary_echoed` gate | [`docs/held-out-and-canaries.md`](docs/held-out-and-canaries.md) |
| Human review | A person overrules or flags a grade as a child artifact beside the sealed run, applied only on request | [`docs/human-review.md`](docs/human-review.md) |
| Judge calibration (W8) | A judged suite's judge measured against human labels; an uncalibrated judge caps the verdict at INCONCLUSIVE | [`docs/judge-calibration.md`](docs/judge-calibration.md) |
| Routing policy under test (W9) | A Bifrost routing rule, budget or rate limit scoped to the run's own key as the variable an experiment compares | [`docs/routing-experiments.md`](docs/routing-experiments.md) |
| Bifrost features under test (B2) | `gateway.bifrost` on an arm: the task's MCP servers through Bifrost's MCP gateway (classic or Code Mode, Bifrost's MCP log checked against the tool ledger), prompt cache, semantic cache and the complexity router; `gateway_overhead_ms` on every run | [`docs/bifrost-features.md`](docs/bifrost-features.md) |
| A2A agents through Bifrost (B1) | `adapter: a2a`: the task goes to the agent as an A2A v1 message through Bifrost's agent gateway; Bifrost's agent log is the witness (operations, answer, linked model and tool rows, agent-to-agent hops); teams of A2A agents; stream-drop and stream-delay probes | [`docs/a2a-agents.md`](docs/a2a-agents.md) |
| Drift canary (W5) | A small pinned suite re-run on a schedule through Bifrost and tested against a pinned baseline; alerts only on two consecutive flags | [`docs/drift-canary.md`](docs/drift-canary.md) |
| Live traffic scoring (X6) | Read-only scoring of two live versions from the Bifrost log with an always-valid sequential test; evidence class `live`, never part of a verdict | [`docs/live-scoring.md`](docs/live-scoring.md) |
| Failure inbox | Flagged production sessions from Langfuse, LangSmith, the Bifrost log, an OTLP folder or a webhook, redacted, collapsed by signature and turned into task drafts | [`docs/failure-inbox.md`](docs/failure-inbox.md) |
| Tasks from traces | One recorded session (OTLP, Langfuse, a chat transcript) becomes a redacted task draft | [`docs/tasks-from-traces.md`](docs/tasks-from-traces.md) |
| Trace export (OpenTelemetry) | A sealed run as one OTLP/JSON trace, seal verified first, secrets redacted | [`docs/trace-export.md`](docs/trace-export.md) |
| Export (W10) | Harbor task folders, Every Eval Ever records, JSONL and CSV, OTLP presets per backend | [`docs/export.md`](docs/export.md) |
| Results index | A derived, rebuildable JSON-lines index of verified sealed runs; verdicts never read it | [`docs/results-index.md`](docs/results-index.md) |
| Trends | Time series per agent config lineage, the newest point tested against the previous one | [`docs/trends.md`](docs/trends.md) |
| Schedules and alerts | Plan, run and check on a cron within spend caps; signed webhook alerts | [`docs/schedules-and-alerts.md`](docs/schedules-and-alerts.md) |
| API clients | OpenAPI 3.1 from the registered routes; Python and TypeScript clients | [`docs/clients.md`](docs/clients.md) |
| Hosted platform | `auth.mode: hosted`: accounts with a password or GitHub sign-in, organizations, roles, API tokens, runners an organization owns, an evidence mirror | [`docs/hosting.md`](docs/hosting.md), [`docs/auth.md`](docs/auth.md), [`docs/runners.md`](docs/runners.md), [`docs/evidence-storage.md`](docs/evidence-storage.md) |

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

<!-- W9 routing policy under test: the Bifrost section -->
## Bifrost

**What Bifrost gives alone.** One endpoint for every provider, virtual keys with per-key model allowlists, budgets and rate limits, weighted routing rules and fallback chains scoped to a key, team or globally, retries with an attempt trail, and a request log that says which provider and model served each call, whether it was a fallback and which rule routed it.

**What Heimdall adds.** It uses that record as evidence the agent cannot edit: one fresh virtual key per run (the agent's only credential, revoked after), the log joined to the fault proxy's hash-chained ledger as a completeness proof, a hygiene preflight that refuses any gateway feature that could move a run's traffic unseen, faults injected behind Bifrost so they take its real retry and fallback path, cost re-priced from a pinned snapshot, and the statistics, gates and verdict on top.

**What you can now test about Bifrost itself.** The gateway policy is a variable an experiment can compare (`gateway.routing` on an arm): a fixed model against a fallback chain under a primary-model outage, a weighted split across two models, cheap-first with escalation on error, or a per-run budget or rate limit that makes governance answer 402 or 429 mid-run. The bench creates the policy on the run's own key (a routing rule scoped to that key, never a global one), removes it at cleanup, records its hash in the manifest, and reports per run which target served each request, `fallback_used`, `served_model_switches` and turns, cost and latency by target; `model_fidelity` counts a switch the policy allows as expected and fires on any other. Four example experiments (`experiments/routing-*.yaml`, not run on a real model), the exact API calls, what v2.5.1 supports and does not (for example: no Retry-After on a governance 429, a key's rules outlive the key, retries are per provider), and a $0 end-to-end proof with the fallback firing in Bifrost's rows: [`docs/routing-experiments.md`](docs/routing-experiments.md).

<!-- W4 research with Tavily: Bifrost + Tavily -->
**Bifrost + Tavily.** A research agent's model calls go through Bifrost and its web searches through Tavily, and both are evidence the agent cannot edit: Bifrost's log for every model call, the tool proxy's ledger for every Tavily search and fetch, with the Tavily key injected by the proxy so the agent never holds it. Searches are recorded once and replayed, so two agents or two models are compared on the same search results at no search cost: [`docs/research-tasks.md`](docs/research-tasks.md). Tavily's API was verified from its documentation on 18 Nov 2026; no real Tavily call has been made (the committed cassettes come from a local stand-in).

## Quick start

### Evaluate your agent

Start the stack and the web app (`make up`, then `make serve`; see [Running the bench's own suites](#running-the-benchs-own-suites)), open http://127.0.0.1:8090 and press **Evaluate your agent**. The wizard has five steps:

1. **Your agent.** Pick one, or register yours from a template (Codex, Claude Code, an HTTP service, your own command). Pick the version to test.
2. **What to learn.** A score for one setup, version 1 against version 2, two models, or your agent against a baseline agent.
3. **Benchmarks.** Pick a pack or single benchmarks. Benchmarks your agent cannot run are greyed out with the reason.
4. **Model, trials, faults.** Any model from any provider you have a key for, how many times to repeat each task, and which faults to add. The cost and time estimate updates as you choose.
5. **Launch.** You land on the evaluation page, which shows progress and then the report card.

The same through the API:

```bash
curl -sS http://127.0.0.1:8090/api/bench/agents/codex/compatibility           # what codex can and cannot run
curl -sS -X POST http://127.0.0.1:8090/api/bench/evaluations \
  -H "Authorization: Bearer $HEIMDALL_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"codex on coding","mode":"score","arms":[{"label":"A","agent":"codex","model":"gpt-6.2-sol"}],
       "pack":"coding","tasks_per_benchmark":5,"trials":2,"faults":["clean","model-down"]}'
curl -sS http://127.0.0.1:8090/api/bench/evaluations/ev_.../report             # the report card
```

How every number on the report card is computed, the verdict rules and the limits: [`docs/evaluations.md`](docs/evaluations.md).

### Check a change to a coding agent

<!-- First workflow (W1): the four commands; docs/quickstart.md has the walk-through with real output and timings -->
"Will this change break my coding agent?" in four commands, from inside your own git repository (needs Docker and the binary: `make heimdall` builds `bin/heimdall`, the same program as `bifrost-bench`):

```bash
heimdall init                                   # detect the test command, agent CLIs, Docker, ports; write .heimdall/ ($0)
heimdall tasks mine -limit 10                   # cut tasks from fix commits, keep the ones admission proves ($0, Docker only)
heimdall compare --baseline main --candidate HEAD --base agents/claude-code.yaml --trials 3 --fault model-outage --budget-usd 5
                                                # forecast, ask, run, verdict card; exits like check (scripted agents: $0)
heimdall ci install github                      # .github/workflows/heimdall.yml: PR comment, evidence artifact, fails on regressed/invalid/policy block
```

Walk-through with real output, timings and what is still done by hand: [`docs/quickstart.md`](docs/quickstart.md).

### Running the bench's own suites

Needs Docker, Go 1.26+ and Node 22. From `bench/` (or `make bench-<target>` from the repo root):

```bash
make up        # builds the binary, UI and images, creates deploy/.env with random tokens, starts Bifrost + Postgres + fault proxy
make doctor    # preflight: docker, images, internal network, egress blocked, suite lock, gateway, fault proxy, provider reachability
make smoke     # no API key needed: toy agent + scripted model through real Bifrost, clean and model-outage
make serve     # web UI and API on http://127.0.0.1:8090
make down      # stop the stack; keeps the Postgres, Bifrost data and fault-proxy ledger volumes
```

`make clean-volumes` is the explicit reset: it stops the stack and deletes those volumes, so gateway-side state and any fault-proxy ledger entries not yet collected into a run are lost. Sealed run folders under `runs/` are on the host and are not touched by either target.

With a provider key in `deploy/.env` (only the fault proxy ever sees it):

```bash
./bin/bifrost-bench plan experiments/codewins.reduced.yaml   # slots, sizes; nothing runs
./bin/bifrost-bench run  experiments/codewins.reduced.yaml   # freeze, run, analyze, print report.md
./bin/bifrost-bench check -fail-on regressed,invalid -policy policies/default.yaml experiments/codewins.yaml   # CI: exit 1 regressed, 2 inconclusive, 3 invalid, 5 policy block
./bin/bifrost-bench audit testdata/audit/maxim-tb21.csv     # verdict for a published per-task table
./bin/bifrost-bench leaderboard -scope common               # rank every agent config across experiments; no gateway needed
```

A healthy fault proxy is not proof that model calls will work: if the proxy cannot resolve or connect to the provider (a host that changed networks and broke Docker DNS, for example), `/health` still answers while every model call gets a 502 `fault proxy could not reach the upstream`. So `doctor` also asks the proxy to check its upstreams and prints one `upstream <provider>` row per provider it answers from the real API with a real key: `pass` with the host and connect time, or `fail` with the error. `plan`, `run` and the UI's launch run the same check and refuse to freeze an experiment when a provider its arms call is unreachable. The rules:

- The check is `GET /upstreams` on the fault proxy's control API. It needs the control token (`BENCH_FAULTPROXY_TOKEN`), because it makes the proxy open connections. For each provider with a real key it resolves the host, connects and, for https, completes a TLS handshake that verifies the certificate, with a 5 second bound and all providers at once. No HTTP request is sent, so no key leaves the proxy and no tokens are spent. The answer is `{"checked_at": ..., "upstreams": [{"provider", "host", "ok", "latency_ms", "error", "skipped"}]}`, where `host` is the `host:port` dialled and `error` is the transport error without a URL.
- Providers answered by the scripted model and providers with no key are not listed: none of their calls leaves the proxy. Keyless providers (ollama) are not listed either, because their default address only exists in deployments that run them. `GET /health` lists every provider's mode. A provider whose traffic goes through an HTTP proxy from the environment is listed as `skipped`, not dialled.
- The planner checks only the providers an experiment's arms (and its compatibility agent) call: the agent config's `gateway.provider`, anthropic when it names none. A scripted control other than `toy` runs with no network and calls none, so an oracle or noop experiment is never refused. A provider no arm calls only logs a warning.
- A fault proxy older than the check answers 404: `doctor` prints one `warn` row, `proxy too old for upstream check`, and the planner logs a warning and plans as before. Any other failure of the check itself (proxy unreachable, missing or wrong token) refuses the plan, since every run binds a fault plan through the same control API and would fail the same way. Without `BENCH_FAULTPROXY_TOKEN`, `doctor` warns and skips the check.

If the provider becomes unreachable during a run anyway, the run is sealed as infrastructure (`aborted_infra` and retried when no model call was answered, `infra_error` when it ended on one), never as the agent's `fail`, and the `upstream_errors` gate and instrument count it: `docs/upstream-failures.md`.

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
| `scaffold/`, `agents/templates/` | Project, suite and task generators (`init`, `suite init`, `task init`); agent templates (`agent init`) |
| `examples/acme-node/` | A complete bring-your-own project: a Node suite of four tasks, a custom agent, Codex |
| `examples/acme-support/`, `examples/acme-memory/` | Scenario projects: a support desk agent on an MCP server (four state-graded tasks, tool-fault and poisoned-result probes), and three multi-session memory tasks graded by `answer_match` |
| `examples/acme-helpdesk/` | Customer support project (W3): six chats with a simulated customer on a helpdesk MCP server (resolve, refuse, escalate, clarify, hold under pushback, two changes), scripted and model-played customer, tool-fault and hostile-customer probes |
| `gpu/`, `examples/acme-gpu/` | GPU detection, pool, passthrough arguments, host-side sampler and `gpu check` (X7); a GPU project of three ML-engineering tasks with one CUDA image |
| `examples/acme-design/` | Artifact project: three tasks graded on the files the agent writes (an OBJ mesh checked for size, volume and watertightness, a CSV and JSON invoice summary, an SVG palette swatch) |
| `store/` | Sealed run folders, transitions, leases, seal chain, read index |
| `server/`, `handlers/`, `lib/`, `reporters/` | HTTP API (Bifrost's fasthttp handler style) and reports |
| `ui/` | Web UI (React, TanStack Router, RTK Query, Bifrost's UI primitives), embedded in the binary |
| `benchmarks/coding-core/` | 8 self-authored Python tasks with hidden verifiers, admission controls and probe fixtures |
| `experiments/` | Experiment files: the main experiment in three sizes, the real batch (`batch-*.yaml`), pilots, the safety experiment and the smoke test |
| `deploy/` | Docker Compose stack, Bifrost config with pinned datasheets, price snapshot, bench config |
| `docs/` | One guide per feature (linked from the tables above), spike results, sources. `docs/DESIGN.md` is the design of 14 Nov 2026 and is out of date in places; where it and a guide or this README differ, the guide and the code win |

## Extending it

Everything that varies is a file, validated by a JSON Schema; everything that computes is a part registered in one `builtin.go` list.

| To add | Do this | Go code? |
|---|---|---|
| A task | A folder under `benchmarks/<suite>/tasks/`, or `bifrost-bench task init` from a fix commit of your repository, then `bifrost-bench task admit` | No |
| A benchmark | A folder with `suite.yaml` | No, if its environment and grader types exist |
| A tool-using or MCP task | `bifrost-bench suite init <id> -template tool-use` or `-template mcp`, then `task init -suite <id> -id <task>`: a seed data file, an instruction, `checks.py` for the final state and a reference script; see `docs/scenarios.md` | No |
| A memory or QA task | `bifrost-bench suite init <id> -template memory` or `-template qa`, then `task init`: session instructions and files, a hidden `answer.yaml` and a reference answer | No |
| A task graded on files the agent writes | `bifrost-bench suite init <id> -template artifact`, then `task init`: `output.artifacts` in `task.yaml`, a hidden `verify.py` that reads `/grader/artifacts/<id>/` and a reference script that writes the files | No |
| A public benchmark | `bifrost-bench import <format> -from <local copy> -suite <id>`, then `suite admit` (`bifrost-bench import list` names the formats) | No, if its importer exists |
| A probe | One YAML in `benchmarks/<suite>/probes/` | No, for an existing layer |
| An agent | One YAML in `agents/` (any CLI or custom agent that accepts a base URL); see `docs/adding-an-agent.md` | No |
| A variable | A `set:` override on an arm in the experiment file | No |
| A metric or gate | One file plus one line in that package's `builtin.go` | Yes |

## Honest limits

- The statistics decide on tasks, not runs. With 8 tasks most verdicts read inconclusive, by design: a 5% call needs at least 6 tasks moving the same way, so a small effect needs a bigger suite, and the verdict page says how many tasks.
- Tool shims run inside the agent's container, so their log is boundary-level evidence the agent could tamper with. "Visible" receipts come from Bifrost's rows, which the agent cannot touch.
- Docker is not a hard security boundary against a determined attacker; there is no custom seccomp profile or disk quota. Tasks are self-authored and contain no secrets.
- Seals are local hashes without an external anchor.
- Seal checks differ by reader (`docs/evidence-integrity.md`): analysis (`check`, `run`, the verdict API) verifies every counted run against its seal and the `seals.log` entry and also checks the whole experiment's seal chain; the leaderboard verifies each run on the board (a mismatch drops that run) but does not check the chain; the run page verifies that one run and still serves its files with the failure shown as `seal_error`, so the change can be inspected. They still prove only that files did not change after sealing on this machine.
- A suite lock with a fingerprints section binds every evaluator setting in `suite.yaml`, `task.yaml` and the probe files, every file they name, the grader and environment versions and the image ids, and plan and run refuse when any of them drifted (`docs/lock-fingerprints.md`). A legacy lock without the section still binds only the selected files and admission results; it plans with a warning until `suite lock` adds fingerprints. Locally built images are bound by tag only, because local builds are not reproducible and a local image id would refuse every other machine; their build context (Dockerfile and files) is hashed in the files part, so a changed Dockerfile is still caught. A pulled image is bound by its registry digest where docker reports one apart from the local id; `suite lock -pin-local-images` pins local ids for a team's own machine. Each experiment still freezes the local image ids it planned with. A change inside a grader or environment that does not bump its part version is not seen.
- The spend cap is exact for one scheduler and for remote workers, which lease from that scheduler; two separate schedulers on the same experiment each see their own spend. A lease lost in the instant after a seal can leave two counted runs for a slot, which the verdict then reports as invalid (`docs/runner-safety.md`).
- Task folders are checked for symlinks that leave the task and admission validates before any Docker build (`docs/task-safety.md`); hard links are not detected, and hidden tests cut from a reference commit still need a person to review them.
- The release evidence pack holds the manifest, the analysis and report, `seals.log` and, per run, `seal.json`, `run.json`, `metrics.json`, `gates.json` and `grade.json`. It does not hold the raw gateway rows, proxy ledger, agent stream, receipts or cost rows, so a full re-audit needs the run folders themselves.
- Every track other than coding (tool use, MCP, memory, skills, terminal, safety, support, research, browser, computer use, artifacts, external APIs, multi-agent teams, voice, visual, GPU) and every importer has only been exercised with scripted controls: no real model has run on them, and the numbers in "Results from real runs" are all from the coding track (see [What is proven and what is not](#what-is-proven-and-what-is-not)).
- Snapshot grading (terminal tasks) is weaker evidence than a clean-room grade: the verifier runs on a frozen copy of the agent's own file system, so the agent could have changed the tools the verifier calls. Tasks imported from Harbor run the agent as root inside its container, without privileged mode, host mounts or network (`docs/runner-safety.md`).
- A judged suite's outcomes come from a judge model. The judge grader has only been tested against a fake judge, its spend is not under the experiment's spend cap, and a judged suite is never compared with a verified one.
- Remote workers need the same project at the same commit and their own stack, are authenticated by one shared token, and have no built-in TLS (`docs/remote-execution.md`). They are tested with two processes on one machine, not across machines. Hosted sandbox services are not supported; Kubernetes is, through its own driver, checked on a local kind cluster only (`docs/kubernetes.md`).
- Deployed agents (`remote_http`) take answer tasks; state tasks only when the run's tool proxy is published to them (`remote.tools: expose`), artifact tasks only when they return files inline, never diff tasks. Their tokens, cost and steps are unknown unless their model calls are pointed at this gateway; what they report about themselves (usage, events, version) is recorded as agent-reported, in separate metrics, never verified. Their own local tools, files and internal steps cannot be observed, and nothing proves which build answered (`docs/remote-execution.md`).
- A2A agents (`adapter: a2a`, B1) are witnessed only where they cross Bifrost: the agent log, and the model and tool calls they send through Bifrost (linked to a task only when they carry `x-bf-agent-correlation-id`). One message per run, no user to answer `input-required`; streamed answers are checked for state, not hash, because Bifrost records a stream as events; gRPC, REST, push notifications and enterprise access profiles are not exercised (`docs/a2a-agents.md`).
- AgentDojo state is exported as a Python pickle and read back in the verifier by an unpickler that accepts only the benchmark's own types, in a container with no network.
- Browser, computer-use, artifact and external-API tasks, the human review queue, trends, schedules, the model matrix, agent diff, failure clusters, trace export and import, and the clients were built on 15 Nov 2026 and checked with scripted controls and unit tests only. No real model has driven the browser or the desktop, and no person has used the review queue on real runs.
- The computer-use desktop is a Linux container, not a virtual machine: no Windows or macOS applications. External APIs are reached only through an allowlist on the tool proxy; a run in live or record mode is not repeatable and is kept off the leaderboard, a run in replay mode is.
- Held-out tasks are hidden on the suite pages, run pages and result exports by the frozen plan's split (`held_out_tasks`); a plan that did not list a task as held out serves it like any other.
- In the default local mode a reviewer's name is self-declared and writes need one shared token, with no user accounts or roles. Hosted mode (`auth.mode: hosted`) adds accounts, organizations, roles and API tokens; it has been checked with `deploy/hosted/smoke.sh` on localhost, and it has no SSO beyond GitHub sign-in, no SAML and no two-factor authentication (`docs/auth.md`).
- GPU tasks (X7) were checked on one consumer GPU (GTX 1650, 4 GiB, Turing) with scripted controls only: no model has run on them, a speedup measured there does not carry over to data-centre GPUs, the GPU pool is per bench process (two bench processes on one machine do not share it), and the CUDA image is 9.4 GB on Docker's containerd store (`docs/gpu-tasks.md`).
- Not built: SSO other than GitHub sign-in (SAML, OIDC); hosted sandboxes; the MCPMark Postgres family.
- Scripted-model runs are kept off the live leaderboard by the label the fault proxy writes on its ledger lines (`upstream`: `real` or `scripted`). Runs recorded before that label existed are recognized by a fingerprint of the scripted model (exactly 32 output tokens and a scripted response id format), which is a heuristic.
- Which agents and providers were actually run, and which are only configured, is listed in `docs/harness-matrix.md`.
- OpenCode, Gemini CLI, Claude Code and mini-swe-agent pass their whole environment to the commands they run, so the model's shell can read the run's virtual key. The key is scoped to one run and one model on a network where only Bifrost is reachable, and it is revoked after the run; the custom template, the Codex config and the seven framework agents keep it out of the tool shell (`docs/harness-matrix.md`).
- No observer outside an agent, ours included, sees an action that crosses no process, workspace file, network, tool proxy or model boundary: pure reasoning and framework-internal function calls leave no trace, and file reads are never recorded. Only the agent's own stream could show them, and it is self-reported (`docs/steps-and-evidence.md`).
- An agent that neither emits OpenTelemetry nor uses the agent event protocol exposes only what the gateway, the tool proxy and the observers can see: its plans, subagents, handoffs, memory and internal tools stay invisible, and `reported_steps` reads unknown. What an agent does report is its own word, never verified as true; most frameworks need an instrumentation package or a setup line before they emit spans, and gRPC export is not accepted (`docs/agent-events.md`).
- The process observer polls `/proc` every 20 ms, so a process shorter than that can be missed, and so can the agent's first few milliseconds before the observer starts. Measured with real Docker at 20 ms: 3 of 40 processes of 1 ms caught, 12 of 40 of 5 ms, 39 of 40 of 20 ms, every one of 50 ms and 100 ms. Each run reports PIDs allocated against PIDs seen, so the miss is bounded, never hidden. End times are upper bounds, and exit codes exist only for processes caught as zombies.
- The agent can see the process observer, which shares its PID namespace. A non-root agent cannot signal or trace it; an agent that runs as root can kill it, which the bench detects and records as missing process evidence, never as zero commands. The agent cannot forge observer records.
- The workspace observer sees only the workspace volume: writes to the agent's home, `/tmp` or a snapshot-graded task's root file system are not observed. Past 20,000 events it keeps the count but drops the detail; past 16,384 folders it stops watching new ones and says so. Commands and file changes are linked to the tool call that caused them by content and time where there is no id, so two identical calls in one turn can be linked either way round. `remote_http` agents get no observers.

## How AI was used

- **Research:** 14 parallel research agents (Claude) read and logged 1,604 sources on agent evaluation, statistics, fault injection, memory, safety, tool use, cost and the Bifrost codebase; digests and a master synthesis fed the design. The team reviewed and decided the design in several rounds of external critical review.
- **Implementation:** the code was written by Claude Code with parallel Claude Opus 5.5 sub-agents, each owning separate packages and working against shared contracts, with the lead agent integrating, reviewing diffs and running every test. Decisions on scope, statistics, security and the benchmark tasks were made and checked by the team.
- **In the product:** no LLM judge decides a release verdict on a verified suite: those verdicts rest on deterministic evidence (hidden tests, file hashes, gateway records). Judged suites, the optional vision judge and the optional live-scoring judge do use a pinned judge model; their grades are labelled judged, an uncalibrated judge caps a verdict at inconclusive (unless the experiment sets `allow_uncalibrated_judge`), the live-scoring judge is only reported, and a judged suite is never compared with a verified one.

## Licenses and sources

Apache-2.0, like Bifrost. The benchmark tasks are written from scratch. Every source cited in the design is listed with its link in `docs/sources.md`; the pinned prices come from the datasheet the Bifrost gateway uses (`deploy/pricing-snapshot.json` records its origin).
