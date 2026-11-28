# Bifrost features under test

Status: built 18 Nov 2026 (B2). Every feature below was checked on the pinned
image `maximhq/bifrost:v2.5.1` (a throwaway stack, compose project `hd-b2`,
no provider keys) and in this fork's source (`transports/version` 2.5.1).
The proofs ran at $0 through the real pipeline with scripted agents and a
scripted model upstream; no real model has run any experiment on this page.

Heimdall normally treats Bifrost as a measuring instrument and refuses any
gateway feature that could change a run's traffic unseen. This page is about
the opposite question: Bifrost claims, for example, that MCP Code Mode
"reduces input token usage by up to 92.8% when using multiple MCP servers"
(`docs/mcp/code-mode.mdx`). A team should be able to test such a claim on its
own agent with the same evidence and statistics as any other change: success
from the hidden verifier, cost from the bench's ledger, behaviour from the
step timeline, a paired verdict and the gates.

An arm can only vary a setting that Bifrost applies per virtual key or per
request. A setting that is gateway-wide needs its own gateway per arm, which
one experiment does not do; those are listed as not testable within one
experiment, with how to compare them anyway.

## What v2.5.1 has, and what an arm can vary

Checked on the `hd-b2` stack on 18 Nov 2026 unless the row says source only.
All of it is in the Apache-2.0 tree of this fork (`core/`, `plugins/`,
`transports/`); the complexity router is tagged Beta in its docs.

| Feature | On v2.5.1 | Scope | Arm setting | How it was checked |
|---|---|---|---|---|
| Gateway overhead in the log | yes: every detail row has `latency`, `upstream_latency`, `overhead_latency` (ms) and `overhead_breakdown` (per stage, in µs) | per row | none needed: always-on metrics | toy run on the stack, `GET /api/logs/{id}` |
| MCP gateway | yes: `POST /api/mcp/client`, `/mcp` and `/mcp/<slug>` per client, virtual key `mcp_configs` grants | per client, per key | `gateway.bifrost.mcp` | clients created, tools listed and called through `/mcp/<slug>` with a granted key; a key without the grant gets 403 |
| MCP Code Mode | yes: `is_code_mode_client` per client; the agent sees `listToolFiles`, `readToolFile`, `getToolDocs`, `executeToolCode`; programs run in Bifrost and call the server | per client | `gateway.bifrost.mcp.code_mode` | a program called two desk tools and returned a value |
| Code Mode binding level, limits | `client_config.mcp_code_mode_binding_level` (`server` here), `mcp_code_mode_limits` | gateway-wide | not testable within one experiment | `GET /api/config` |
| `mcp_tool_logs` | yes: `GET /api/mcp-logs?virtual_key_ids=<id>`, one row per tool execution with tool, server label, key, arguments, status, latency | per key | read as a second witness | rows matched the tool proxy ledger call for call |
| MCP agent mode | yes, and it matters: see "Agent mode takes Code Mode's tools" below | gateway-wide (per client `tools_to_auto_execute`; Code Mode's read-only tools always) | not an arm; the bench avoids it | `e_b2_mcp_proof_5nm0` |
| Auto prompt caching | yes: provider `prompt_cache` block (`{"auto_inject": false}`), and the request header `x-bf-prompt-cache-auto-inject: true` turns injection on for that request | header per request, but honoured only on a provider whose config has the block (gateway-wide, once) | `gateway.bifrost.prompt_cache` | with the header the upstream received a `cache_control` marker and reported a cache write, then a read; without it, no marker |
| Semantic cache | yes: plugin `semantic_cache` with a vector store (`vector_store` in config.json; Redis 8 here) | plugin and store gateway-wide; engaged only by a request that sends `x-bf-cache-key` (with no `default_cache_key`) | `gateway.bifrost.semantic_cache` | direct mode: miss stored an entry (`cache_debug.cache_id`), the same request with the same key was a hit with no upstream attempt, another key missed, no key gave no `cache_debug` |
| Semantic cache, embedding mode | yes: plugin `provider`, `embedding_model`, `dimension`, `threshold` | as above | `semantic_cache.mode: semantic` | with a scripted embedding upstream (bag-of-words, not a real model): a similar request was served another request's reply (similarity 0.957) |
| Semantic cache limits for agents | a streamed tool-call answer is never served from the cache; a conversation over 3 messages is not cached (`conversation_history_threshold`, gateway-wide); entries are shared across virtual keys (no identity scoping on v2.5.1) | gateway-wide | none | probes, "Semantic cache: what v2.5.1 caches for an agent" |
| Complexity router | analyzer config and status at `/api/routing/complexity-analyzer-config` and `-status` (150 reference phrases, tier boundaries, an `llm` fallback; `state: disabled` until an embedding provider is set); `complexity_tier` is a declared CEL variable; each routed row logs its `complexity_tier` | classifier gateway-wide; a rule reading the tier can be scoped to one key | `gateway.routing` strategy `rule` with `when` reading `complexity_tier` (W9) | with the scripted embedding upstream: analyzer `ready` (150 phrases), a key-scoped rule matched only that key's requests, rows carried the tier; `e_b2_complexity_router_proof_s7wx` |
| Model routers | 404 on v2.5.1 (W9) | | none | `GET /api/routing/model-routers` |

## Using it

All settings are part of the agent config, so an arm sets them with `set:`
and the arm's config hash covers them. The manifest records
`bifrost_features: {arm: sha256 of the arm's canonical gateway.bifrost}`; a
plan without them has no such key and keeps its hash.

```yaml
arms:
  A: {label: MCP direct}
  B: {label: MCP through Bifrost (classic), set: {gateway.bifrost: {mcp: {code_mode: false}}}}
  C: {label: MCP through Bifrost (Code Mode), set: {gateway.bifrost: {mcp: {code_mode: true}}}}
  D: {label: auto prompt caching, set: {gateway.bifrost: {prompt_cache: auto_inject}}}
  E: {label: semantic cache, set: {gateway.bifrost: {semantic_cache: {mode: direct, scope: task, ttl: 1h}}}}
```

### MCP through Bifrost

For a run whose arm sets `gateway.bifrost.mcp`, after the services and the
run's tool proxy are up and its key is minted (`engine/bifrostfeatures.go`,
`environments/bifrostmcp.go`, `gateways/bifrost/bifrostmcp.go`):

