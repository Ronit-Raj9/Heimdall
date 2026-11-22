# External APIs: tasks that call real HTTP services

Some agents are worth testing only against the services they call in
production: a rates API, a weather API, a SaaS endpoint. Heimdall lets a task
declare such an API without giving up what the bench stands on:

- **No open internet.** The agent container stays on internal networks. Only
  the per-run tool proxy may reach the API's host, only that host and port,
  and only when the run opts in.
- **Every call is recorded.** Each call is a row in the run's `tools.jsonl`
  (protocol `external`), sealed with the run.
- **Results can be repeated.** By default a run is answered from a cassette
  of recorded calls committed with the task, with no network at all.

The tool proxy side is described in [tool-proxy.md](tool-proxy.md#external-routes);
the scaffold is `bifrost-bench suite init <id> -template external-api`, and
[`examples/acme-external`](../examples/acme-external/README.md) is a complete
project.

For an internal API you would rather stand in for than replay, write a stateful mock from its OpenAPI file instead: see [openapi-mocks.md](openapi-mocks.md).

## Declaring an API

`external:` is a list on the task (`task.yaml`) or the suite (`suite.yaml`,
then every task gets it). Suite entries come first; an id may appear once.

```yaml
external:
  - id: fx                                  # host-name safe; the agent's URL ends /x/fx
    base_url: https://api.fx.example/v1     # http or https; the host is fixed
    allow:                                  # default deny
      - {methods: [GET], path_prefix: /rates}
      - {methods: [GET], path_prefix: /currencies}
    headers: {Accept: application/json}     # static, added to every call
    auth_env: FX_API_KEY                    # host variable holding the credential
    auth_header: Authorization              # default Authorization
    auth_scheme: Bearer                     # optional: "Bearer <value>"
    mode: replay                            # live | record | replay; see Modes
    cassette: cassettes/fx.cassette.jsonl   # relative to the task folder (suite folder for suite APIs)
    max_calls: 50                           # per run, default 200
    max_bytes: 262144                       # per request body and per answer, default 4 MiB
    timeout_s: 20                           # per call, default 30
```

`path_prefix` is matched against the path after the base URL, on a path
segment boundary: with the base URL above, `/rates` allows
`https://api.fx.example/v1/rates?base=EUR` and `/v1/rates/today`, not
`/v1/rates-admin`. `methods` defaults to `GET`.

`suite lint` checks every field: the id, a plain `http(s)://host[:port][/path]`
base URL without user info, query or fragment, path prefixes that start with
`/`, upper-case methods, header names (no `Host` or hop-by-hop headers), the
variable name, the mode, and that an id is not also a service id.

## What the agent sees

The agent reaches API `<id>` at `http://toolproxy:9200/x/<id>/<path>`, plain
HTTP inside the run network; the proxy speaks TLS to the real host. The URLs
are also in `BENCH_EXTERNAL_APIS`, a JSON object `{"<id>": {"url": "..."}}`,
and command agents can use the template variables `{{external.<id>.url}}`
and `{{external.apis_json}}` in argv, env and files. The agent never sees the
credential and has no other route to the host.

## Modes

| Mode | Network | Cassette | Repeatable |
|---|---|---|---|
| `replay` | none: the proxy container gets no route out | read: every call is answered from it | yes |
| `record` | the proxy reaches the API's host and port only | written to the run folder | no |
| `live` | the proxy reaches the API's host and port only | not used | no |

The mode of each API in a run is decided in this order:

1. The experiment's `external: live | record | replay`, when set, for every
   API of every task of that experiment.
2. Else the API's own `mode`.
3. Else `replay` when the cassette exists.

`replay` needs the cassette; a missing one refuses the plan. `live` and
`record` call the real host and make the run unrepeatable, so they need the
experiment to opt in with `external:`; a task that says `mode: live` in an
experiment without `external:` is refused at plan time, not silently run
live. Admission (`suite admit`) has no experiment, so it always replays: a
task is admitted only with a cassette its reference passes against.

`external:` is part of the experiment and so of the frozen manifest; it is
written only when set, so manifests without it hash as before.

## Cassettes

A cassette is a JSONL file, one recorded call per line:

```json
{"method":"GET","path":"/rates","query":"base=EUR&symbols=USD%2CJPY","body_sha256":"e3b0c442...","status":200,"headers":{"Content-Type":"application/json"},"body":"{\"rates\": {...}}"}
```

- `path` is the path after the base URL, `query` the query string normalized
  (parsed, keys sorted, re-encoded), `body_sha256` the sha256 of the request
  body (the empty-body hash for a GET).
- `body` is the answer as text, or base64 with `"base64": true` when it is not
  UTF-8. `headers` keeps a small subset (`Content-Type` and a few others).
- A call matches the lines with the same method, path, normalized query and
  body hash. Several matching lines are served in recorded order, and the
  last one repeats, so a client that asks twice gets an answer.
- A call that matches nothing gets status 599 with a JSON error and a ledger
  row marked `unmatched`. That is the agent's call, not an infrastructure
  failure: the run is graded normally and the metric `external_unmatched`
  counts it.

The cassette file is hashed into the suite lock with the task's `external:`
entries (only for tasks that have them, so other locks keep their hashes),
so a changed cassette is a changed task.

### Recording and promoting a cassette

1. Set the credential in the shell that runs the bench:
   `export FX_API_KEY=...`.
2. Copy a smoke experiment and add `external: record`, then plan and run it.
   One arm is enough (it is scored on its own): keep the oracle arm for the
   reference's calls, add a noop arm (which makes no calls) for a
   comparison, or use a real agent as an arm to capture what agents ask. Every arm that calls the API
   records its own cassette.
3. Each run folder holds `external/<id>.cassette.jsonl`, written by the proxy
   with the credential redacted, and sealed with the run. Run folders sit at
   `runs/<exp>/slots/<slot>/runs/<run>/`; `report` or the run's `plan.json`
   names the slot of each arm.
4. Promote it by copying the oracle run's cassette over the task's:
   `cp runs/<exp>/slots/<slot>/runs/<run>/external/fx.cassette.jsonl benchmarks/<suite>/tasks/<task>/cassettes/fx.cassette.jsonl`,
   review it (it is the API's real answers), then `suite lock` and
   `suite admit` again.

Hand-written cassettes work just as well: the template and the example ship
one each, for APIs that do not exist.

To try record and live mode end to end without any real third-party API,
`examples/acme-external/tools/standin.py` serves a cassette's answers over
plain HTTP on the host and checks the injected key; the example README walks
through pointing a scratch copy of the task at it, recording, and replaying
the result.

## Secrets

`auth_env` names a variable of the bench's own environment. In live and
record mode the bench passes the variable by name to the tool proxy
container (`docker create -e NAME`, the value comes from the bench process,
never the command line), and the proxy sends it in `auth_header`. A live or
record run whose variable is empty fails before the agent starts. In replay
mode the variable is not passed at all.

The proxy replaces the value with `[REDACTED]` wherever it would be written:
the ledger, the cassette, its log lines, and the answer the agent receives
(an API that echoes the key cannot leak it). The agent's own
`Authorization`, `Proxy-Authorization`, `Cookie` and auth header are dropped
before the call.

## What the proxy refuses

Each refusal is a ledger row with `blocked: true`:

- a method or path not on the allowlist (403);
- an absolute request target, a `Host` header naming another host (ignored:
  the upstream host is always the base URL's), and paths with `..`, `//`,
  `://`, backslashes or encoded slashes, dots or percent signs (400);
- more than `max_calls` calls (429), a request body over `max_bytes` (413),
  an answer over it (502).

Redirects are never followed; a 3xx goes back to the agent as it came, and
the agent has no route to wherever it points. A call slower than `timeout_s`
gets 504.

## Network

In live and record mode the run gets a second internal network that only the
tool proxy joins, and on it one forwarder container per API (the bench
helper image, uid 65534, every capability dropped, read-only, 64 MB) that
also sits on its own outbound bridge network and forwards TCP to exactly the
base URL's host and port. The proxy dials its forwarder and runs TLS end to
end with the real host (server name and `Host` header stay the base URL's).
The agent is never on that network, so it cannot reach a forwarder, and no
other container can reach the API. The forwarders and networks carry the
run's labels and are removed with the run; `external/egress.json` in the run
folder records the allowed destinations.

In replay mode none of that exists: the proxy has no route out, and replay
works with the network fully closed.

The `egress` gate keeps checking what it always checked (the egress probe
and the agent's network) and, when it passes, notes the run's external APIs
with their hosts and modes instead of treating them as a leak.

## Faults

Tool probes (`layer: tool`) apply to external APIs like to tool services:
`server` is the API id, `tool` the method and path (`GET /rates`). An error
or timeout fault answers without touching the API or the cassette; a poison
fault appends its text to the real or replayed answer. The template ships a
`fx-error-first` probe.

## Metrics and labels

- `external_calls`: ledger rows of protocol `external`.
- `external_unmatched`: replay calls the cassette did not hold (lower is
  better).
- `external_error_rate`: share of external calls that failed, blocked ones
  included (lower is better).

All three are unknown, not zero, when the task has external APIs and the
ledger is missing.

An experiment with `external: live` or `external: record` is not
repeatable: the verdict report says `external APIs: live (not repeatable)`
(or `record`) in its header, and the leaderboard leaves it out with that
reason. Replay experiments count like any other.

## What live mode costs you

A live run measures the agent against whatever the API answered at that
moment. Run it again tomorrow and rates, forecasts or search results differ,
the API may be slow or down, and a quota may run out halfway: a difference
between two arms can come from the API, not the agent. That is why live
results are labelled and kept off the leaderboard, and why the recommended
loop is record once, review the cassette, commit it, and compare agents in
replay.

## Limits

- HTTP and HTTPS request and answer only. No websockets, no server-sent
  event or chunked streaming replay: an answer is read whole, up to
  `max_bytes`, before it is relayed.
- One base URL host per API; an API that spans hosts needs one entry per
  host.
- Static credentials only (a key or token in one header). No OAuth flows,
  token refresh or request signing.
- Matching ignores request headers. Two calls that differ only in headers
  get the same recorded answer.
- Clock-dependent APIs ("today's rate", "next hour") need replay to be
  graded with fixed expectations; a live run of such a task needs a verifier
  that reads the answers from the tool ledger.
- The verifier has no network either: it never calls the API.
