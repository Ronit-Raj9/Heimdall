# Adoption wave: design contract

Status: in progress (18 Nov 2026). Twelve features that make Heimdall usable
by a team in its first half hour and in its weekly routine. This file fixes
names and ownership so eleven parallel pieces of work fit together. Where it
and the code differ, the code wins and this file is corrected.

## Why these

Research into how teams ship agents (docs: see the lane reports named in the
round rules) found that a benchmark is used in three moments: the gate
before a merge or release, the check when a model or harness is swapped, and
the watch for silent drift. The features below serve those three moments,
cover the other two categories the brief names (customer support, research),
and connect Heimdall to the tools teams already run.

## Rules that do not bend

1. Additive schemas: new fields are `omitempty` (or `omitzero`), so every
   manifest, lock, seal and recorded run keeps its hash. The hash tests in
   `engine` and `suite` stay green.
2. Nothing here decides a verdict from data the bench did not witness. A
   model-played user, a judge, an imported trace and a self-report are each
   labelled with their strength and kept out of trusted metrics.
3. No real provider or third-party call in any test. Everything is proved
   with scripted models, fakes and recorded cassettes, at zero cost. Where a
   real key would be needed (Tavily, Langfuse, LangSmith), the code path is
   tested against a local fake that speaks the same API, and the doc says a
   real call has not been made.
4. Statistics stay honest. A feature that looks at results early or often
   (adaptive trials, the drift canary) controls its false-alarm rate and
   says how; otherwise it reports, and does not decide.
5. The local single-user flow keeps working with no database and no login
   (the hosted platform is built on another branch).

## Names

Binary: the same program also builds as `heimdall`; usage text prints the
name it was started as. `bifrost-bench` stays valid everywhere.

| Verb | Owner | What |
|---|---|---|
| `init` (in a repository, no argument or `-detect`) | W1 | detect test command, agent CLIs, Docker; write `.heimdall/` |
| `tasks mine` (`task mine`) | W1 | cut tasks from fix commits in git history |
| `compare` | W1 | two refs, two configs or two models, one verdict |
| `ci install <github>` | W1 | write the workflow and the PR comment step |
| `canary init|run|status|baseline` | W5 | drift canary |
| `migrate` | W6 | model migration report |
| `cost explain` | W6 | why arm B cost more |
| `judge calibrate|status` | W8 | judge agreement with human labels |
| `export harbor|eee|otlp` | W10 | `otlp` exists; W10 adds presets and the two formats |
| `inbox pull|list|show|accept|reject` | W11 | production failures to task drafts |

Schema additions (all optional):

| Where | Field | Owner |
|---|---|---|
| `schemas/scenario.go` tracks | `TrackSupport = "support"`, `TrackResearch = "research"` | W3, W4 |
| `ServiceSpec` | `Gateway *ServiceGateway{Provider, Model, BudgetUSD}`: the service gets its own virtual key and may call models through the gateway; its spend is reported apart from the agent's | W3 |
| `Suite` | `Simulator *SimulatorSpec` (user simulator settings, pinned like a judge) | W3 |
| `Experiment`, `Manifest` | `Simulator *SimulatorSpec`: the experiment's override for every arm, and the effective settings frozen at plan time (`SimulatorSpec` also has `Mode scripted|llm`) | W3 |
| `Task`, `StateFile` | `Escalation expected|not_expected` (read by `escalation_correct`); `Gateway *ServiceGatewayUsage` on a service's `_gateway.json` export (its own spend) | W3 |
| `Suite` | `Tiers map[string]TaskSelector` (e.g. `pr`, `nightly`, `headroom`) | W7 |
| `Experiment` | `Tier string`, `Trials *TrialsPolicy{Mode fixed|adaptive, Min, Max, ...}` | W7 |
| `GatewayPolicy` (agent config) | `Routing *RoutingPolicy` (targets with weights, fallback chain, rule name) | W9 |
| `Grade` / `JudgeRecord` | calibration reference | W8 |

Metrics (owner adds the descriptor, the direction-table row in
`verdict/direction.go` and a test row in `metrics/builtin_test.go`):

| Metric | Owner |
|---|---|
| `escalated`, `escalation_correct`, `policy_violation`, `user_turns`, `simulator_cost_usd` | W3 |
| `searches`, `sources_cited`, `citations_grounded`, `citation_precision` | W4 |
| `fallback_used`, `served_model_switches`, `routed_target` (derived per target) | W9 |

`verified_success` remains the success metric of every track; on a support
suite reports call it resolution, and cost per verified success is cost per
resolution.

## Ownership

| ID | Work | Packages and files |
|---|---|---|
| W1 | First workflow | `bench/quickstart/`, `scaffold/mine*.go`, `cmd/bifrost-bench/{quickstart,tasksmine,compare,ciinstall}.go`, `bench/ci/` additions, the `heimdall` binary name |
| W2 | Framework agents | `bench/examples/frameworks/`, `bench/agents/` new configs, `experiments/matrix-frameworks.yaml`, `docs/harness-matrix.md`, `docs/frameworks.md` |
| W3 | Customer support | `bench/simuser/`, `metrics/support.go`, `environments/servicegateway.go`, tau2 importer llm user, `scaffold/templates/suites/support/`, `examples/acme-helpdesk/` |
| W4 | Research with Tavily | `bench/research/`, `metrics/research.go`, `scaffold/templates/suites/research/`, `examples/acme-research/`, `docs/research-tasks.md` |
| W5 | Drift canary | `bench/canary/`, `cmd/bifrost-bench/canary.go`, `handlers/canary.go`, `docs/drift-canary.md` |
| W6 | Migration report and cost | `bench/migration/`, `bench/costexplain/`, `cmd/bifrost-bench/{migrate,costexplain}.go`, `handlers/{migration,costexplain}.go` |
| W7 | Adaptive trials and tiers | `engine/adaptive*.go`, `verdict/sequential*.go`, `suite/tiers.go`, `experiments/presets/` |
| W8 | Judge calibration | `bench/calibration/`, `graders/judgecalibration.go`, `cmd/bifrost-bench/judge.go`, `handlers/calibration.go` |
| W9 | Routing policy under test | `gateways/bifrost/routing*.go`, `engine/routingplan.go`, `metrics/routing.go`, `experiments/routing-*.yaml`, `docs/routing-experiments.md` |
| W10 | Export | `bench/export/`, additions in `cmd/bifrost-bench/export.go`, `handlers/export.go`, `ui/components/export/`, `ui/lib/store/apis/exportApi.ts`, `docs/export.md` |
| W11 | Failure inbox | `bench/traceinbox/`, `cmd/bifrost-bench/inbox.go`, `handlers/inbox.go` |

Shared files (`schemas/*`, `metrics/builtin.go`, `verdict/direction.go`,
`handlers/openapi.go`, `server/server.go`, `cmd/bifrost-bench/serve.go`,
`README.md`, `ui/lib/types/bench.ts`, `ui/locales/en/bench.json`,
`ui/lib/constants/navigation.ts`): additive edits only, re-read right before
each edit, one marked line or block per feature.

## UI rule for this wave

Each feature that has something to show ships: TypeScript types, an RTK
Query hook, contract key files, and one self-contained page or panel under
`ui/components/<area>/` with vitest tests, reachable by one navigation
entry. A later pass regroups navigation around Agents, Suites, Experiments
and Runs, so keep pages self-contained and do not restructure shared UI.
