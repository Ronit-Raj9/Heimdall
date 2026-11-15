# The tool proxy

Every run whose task has a proxied service gets its own tool proxy: a small
reverse proxy that sits between the agent and the run's tool and MCP
services. It records every tool call in a ledger the agent cannot reach, and
it can fault matching calls for a probe of layer `tool`. The code is in
`toolproxy/`, the command is `bifrost-bench toolproxy`, and the design is
section 4 of [scenarios-design.md](scenarios-design.md).

## How a run uses it

The environment kernel starts the proxy, after the services and before the
agent, in the container `bench-tp-<run id>` on the run's service network,
with host name `toolproxy`:

```
bifrost-bench toolproxy -listen :9200 -routes /cfg/routes.json \
    -fault /cfg/fault.json -ledger /ledger/tools.jsonl
```

The image is `images.toolproxy` in the bench config (default
`bench/bifrost-bench:dev`). The proxy refuses to start without valid routes or
a writable ledger, which the run reads as an infrastructure failure.

The agent reaches service `<id>` at `http://toolproxy:9200/s/<id>/<path>`;
an MCP service's endpoint is `http://toolproxy:9200/s/<id>/mcp` (or its
`mcp.path`). `GET /healthz` answers 200 once the proxy is ready and 503 if the
last ledger write failed. When the agent has exited, the environment kernel
copies the ledger to `tools.jsonl` in the run folder, where the evidence
source `tool_ledger` reads it.

A service is proxied when it says `proxy: true`, and by default when its role
is `tool` or `mcp`. A hidden service is never proxied: the agent must not
reach it at all.

## routes.json

Written by the environment kernel, one entry per proxied service:

```json
{"run_id": "r_abc", "routes": [
  {"service": "fs",  "upstream": "http://fs:8080",  "protocol": "mcp", "mcp_path": "/mcp"},
  {"service": "api", "upstream": "http://api:8000", "protocol": "http"}
]}
```

`protocol` defaults to `mcp` and `mcp_path` to `/mcp`. Service ids are
checked, upstreams must be http or https URLs, and a service may appear once.

## What is recorded

One `schemas.ToolCall` JSON line per call, appended and fsynced before the
next row is numbered. `seq` is 1-based and keeps counting after a proxy
restart. Arguments and the result are kept up to 64 KiB each; the sha256 on
the row always covers the whole value.

**MCP** (streamable HTTP). Only POSTs to the service's MCP path are read.
The body is JSON-RPC, one message or a batch.

- Every `tools/call` with an id becomes a row when its answer has been relayed:
  `tool` is the tool name, `arguments` the call's arguments, `result` the
  result object (or the JSON-RPC error), `is_error` true for a JSON-RPC error
  or a result with `isError: true`, `status` the tool server's HTTP status.
- `tools/list` is noted as a row with tool `tools/list`. It is discovery, not
  a call: the tool metrics skip it.
- Answers may come as plain JSON or as a server-sent event stream. A stream is
  relayed event by event and flushed, with server notifications and requests
  passed through untouched.
- Headers pass through both ways (the `Mcp-Session-Id`, the protocol version,
  authorization), except hop-by-hop ones. `Accept-Encoding` is not forwarded,
  so the proxy always reads plain bodies.
- GET streams, session DELETEs and other paths pass through unrecorded.

**HTTP**. Every request is one call: `tool` is the method and path
(`GET /items`), `arguments` the query (as `?a=1`) and the body, `result` the
response body, `is_error` true for a status of 400 or more.

**Failures are recorded, never hidden.** A call whose server is unreachable
is a row with status 502. A call that never gets an answer (the stream
ended, the body was not JSON-RPC) is a row with `is_error` true. A row the
proxy could not hand to the agent, because the agent hung up first, has
`undelivered: true`.

**Traffic it does not understand passes through.** A body that is not
JSON-RPC, or one over the 32 MiB parse cap, is forwarded as it is and makes no
row; nothing in the proxy panics on malformed input.

## Faults

A probe of layer `tool` carries a `schemas.ToolFault` in `apply.tool`:

```yaml
id: fs-write-fails
layer: tool
apply:
  tool:
    mode: error          # error | timeout | poison
    server: fs           # empty matches every proxied service
    tool: write_file     # empty matches every tool
    from_call: 1         # first affected matching call, 1-based
    calls: 2             # how many matching calls; 0 means all from from_call
    message: "EIO: disk write failed (ref {{nonce}})"
```

The tool probe layer (`probes/toolfault.go`) arms it by writing the fault,
with a fresh secret nonce, to `/cfg/fault.json` in `bench-tp-<run id>` with
`docker cp`, and checks the sha256 of the copy from inside the container. It
disarms by removing the file. The proxy reads the file on every call, so a
call sees exactly the fault on disk when it arrives; a changed file is a new
fault and restarts the count of matching calls.

- `error`: the call is not forwarded. The agent gets an MCP tool result with
  `isError: true` and the message (HTTP routes: status 500 and
  `{"error": message}`).
- `timeout`: the call is held for `timeout_s` (default 60) and then answered
  like `error` (HTTP: status 504). An agent that gives up first leaves an
  `undelivered` row.
- `poison`: the real call goes through and `text` is appended to the last
  text item of its result (HTTP: to a JSON object as a `notice` member, else
  as trailing text).

`{{nonce}}` in `message` or `text` is replaced by the nonce. A message without
it gets the nonce on a line of its own (`request id: <nonce>`), so a later
model request that carries it proves the agent saw the fault.

In a batch, faulted calls are answered by the proxy and the rest are forwarded;
the answers are merged. A timeout in a batch holds the whole request.

## The receipt

Every row the fault took carries `fault` and `nonce`. The tool layer's
receipt reads them:

| Step | From |
|---|---|
| fired | rows carrying the run's nonce: how many calls the fault took |
| delivered | those rows without `undelivered` |
| visible | the nonce in a later gateway request |
| acknowledged | the final message naming the failure (or the injection, for poison) |
| effect | the grade |

With no ledger, fired and delivered are unknown; with a ledger and no such
row, fired is "no".

## Metrics

All four read `Evidence.ToolCalls` (tool listings excluded) and are
boundary-level evidence. They are not applicable to a task without a proxied
service, and unknown, never zero, when the task has one and the ledger is
missing.

| Metric | Kind | Better | Per run |
|---|---|---|---|
| `tool_calls_proxied` | count | lower | calls to proxied services |
| `tool_error_rate` | rate | lower | failed calls over calls, injected faults included; not applicable with no call |
| `tool_recovery` | rate | higher | failed calls followed by a later success on the same server, over failed calls; not applicable with no failure |
| `distinct_tools` | count | lower | different (server, tool) pairs called |

External API calls (protocol `external`, below) have three metrics of their
own, not applicable to a task without an external API and unknown when it
has one and the ledger is missing. The four tool metrics above count only
proxied services' calls on a task without them, and every row, external ones
included, on a task that has both.

| Metric | Kind | Better | Per run |
|---|---|---|---|
| `external_calls` | count | lower | calls to the run's external APIs, allowed, refused, replayed and unmatched alike |
| `external_unmatched` | count | lower | replay calls the cassette held no answer for (status 599) |
| `external_error_rate` | rate | lower | failed external calls (refused, unmatched, faulted, 4xx and 5xx) over external calls |

## External routes

A task can declare real HTTP APIs outside the bench (`external:` on the task
or the suite; the guide is [external-apis.md](external-apis.md)). Each one
becomes a route of protocol `external`, and the agent reaches it at
`http://toolproxy:9200/x/<id>/<path>`, never under `/s/`:

```json
{"service": "acme", "upstream": "https://api.acme.example/v1", "protocol": "external",
 "external": {"mode": "replay", "allow": [{"methods": ["GET"], "path_prefix": "/rates"}],
              "headers": {"Accept": "application/json"}, "auth_env": "ACME_KEY", "auth_scheme": "Bearer",
              "cassette": "/cfg/external/acme.cassette.jsonl", "max_calls": 200, "max_bytes": 4194304, "timeout_s": 30}}
```

