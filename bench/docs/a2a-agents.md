# A2A agents through Bifrost's agent gateway

Heimdall can benchmark an agent that speaks [A2A](https://a2a-protocol.org/latest/)
(Agent2Agent) and is deployed behind Bifrost's agent gateway. The bench sends
each task as an A2A message through the gateway, follows the task to its end,
grades the result like any other agent's, and uses Bifrost's own agent log as
the witness of what happened on the wire. A team that ships A2A agents behind
Bifrost can therefore gate a release on them with the same verdict, policies
and evidence as a local agent.

This page says what is witnessed, what is not, which Bifrost facts the design
rests on and where each fact came from.

## Bifrost facts this rests on

Checked on 18 Nov 2026 against the pinned image `maximhq/bifrost:v2.5.1`
(`sha256:5ec9873acaf1687556b6e4dd854b92b539d9debf87a5a683b66338648b84d3c8`,
`/api/version` answers `v2.5.1`) on a throwaway stack with no provider keys,
and in the fork's source at tag `transports/v2.5.1` (the agent gateway landed
in commit `c741ef9ed`, 6 Oct 2026, an ancestor of the tag) and its docs
`docs/features/agent-gateway/*.mdx` (identical at the tag).

| Fact | Verified how |
|---|---|
| The agent gateway is in the OSS image v2.5.1 and starts with it ("agent gateway initialized" in the log). Licence: Apache 2.0 (the fork's `LICENSE`); it uses `github.com/a2aproject/a2a-go/v2` v2.5.0 (Apache 2.0). | Run on the image; `core/go.mod` at the tag |
| Virtual-key grants per agent are OSS. Enterprise adds access profiles, projects, user attribution and scoped log access; the bench uses none of them. | `access-control.mdx`; run: a key without a grant gets JSON-RPC error `-31403` "Agent ... is not allowed for virtual key ..." |
| Protocol routes: `GET /agents/a2a/{name}/.well-known/agent-card.json`, `POST /agents/a2a/{name}/json-rpc`, `GET/POST/DELETE /agents/a2a/{name}/rest/*`, push callback `POST /agents/a2a/{name}/push/callback`; gRPC only with `a2a_grpc_base_domain` and a port. | `handlers/agentgateway.go` at the tag; JSON-RPC exercised on the image |
| The gateway speaks A2A v1 (`A2A-Version: 1.0`, methods `SendMessage`, `SendStreamingMessage`, `GetTask`, `SubscribeToTask`, `CancelTask`, `ListTasks`; states such as `TASK_STATE_COMPLETED`; SSE events wrapping `task`, `statusUpdate`, `artifactUpdate`). | Run on the image; a2a-go wire types |
| Protocol routes answer 503 until `client.a2a_external_client_url` is set. Private agent addresses need `client.a2a_private_allowlist` (host, IP or CIDR). | `configuration.mdx`; `requireAgentGatewayURL` at the tag |
| Registration: `POST /api/agents` with `name`, `agent_card_url`, `enabled`, `allow_by_default`, `virtual_key_ids`, `forward_accepted_credential` and optional `runtime_auth`; `GET` and `DELETE /api/agents/{name}`; the setup token authenticates them. Bifrost fetches the card at registration. | Run on the image |
| With `forward_accepted_credential` the agent receives the caller's `x-bf-vk`. With `runtime_auth` headers `{"x-bf-vk": <key>}` and forwarding off, the agent receives that configured key instead. Message metadata and the caller's `x-bf-agent-correlation-id` header reach the agent. | Run on the image: the reference agent logged the sha256 prefix of each key it received |
| `agent_logs` (via `GET /api/agents/history`, filters `agent_name`, `virtual_key_id`, `task_id`, `context_id`, time window; at most 1000 per page): one `request` row per operation with `operation`, `status` (`success` or `error`), `agent_name`, `virtual_key_id`, `request_id`, `trace_id`, `task_id`, `context_id`, `request_message_id`, `downstream_transport`, `upstream_transport`, `latency`, `upstream_latency`, `overhead_latency` and an overhead breakdown; `event` rows for each stream event with `event_type` and `task_state`. `GET /api/agents/history/{id}` adds `request_body` and `response_body` (the returned task, with its artifacts). | Run on the image; columns read from Postgres |
| A `SendMessage` request row does not record the returned task state in `task_state`; the state is in its `response_body`. A dropped stream's request row is still `success`. | Run on the image |
| There is no `caller_correlation_id` column in v2.5.1 (the fork's later source adds one), and the history API silently ignores that filter: a query with it returns every row. The bench never relies on it and filters every answer client-side. | Postgres columns; run on the image |
| LLM and MCP log rows carry `agent_correlation_id` (from `x-bf-agent-correlation-id`) and `session_id`; `GET /api/logs` and `GET /api/mcp-logs` accept an `agent_correlation_id` filter. | `framework/logstore/tables.go` and `handlers/logging.go` at the tag; run on the image |
| Transport recovery: a card's first interface answering 404 is skipped within the same unary operation and the card is refreshed in the background (observed in 3 of 4 tries, see the results). A stream is never replayed or failed over: a stream whose first interface answers 404 fails with JSON-RPC `-32603`. A client that disconnects does not cancel the task, which `GetTask` and `SubscribeToTask` recover. | `transports-and-recovery.mdx`; run on the image with the reference agents' `failover` and `dropper` personas and the stream probes |

Not verified: gRPC, the REST binding, push notifications, OAuth card fetching,
TLS to agents, A2A extensions, and any enterprise feature. Nothing was run on
a newer Bifrost.

## How a run works

```yaml
id: support-agent
adapter: a2a
image: bench/coding-core-base:1   # the launcher image: needs python3
model: claude-sonnet-4-5          # the run key's model, for the agent's model calls
a2a:
  card_url: http://support-agent.internal:9000/.well-known/agent-card.json
  streaming: true                 # SendStreamingMessage; default SendMessage then GetTask
  timeout_s: 120
  credential: forward             # default; none sends the agent no Bifrost credential
  tools: expose                   # optional: publish the run's tool proxy (state tasks)
  artifacts: inline               # optional: write file parts into the workspace (artifact tasks)
budgets: {wall_s: 300, cpus: 1, mem_gb: 1}
gateway: {provider: anthropic, max_retries: 0}
```

1. **Prepare.** After the run's virtual key exists, the engine registers
   the card under a run-scoped name (`hb-<run id>-lead`), enabled, granted to
   the run's key only, with credential forwarding on unless `credential:
   none`. Bifrost fetches the card at that moment. `registration: <name>`
   uses an existing registration instead; it must allow any virtual key,
   because the run key is new for every run and the bench never edits a
   registration it did not make (the run fails with that reason otherwise).
   Other callers of a shared registration are not the run's: the witness
   keeps only rows on the run's keys and counts the rest in `a2a.foreign`.
2. **Run.** The container is the bench's launcher (`adapters/a2a.py`) on the
   evaluation network, where it reaches only Bifrost; it needs no egress,
   because Bifrost, not the container, connects to the agent. It sends one
   A2A message: the instruction as a text part, the workspace's small text
   files (100 files, 256 KiB) as one data part, and metadata
   `heimdall.output` (and `heimdall.team`, `heimdall.tools` when set), with
   the run key as `x-bf-vk`. It follows the task: `GetTask` every second for
   a non-terminal `SendMessage` result; for a stream, every event, and when
   the stream ends before a terminal state, `GetTask` then `SubscribeToTask`
   (Bifrost never replays a stream). A task that ends without completing
   (failed, rejected, canceled, or stopped in `input-required` or
   `auth-required`, since the bench has no user to answer) is the agent's
   result: the launcher writes an empty answer and exits 0, so the grade
   fails on the answer. Only a failure to reach the gateway, a protocol
   error (a JSON-RPC error, a non-200 answer) or the timeout exits 1. The
   last stdout line is the agent's own final text (the status message),
   which the bench reads as the final message.
3. **Output.** An answer task gets the text parts of the task's artifacts
   (appended stream chunks joined), else the status message, else a direct
   message. With `artifacts: inline`, file parts are written into the
   workspace at their `filename` (relative, no `..`, no hidden segments, 50
   files, 16 MiB). A state task needs `tools: expose`: the run's tool proxy is
   published through the same per-run gate as for `remote_http`
   (`docs/remote-execution.md`), and its URLs and bearer token travel in
   `heimdall.tools`. The gate must be reachable from the agent's host:
   `remote_tools.listen` and `remote_tools.public_url` name an address the
   agent can route to (loopback, the default, only serves an agent on the
   bench host). A diff task is refused at plan time: the agent is not
   beside the workspace.
4. **Collect.** The engine reads the agent log of the run's registrations
   since the run started (Bifrost writes it asynchronously; the engine waits
   up to 30 s for every launcher operation to appear) and the LLM and MCP rows
   whose `agent_correlation_id` is one of the run's task ids. That is the
   witness, `evidence.json` `a2a`.
5. **Cleanup.** The registrations the bench made are deleted, before the run
   key is revoked.

The run key carries the run's model and budget as for any agent. With
`credential: forward` the agent can reuse the forwarded key for its model
calls through Bifrost, which then count in `tokens`, `cost_usd` and steps like
any agent's. With `credential: none` the gateway log is marked incomplete
with that reason and those metrics are unknown, never zero.

## What is witnessed, and by whom

| Claim | Witness | Strength |
|---|---|---|
| The task reached the agent through the gateway, on which transport, how long it took, and whether the gateway saw an error | Bifrost's agent log request rows of the run's registrations | gateway (trusted) |
| Every operation the bench sent is in the agent log | the launcher's `a2a_report` joined with the request rows; `a2a.complete`, `a2a.unmatched` | gateway |
| The task's final state, and that the graded answer is the one the gateway returned | the recorded `response_body` of the bench's request rows (`a2a.agreement`: states and answer hash); stream results are recorded as events only, so a streamed answer has a state but no answer hash | gateway |
| Who called whom inside a team | the key each operation came in on (below) | gateway (proven) |
| Which model and tool calls belonged to which A2A task | LLM and MCP rows whose `agent_correlation_id` is a task id of the run; `on_run_key` says whether the row is on one of the run's keys, and so in the run's cost | gateway, but only for calls the agent chose to tag |
| The grade | the suite's grader on the exported answer, state or files | outcome |

Not witnessed: anything the agent does without crossing the gateway: model
calls sent straight to a provider, tools it runs itself, files on its own
machine, its reasoning. A model call through Bifrost that carries no
correlation header is still on the run key (and counted), but not linked to
a task. The agent's own card, version and build are whatever its server says.

## Teams: agents that call agents through the gateway

```yaml
id: support-team
adapter: a2a
a2a:
  card_url: http://agents.internal:9000/lead/.well-known/agent-card.json
  team:
    - {id: researcher, role: worker, card_url: "http://agents.internal:9000/researcher/.well-known/agent-card.json"}
    - {id: writer, role: worker, card_url: "http://agents.internal:9000/writer/.well-known/agent-card.json"}
```

The entry agent (member `lead`) is called by the bench and receives the run
key through forwarding. Every other member is registered per run beside it,
gets its own member key on its own key slot (the multi-agent area's member
keys, `docs/multi-agent.md`), and Bifrost sends it that key as its runtime
credential, so it is the only agent that holds it. Each member's registration
allows the run key and every other member key. The members' gateway endpoints
reach the entry agent in `heimdall.team` in the message metadata.

Attribution is from the gateway, by key, in the same way as the `gateway_key`
source of team runs:

- an operation on the entry registration with the run key is the bench's
  launcher;
- an operation on a member's registration with the run key is the entry
  agent's (the launcher only ever calls the entry agent);
- an operation with a member key is that member's.

An operation on a member's registration by another agent is an
agent-to-agent hop (`a2a.hops`, source `gateway_key`), and becomes a team
message in `evidence.json` `messages` with the request text the gateway
recorded (`fields.source: agent_gateway`), so the multi-agent metrics
(`messages`, `message_bytes`) count it like a mailbox message. A member's model
calls through Bifrost are on its own key, so `FetchRequests` attributes them
to it. A team is declared under `a2a.team`, not `team:` (which starts one
container per member), and the planner refuses a `team:` on an a2a agent.

## Metrics

All are lower-is-better, `evidence: gateway`, `source: bifrost_log`, not
applicable to an agent that is not an a2a agent, and unknown with the reason
when the witness is missing or incomplete (`verdict/direction.go` rows,
`metrics/builtin_test.go` rows):

| Metric | Kind | Value |
|---|---|---|
| `a2a_tasks` | count | distinct A2A tasks of the run's operations, across the team |
| `a2a_latency_ms` | count | ms from the start of the first operation the bench sent to the end of the last (timestamp plus latency in the agent log) |
| `a2a_errors` | count | operations the agent log recorded as errors, plus tasks whose recorded final state is failed or rejected (a successful operation can return a failed task) |
| `a2a_hops` | count | agent-to-agent operations through the gateway, each proven by key |

## Transport faults: the a2a probe layer

A probe with `layer: a2a` perturbs the client side of the stream, so it works
for any A2A agent behind the gateway; the planner refuses it on an arm that is
not an a2a agent or does not stream.

```yaml
layer: a2a
apply:
  a2a: {stream: drop, after_events: 1}   # or {stream: delay, delay_ms: 3000}
```

`drop` closes the stream after the given number of events, as a client that
lost its connection; `delay` stops reading for a while after the first event,
as a slow client. Receipts: `fired` from the launcher's report, `delivered`
when the agent log holds the stream operation of the task, `absorbed_by`
gateway when the agent log shows the recovery (a `GetTask` or
`SubscribeToTask` of the same task from the bench) and a completed final
state, `effect` from the grade. `evidence.json` `a2a.fault` says the same.

Interface failover is the gateway's own and cannot be injected for an
arbitrary agent (Bifrost reads the interfaces from the agent's card); the
reference agents' `failover` persona advertises a first interface that
answers 404 to show it.

## Reference agents and the example

`bench/a2a/refagent.py` is a standard-library A2A v1 JSON-RPC server that
serves several scripted personas under one process (`/<persona>/...`):
`scripted` (answers from a script, optionally after a delay, optionally
dropping its own stream or advertising a dead first interface), `noop`,
`broken` (a failed task, a JSON-RPC error, HTTP 500 or a non-JSON body) and
`delegate` (calls other agents through the gateway). With `model_call` a
persona makes one chat call through Bifrost with the credential it received
and the two correlation headers. It never logs a credential, only its sha256
prefix.

`examples/acme-a2a` is a complete project: three answer tasks on a fictional
handbook (suite `acme-a2a`, admitted and locked with the scripted oracle and
noop controls), one artifact task (`acme-a2a-files`) and one state task
(`acme-a2a-stock`), both from the bench's templates, A2A agent configs for
each persona and for a team, the two stream probes, and six experiments.
Its README says how to start the reference agents beside Bifrost and which
Bifrost settings the gateway needs.

## Results of the $0 proofs

All on 18 Nov 2026, on a throwaway stack (stock `maximhq/bifrost:v2.5.1`,
Postgres 18, the bench's fault proxy with no provider key, so every model
call was answered by its scripted model; the reference agents beside
Bifrost), through the real pipeline: per-run registration, the launcher on
the evaluation network, the agent log witness, grading, sealing. No provider
was called and nothing was spent; the cost figures in the reports are the
pinned price snapshot applied to the scripted model's token counts.

| What | Result |
|---|---|
| Admission of `examples/acme-a2a` (scripted oracle and noop controls) | 3 of 3 tasks admitted, 44 of 44 checks as expected (reference passes three times, empty, mutants, wrong answers, exploit, noop and refuse fail); suite locked |
| `a2a-oracle-vs-noop` (`e_a2a_oracle_vs_noop_qk2t`), 6 runs | oracle persona 3 of 3, noop persona 0 of 3; every run's witness complete (`witnessed`), the gateway's recorded final state and answer hash equal to the launcher's in 6 of 6; each oracle run has 1 LLM row linked to its task on the run key (123 to 139 scripted tokens, in `tokens`); `a2a_tasks` 1, `a2a_errors` 0. Verdict inconclusive (3 tasks, p 0.25), as 3 tasks must be |
| `a2a-team` (`e_a2a_team_hdp8`), 4 runs | single agent 2 of 2, team 2 of 2. Each team run: 3 registrations, 3 operations in the agent log (bench to lead, lead to researcher, lead to writer), `a2a_hops` 2 (source `gateway_key`), `a2a_tasks` 3, 3 LLM rows linked, one per member on its own key, the gateway rows attributed to `lead`, `researcher` and `writer` by key, 2 team messages with source `agent_gateway` (`messages` 2, `message_bytes` 616), `agents_spawned` 2 |
| Admission of `acme-a2a-files` (artifact) | 1 of 1 task admitted, 12 of 12 checks as expected; suite locked |
| `a2a-outputs` (`e_a2a_outputs_ehi0`), 4 runs | the artifact output kind: noop 0 of 2; persona reporter 2 of 2, its two file parts (`report.csv`, `chart.svg`) written into the workspace by the launcher (`a2a.artifacts: inline`), exported and passed by the template's hidden verifier (reward 1); witnesses complete |
| Admission of `acme-a2a-stock` (state) | 1 of 1 task admitted, 12 of 12 checks as expected; suite locked |
| `a2a-state` (`e_a2a_state_4u9o`), 4 runs | the state output kind: noop 0 of 2; persona clerk 2 of 2. With `remote_tools.listen` on the upstream network's gateway address (`10.231.48.1:0`) the bench published the run's tool proxy through the per-run gate; the agent, beside Bifrost, read the gate's URL and bearer token from `heimdall.tools` in the message metadata and made one `POST /items/W-100/adjust`, which is in `tools.jsonl` (`tool_calls_proxied` 1) and in the inventory's exported state the hidden verifier passed; witnesses complete |
| `a2a-transport` (`e_a2a_transport_yjil`), 6 runs | streaming oracle 3 of 3: clean; `stream-drop` (receipt fired, delivered, absorbed_by gateway: the launcher dropped the stream after the first event, then `GetTask` through the gateway returned the completed task); `stream-delay` (fired, delivered, absorbed). The arm whose card's first interface answers 404 failed 3 of 3: Bifrost answered every stream with JSON-RPC `-32603` "unexpected upstream HTTP status: 404" and did not try the second interface, as its guide says for streams; the probes could not fire. Verdict inconclusive (1 task) |
| `a2a-failover` (`e_a2a_failover_4kuw`), 6 runs | oracle 3 of 3; the agent whose first interface answers 404, over `SendMessage`, 2 of 3. In two runs the reference agent logged the 404 hit and then the request on the second interface within the same operation (Bifrost refreshed the card right after), as the guide says. In the third, the first task of the experiment, Bifrost returned the 404 to the caller as JSON-RPC `-32603` "failed to send HTTP request ... unexpected upstream HTTP status: 404 Not Found" without trying the second interface. The step 0 probe failed over correctly, so unary failover worked in 3 of 4 tries on v2.5.1; why the fourth did not is not established |

After the last code change, `a2a-oracle-vs-noop` (`e_a2a_oracle_vs_noop_0kjt`)
and `a2a-team` (`e_a2a_team_g8pe`) were run again with the same results
(3 of 3 against 0 of 3; 2 of 2 and 2 of 2 with 2 hops per team run).

Facts these runs added (v2.5.1): a stream the client drops is recorded by
Bifrost as an operation with status `error`, so `a2a_errors` counts it under
`stream-drop`; a stream's request row has no response body, so a streamed
answer is checked for its final state (from the event rows and the recovery
`GetTask`) but has no answer hash; a 3 s client stall over a few small events
is absorbed by socket buffers (the gateway's latency for that operation stayed
at about 20 ms), so `stream-delay` as shipped does not stress the gateway's
client delivery. Inferred, not observed: since every run makes a new
registration, the interface preference Bifrost keeps after a failure (its
guide) cannot carry over from one run to the next.

`agent check` (one run each, scripted model): `a2a-oracle` passed every row
it could, with the new row "a2a agent: agent gateway witness" PASS (1
operation, 1 linked LLM row, strength witnessed); `a2a-broken` showed the
failed task as the agent's result (exit 0, empty answer, outcome fail,
`a2a_errors` 1) and the witness row WARN (no linked model call), and failed
the "model call reached Bifrost" row, as a broken agent that makes no model
call should.

The first run of `a2a-oracle-vs-noop` failed its seal: the launcher's source,
which lands in `plan.json`, held two lines the seal's secret scan reads as
credentials (`token = ...`); they were renamed and a test now checks the
launcher source against that scan. A second run found the oracle persona's
script did not match one instruction (a line break inside the matched words);
the persona's script was fixed, not the locked task.

## Limits

- One message per run (and per session of a multi-session task): the bench
  plays no user, so an agent that asks for input gets no answer and its run
  is graded on an empty answer.
- The witness is what Bifrost recorded. If the agent log drops rows, the
  witness is incomplete and the metrics say unknown; the bench never fills
  the gap from the launcher's own account.
- Registrations the bench made are deleted at cleanup; a crashed bench can
  leave `hb-*` registrations behind, granted only to revoked keys. Delete
  them with `DELETE /api/agents/{name}`.
- Streamed answers are checked for state but not for the answer hash: the
  request row of a stream has no response body.
- gRPC and REST bindings, push notifications and enterprise access profiles
  are not exercised.
- Only the Docker driver runs a2a agents; the Kubernetes driver refuses them
  at plan time.
- A diff task is refused: an A2A agent never shares the run's workspace, and
  the bench has no workspace service an agent could edit remotely.
- No UI panel shows the witness yet. `GET /api/bench/runs/{id}/a2a` serves it
  (`RunA2A`: `available` and the `a2a` block of `evidence.json`; available
  false for other agents).
