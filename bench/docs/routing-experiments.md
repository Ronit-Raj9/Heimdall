# Routing policy under test

Status: built 18 Nov 2026 (adoption wave, W9). Proved end to end at $0 with
scripted agents on a throwaway Bifrost v2.5.1 stack; no real model has run a
routing experiment yet.

Heimdall normally treats Bifrost as a measuring instrument: every run gets
one virtual key for one model on one key slot, and the hygiene preflight
refuses any gateway feature that could move a request. This feature lets the
gateway **policy** itself be the thing an experiment compares, so a team can
ask: does a fallback chain, a cheaper-model split or a governance limit
change my agent's success, cost and behaviour under failures?

The policy is part of the agent config, so two arms that differ only in
`gateway.routing` are a routing experiment, with the same tasks, trials,
faults, verdict and gates as any other.

## Declaring a policy

`gateway.routing` in an agent config, or `set: {gateway.routing: ...}` (or
one field, `gateway.routing.limits.request_max: 6`) in an arm. An arm can
also point `agent:` at a file that carries the policy.

```yaml
gateway:
  provider: anthropic
  routing:
    strategy: fallback_chain          # fixed | weighted | fallback_chain | rule
    targets: [{model: claude-sonnet-4-5}]           # provider defaults to gateway.provider
    fallbacks: [{model: claude-haiku-4-5}]          # tried in order on error
    # when: 'request_type == "chat_completion"'     # strategy rule only: CEL condition
    # retries: 0                                     # must equal the provider's max_retries
    limits: {request_max: 6, request_reset: 1h}     # also budget_usd/budget_reset, token_max/token_reset
```

| Strategy | Shape the planner requires | What the bench creates in Bifrost |
|---|---|---|
| `fixed` | one target, no fallbacks | the usual key (one model), plus any limits; no rule |
| `weighted` | two or more targets, each weight above 0, weights summing to 1 (Bifrost's tolerance 0.001) | one rule with weighted targets, optional fallbacks |
| `fallback_chain` | one target, one or more fallbacks | one rule, target weight 1, fallbacks in order |
| `rule` | one or more targets and a `when` CEL condition | one rule with that condition; requests it does not match go to the model the agent asked for |

Planner rules (`engine/routingplan.go`, refused at plan time with the arm
named): `gateway.provider` is set; every target and fallback is on that
provider (a run key holds one key slot, see Limits); the first target is the
model the agent asks for (`model:`), so the fault probe and the fidelity gate
know which model is primary; no target or fallback is listed twice; `when`
only with `rule`; `retries`, when set, equals the provider's `max_retries`
recorded at preflight; limit durations use Bifrost's syntax (`30s`, `5m`,
`1h`, `1d`, `1w`, `1M`); `gateway.fallbacks` (the older declared-fallback
list) and `gateway.routing.fallbacks` are not both set. The manifest records
`routing: {arm: sha256 of the canonical policy}`; a plan without routing has
no such key, so its hash is unchanged, and the arm's config hash covers the
policy as it covers every other field.

## What the bench does in Bifrost, per run

All calls go through the management API with the setup token
(`gateways/bifrost/routing.go`, `keys.go`). Bodies are the exact shapes the
golden tests pin against Bifrost v2.5.1's request structs
(`gateways/bifrost/routing_test.go`).

1. Make sure the run's key slot serves every policy model
   (`PUT /api/providers/{provider}/keys/{id}` with `models` extended).
2. Create the run's virtual key, `POST /api/governance/virtual-keys`:

   ```json
   {"name": "r_<run id>", "is_active": true,
    "provider_configs": [{"provider": "anthropic", "weight": 1, "key_ids": ["<slot key id>"],
                          "allowed_models": ["claude-sonnet-4-5", "claude-haiku-4-5"]}],
    "budgets": [{"max_limit": 0.5, "reset_duration": "1d"}],
    "rate_limit": {"request_max_limit": 3, "request_reset_duration": "1h"}}
   ```

   Governance refuses any routed attempt whose model the key does not allow,
   so the key allows every target and fallback model, and only those.
3. For every strategy but `fixed`, create one routing rule scoped to that key,
   `POST /api/routing/rules`:

   ```json
   {"name": "heimdall-route-r_<run id>", "enabled": true, "cel_expression": "true",
    "targets": [{"provider": "anthropic", "model": "claude-sonnet-4-5", "weight": 1}],
    "fallbacks": ["anthropic/claude-haiku-4-5"],
    "scope": "virtual_key", "scope_id": "<the run's virtual key id>", "priority": 0,
    "description": "Heimdall routing policy under test for run r_<id> (fallback_chain); deleted at run cleanup"}
   ```

   A failed rule create deletes the key and frees the slot; the run fails
   before the agent starts. The bench never creates a global rule.