1. The tool proxy joins the evaluation network under the alias
   `tp-<run id>`, so Bifrost can reach it. It stays in front of every MCP
   service and keeps the trusted ledger.
2. One Bifrost MCP client per visible proxied MCP service:
   `POST /api/mcp/client` with name `<service>_<run id>` (Bifrost refuses
   hyphens; in Code Mode the name is a Python identifier),
   `connection_string` the proxy's route `http://tp-<run id>:9200/s/<service>/mcp`,
   `tools_to_execute: ["*"]`, `is_code_mode_client` from the arm,
   `allow_on_all_virtual_keys: false`. The bench checks that Bifrost
   discovered tools and applied the Code Mode flag, else the run fails before
   the agent starts.
3. One MCP key for the run, `POST /api/governance/virtual-keys` named
   `hm-mcp-<run id>`, with `mcp_configs` granting exactly the run's clients
   and no provider, so it cannot make model calls. It is not the run's model
   key on purpose (next section).
4. The agent is told `http://bifrost:8080/mcp/<slug>` for each service, with
   `Authorization: Bearer <MCP key>`: in `BENCH_MCP_SERVERS`
   (`{"desk": {"url": ..., "headers": {...}}}`), `{{mcp.<id>.url}}`, and the
   agent's own MCP config (Codex `http_headers`, Claude Code, OpenCode and
   generic JSON `headers`). `plan.json` keeps the headers with the token
   blanked.
5. After the agent exits, the run's rows of `GET /api/mcp-logs` for the MCP
   key are read into `evidence.json` (`bifrost_mcp`: clients, key id, calls),
   waiting up to 8 s for Bifrost's batched log writer to reach the ledger's
   count.
