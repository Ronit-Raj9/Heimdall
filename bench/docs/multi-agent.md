# Multi-agent runs

## Contract for M2

Status 18 Nov, M1. Everything below is in `schemas/` now (additive,
`omitempty`, hash tests green) and these names stay stable:
`Evidence.Agents`, `StepRecord.AgentID`, `StepRecord.AgentSource`,
`GatewayRequest.AgentID`.

1. **Spawn caps.** `AgentConfig.Limits *AgentLimits` (yaml `limits`):
   `max_agents`, `max_depth`, `max_concurrent` (ints, 0 = no cap). Part of the
   agent config, so frozen and hashed in the manifest with the arm. Compare
   with `Evidence.Agents`: agents = `len(Agents)`, depth = max
   `AgentNode.Depth`, concurrency = max overlap of `[Started, Ended]`.
2. **Mailbox.** Confirmed: the message bus is an HTTP service reached only
   through the tool proxy, so every message is a ledger row
   (`tools.jsonl`). Service id `mailbox` (`schemas.MailboxServer`), started
   by the bench beside every team run. Each member reaches it on its own
   route, `http://toolproxy:9200/m/<token>/s/mailbox/` (env
   `HEIMDALL_MAILBOX_URL`); the plain `/s/mailbox/` route also works but
   leaves the sender unproven (item 6).
   - Send: `POST /send`, JSON body `{"from": "<member id>", "to": "<member
     id or *>", "body": "<text>", ...any other keys}`; answer `{"id": "<n>"}`.
     Ledger row `Server` `mailbox`, `Tool` `POST /send`
     (`schemas.MailboxSend`), `Arguments` the body as sent.
   - Receive: `POST /recv`, body `{"agent": "<member id>"}`; answer
     `{"messages": [{"id", "from", "to", "body", "fields", "at"}]}`, each
     message once per reader. Ledger row `Tool` `POST /recv`
     (`schemas.MailboxRecv`), `Result` what the reader actually got (after
     any tool fault).
   - The service exports its own record as `state/mailbox/messages.jsonl`.
   - **Picked: `Evidence.Messages []AgentMessage`, sealed as
     `messages.jsonl`**, derived at collect from the ledger rows and the
     export (`evidence/messages.go`). Per message: `Seq`, `ID`, `From`,
     `To`, `Body`, `BodySHA256`, `Size`, `Fields` (send body keys other than
     from/to/body: the unmonitored extra fields), `SenderAgentID` (the member
     whose route the send came in on; proven), `At`, `Delivered`,
     `DeliveredTo`, `DeliveredAt`, `Received` (what the reader got, set only
     when it differs from `Body`), `Altered`, `Fault`, `Nonce` (from the send
     or receive row that carried a fault), `SendSeq`, `RecvSeqs` (ledger
     `Seq`s).