4. Cleanup deletes the rule first (`DELETE /api/routing/rules/{id}`), then the
   key. Bifrost does **not** delete a key's rules with the key (verified on
   the stack: the rule stayed after `DELETE /api/governance/virtual-keys/{id}`),
   so the order matters. The janitor (`RevokeStale`) deletes every
   `heimdall-route-r_*` rule scoped to a virtual key whose run is not active,
   then the stale keys.
5. Hygiene: preflight still refuses every enabled rule that can move a
   request (more than one target, fallbacks, a pinned provider, model or
   key), whatever its scope, unless it is a bench run rule: named
   `heimdall-route-r_<run id>` **and** scoped to a virtual key. Those are
   recorded in the manifest's `gateway.hygiene.bench_routing` and are not
   violations, because Bifrost applies a virtual-key rule only to requests
   that key authenticates. A bench-named global rule is still refused.

## Evidence, metrics, gate

Per Bifrost row (`schemas.GatewayRequest`, from `GET /api/logs/{id}`):
`provider`, `model_served`, `fallback_index`, `parent_id` (a fallback row
points at the primary row), `routing_rule_id`, `routing_rule_name`,
`routing_engines` (`routing-rule`, `governance`, `core` when a fallback ran).

| Metric | Kind | What |
|---|---|---|
| `fallback_used` | rate | 1 when a fallback row answered a request in the run |
| `served_model_switches` | count | answered turns served by another model than the previous answered turn |
| `routed_target` | count | distinct `provider/model` targets that answered a turn |
| `routed_target_<target>` | derived, 0..1 | the target's share of answered turns |
| `routed_cost_usd_<target>` | derived, USD | priced spend on the target, every attempt (unknown, never 0, when an attempt is unpriced) |
| `routed_latency_ms_<target>` | derived, ms | mean latency of the target's answered rows |

`<target>` is `provider/model` folded to `[a-z0-9_]`
(`anthropic_claude_haiku_4_5`). The three registered metrics are computed for
every run (0 for a fixed key); the derived per-target values only for runs
whose arm declares a policy, so experiments of fixed keys gain no rows. All
read Bifrost rows only, so they work for any agent; an incomplete log makes
them unknown.

**model_fidelity** (`gates/modelfidelityrouting.go`). A Bifrost row records
the model it sent upstream, which for a routed request is the routed model,
so for a routed run the requested model is the agent config's. A served
model the policy names is an **expected switch**: the gate does not fire and
its detail says `routing policy fallback_chain: 1 request(s) served by
another policy model than claude-sonnet-4-5 (expected switch)`. It fires on a
model outside the policy, on a fallback row when the policy declares no
fallback, and on a cache hit. Runs without a policy keep the old rule
unchanged, which is why the gate version did not move. The gate is not a
baseline gate: the routing experiments list it in `extra_gates`.

**Step timeline**: a `model_turn` row answered by another model than the
previous answered turn carries `model_switch: "claude-sonnet-4-5 to
claude-haiku-4-5"` (`evidence/stepsrouting.go`).

**API, UI and report**: `GET /api/bench/experiments/{id}/routing` (every
arm's policy and hash, the lines on which the arms' policies differ, and per
arm and condition: runs, fallbacks fired, model switches, turns, cost and
latency by target) and `GET /api/bench/runs/{id}/routing` (the run's policy,
its Bifrost message rows and the model fidelity result). The verdict page
shows the Gateway policy card (`ui/components/routing/gatewayPolicyCard.tsx`)
for any experiment with a routed arm; the run page shows the run card under
each arm's step tab. `report.md` gains a Gateway policy section with one row
per arm and the "Arms differ in" lines. Nothing appears for experiments of
fixed keys.

## Faults that test a policy

**Outage of the primary target only.** The fault plan has an optional
`model` (`schemas.FaultPlan.Model`): the proxy then fails only requests for
that model and numbers them among themselves (`from_call: 1` fails the first
call for the model), while the ledger call number still counts every call of
the slot. For a run with a routing policy, a gateway probe whose plan names
no model is narrowed to the policy's primary target, the model the agent asks
for (`probes/gateway.go` routedFaultModel), so the existing `model-outage`
probe takes down the primary and leaves the fallback healthy; the receipt's
armed line says so (`slot fp-slot-2: 503 from conversation call 3, model
claude-sonnet-4-5 only`). A probe can still fail every model of a routed run
with `model: "*"`. Unrouted runs keep the plan exactly as written. The proxy
advertises `plan_fields: ["model"]` on `/health`, and its control API decodes
plans strictly, so an older proxy refuses a model-filtered plan instead of
failing every model.

