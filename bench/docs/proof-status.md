# What is proven and what is not

Moved here from `bench/README.md`. Paths are relative to `bench/`.

**Only the coding track has real-model results:** six experiments on 14 Nov 2026, 318 sealed runs, `gpt-5.4-mini` on OpenAI through stock Bifrost v2.5.1, with Codex CLI 0.169.0 and the bench's own no-framework `custom-python` agent, $8.08 in all ([Results from real runs](../README.md#results-from-real-runs)). Even there, 5 of the 6 verdicts are inconclusive, none has said "improved", and the one directional verdict (regressed, `e_batch_frameworks_9z0h`) comes from the integrity guard on one run (`r_mxfbr3dqg3`), not from a difference in success. The 318 run folders (about 560 MB) are git-ignored: the repository keeps every report, manifest and verdict, and the full sealed folders of the 10-run pilot `e_pilot_mixed_nc93` only, whose seals `GOWORK=off go test ./ci/` re-verifies.

**Every other track and feature is proven with scripted controls at $0:** scripted agents (oracle, noop, exploit, refuse, unsafe controls), the fault proxy's scripted model, local fakes and stand-ins of third-party APIs, and real Docker. That shows the plumbing works and the controls come out as expected; it says nothing yet about how a real agent or model does on those tracks. "Last proof" is the experiment the guide names as its most recent check; "-" means the guide names unit, integration or admission tests instead of an experiment id. Only the six real experiments (`docs/results/`) and the two recorded bundles (`testdata/recorded/`: `e_pilot_mixed_nc93`, `e_smoke_sdoj`) ship their evidence in this repository. Every other id below is a manual demonstration recorded in its guide: its run folder stayed on the machine that ran it, so it cannot be re-checked from a fresh clone, and an id marked INVALID is a run that exercised an instrument, not a pass.

| Track or feature | How it is proven | Last proof |
|---|---|---|
| Coding: repository edits (`coding-core`, 8 tasks) | Real model, 318 runs; admission 60 of 60 scripted controls as expected | `e_batch_frameworks_9z0h` (all six in [`docs/results/`](results/README.md)) |
| Failure probes: `model-outage`, `test-crash`, `empty-test-run`, `stale-notes` | Real model, in the coding batch | `e_batch_frameworks_9z0h` |
| Memory-trap probe | Real model, on the coding track | `e_memory_trap_wn4b` |
| Framework independence | Real model: Codex CLI and `custom-python` only. Seven framework agents (LangGraph, OpenAI Agents SDK, CrewAI, Pydantic AI, LlamaIndex, Vercel AI SDK, a plain OpenAI SDK loop): `agent check` and a scripted-model smoke. Claude Code, OpenCode, Gemini CLI, mini-swe-agent: a fake-API smoke ([`docs/harness-matrix.md`](harness-matrix.md)) | `e_matrix_frameworks_smoke_30uo` |
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
| Leaderboard, failure clusters, agent diff, model migration, cost explain | Computed over the real coding runs; the model matrix itself has not run on a real model | [`leaderboard-clean.md`](results/leaderboard-clean.md), `e_batch_frameworks_9z0h` |
| Release policy, CI gate, release records | A recorded replay in CI; the pilot bundle's seals re-verified by `go test ./ci/` | `e_smoke_sdoj`, `e_pilot_mixed_nc93` |
| Bring your own tasks and agents, quickstart | Fresh-project walkthroughs with scripted controls | `e_controls_webapp_wtym`, `e_walk_oracle_vs_noop_047n` |
| Live traffic scoring | A fixture gateway and a throwaway Bifrost v2.5.1 stack with scripted sessions | - |
| A2A agents (`acme-a2a`) | Scripted reference agents on a separate keyless stack with the scripted model ([`docs/a2a-agents.md`](a2a-agents.md)) | `e_a2a_oracle_vs_noop_qk2t`, `e_a2a_transport_yjil`, `e_a2a_team_hdp8` |
| Bifrost gateway features (MCP, routing, caches) | Scripted model on a throwaway Bifrost stack ([`docs/bifrost-features.md`](bifrost-features.md)) | `e_b2_mcp_proof_5nm0`, `e_b2_complexity_router_proof_s7wx`, `e_b2_semantic_cache_proof_p249` |
| Failure inbox, tasks from traces | Local fakes of the Langfuse, LangSmith and Bifrost log APIs; no real backend called | - |
| Trace export and exports | Local fake collectors for every backend preset; Jaeger called for real in a container | `e_pilot_mixed_nc93` |
| Judged suites and judge calibration | A fake judge only | - |
| Agent steps, observers, agent events | Real-Docker integration tests (observer catch rates measured; events file and collector channels); framework spans in the scripted smokes | - |
| Evaluation awareness, compliance pack, monitor certification, skill and MCP admission scan | Unit tests, plus real-Docker tests of the neutral view and the monitor sandbox | - |
| Remote workers and deployed agents (`remote_http`) | Two processes on one machine | - |
| Hosted platform (accounts, organizations, runners, evidence mirror) | `deploy/hosted/smoke.sh` on localhost: TLS, headers and internal ports, sign-up, an organization, recorded runs imported and anonymous reads refused, the evidence mirror with --s3 | - |