3. **Per-agent targeting.** `ProbeApply.Agent string` (yaml `agent`): a
   member id, or a role (every member with it). `ToolCall.AgentID`: the
   member whose call it was, stamped by the tool proxy from a per-member
   route prefix `/m/<route token>/` (each member's MCP and mailbox URLs carry
   its own unguessable token; the proxy strips it). **Per-member model
   outage: each member is on its own fault-proxy key slot**
   (`MemberKey.KeySlot`; the lead's is the run slot, item 10); a
   gateway-layer probe with `agent` set binds its plan to that member's slot
   only. No agent filter in the fault plan: the
   fault proxy sits behind Bifrost and never sees which virtual key called.
4. **Replay opt-in.** `Experiment.Attribution *AttributionSpec` (yaml
   `attribution`): `replay bool`, `max_replays int`, `stub string`.
5. **Mailbox knobs** (all optional env on the `mailbox` service, so the
   `service` probe layer drives them through `apply.service_env`; all
   accepted, none too costly):
   - `MAILBOX_PLANT`: JSON list `[{from, to, body, fields?}]` stored as
     already-sent messages at start (`planted: true`, `sender` empty; `from`
     may be a non-member id).
   - `MAILBOX_POISON_FROM` + `MAILBOX_POISON_TEXT`: text appended to every
     message from that member when delivered; record `fault: "poison"`.
     "From" is the proven `sender` when the proxy named one, else the
     declared `from`. Faults combine (`poison+truncate`).
   - `MAILBOX_TRUNCATE_TO` + `MAILBOX_TRUNCATE_BYTES`: messages to that
     member cut to n bytes when delivered; record `fault: "truncate"`.
   - Extra JSON keys on a send (outside from, to, body) are stored in the
     record's `fields` and are not part of what a reader receives as `body`
     (they are delivered under `fields`, so an unmonitored channel exists on
     purpose and is measurable).
   - Service record (`state/mailbox/messages.jsonl`, one JSON per message):
     `seq`, `id`, `from`, `sender`, `to`, `body`, `fields`, `at`,
     `planted`, `fault`, `delivered` (the last altered text a reader got,
     only when it differs from `body`), `read_by` (member ids), `read_at`
     (member id to time), `received_by` (member id to the text that member
     got, so a broadcast altered for one reader names that reader), `nonce`
     (item 7). `Evidence.Messages` merges this record with the ledger rows;
     a message's `received_by` lists only the readers whose text differs
     from `body`, and `received` is the earliest of them.
6. **Is `from` proven?** Yes, when the member uses its own URL, by its
   route token, a bench-minted per-member credential like its gateway key
   (the mailbox never sees the gateway key). Every
   member's mailbox URL (`HEIMDALL_MAILBOX_URL`) carries its route token
   (`http://toolproxy:9200/m/<token>/s/mailbox/`); the tool proxy maps the
   token to the member, stamps `ToolCall.AgentID`, drops any
   `X-Heimdall-Member` header the agent sent and sets its own on the request
   to the service. The mailbox stores that as `sender` beside the declared
   `from`; `AgentMessage.SenderAgentID` is `sender`. A call that comes in on
   the plain `/s/mailbox/` route has an empty `sender` (unproven), and a
   `from` that differs from `sender` is kept as sent, so spoofing shows
   instead of being refused. Tokens are 128-bit random per run and member;
   a member could only use another's token if it leaked through the shared
   workspace.
7. **Nonce in mailbox knobs.** Agreed: the mailbox service mints one nonce
   at start in the bench shape `nonce_` + 24 characters of `[0-9a-z]` (as
   `utils.NewNonce`), replaces every literal `{{nonce}}` in `MAILBOX_PLANT`
   bodies and field values and in `MAILBOX_POISON_TEXT` with it, and writes
   it to `nonce` on every record a knob touched (planted, poisoned,
   truncated). `MAILBOX_NONCE` is not needed.
8. **Scripted multi-agent controls.** One agent config per control, no
   per-member scripts: `adapter: scripted` with `scripted.kind: team` and a
   `team:` list. Every member runs the same embedded script
   (`adapters/teamcontrol.py`, written to `/tmp/bench/teamcontrol.py`) and
   acts by `HEIMDALL_AGENT_ROLE` (`lead` or `worker`) and
   `HEIMDALL_AGENT_ID`; the lead reads its workers from `HEIMDALL_TEAM`
   (comma list of member ids) and talks to them only through
   `HEIMDALL_MAILBOX_URL`. Shipped config: `agents/team-control.yaml`
   (members `lead` role lead, `worker-a` and `worker-b` role worker). The
   in-process control is `scripted.kind: subagents`
   (`adapters/subagentscontrol.py`, `agents/subagents-control.yaml`): one
   container that reports `subagent_start`, `tool_call`, `handoff` and
   `subagent_end` as `@@heimdall-event` lines. An example's controls should
   copy `team-control.yaml` and change only `id`, `team` ids and roles
   (the script treats any role other than `lead` as a worker).
9. **Member-targeted gateway probes.** Applied in `engine/stages.go`
   `bindFaultPlan`: a gateway-layer probe with `apply.agent` set is not
   treated as a gateway probe there, so the run slot gets the run's plan
   (scripted mode allowed) and `mintTeam` (`engine/team.go`) binds the same
   plan to every member slot; your layer's `Arm` then overrides only the
   target member's `KeySlot`. Sound with the minting: every member but the
   lead has a slot of its own, the lead's is the run slot (item 10), and
   cleanup clears every member slot (`clearTeamPlans`). `mintTeam` always
   binds the run's plan to every non-lead member slot (so the fault proxy
   tags their calls with the run) and never touches the run slot, which
   `bindFaultPlan` owns. A whole-run gateway probe (no `agent`) on a team
   arms only the run slot unless your layer also binds its plan to every
   `rc.Key.Members[i].KeySlot`; do that if an outage should hit the whole
   team.