6. Cleanup deletes the MCP key and the clients before the tool proxy goes
   (Bifrost deletes a key's grants with the client); the janitor sweeps any a
   killed bench process left.

**Per evaluation (`mcp_route`).** An evaluation (`POST /api/bench/evaluations`,
wizard step 4 "MCP tools") can set `mcp_route` for all its arms:
`direct` (the default, stored as unset) leaves the tool proxy as the only hop;
`bifrost` sets `gateway.bifrost.mcp.code_mode: false` on every arm, and
`bifrost_code_mode` sets it to `true` (`handlers/evaluations.go`,
`mcpRouteSet`). The planner then does everything above per run. A benchmark
the planner refuses for it (no visible MCP service, or an agent that cannot
reach the gateway) is skipped with the refusal as its reason; the other
benchmarks run. To compare direct against Bifrost, use an experiment with the
arms shown at the top of this file instead.

**Trade-off: tool faults and the tool proxy.** Routing through Bifrost does not
replace the tool proxy: each Bifrost MCP client points at the run's proxy route
(`http://tp-<run id>:9200/s/<service>/mcp`), so the chain is agent, Bifrost,
tool proxy, MCP server. Tool probes (`broken-tool` and every `apply.tool`
fault in `docs/tool-proxy.md`) still fire at the proxy, and the proxy ledger
stays the first witness. What changes: (1) the agent sees a fault after
Bifrost relays it, so Bifrost may reshape an error (a JSON-RPC error becomes
Bifrost's tool error body) and adds its own latency, which shifts `timeout`
and latency faults by a few milliseconds; (2) a fault that cuts the stream
(`drop`, `disconnect`) is seen by Bifrost, which answers the agent with its
own error, not a broken connection; (3) Bifrost's MCP log records a faulted
call as an error row, so `bifrost_mcp_witness` still matches call for call;
(4) in Code Mode one program may make several proxied calls, so a fault
counted by call number (`from_call`) counts the program's inner calls, not
the agent's turns. Faults that must reach the agent byte for byte are best
run with `mcp_route: direct`. Points (2) and (3) are expected from how
Bifrost relays MCP calls and were not yet checked on a live stack for this
option.

Plan-time refusals (with the arm named): a scripted control that runs without
the gateway network (only `toy` and `mcploop` can reach it), a remote agent,
a team, a suite with no visible MCP service, an empty `gateway.bifrost`.

**The two witnesses.** The tool proxy ledger stays the witness of what ran.
Bifrost's MCP log is matched to it call for call by service, tool and
canonical arguments; `bifrost_mcp_witness_mismatch` counts the calls only one
of them saw (a call that bypassed the gateway, or one the gateway logged that
never reached the tool). The verdict instrument `bifrost_mcp_witness` holds
the arm to it: any mismatch in a counted run makes the experiment INVALID (the
arm did not test what it declares), a run whose MCP log could not be read
completely leaves it unknown, and the instrument appears only in experiments
that served MCP through Bifrost. The report's meters add the mean mismatch per
run. Every proof run below had 0.

**What Bifrost's MCP log shows and does not.** A Code Mode program logs one
row per tool it calls; `executeToolCode`, `listToolFiles` and `readToolFile`
themselves are not logged. A tool result with `isError: true` is logged with
status `success`.

### Agent mode takes Code Mode's tools

On v2.5.1, when a Code Mode client exists on the gateway, a **non-streaming**
model request whose answer calls `listToolFiles` or `readToolFile` is
answered by Bifrost's agent mode: Bifrost executes those tools itself and
calls the model again inside the same request, and the agent sees only the
end of that loop. Bifrost's docs say these two tools are "always
auto-executable". It happened with `mcp_disable_auto_tool_inject: true`, with
a key that has no MCP grant at all, and with the request header
`x-bf-mcp-include-clients:` (empty, documented as deny-all); it did not
happen on a streamed request. In the proof without streaming
(`e_b2_mcp_proof_5nm0`) all three Code Mode runs failed: the agent's
conversation lost its own tool calls and the program never ran (0 rows in
either witness). The fault proxy ledger also saw more upstream calls than
Bifrost logged rows, so the gateway log join reported the log incomplete and
the token metrics unknown, which is the honest reading.

What the bench does about it: the MCP key is separate from the model key, the
`mcploop` agent streams (as Codex and Claude Code do), and the guide says so.
An agent that calls the model without streaming cannot be measured in Code
Mode through Bifrost on v2.5.1. Also note for any experiment on a gateway with
Code Mode clients: an agent whose own tools are named like Code Mode's would be
intercepted the same way.

### Auto prompt caching

`gateway.bifrost.prompt_cache: auto_inject` (or `off`) asks the agent to send
`x-bf-prompt-cache-auto-inject: true` (or `false`) on every model request:
`BENCH_GATEWAY_HEADERS` (a JSON object), `{{gateway.headers_json}}`, and for
Claude Code appended to `ANTHROPIC_CUSTOM_HEADERS`. The planner refuses the
arm unless the provider's Bifrost config has a `prompt_cache` block (Bifrost
ignores the header otherwise: it "cannot manufacture opt-in"), and refuses it
for scripted controls that send no headers. Configure the block once,
gateway-wide, as `{"auto_inject": false}`, so arms without the header are
unchanged. On the website it is the "Prompt caching" choice under "Change"
for Setup B on the New comparison form. Two limits: a client that sends its own `cache_control` markers is
forwarded unchanged ("caller markers always win"; Claude Code does), so the
arm tests nothing for it; and an agent that does not forward the headers does
not get the feature, which `cache_read_share` then shows as 0.

### Semantic cache

`gateway.bifrost.semantic_cache: {mode, scope, ttl, threshold}` asks the agent
to send `x-bf-cache-key`, `x-bf-cache-type` (`direct` or `semantic`), and the
TTL and threshold when set. The key is `hm-<experiment>-<arm>-<task>` for
`scope: task`, `hm-<experiment>-<arm>` for `arm`, `hm-<experiment>` for
`experiment`, so no reply crosses experiments, and with `task` none crosses
tasks; on v2.5.1 the key is the only boundary (entries are shared across
virtual keys). What v2.5.1 caches for an agent loop is narrow: no tool-call
answers and nothing past the third message (see the proofs). The plugin and
its vector store are gateway-wide. Hygiene still
refuses a loaded or enabled `semantic_cache` plugin for every experiment,
except one with a `semantic_cache` arm, and then only when the plugin has no
`default_cache_key` (requests without a key bypass the cache, so the other
arms are untouched); the accepted findings are kept in the manifest
(`hygiene.bench_semantic_cache`).

The risk is the point of measuring it: a cached answer is not a model call
(no fresh output, no cost), and a later trial can be answered with an earlier
trial's reply, so trials stop being independent samples. Metrics
`semantic_cache_hits` and `semantic_cache_foreign_hits` (hits on an entry no
miss row of the same run stored), and `bifrost-bench bifrost cache-replays
-exp <id> [-json]`, which traces every hit to the run that stored its entry
(Bifrost logs the entry's `cache_id` on the storing miss row and on every hit)
and counts hits in the same run, in another trial or arm of the same task,
and in another task (a cross-task replay, printed with a warning).

#### Bifrost cache on vs off (evaluation option)

An evaluation in score mode can make the cache its test variable:
`"bifrost_cache": true` on `POST /api/bench/evaluations` (the "Bifrost cache
on vs off" box in the wizard's setup step). Each benchmark's experiment then
has two arms: the spec's own arm with the cache off, and arm `cache`
(label `<arm label> (Bifrost cache on)`), identical except for
`gateway.bifrost.semantic_cache: {mode: direct, scope: task, ttl: 1h}`
(handlers/evaluations.go, `cacheOnArm`). Both arms run the same tasks,
trials and faults, and the estimate counts both. The planner still refuses
the cache arm unless the gateway is Bifrost with the `semantic_cache` plugin
loaded and no `default_cache_key` (the hosted built-in gateway does not load
the plugin, so there the box only works with your own Bifrost connected), so the child is skipped with that reason
on a gateway without the cache. The option cannot be combined with memory
carried from task to task, nor with an A/B mode.

`bifrost-bench bifrost cache-ab -exp <child experiment id> [-json]`
(bifrostfeatures.CacheAB) compares the two arms on the clean tasks both
ran: model requests, cache hits and hit rate, mean latency per model
request, cost (the run's `cost_usd`, else the rows' Bifrost price), cost
saved (cache-off cost per run minus cache-on cost per run, times the
cache-on runs), and the change in verified success, with a one-line
summary that flags a success drop as possible stale replies. The paired
verdict on `verified_success` between the arms is the significance test for
the quality change.

### Complexity router

The classifier is gateway-wide; a routing rule that reads `complexity_tier`
can be scoped to the run's key, so the arm is a W9 routing policy of strategy
`rule` (`experiments/bifrost-complexity-router.yaml`):

```yaml
set:
  model: claude-haiku-4-5
  gateway.routing: {strategy: rule, when: 'complexity_tier == "COMPLEX"', targets: [{model: claude-sonnet-4-5}]}
```

For such a rule the W9 requirement that the first target is the agent's own
model is lifted (matched requests go to another model by design; unmatched
requests keep the agent's model), and the planner refuses the arm while
`GET /api/routing/complexity-analyzer-status` says `disabled`, because the rule
would never match. The effect shows in W9's `routed_target_<target>` shares,
cost and latency by target and `model_fidelity`. A gateway probe with no
model in its plan is narrowed to the rule's first target, so an outage on
such an arm takes down the routed model; name the model in the probe to test
the agent's own. Each row's tier is read from Bifrost's log
(`complexity_tier`, present on v2.5.1 rows a rule classified) into the run's
gateway rows.

### Gateway overhead

Always on, for any agent: `gateway_overhead_ms` (median over the run's model
request rows of `overhead_latency`) and `gateway_overhead_p95_ms` (nearest
rank). Unknown when the log is incomplete or no row records the overhead (a
gateway older than v2.5.1, or a run sealed before B2). The report's meters
gain "Gateway overhead (ms), median" and "Gateway overhead p95 (ms), median"
per arm, and the run page's metric strip shows both. On the proof stack the
overhead was 1 to 4 ms per request, and the semantic cache's own lookup added
about 96 ms to a direct-mode miss and 183 ms to a hit on a cold local Redis,
all of it counted as gateway overhead, so the overhead metric is also how a
cache's latency cost shows.

### Input tokens and the cache share

`input_tokens` (fresh plus cache read plus cache write over the run's model
rows) is what Code Mode and prompt caching claim to change;
`cache_read_share` is cache-read over all input tokens. Both are unknown when
a model row has no usage.

## Gateway-wide settings: two gateways, two experiments

Binding level, Code Mode limits, agent mode, the semantic cache's vector
store and embedding settings, the complexity classifier and its phrases, and
a provider's default `prompt_cache` are gateway-wide. To compare two values,
run the same experiment file twice, each against a gateway configured one
way (`-config` pointing at a different `gateway.url`), with the same seed,
tasks and trials, then compare the two experiments' per-task results (the
`agent diff` and leaderboard views read across experiments). This is weaker
than an arm, for four reasons, and the comparison should say so: the runs are
not interleaved (time, provider load and model drift differ between the two
experiments), arm order is not randomised across the two, the agent config is
the same so the two experiments share a config hash and the leaderboard
counts them as one entry, and the verdict's paired test runs within one
experiment only, so any cross-experiment test is exploratory. The manifests
record the gateway's client config hash and loaded plugins
(`gateway.config_sha256`, `gateway.plugins`), which do not show a provider's
or a plugin's own settings, so name the setting in each experiment's `name`.

## The $0 proofs

Stack `hd-b2`: Bifrost v2.5.1, Postgres 18, Redis 8 (vector store), the
fault proxy from this tree, and `toolscript`
(`bifrostfeatures/testdata/toolscript`), a scripted Anthropic Messages
upstream behind the fault proxy (`BENCH_FAULTPROXY_UPSTREAM`) that replays a
per-task script of tool calls, Code Mode programs and answers
(`bifrostfeatures/testdata/toolscript/acme-support.json`), streams when asked,
and simulates a caching provider from `cache_control` markers. Agent
`mcploop` (`agents/mcploop.yaml`, `adapters/mcploopagent.py`): a
standard-library tool loop over the run's MCP servers. Suite
`examples/acme-support` (four MCP tasks, one desk server with nine tools);
three tasks used. Every experiment is `smoke: true`; the numbers are scripted
and prove the plumbing and the measurements, not any model's behaviour.

### MCP direct, through Bifrost, and Code Mode (`e_b2_mcp_proof_re2o`)

| Arm | Task | Outcome | Turns | Input tokens | Overhead ms (median) | Bifrost MCP rows | Ledger tool calls | Mismatch | Tool proof | Agent steps |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| A direct | refund-ok | pass | 6 | 7,688 | 1.7 | n/a | 5 | n/a | 1.0 | 11 |
| A direct | address-change | pass | 5 | 5,581 | 0.9 | n/a | 4 | n/a | 1.0 | 9 |
| A direct | duplicate-merge | pass | 4 | 4,346 | 3.6 | n/a | 3 | n/a | 1.0 | 7 |
| B classic | refund-ok | pass | 6 | 7,998 | 1.4 | 5 | 5 | 0 | 1.0 | 11 |
| B classic | address-change | pass | 5 | 5,828 | 0.8 | 4 | 4 | 0 | 1.0 | 9 |
| B classic | duplicate-merge | pass | 4 | 4,536 | 1.8 | 3 | 3 | 0 | 1.0 | 7 |
| C Code Mode | refund-ok | pass | 4 | 8,987 | 2.2 | 5 | 5 | 0 | 0 | 12 |
| C Code Mode | address-change | pass | 4 | 8,998 | 0.8 | 4 | 4 | 0 | 0 | 11 |
| C Code Mode | duplicate-merge | pass | 4 | 9,098 | 2.9 | 3 | 3 | 0 | 0 | 10 |

Verdict inconclusive (three tasks). What it shows: the pipeline serves the
same desk through Bifrost with identical outcomes; Bifrost's MCP log agrees
with the ledger on every call in both modes; and on one server with nine
short tools Code Mode **costs** input tokens: the four meta-tool definitions
are 7,007 bytes in the shape the agent sends them against the desk's own
2,944 (7,079 and 3,169 as `tools/list` returns them), plus the `.pyi` stub read into
the conversation, so input tokens rose 12% to 100% although Code Mode needed
4 turns instead of 4 to 6. The claimed saving needs many servers or large
tool sets: classic cost grows with every tool definition on every turn, Code
Mode's does not (next table). The classic arm costs a few hundred tokens
more than direct because Bifrost prefixes tool names with the client name.

**Where Code Mode starts to pay (measured sizes, derived totals).** The same
desk server registered as 1 to 16 Bifrost clients, granted to one key, and
listed through `/mcp` as an agent would (definitions as the agent sends them
to the model):

| Servers (tools) | Classic: tool definitions per request (bytes) | Code Mode: meta-tools per request | Code Mode: `listToolFiles` result | Code Mode: one `.pyi` stub | Estimated definition bytes per run, classic (6 turns) vs Code Mode (4 turns, one stub) |
|---|---:|---:|---:|---:|---|
| 1 (9) | 2,944 | 7,007 | 207 | 2,099 | 17.7K vs 32.8K: Code Mode +85% |
| 2 (18) | 5,888 | 7,007 | 223 | 2,099 | 35.3K vs 32.9K: about even |
| 4 (36) | 11,776 | 7,007 | 255 | 2,099 | 70.7K vs 32.9K: -53% |
| 8 (72) | 23,552 | 7,007 | 319 | 2,099 | 141K vs 33.0K: -77% |
| 16 (144) | 47,104 | 7,007 | 447 | 2,099 | 283K vs 33.4K: -88% |

The first four columns were measured on the stack; the last is arithmetic
(definitions on every turn, the listing and one stub carried in the
conversation after they are read), not a run, and leaves out the task's own
conversation, which both modes carry. It has the shape of Bifrost's published
curve (58.2% to 92.8% as tools grow): on this desk, Code Mode loses below
about 18 tools and wins clearly from about 36.

The step timeline links each requested call to its ledger row, through
Bifrost's `<client>-<tool>` prefix too (the hyphen separator was added for
B2), so classic Bifrost runs have the same tool proof and agent steps as
direct ones. Code Mode runs cannot be linked call for call: the model asks
for `executeToolCode` and the ledger sees the program's nested calls, so their
tool proof reads 0 and their agent steps count both; for Code Mode arms read
`bifrost_mcp_witness_mismatch` (0 here), not `tool_proof_rate`. The first
passing run of this design (`e_b2_mcp_proof_8uy4`, before the separator fix)
had the same outcomes and tokens and a tool proof of 0 in arm B.

Before these: `e_b2_mcp_proof_jvvk` (arms B and C got 401: the scenario
environment rewrote `BENCH_MCP_SERVERS` without the auth header; fixed),
`e_b2_mcp_proof_nitb` (another agent rebuilt the shared base image mid-run;
the bench refused the drifted image as `aborted_infra`),
`e_b2_mcp_proof_5nm0` (the agent did not stream: Code Mode 0 of 3, agent
mode, above; A and B 3 of 3 each, mismatch 0).

### Auto prompt caching (`e_b2_prompt_cache_proof_37dq`)

Arm B sends `x-bf-prompt-cache-auto-inject: true`; the anthropic provider has
`prompt_cache: {"auto_inject": false}`; toolscript simulates provider caching.

| Arm | Task | Outcome | Input tokens | Cache read share | Cost (USD, pinned prices) |
|---|---|---|---:|---:|---:|
| A | refund-ok | pass | 7,688 | 0 | 0.0245 |
| A | address-change | pass | 5,581 | 0 | 0.0179 |
| A | duplicate-merge | pass | 4,346 | 0 | 0.0140 |
| B | refund-ok | pass | 7,744 | 0.464 | 0.0155 |
| B | address-change | pass | 5,627 | 0.511 | 0.0109 |
| B | duplicate-merge | pass | 4,383 | 0.515 | 0.0086 |

The marker reached the upstream only in arm B; cost per run fell 37% to 39%
at the same success. A plan of this experiment after the semantic cache
plugin was loaded was refused by hygiene ("semantic cache plugin
semantic_cache is loaded"), as it should be: it has no semantic cache arm.

### Semantic cache: what v2.5.1 caches for an agent

Two pipeline experiments ran the mcploop agent on two support tasks with two
trials, arm B opting in: `e_b2_semantic_cache_proof_p249` (direct mode, key
per arm and task) and `e_b2_semantic_cache_replay_proof_ohnk` (semantic mode
with toolscript's bag-of-words embeddings, key per arm, threshold 0.8). Both
had **0 cache hits** in 16 runs, all passing, with the cache engaged (every
B run's first two rows carry a `cache_id`). Probes on the same stack
(`curl`-style requests through Bifrost, 18 Nov 2026) explain why, and they are
the findings a team needs before it tests the cache on an agent:

| Check | Result |
|---|---|
| The same text-answer request twice, same cache key, direct mode | second served from cache (`hit_type: direct`, no upstream attempt, `cache_hit_latency` 183 ms on a cold local Redis) |
| The same request with another cache key, or with no key | miss; no key gives no `cache_debug` at all |
| Semantic mode, "Handle ticket T-1001 ..." then "Handle ticket T-3001 ..." | the second was served the first ticket's reply (`similarity` 0.957, threshold 0.8): a cross-task replay, exactly the risk to measure |
| The same request from two different virtual keys | shared (hit), with or without `disable_identity_scoping`: the identity scoping the fork's docs describe is not in v2.5.1, so the cache key is the only boundary, which is why the bench puts the experiment, arm and task in it |
| A streamed response that is a tool call, the identical request twice | never served from the cache (miss both times, a new `cache_id` each time); a text answer to the same request shape was |
| A conversation with more than 3 messages | not cached at all (no `cache_debug`): the plugin's default `conversation_history_threshold` is 3, and it is gateway-wide |

So, on v2.5.1 with default settings, an agent loop gets cached replies only
for text answers within its first three messages: tool-calling turns are
never replayed, and later turns are skipped. For tool-using agents the cache
saves nothing and replays nothing; for question-answering agents (a text
answer to the first message) it saves the model call and is exactly where a
cross-task replay can happen.

### Semantic cache: a cross-task replay caught (`e_b2_semantic_cache_crosstask_proof_wdrt`)

To see that case through the pipeline, toolscript answered each support
task's first message with a one-line text answer
(`bifrostfeatures/testdata/toolscript/answer-first.json`), so every run fails
verification by design and the measurement is only what was replayed: arm B
semantic mode, one key per arm, threshold 0.8, two tasks, two trials, runs in
the order the seed drew.

| Order | Arm | Task | Trial | Run | Cache hits | From other runs | Cost (USD) | Overhead ms |
|---:|---|---|---:|---|---:|---:|---:|---:|
| 0 | B | refund-ok | 1 | r_qy7i1xw90h | 0 | 0 | 0.00246 | 16.4 |
| 3 | B | address-change | 1 | r_i9tannqttd | 1 | 1 | 0 | 505.8 |
| 4 | B | refund-ok | 2 | r_bgumx13k8h | 1 | 1 | 0 | 27.0 |
| 7 | B | address-change | 2 | r_a40tgre6km | 1 | 1 | 0 | 5.9 |
| | A (4 runs) | both | 1, 2 | | 0 | 0 | 0.00246 each | 1.3 to 20.7 |

`bifrost-bench bifrost cache-replays -exp e_b2_semantic_cache_crosstask_proof_wdrt`:

```
experiment e_b2_semantic_cache_crosstask_proof_wdrt: 3 semantic cache hits; same run 0, same task (other trial or arm) 1, cross-task 2, unattributed 0
SCOPE       HIT RUN       TASK            ARM  STORED BY     TASK       ARM  CACHE ID
cross_task  r_a40tgre6km  address-change  B    r_qy7i1xw90h  refund-ok  B    faebac79-5798-4c69-a37e-ea20df252053
same_task   r_bgumx13k8h  refund-ok       B    r_qy7i1xw90h  refund-ok  B    faebac79-5798-4c69-a37e-ea20df252053
cross_task  r_i9tannqttd  address-change  B    r_qy7i1xw90h  refund-ok  B    faebac79-5798-4c69-a37e-ea20df252053
WARNING: 2 replies stored for one task were served to another task
```

Both address-change runs of arm B were given the answer about ticket T-1001
that the first refund-ok run stored, at no cost and with no model call: the
saving and the harm in one row. The first semantic hit paid half a second of
gateway overhead (the embedding call and vector search). The embeddings came
from a scripted bag-of-words upstream, not an embedding model, so the
similarity says nothing about a real model's; the mechanism, the cache key
scoping and the attribution are what this proves.

### Complexity router (`e_b2_complexity_router_proof_s7wx`)

The analyzer was configured gateway-wide with an embedding provider whose
upstream is toolscript (bag-of-words), which embedded the 150 shipped phrases
(`state: ready`). Arm A: fixed claude-sonnet-4-5. Arm B: the agent asks for
claude-haiku-4-5 and a rule on its key sends `complexity_tier == "COMPLEX"`
to sonnet; `extra_gates: [model_fidelity]`.

| Arm | Runs | Outcome | Rows | Tier on every row | Served | model_fidelity |
|---|---:|---|---:|---|---|---|
| A | 3 | pass | 4 to 6 per run | none (no rule) | sonnet | not fired |
| B | 3 | pass | 4 to 6 per run | COMPLEX, rule `heimdall-route-r_<run>` | sonnet on every row (`routed_target_anthropic_claude_sonnet_4_5` 1.0) | not fired: all expected switches |

Every turn of every B run was classed COMPLEX, not only the first. The
agent's later messages are tool results with no text, so the classifier most
likely read the conversation's only user text, the instruction, on every turn
(inferred: Bifrost logs the tier, not the text it classified). With these
(scripted) embeddings the support instruction lands on a COMPLEX phrase, so
the "cheap by default" arm never used haiku and cost the same as A. A team
should expect complexity routing to act per conversation for agent loops
like this one, and check `routed_target_*` before reading cost.

### Plan-time refusals on the stack

| Experiment | Gateway state | Planner answer |
|---|---|---|
| `b2-prompt-cache-proof` | anthropic provider without `prompt_cache` | `prompt_cache: provider anthropic has no prompt_cache block in Bifrost, so x-bf-prompt-cache-auto-inject is ignored; configure {"auto_inject": false} on the provider to let arms opt in per request` |
| `b2-prompt-cache-proof` | semantic cache plugin loaded | `gateway hygiene check failed ...: semantic cache plugin semantic_cache is loaded` (no arm tests it) |
| `b2-semantic-cache-proof` | semantic cache plugin loaded, no default key | planned; finding kept in `hygiene.bench_semantic_cache` |
| `b2-complexity-router-proof`, `bifrost-complexity-router` | analyzer disabled | `routing.when reads complexity_tier, but the gateway's complexity analyzer is disabled, so no request gets a tier and the rule never matches; configure a classifier (gateway-wide) first` |
| `bifrost-mcp-codemode`, `bifrost-prompt-cache`, `bifrost-semantic-cache` | as shipped here | planned: 36, 24 and 24 slots |

## Experiments (not run on a real model)

| File | Question | Arms | Forecast |
|---|---|---|---|
| `experiments/bifrost-mcp-codemode.yaml` | Does Code Mode cut input tokens on one small MCP server, and does success move? | A direct, B classic, C Code Mode; Claude Code | 36 runs, $3 to $5.5; input tokens up in C, success equal A and B |
| `experiments/bifrost-prompt-cache.yaml` | What does auto prompt caching save for an agent that sends no markers? | A none, B auto_inject; mcploop on claude-haiku-4-5 | 24 runs, under $0.30; cache read share well above 0 in B, success equal |
| `experiments/bifrost-semantic-cache.yaml` | What does the semantic cache save, and what does it replay? | A none, B direct per task; mcploop | 24 runs, under $0.30; expected 0 hits for this tool-using agent (the v2.5.1 limits above) |
| `experiments/bifrost-complexity-router.yaml` | Does haiku with COMPLEX requests escalated to sonnet keep sonnet's success at lower cost? | A fixed sonnet, B haiku plus a rule on `complexity_tier`; custom-python | 32 runs, about $1.5; refused until the gateway's classifier is configured |

The three acme-support experiments run from `bench/` with
`BENCH_BENCHMARKS_ROOT=examples/acme-support/benchmarks`. Forecasts are
assumptions written in each file; `bifrost-bench estimate` gives a
history-based one after a first run.

## Limits

- No real model has run any of these experiments, and no real embedding
  model was used: the semantic cache's and the complexity classifier's
  embeddings came from a scripted bag-of-words upstream, so the mechanisms
  are verified on v2.5.1 but no tier or similarity says anything about a real
  embedding model.
- Code Mode through Bifrost needs an agent that streams its model calls
  (agent mode, above).
- The prompt cache and semantic cache arms work only for agents that send
  the headers: Claude Code (through `ANTHROPIC_CUSTOM_HEADERS`) and
  `mcploop`; Codex and the framework agents do not send extra headers today.
  Claude Code also sends its own cache markers, so auto-inject is a no-op for
  it.
- Agent config shapes for the MCP key, checked with the bench's images and
  no model call: Claude Code 2.1.325 read the `headers` entry and
  `claude mcp get` reported **Connected** to Bifrost's `/mcp/<slug>` on the
  evaluation network with the key; Codex 0.169.0 `codex mcp list --json`
  parsed the `http_headers` table (`auth_status: bearer_token`) but was not
  connected; the OpenCode entry was not checked.
- A killed bench process can leave a run's MCP key and clients behind; the
  janitor deletes them for runs that are no longer active (a key named
  `hm-mcp-<run id>`, and a client named `<service>_<run id>` whose connection
  points at `tp-<run id>`, so a team's own clients are never touched). This
  sweep is unit-tested against a fake gateway only.
- `bifrost_mcp_witness_mismatch` compares the ledger's captured arguments,
  which the proxy clips at its recording cap; a call with very large
  arguments would count as a mismatch.
- Gateway-wide settings are compared with two experiments only, with the
  weaknesses listed above.