**Governance chaos.** `limits` put a budget or a request or token rate limit
on the run's own key. Bifrost governance then refuses mid-run with 429
(`DecisionRateLimited`, `rate_limit_error`) or 402
(`DecisionBudgetExceeded`). On v2.5.1 a governance 429 carries **no**
`Retry-After` and no `x-bifrost-retry-after-ms` (verified on the stack; the
header is set only from a provider's own Retry-After), so Retry-After
compliance is tested with the fault proxy's 429 plan (`retry_after_s`), not
with governance. Budgets are check-then-record, so concurrent requests can
overshoot (Bifrost issue #7870).

## Experiments (not run for real)

| File | Question | Arms | Forecast (assumption-based, see the file) |
|---|---|---|---|
| `experiments/routing-fallback-outage.yaml` | (a) does a fallback keep the task alive under `model-outage`, at what success and cost | A fixed sonnet; B sonnet then haiku | 64 runs, about $4.2; A about 0/16 under outage, B near haiku's clean rate |
| `experiments/routing-weighted-split.yaml` | (b) fixed vs a 50/50 per-request split | A fixed sonnet; B sonnet 0.5, haiku 0.5 | 48 runs, about $4; lower cost per success in B, switches about half the turns |
| `experiments/routing-cheap-first.yaml` | (c) cheap model first, strong on error | A fixed sonnet; B haiku then sonnet | 64 runs, about $3.5; B mostly haiku on clean, keeps working on sonnet under outage |
| `experiments/routing-governance-chaos.yaml` | (d) does the agent stop cleanly on 429 or 402 from its own key | A no limits; B 6 requests per hour; C $0.02 budget | 48 runs, about $2 |

All four plan on a v2.5.1 stack (`bifrost-bench plan`, nothing run). The
cost forecasts are scaled from the 14 Nov `gpt-5.4-mini` runs (about $0.024
per run) by list price, because no run of these configs exists;
`bifrost-bench estimate` gives the history-based forecast once one does.

## The $0 proof

A throwaway stack (compose project `hd-w9`: `maximhq/bifrost:v2.5.1`,
`postgres:18`, and the fault proxy from this tree in `bench/bifrost-bench:dev`
with the freshly built binary mounted, on ports 28080 and 29101), the toy
scripted agent (three Anthropic Messages calls, then the reference patch;
stops at the first failed call) and the fault proxy's scripted model, which
echoes the requested model and so stands in for both targets. Experiment (a)
in miniature: arms A fixed `claude-sonnet-4-5`, B `fallback_chain` sonnet then
`claude-haiku-4-5`; task t01; clean and `model-outage` (persistent 503 from
call 3), one trial; `smoke: true`, `extra_gates: [model_fidelity]`.

Experiment `e_hd_w9_proof_routing_outage_gated_e5tn`, Bifrost rows of arm B
under `model-outage` (run `r_6zi5wmxr3y`):

| Row | Parent | Served | fallback_index | Status | Rule | Engines |
|---|---|---|---|---|---|---|
| 9ec33773 | | claude-sonnet-4-5 | 0 | success | heimdall-route-r_6zi5wmxr3y | routing-rule |
| f3d86fc7 | | claude-sonnet-4-5 | 0 | success | heimdall-route-r_6zi5wmxr3y | routing-rule |
| 44383a9b | | claude-sonnet-4-5 | 0 | error 503 (injected) | heimdall-route-r_6zi5wmxr3y | routing-rule |
| d025757d | 44383a9b | claude-haiku-4-5 | 1 | success | heimdall-route-r_6zi5wmxr3y | routing-rule, core |

| Run | Arm | Condition | Outcome | fallback_used | served_model_switches | Targets (share) | model_fidelity |
|---|---|---|---|---|---|---|---|
| r_9rjpob1vq8 | A | clean | pass | 0 | 0 | sonnet 1.0 | not fired |
| r_eriji94qm3 | A | model-outage | fail (stopped at the 503) | 0 | 0 | sonnet 1.0 | not fired |
| r_ybd7xwg2oh | B | clean | pass | 0 | 0 | sonnet 1.0 | not fired |
| r_6zi5wmxr3y | B | model-outage | pass | 1 | 1 | sonnet 0.67, haiku 0.33; cost $0.004485 / $0.000847 | not fired: 1 expected switch |

Its step timeline marks the switch (`model_switch: "claude-sonnet-4-5 to
claude-haiku-4-5"`), and the armed receipt reads `model claude-sonnet-4-5
only`. The same proof without the gate ran first
(`e_hd_w9_proof_routing_outage_8sns`, same outcome). Experiment (d) in
miniature (`e_hd_w9_proof_governance_429_4k9p`: B with `request_max: 2`): the
third call got a Bifrost row `error 429 Rate limit exceeded: ... request
limit exceeded`, the toy stopped, outcome fail, `false_completion` 0; arm A
passed.

Also verified on the stack: a VK-scoped rule routes only its key's traffic
(key 1 with a rule to haiku was routed, key 2 with none was served sonnet);
a 50/50 rule split 12 calls 6/6; after every run Bifrost held 0 rules and 0
keys; the janitor deleted a leftover `heimdall-route-r_hdw9leftover` rule and
its key; a foreign global two-target rule made `plan` fail the hygiene check,
the bench-named VK-scoped leftover did not; `/api/routing/model-routers`
answers 404 on the v2.5.1 image. The stack was removed afterwards.

## What v2.5.1 supports, and how it was checked

| Capability | Source (this fork, `transports/version` 2.5.1) | Stack (`maximhq/bifrost:v2.5.1`) |
|---|---|---|
| VK `provider_configs` with `weight`, `allowed_models`, `key_ids`; VK `budgets[]`, `rate_limit` | `transports/bifrost-http/handlers/governance.go:371,573,603` | yes (keys minted, 429 from `request_max_limit`) |
| Routing rules API, body and validation (weights sum to 1, unique targets, known fallback providers, CEL compiled at write) | `handlers/routing.go:163,173,298,342,654` | yes |
| `scope: virtual_key` with `scope_id`, checked to exist; matched only for that key | `handlers/routing.go:241`; `plugins/routing/rules/engine.go:133,414` | yes (isolation test) |
| Rule fallbacks as `"provider/model"` strings | `framework/configstore/tables/routingrules.go:99-166` | yes (fallback row) |
| Routing plugin loaded whenever governance is | `transports/bifrost-http/server/plugins.go:306-317` | yes |
| A rule that sets the provider skips governance load balancing and its automatic fallbacks | `plugins/governance/main.go:484-492,627-652`; `plugins/routing/main.go:491-543` | yes (rows show no `governance` engine) |
| Fallback execution: fresh attempt per fallback, own log row with `parent_request_id` and `fallback_index` | `core/bifrost.go:5865,6074-6240`; `plugins/logging/main.go:2099-2117` | yes |
| Log fields `routing_rule_id`, `routing_rule_name`, `routing_engines_used`, `fallback_index`, `parent_request_id` | `framework/logstore/tables.go:357,369,377` | yes |
| Governance refusals 402 (budget) and 429 (rate limit) | `plugins/governance/main.go:936-995` | 429 yes; 402 source only |
| Retry-After on a governance 429 | not set (`refusal` carries none; `lib/responseheaders.go:296` copies only a provider's) | confirmed absent |
| Deleting a key deletes its rules | no | confirmed: rules stay |
| Retries per key or per rule | no: `max_retries` is per provider (`core/schemas/provider.go:69`) | not tested |
| Fallbacks on a virtual key | no field in `CreateVirtualKeyRequest`; governance adds unselected weighted providers as fallbacks only when the request has none | not tested |
| Session affinity reorders the chain after a fallback serves | `core/sessionaffinity.go:307-352` | not tested (the toy sends `x-bf-session-affinity: off`) |
| Model routers | in this fork's source (`plugins/routing/modelrouters.go`) | 404 on v2.5.1 |

## Limits

- **One provider per run.** A run key holds one key slot, so every target
  and fallback is on the agent's gateway provider. Provider-path equivalence
  (the same model through two providers) is two fixed arms with different
  `gateway.provider`; a cross-provider fallback would need a slot per
  provider and a ledger join over both, which is not built.
- **Retries are not a per-run treatment.** Bifrost's `max_retries` is per
  provider; a policy can only restate the gateway's value.
- **Weighted routing is not seeded.** Bifrost draws per request with an
  unseeded generator, so a split cannot be replayed; report the realised
  `routed_target_*` share, never the nominal weight.
- **Session affinity** is part of the system under test unless the agent
  sends `x-bf-session-affinity: off`: after one fallback serves, later turns
  of the session go to the fallback first. `served_model_switches` and the
  per-target shares show it.
- **Governance 429 has no Retry-After** on v2.5.1, and budgets can overshoot
  under concurrency.
- **The fidelity gate must be switched on** (`extra_gates: [model_fidelity]`),
  as for unrouted experiments.
- No routing experiment has run on a real model. The forecasts are
  assumptions, labelled as such in each file.