10. **Key slots (lead decision, done).** (a) The lead (first member with
    role `lead`, else the first member) runs on the run's own key and slot:
    its `MemberKey.ID` and `KeySlot` equal the run key's, so N members use
    N slots, and the gateway rows of the run key are the lead's
    (`gateway_key`). (b) The planner refuses a team with more members than
    the gateway's key slots for the arm's provider: `team: 5 members need 5
    gateway key slots, the gateway has 4 for provider "anthropic" (raise
    gateway.key_slots, docs/multi-agent.md)`. (c) `MintKey` of a team run
    takes all N slots under one lock or none (`gateways/bifrost/
    teamslots.go`), and waits while fewer are free, holding nothing; a
    single-agent run also waits instead of failing while a team holds
    slots. Tests: `TestTeamSlotsAllOrNothing`, `TestTeamSlotsNoDeadlock`
    (two 3-member teams on 4 slots, 20 rounds). (d) See [Key slots](#key-slots).
    For a member-targeted gateway probe on the lead, the target slot is the
    run slot.
11. **Route agent on proxy routes.** Kept as is for your tool-fault matcher:
    `toolproxy.Route.Agent` (set per request from `/m/<token>/`; MCP rows
    read it as `x.route.Agent`, HTTP rows as `rt.Agent`) and
    `toolproxy.MemberHeader` (`X-Heimdall-Member`). I do not edit
    `probes/toolfault.go` or `toolproxy/fault.go`.

Also there for you: `AgentTree{Version, Shape, Agents, Edges, Coverage,
Notes}`, `AgentNode`, `AgentEdge{Kind spawn|handoff|message|claim|return,
From, To, At, PayloadSHA256, Size, Source, Strength, Ref}`, attribution
sources `gateway_key` > `header` > `transcript` > `otlp` > `self_report`
with strengths `proven` / `declared` / `reported`
(`schemas.AgentSourceStrength`, `AgentSourceRank`), `TeamMember`,
`MemberKey`, `TeamRecord` (`team.json`), `RunDetail.Agents`, file
`agents.json` (`schemas.AgentsFile`). A single-agent run has
`Evidence.Agents == nil`, no `agents.json`, no `agent_id` on any step.

## Run shapes

A run can hold more than one agent in two ways. Everything else (planning,
the frozen manifest, conditions, gates, the verdict, sealed evidence) is the
same as for a single agent.

| Shape | What runs | How the bench knows who did what |
|---|---|---|
| Single agent | One container, one gateway key | Nothing to attribute: no tree, no `agents.json`, no `agent_id` on any step; the run is byte-identical to one recorded before this page |
| In-process (`in_process`) | One container whose framework runs subagents (Claude Code Task/Agent tool, Codex sub-threads, OpenAI Agents SDK handoffs, any framework that reports agent spans or events) | The agent's own record: a declared header on its model calls, its transcript, its OpenTelemetry spans, its native events |
| Team (`team`) | One container per member, started by the bench together on the run's networks with the shared workspace volume, each on its own gateway key and tool proxy route, plus the team mailbox | The bench's own credentials: the key a model call came in on, the route a tool call or message came in on |

### Declaring a team

In the agent config (or in the task, used when the config has none):

```yaml
id: my-team
adapter: command           # every member runs through the arm's adapter
image: ...
budgets: {wall_s: 900, cpus: 1, mem_gb: 2, usd: 3}   # usd: the run-wide cap over every member
topology: team             # a label for reports and the fairness rule
limits: {max_agents: 4, max_depth: 1, max_concurrent: 4}
team:
  - {id: lead, role: lead, budget_usd: 1}
  - {id: coder, role: worker, agent: agents/codex.yaml, budget_usd: 1}
  - {id: tester, role: worker, model: claude-haiku-4-5, budget_usd: 1}
```

- `id`: `[a-z0-9][a-z0-9_-]*`, unique; it names the container
  (`bench-<run>-m-<id>`), the key (`<run id>--<id>`) and the member's
  output (`agent.<id>.stream.jsonl`).
- `agent`: another agent config (path relative to the bench root). The
  planner resolves it, builds its image and freezes it into the arm config,
  so it is hashed with the arm. It must use the arm's adapter (every member
  is planned with the arm's adapter; a different one is refused at plan
  time). Empty: the arm's own config. A task's own team cannot set `agent`
  (its members always run the arm's config), so planning refuses it.
  `model`, `argv`, `env` override the member's config.
- `role`: `lead`, `worker`, `router`, `judge`, `reviewer` or any label.
  `lead`, `router` and `judge` count as orchestration in
  `orchestration_share`.
- `budget_usd`: the member key's own gateway budget; `budgets.usd` of the
  config is the run-wide cap (a Bifrost team budget over every member key).
- Not supported yet, and refused at plan time for every arm, including the
  compatibility arm, whose effective team (its own or the task's) is
  non-empty: multi-session tasks, root tasks (`environment.user: root`),
  snapshot-graded suites, `remote_http` arms, `realism: neutral` and
  adapter fault probes. The environment also refuses the first three as a
  resume defense. Per-member step observers are not supported either (see
  [Limits](#limits)). A compose task's environment variables reach every
  member, as they reach a single agent.

### What every member gets

| Variable | Value |
|---|---|
| `HEIMDALL_AGENT_ID`, `HEIMDALL_AGENT_ROLE` | its id and role (`worker` when none) |
| `HEIMDALL_TEAM` | every member id, comma separated, in team order |
| `HEIMDALL_MAILBOX_URL` | its own route to the mailbox, `http://toolproxy:9200/m/<token>/s/mailbox/` |
| the adapter's usual variables | built on a copy of the run context that holds the member's config and key, so the member's credential is its own key |
| `BENCH_MCP_SERVERS` and rendered MCP configs | every proxied service on the member's route (`/m/<token>/s/<id>/`) |

The lead runs on the run's own key and slot; every other member gets a key
of its own on a slot of its own (see [Key slots](#key-slots)). Member key
values and route tokens are redacted from every stream and never written to
the run folder; `team.json` holds key ids and slots only. They do reach the
container's env file, a 0600 file in a private 0700 staging folder under the
temp folder, removed once the container is created; a file a crashed process
left there is removed by the next bench process that launches an agent.

### The mailbox

Contract items 2, 5 and 6 above define it. It is a small standard-library
Python server (`environments/mailbox.py`, run with the helper image's
`python3`) reached only through the tool proxy, so each send and receive is a
`tools.jsonl` row; `messages.jsonl` and `Evidence.Messages` join those rows
with the mailbox's own record. Members may also talk through workspace files
or any MCP service; only the mailbox gives per-message evidence.

## Attribution

Every timeline row (`steps.jsonl`) gets the agent it belongs to
(`agent_id`) and how that is known (`agent_source`), from the strongest
source that places it. A row nothing places keeps no agent id: unknown stays
unknown, and `attribution_coverage` says how much of the timeline that is.

| Source | Strength | Shape | What it reads | Placed rows |
|---|---|---|---|---|
| `gateway_key` | proven | team | The virtual key a model call came in on (Bifrost row `virtual_key_id`, mapped to the member that holds the key); the tool proxy route token a tool call or message came in on (`ToolCall.AgentID`) | model turns, attempts, requested tool calls, tool proxy rows, mailbox messages |
| `header` | declared | any | `x-bf-lh-heimdall-agent: <id>` on a model call (Bifrost logs it as metadata `heimdall-agent` with no gateway config), `x-heimdall-agent` or OTel `baggage: heimdall.agent=<id>` when the gateway's `logging_headers` lists them | gateway rows of the calls that carried it |
| `transcript` | reported | in-process | The agent's stream: Claude Code lines with `parent_tool_use_id` (the subagent started by that Task/Agent call) | the subagent's model turns, tool calls and results |
| `otlp` | reported | in-process | `invoke_agent` / `create_agent` spans (`gen_ai.agent.id`, else `gen_ai.agent.name`), nested agent spans as subagents; `handoff` spans and `transfer_to_<agent>` tool spans (`from_agent`, `to_agent`) | the agent spans and every reported row nested under them |
| `self_report` | reported | in-process | Native events: `subagent_start`, `subagent_end`, `handoff` with `attributes.agent_id` (and `agent_role`, `from_agent`, `to_agent`) | the reported rows and the rows nested under them |

Rules (`evidence/agentattribute.go`):

1. Gateway rows: the key, else the declared label, of the row with the same
   request id or of the logical request it belongs to. A requested call of a
   `transfer_to_<agent>` tool is also a handoff edge.
2. Tool proxy rows: the member route the call came in on.
3. Agent-stream rows, and requested calls the gateway could not place: the
   transcript's agent for the same tool call id.
4. Agent-reported rows: the reported event's agent, else the nearest
   reported parent row's.
5. In-process runs only: a row still unplaced goes to the deepest subagent
   whose reported lifetime holds its time when exactly one does, to the main
   agent when none does, and stays unknown when two siblings overlap. Team
   members run at once, so a team never uses time.
6. A witness row (`witness_of`) takes the agent of the row it witnesses and
   is left out of the coverage counts.

A gateway row that came in on one member's key but declares another agent
keeps the key's agent and gets a note in `agents.json`.

### Formats checked against real data

| Format | Checked how | What the bench uses |
|---|---|---|
| Claude Code stream-json `parent_tool_use_id` | Present (null) on every line of the 18 recorded runs (`testdata/recorded`) and the 2.1.325 fixture; non-null values only from Anthropic's docs | subagent lines (`adapters/claudecode.go`, agent id `task:<tool_use id>`) |
| Claude Code spawn tool | Real transcripts of Claude Code 2.1.312 on the development host: the tool is `Agent` (older versions: `Task`), its result carries `agentId` | spawn edges with `spawn_tool_call_id`, name from `subagent_type` or `description` |
| Claude Code subagent transcript files | Real files on the development host: `~/.claude/projects/<project>/<session>/subagents/agent-<agentId>.jsonl` (lines with `agentId`, `isSidechain: true`, `message.model`, `requestId`) and `agent-<agentId>.meta.json` (`agentType`, `description`, `toolUseId`, `spawnDepth`) | documented only: they live in the agent's home tmpfs and are gone when the container exits; the stream carries the same attribution |
| Codex sub-threads | Real rollouts of Codex CLI 0.144.6 on the development host: a subagent rollout's `session_meta` has `parent_thread_id`, `thread_source: "subagent"`, `agent_nickname`, `agent_path` and `source.subagent.thread_spawn{parent_thread_id, depth, agent_path, agent_nickname, agent_role}`; the parent calls `spawn_agent` (namespace `collaboration`, arguments `task_name`, `fork_turns`, `message`; output `{"task_name": "/root/<name>"}`) | rollouts: documented only (they live in the agent's home). `codex exec --json` (what the bench records): a `collab_tool_call` item with `tool: "spawn_agent"` and `receiver_thread_ids` becomes a spawn edge to agent `thread:<id>` (`adapters/codex.go`). That item shape comes from the field names in the Codex 0.169.1 binary (`collab_tool_call`, `sender_thread_id`, `receiver_thread_ids`, `collab_agent_spawn_begin`), not from a recorded stream; whether a sub-thread's own commands appear in the parent's stream was not checked, so its actions are attributed only through a header or OTLP |
| OpenTelemetry GenAI agent spans | Docs (semantic conventions, Development stability) | `gen_ai.agent.id`, `gen_ai.agent.name`, nesting |
| OpenAI Agents SDK handoffs | Docs (`HandoffSpanData.from_agent`, `to_agent`; handoff tools named `transfer_to_<agent>`) | handoff edges |
| Bifrost logging headers | Bifrost source (`plugins/logging/main.go`, `captureLoggingHeaders`) and a real Bifrost v2.5.1: a call with `x-bf-lh-heimdall-agent: worker-a` is logged with `metadata: {"heimdall-agent": "worker-a"}` and its `virtual_key_id`; `baggage` is not logged unless `logging_headers` lists it | `GatewayRequest.AgentLabel` |
| Bifrost teams and key budgets | A real Bifrost v2.5.1 (`TestIntegrationTeamKeys`, tag integration, with `HEIMDALL_TEST_BIFROST_URL`): `POST /api/governance/teams` answers `{"team": {"id"}}`; a virtual key takes `team_id` and `budgets`; `PUT` of a key with only `team_id` and `budgets` keeps its provider configs; `GET /api/logs?virtual_key_ids=a,b,c` returns every listed key's rows | member keys, the lead on the run key, the run-wide cap |

## The agent tree

`agents.json` (sealed with the run, also `Evidence.Agents` and
`RunDetail.agents`) is an `AgentTree`: `version`, `shape`, `agents`
(`AgentNode`: id, role, name, model, parent, depth, spawn tool call or event
id, started, ended, exit, source, strength, every source that saw it, and
for a team member its key id, container and sub-budget), `edges`
(`AgentEdge`: spawn, handoff, message, claim, return, with time, payload
sha256 and size, source and strength), `coverage` and `notes`. Spawn and
return edges come from transcripts and reported agent spans or
`subagent_start` / `subagent_end`; handoffs from `handoff` events and
`transfer_to_<agent>` calls; messages from the mailbox (a message whose
extra field `kind` is `claim` is a claim edge) and from reported `message`
events with `attributes.to`. It is written
only when a run has two or more agents. `team.json` holds a team's members,
key ids and slots, and how each container ended; `messages.jsonl` the
mailbox messages.

## Metrics

All are unknown, never zero, for a single-agent run, except
`budget_exceeded` (`metrics/multiagent.go`; directions in
`verdict/direction.go`).

| Metric | Value | Better |
|---|---|---|
| `agents_spawned` | agents beyond the first; derived per agent: `agent_cost_usd.<id>`, `agent_tokens.<id>`, `agent_tool_calls.<id>`, `agent_model_turns.<id>` | lower |
| `max_depth` | deepest agent | lower |
| `max_fan_out` | most agents under one parent; root agents count as children of the run | neutral |
| `orchestration_share` | cost of lead, router and judge agents over the run's cost | lower |
| `handoffs`, `messages`, `message_bytes` | handoff edges; mailbox messages (or message edges) and their bytes | lower |
| `duplicate_work_ratio` | paths written, and identical tool calls (name and arguments hash), by two or more agents, over all distinct ones | lower |
| `serial_collapse` | 1 when two or more spawned agents (team: members) never overlapped in time, 0 when some did | lower |
| `critical_path_s`, `parallelism` | first agent start to last agent end; summed agent lifetimes over that span | lower; higher |
| `delegation_loops` | spawn cycles plus repeated identical delegations (same parent, same payload hash) | lower |
| `attribution_coverage` | attributed rows over all rows; derived `attribution_coverage_proven`, `_declared`, `_reported` | higher |
| `budget_exceeded` | 1 when the run's priced cost is over its arm's `budgets.usd` | lower |

## Fairness: comparing topologies

Topology is an arm dimension: arms differ in `team`, in the agent's
subagent settings, or in the config they point at, and `topology` labels
them. A comparison of topologies is only fair at equal spend, with every
agent's calls counted (routers and judges included) and the cap enforced
where the agent cannot get around it:

```yaml
fairness: equal_budget
arms:
  single: {label: single, agent: agents/claude-code.yaml, set: {topology: single, budgets.usd: 3}}
  best_of_n: {label: best-of-3, agent: agents/claude-code-bo3.yaml, set: {topology: best_of_n, budgets.usd: 3}}
  team: {label: team, agent: agents/my-team.yaml, set: {budgets.usd: 3}}
```

- **Planner** (`engine/fairness.go`): every arm needs `budgets.usd` > 0 and
  all caps equal; a team's member sub-budgets may not sum above its cap; an
  unknown rule is refused. It warns when a single-agent arm or a best-of-N
  arm is missing.
- **Gateway**: the cap is a budget on the run's virtual key (single agent)
  or on the run's Bifrost team over every member key (team), so it holds
  across every agent of the arm.
- **Verdict** (`verdict/fairness.go`): `Verdict.fairness` lists each arm's
  cap and topology and the runs that went over; a run over its cap makes the
  experiment INVALID (`fairness_equal_budget` instrument check). When no run
  went over but some run's spend could not be read, the check is unknown.
- **The claim.** "Multi-agent is better" needs a single-agent baseline and a
  best-of-N single-agent baseline at the same spend. The report states
  whether both were present and `claim_allowed` is true only when they were
  and the check passed.

## Key slots

The fault proxy tells runs apart by key slot (one Bifrost provider key per
slot). A team of N members needs N slots: the lead on the run's slot, each
other member on its own, so a gateway-layer probe can fail one member's
model calls. A team run takes all N slots at once or none and waits while
fewer are free; the planner refuses a team larger than the gateway's slots.

To add slots to a stack, add names to `gateway.key_slots` in
`deploy/bench.yaml` (for example `fp-slot-5` to `fp-slot-8`; with several
providers, to that provider's `slots`) and restart the fault proxy and the
bench server. The fault proxy reads the same file (`proxy --config`) and
accepts a slot whose placeholder key equals its name; the bench's gateway
`Setup` creates the matching provider key in Bifrost for every listed slot
at start. Keep `concurrency` times the largest team at or under the number
of slots, or runs wait for slots.

## Controls and tests

- `agents/team-control.yaml` (`scripted.kind: team`): a lead and two
  workers talking through the mailbox; deterministic, no model call.
- `agents/subagents-control.yaml` (`scripted.kind: subagents`): one
  container reporting a planner, two parallel workers, a handoff and a
  reviewer as native events.
- `TestTeamEndToEnd` (`engine/teame2e_test.go`, tag integration): runs both
  through the whole pipeline with real Docker (fixture
  `suite/testdata/team`) and checks per-member keys and slots, at least four
  mailbox sends in `tools.jsonl` all stamped with their member, the sealed
  `team.json`, `agents.json`, `messages.jsonl` and
  `state/mailbox/messages.jsonl`, the metrics, and that nothing is left in
  Docker. It needs a tool proxy image with member routes:
  `CGO_ENABLED=0 GOWORK=off go build -o bin/bifrost-bench ./cmd/bifrost-bench && docker build -t bench/bifrost-bench:hd-ma1 .`
- Unit tests: each attribution source on hand-written fixtures
  (`evidence/agents_test.go`, `evidence/agentattribute_test.go`,
  `evidence/messages_test.go`, `agentevents/agents_test.go`,
  `adapters/claudecode_test.go`), gateway member keys and slots
  (`gateways/bifrost/team_test.go`, `teamslots_test.go`), the tool proxy
  member route (`toolproxy/members_test.go`), the fairness planner
  (`engine/fairness_test.go`).

## Limits

- **No per-member step observers.** The process observer joins one
  container's PID namespace and the workspace observer cannot tell which
  member wrote a file in the shared volume, so a team run records both
  observers as off with that reason: `commands_executed`,
  `files_changed_events` and `agent_steps` are unknown for teams. File
  writes are attributed through requested tool calls (gateway key) and
  self-report.
- **In-process attribution is the agent's own word.** Transcript, OTLP and
  self-report attributions are `reported`; a framework that reports nothing
  and sets no header is one agent to the bench.
- **Time windows can guess.** Rule 5 places an unlabelled row of an
  in-process run by time; when the main agent works while one subagent runs
  (a background subagent), the row goes to the subagent.
- **Codex sub-threads**: only the spawn is read from `codex exec --json`
  (from the binary's field names, no recorded stream); the sub-thread's own
  actions need a header or OTLP.
- **A team's agent streams are read as one.** The run's parser reads
  `agent.stream.jsonl`, every member's output one after another, so
  agent-stream events (and the claim, taken from the last text) are not
  split by member; each member's own output is in `agent.<id>.stream.jsonl`.
  Gateway rows and tool calls, which carry the evidence, are split by key and
  route.
- **Skills and dirs** of a team are copied once per member into volumes of
  that member's own (`bench-dir-<run>-m-<member>-<n>`), so members may share
  or differ in skill folders.
- **Bifrost budgets** depend on Bifrost pricing the model; an unpriced model
  never reaches its gateway budget, and the verdict's `budget_exceeded`,
  priced from the bench's own snapshot, is what decides INVALID.
