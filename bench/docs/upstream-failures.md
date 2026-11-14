# Provider unreachable during a run

A run's model calls go agent, Bifrost, fault proxy, provider API. When the fault proxy cannot reach the provider (DNS, connect, TLS or a response-header timeout), the agent sees a 502 `fault proxy could not reach the upstream` on every call and usually exits with nothing done. That is the infrastructure failing, not the agent, so it must never be scored as a `fail`.

This happened on a live experiment: the host changed networks, Docker DNS broke, and the fault proxy logged `upstream request failed ... dial tcp: lookup api.openai.com ... i/o timeout` for every call. Codex exited 1 after 0 answered model turns and the runs were sealed as `fail`.

## How it is recorded

The fault proxy writes `upstream_error` on the ledger line of a call it passed on (action `pass`, no fault planned) but could not deliver: the transport error with the URL dropped. The status on that line is the proxy's own 502, not the provider's. It is not set when the provider answered (any status, including the provider's own 5xx), on injected or rejected lines, or when the caller went away before the provider answered (an agent killed at its wall budget is not a provider outage).

The field is additive and `omitempty`, so lines written before it keep their hashes. A run recorded by an older proxy has no `upstream_error` at all. For such a run the bench infers the same thing from the gateway log: when a model request in `gateway.requests.jsonl` carries the proxy's message `fault proxy could not reach the upstream`, every `pass` line with the proxy's 502, no response id and no usage is read as an upstream error. A ledger with any `upstream_error` line never infers.

`utils.TallyUpstream` counts, per run: upstream errors, calls the provider answered (2xx), and whether the run's last model call was an upstream error. Injected calls are a probe's planned faults and are never upstream errors.

## Outcome rule

`engine.outcomeFrom` applies it after the veto check, so a fired veto still wins:

| Ledger shows | Outcome | Why |
|---|---|---|
| At least one upstream error and no call answered | `aborted_infra` | The agent never got a model response, so nothing it did depends on the model. Not counted; the scheduler gives the slot a new run, at most 2 retries, as for any aborted run. |
| Some calls answered, the last model call an upstream error, grade not passed | `infra_error` | The agent stopped because the provider went away. Unknown, never counted against the agent, and never rerun, because the agent did real work. |
| Some calls answered, the last model call an upstream error, grade passed | `pass` | The work was done; a lost final call does not undo it. |
| Upstream errors the agent recovered from (a later call was answered and the run ended on it) | unchanged | Reported only. |

When the rule decides the outcome, the run also records a part failure (stage `run`, part `upstream`) with the counts and the first error, so `run.json` says why.

## The `upstream_errors` gate

A baseline gate with effect `report`: it fires on every run with at least one upstream error, whatever the outcome, with the counts in its detail and the ledger call numbers as evidence. It never changes an outcome itself; it makes provider flakiness visible on runs the agent recovered from too.

The verdict's `upstream_errors` instrument check counts, per arm, the counted runs on which the gate fired. It only warns, so the reader knows the provider was unreachable during the experiment and in which arm; runs sealed before the gate are left out, and with none carrying it the check is `not_applicable`. An attempt that was sealed as `aborted_infra` and retried is not a counted run and does not appear in it.

## Runs sealed before the rule

`bifrost-bench regate -exp <id>` applies the rule to sealed runs at no cost: a run sealed as `fail` or `timeout` whose sealed ledger shows it never got a model response, or ended on an upstream error, becomes `infra_error` in the regrade (not `aborted_infra`: a regate cannot give the slot a new run). The new gate is computed for those runs too. Analyse with `-grade-source <regrade_id>`.

## Deploying

Deploy the bench binary before the fault proxy image. A new proxy writes `upstream_error`; an older bench binary drops the field when it reads the ledger, recomputes a different hash and reports a ledger chain break on exactly those lines. The new bench binary reads old and new ledgers. Never roll the proxy back after it has written `upstream_error` lines: an older proxy verifies its ledger on start and refuses a chain it cannot reproduce.
