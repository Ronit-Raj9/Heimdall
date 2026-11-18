# Acme A2A example: agents that speak A2A, behind Bifrost's agent gateway

This folder is a complete Heimdall (bifrost-bench) project for a team that
deploys its agents as A2A servers behind Bifrost and wants to gate releases
on them. It runs at no cost: the agents are scripted reference servers and
every model call is answered by the fault proxy's scripted model. The design,
the witness and the Bifrost facts behind it are in
[docs/a2a-agents.md](../../docs/a2a-agents.md).

- **Three answer tasks** on a fictional tea shop's support handbook
  (`benchmarks/acme-a2a`), graded by `answer_match` with no model, admitted
  with the scripted `oracle` and `noop` controls and locked.
- **One artifact task and one state task** (`benchmarks/acme-a2a-files`,
  `benchmarks/acme-a2a-stock`), written by `suite init -template artifact`
  and `-template tool-use`, admitted and locked, to show the other output
  kinds an A2A agent can produce.
- **A2A agents** (`agents/a2a-*.yaml`, adapter `a2a`), each a persona of the
  reference server `bench/a2a/refagent.py` (`agents/refagent/personas.json`):
  `a2a-oracle` answers correctly, `a2a-noop` answers nothing useful,
  `a2a-broken` fails every task, `a2a-stream` streams, `a2a-failover`
  (streaming) and `a2a-failover-unary` advertise a first interface that is
  gone, `a2a-reporter` returns files as artifacts, `a2a-clerk` changes a
  tool server's state through the published tool gate, and `a2a-team` is an
  entry agent that delegates to two others through the gateway.
- **Two stream probes** (`probes/stream-drop.yaml`, `probes/stream-delay.yaml`,
  layer `a2a`): the bench's launcher drops or stalls its own side of the
  stream and the task must be recovered through the gateway.
- **Six experiments**: `a2a-oracle-vs-noop` (the pipeline proof),
  `a2a-team` (one agent against a team at the same task), `a2a-failover`
  (Bifrost's unary interface failover), `a2a-transport` (the two stream
  probes, and a stream whose first interface is gone), `a2a-outputs` (files
  as A2A artifacts) and `a2a-state` (a state change through the published
  tool gate).

## What is fictional

Acme Tea, its handbook and every name in it are invented for this example.
Everything was written from scratch and is MIT licensed (`provenance` in
every `task.yaml`).

## Running it

1. Start the bench's stack with the reference agents beside Bifrost, and
   merge `deploy/bifrost-a2a.json` into Bifrost's `config.json` (see the
   comment in `deploy/a2a-agents.compose.yml`). The agent gateway's protocol
   routes answer 503 until `a2a_external_client_url` is set.
2. From this folder:

```bash
bifrost-bench suite admit -config deploy/bench.yaml acme-a2a
bifrost-bench suite lock -config deploy/bench.yaml acme-a2a
bifrost-bench run -config deploy/bench.yaml experiments/a2a-oracle-vs-noop.yaml
```

`a2a-state` publishes the run's tool proxy to the agent, which runs beside
Bifrost, not on the bench's host: set `remote_tools.listen` to an address of
the bench host the agent's container can reach (for the compose stack, the
upstream network's gateway address with port 0) and
`remote_tools.public_url` to `http://<that address>:{port}`
(`docs/remote-execution.md`).

Each run registers its agents in the gateway under run-scoped names
(`hb-<run id>-<member>`), grants only the run's keys, and deletes them at
cleanup. `evidence.json` `a2a` holds the agent log rows of the run, the
agreement between the graded answer and the gateway's record, the hops of a
team, and the LLM rows correlated with each A2A task.

## Using your own agent

Copy `agents/a2a-oracle.yaml`, point `a2a.card_url` at your agent's card as
Bifrost reaches it, and add the agent's host to
`client.a2a_private_allowlist` when it is on a private network. For
`credential: forward` (the default) your agent should reuse the `x-bf-vk` it
receives for its model calls through Bifrost and send
`x-bf-agent-correlation-id: <A2A task id>` and
`x-bf-session-id: <A2A context id>` on them, as Bifrost's
"Connect model and tool calls" guide says; those calls then count in the
run's tokens and cost and are linked to the task in the witness.