What the proxy enforces on every call:

- The allowlist. A call passes only when its method (default GET) and its
  path after `/x/<id>` match an entry; a prefix matches on a path segment
  boundary, so `/rates` allows `/rates/today` but not `/rates-admin`.
  Anything else is answered 403 and recorded with `blocked: true`.
- The host. The upstream host is always the route's. A request that names its
  own host (an absolute request target), and paths holding `..`, `//`,
  `://`, backslashes or encoded slashes, dots or percent signs, are answered
  400 and recorded as blocked. Redirects are never followed: a 3xx goes back
  to the agent as it came.
- Headers. Hop-by-hop headers, `Accept-Encoding`, forwarding headers and the
  agent's own `Authorization`, `Proxy-Authorization`, `Cookie` and auth header
  are dropped; the route's static headers are added; in live and record mode
  the value of the variable `auth_env` in the proxy's own environment is sent
  in `auth_header` (default `Authorization`), after `auth_scheme` and a space.
- Caps. More than `max_calls` calls get 429, a request body over `max_bytes`
  gets 413 and an answer over it 502, all recorded as blocked; an answer that
  takes longer than `timeout_s` gets 504.
- Secrecy. The credential is replaced by `[REDACTED]` wherever it appears in
  what the agent receives, in the ledger, in a cassette and in log lines.

Each call is one ledger row with protocol `external`, `server` the API id and
`tool` the method and path (`GET /rates`). In live mode the proxy dials
`connect` (the run's egress forwarder) in place of the upstream host while the
TLS server name and the Host header stay the upstream's. In record mode it
also appends each answered call to `record_to` as one cassette line
(`method`, `path`, normalized `query`, `body_sha256` of the request body,
`status`, a few response `headers`, and the `body`, base64 when it is not
UTF-8). In replay mode it opens no connection at all: a call is answered from
the entries with the same method, path, normalized query and body hash, in
recorded order with the last one repeating, and the row says `replayed`. A
call the cassette does not hold gets status 599 and a row marked `unmatched`:
it is the agent's call, not an infrastructure failure. A replay route whose
cassette is missing or does not parse stops the proxy from starting.

Tool faults apply to external routes like any other: `server` is the API id
and `tool` the method and path. An error or timeout fault answers without
reaching the API or the cassette; a poison fault appends its text to the real
or replayed answer.

## Serving a stdio-only MCP server

Many MCP servers speak only stdio. Their service image runs them behind
`bifrost-bench toolproxy bridge`, which serves the server as a streamable
HTTP endpoint (plain JSON answers, `GET /healthz`):

```dockerfile
FROM bench/bifrost-bench:dev AS bench
FROM node:22-slim
RUN npm install -g @modelcontextprotocol/server-filesystem@2025.12.18
COPY --from=bench /usr/local/bin/bifrost-bench /usr/local/bin/bifrost-bench
ENTRYPOINT ["bifrost-bench", "toolproxy", "bridge", "-listen", ":8080", "-path", "/mcp", "--", "mcp-server-filesystem", "/data"]
```

The bridge writes each message to the server's stdin as one line and answers
with the matching responses. Requests the server sends to the client (roots,
sampling, elicitation) are refused with a JSON-RPC error, so the server falls
back to its command-line setup. If the server exits, pending requests fail and
`/healthz` answers 503.

## Testing

```
GOWORK=off go test ./toolproxy/ ./evidence/ ./metrics/ ./probes/
GOWORK=off go test -tags integration -run TestIntegrationProxyContainer ./toolproxy/
```

The unit tests run the proxy against an in-process MCP server that answers in
JSON and in SSE, with every fault mode, batches and malformed traffic, and run
the bridge against the test binary acting as a stdio server. The integration
test builds the binary into the bench image, starts it in a proxy container in
front of a tiny MCP server on an internal network, calls a tool with a raw
JSON-RPC client, arms and disarms a fault with `docker cp`, and checks the
ledger rows.
