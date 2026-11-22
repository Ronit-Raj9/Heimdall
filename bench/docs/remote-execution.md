# Remote execution

Two features let an experiment run beyond one machine's agent containers:

- **Remote workers.** `bifrost-bench worker` runs on another machine, leases slots from a coordinator over HTTP, runs them with the normal pipeline and uploads each sealed run folder. The coordinator verifies the worker's seal before it accepts the run.
- **Remote agents.** Adapter `remote_http` runs an agent that is a deployed service: the run posts the instruction to the service's URL and grades the answer it returns.

On the hosted platform remote workers are called **runners**: they authenticate with a runner token scoped to one organization and one pool instead of `BENCH_WORKER_TOKEN`, each organization has its own coordinator, and the hosted server runs no slot itself. Everything below holds for them too; docs/runners.md describes what hosted mode adds. `bifrost-bench worker` is now `bifrost-bench runner start` (the old name stays as an alias with the same flags; `-id` is `-name`).

Code: `engine/remote.go` (coordinator), `engine/remoteworker.go` (worker), `engine/remotearchive.go` (archive and seal checks), `engine/scheduler.go` (shared slot queue), `handlers/workers.go` (HTTP routes), `cmd/bifrost-bench/worker.go` (CLI), `adapters/remotehttp.go`, `adapters/remotehttp.py` and `adapters/remotecoverage.go` (remote agents), `utils/remoteegress.go` (the remote agent's one route out), `utils/remotetools.go`, `environments/remotetools.go` and `toolproxy/remotegate.go` (the published tool gate), `evidence/remotereport.go` and `metrics/remotereported.go` (channel summary and self-report).

## Remote workers

### Where the coordinator runs

The coordinator lives in `bifrost-bench serve`, under the API server that already exists, and every experiment the UI or `POST /api/bench/experiments/{id}/start` launches is opened to workers while it runs. It sits in the process that runs the experiment's scheduler on purpose: remote workers then take slots from the same queue, hold them under the same store leases and reserve against the same spend cap as the server's own local workers. `bifrost-bench run` does not open experiments to workers.

Worker mode is off unless `BENCH_WORKER_TOKEN` is set when `serve` starts, and it needs a server that launches experiments (`serve -offline` ignores the token).

### Setting it up

On the coordinator:

```sh
export BENCH_WORKER_TOKEN="$(openssl rand -hex 32)"
bifrost-bench serve
```

`bifrost-bench doctor` shows a `worker mode` row: off, on, or on with a warning (a token shorter than 24 characters, or a server that listens beyond loopback).

On each worker machine, with the same project at the same commit and its own stack (gateway, fault proxy, docker, the agent and task images built):

```sh
printf '%s' "$TOKEN" > ~/.bench-worker-token && chmod 600 ~/.bench-worker-token
ssh -N -L 8090:127.0.0.1:8090 coordinator-host &     # see "Reaching the coordinator"
bifrost-bench worker -server http://127.0.0.1:8090 -token-file ~/.bench-worker-token -slots 2
```

A worker of a hosted server is installed with the server's own script instead (`deploy/runner/README.md`): `curl -fsSLo install.sh https://<server>/api/bench/runners/install.sh && sudo bash install.sh --server https://<server> --token-file ./runner-token`. It downloads the project bundle and the CLI binary of the commit the server runs from the server itself (`GET /downloads/bench-<commit>.tar.gz` and `GET /downloads/bifrost-bench-linux-<arch>`, each checked against its `.sha256`), so no git repository has to be reachable. `--repo URL` clones and builds from source instead.

`-slots` is how many slots the worker runs at once (default 1), `-id` names the worker (default a random id), `-config` is the worker's own bench config. The worker refuses a token file that others can read. Runs are sealed in the worker's own runs folder first, then uploaded.

### Reaching the coordinator

`serve` binds `server.listen` from `deploy/bench.yaml`, `127.0.0.1:8090` by default, and that default stays. Two ways for a worker to reach it:

- **SSH tunnel (recommended).** On the worker: `ssh -N -L 8090:127.0.0.1:8090 coordinator-host`, then `-server http://127.0.0.1:8090`. The token and the uploads travel inside SSH.
- **Explicit bind.** Set `server.listen` to a non-loopback address (and `server.allowed_hosts` to the name workers use). The server warns at start that it is not loopback.

The worker token is the worker API's only authentication. There is no TLS built into the server: on an explicit bind the token and every upload cross the network in clear text unless something wraps them (an SSH tunnel, a VPN, or a TLS reverse proxy in front of `serve`). Every worker call carries `Authorization: Bearer <token>`; the coordinator keeps only the token's sha256, compares in constant time, and never logs the token.

### The API

Every route is under `/api/bench/workers`. All but the list require the bearer token: a missing or wrong token answers 401, and every token route answers 503 when worker mode is off. The list (`GET /api/bench/workers`) is the exception: it is a discovery route that always answers 200, with `"enabled": false` and an empty `workers` list when worker mode is off. Clients must read `enabled`, not the status code, to learn whether worker mode is on.

| Method and path | Body | Answer |
|---|---|---|
| `GET /api/bench/workers` | | `{"enabled": bool, "workers": [WorkerInfo...]}`; read-only, for the UI |
| `POST /register` | `schemas.WorkerRegister`: worker id, host, build version, part versions, slots | `schemas.WorkerRegistered`: upload cap and chunk size |
| `POST /lease` | `{"worker_id"}` | 204 when there is no work now, else `schemas.WorkerLease`: lease id, slot, attempt, manifest hash, suite lock hash, part versions, heartbeat interval and stale threshold |
| `GET /manifests/{exp_id}?worker_id=` | | the experiment's manifest |
| `POST /leases/{id}/heartbeat` | `{"worker_id", "events"?}` | 204, or 410 when the lease is gone. `events` are the slot run's live progress events (see below) |
| `POST /leases/{id}/upload?worker_id=&offset=` | raw archive bytes, at most 512 KiB per call | `{"lease_id", "bytes"}` held so far |
| `POST /leases/{id}/complete` | `schemas.WorkerComplete`: run id, seal.json sha256, archive sha256 and size, outcome, observed cost | `schemas.WorkerResult`: accepted, or rejected with the reason |
| `POST /leases/{id}/release` | `schemas.WorkerRelease`: reason, refuse, cost | 204 |

Statuses: 404 an unregistered worker (it registers again) or an experiment not open to workers; 410 a lease that is gone (lost, released, completed, or its experiment stopped); 409 an upload offset that is not the number of bytes held; 413 an upload past the cap; 400 a malformed body (unknown fields are refused).

### What a worker refuses

Before it runs anything of an experiment, the worker fetches the manifest and checks, once per experiment:

- the manifest hashes to its recorded hash and to the lease's manifest hash, and its suite lock hash is the lease's;
- the bench version and every part version registered on the worker equal the manifest's;
- the suite loaded from the worker's own project has the manifest's lock hash;
- planning the same experiment again with the worker's own project (its agent configs, policy files, suites and parts) gives the same manifest hash. Only what a second plan cannot repeat is taken from the coordinator's manifest: the creation time, the random slot and pair ids (by position), the gateway preflight record and each arm's local image id. A changed agent config, policy, task, seed or part shows up here and the refusal names the differing fields.

A refusal releases the lease with `refuse: true`. It costs nothing, the slot goes back to the queue, the coordinator records the reason and never offers that experiment to that worker again.

### Leases, heartbeats and lost workers

A remote lease is an ordinary store lease on the slot, held by the coordinator for owner `worker:<worker id>:<lease id>`, with the same rules as a local slot (`docs/runner-safety.md`): the worker heartbeats at the scheduler's interval (15 s) and each heartbeat refreshes the store lease.

- **Live events.** While a slot runs, the worker forwards its pipeline's progress events (`run_started`, `stage`) on the heartbeat, and between heartbeats flushes them every 2 s when any wait. The coordinator puts them on the experiment's live event stream with the lease's slot id, exactly as a local run emits them, so the Home running cards, experiment progress and the SSE stream show a remote run as it goes. At most 200 events per heartbeat and 1024 bytes per message; messages are redacted on both sides; any other kind is dropped, so a runner can never make the server count or charge a run. Only the runner holding the lease, under its own organization's token, may post them. They are display only: the uploaded, sealed run stays the record.
- **Lost worker.** A lease with no heartbeat for longer than the stale threshold (2 min) is lost: the summary counts it in `lease_lost`, the run the worker may have started is charged at the per-run estimate (its cost can never be observed here), the slot goes back to the queue, and since its store lease is stale by then, whoever leases the slot next reclaims it under the existing rules.
- **Lease lost to another owner.** When the store says another owner holds the slot or the lease is gone, the heartbeat answers 410 and the worker cancels the slot with an `ErrLeaseLost` cause. The pipeline seals that run as `aborted_infra` in the worker's own store and nothing is uploaded.
- **Late upload.** A complete call for a lease that is gone is refused with 410 and logged, so a slot that was re-leased never gets two counted runs. Right before an accepted run is written, the coordinator also refreshes the store lease and checks that the slot has no counted run.

A worker that registers again (it restarted) loses its old leases at once. The lease table is in memory: when the coordinator restarts, workers get 404 and register again, their old leases are gone, and the store leases go stale and are reclaimed as usual.

### Uploads and seal verification

The worker packs its sealed run folder as a gzip-compressed tar of regular files and folders (a symlink fails the pack) and sends it in chunks below the API server's 1 MiB request cap; each chunk names its offset, so a retried chunk is never written twice. The archive is capped at 512 MiB, the extracted folder at 2 GiB.

On complete the coordinator:

1. checks the archive's size and sha256 against what the worker reports;
2. extracts it into its staging folder (`<runs root>/.remote/`), refusing any absolute or non-local name, any `..` segment, any symlink, hard link or device, a duplicate name, more than 100000 entries, or more than the size cap; nothing is repaired;
3. verifies the worker's seal with the rules of `store.Verify`: `seal.json` hashes to the sha256 the worker reported, names the run and the experiment's manifest, and lists every other file with the same sha256, none missing and none added. `run.json` must be the leased slot's run, at the leased attempt, with the experiment's manifest and suite lock hashes, sealed and with an outcome;
4. writes the run into its own store as a new run of the slot, adds `remote/worker.json` (worker, host, build, lease, attempt, the worker seal's sha256, the archive's sha256, the reported cost, the time) and the worker's `seal.json` byte for byte as `remote/seal.json`, seals the folder into its own experiment chain, and runs `store.Verify` on the result.

A worker seal cannot be chained into the coordinator's `seals.log` as it is (its `prev_seal` is the worker's previous seal), so the coordinator seals the run again; the worker's seal stays inside the folder as evidence of what arrived. A rejected upload answers `accepted: false` with the reason, the slot goes back to the queue, the summary counts it in `remote_rejected`, and a worker with three rejected uploads gets no further leases.

### Spend cap

The coordinator owns the reservation. A remote lease is made only after the same reservation a local slot makes (`docs/runner-safety.md`, "The spend cap holds under concurrency"), under the same lock and against the same running total. A lease request that would have to wait for an in-flight slot gets "no work for now". The worker reports its observed cost with the upload; an accepted run is counted from its sealed `metrics.json`, and a rejected, abandoned or lost run is charged the reported cost when it was observed, else the estimate, never zero.

So the cap holds across every worker and the server's local slots of one experiment. This closes the documented limit that two schedulers do not share a cap, for workers only: a `run -resume` of the same experiment beside `serve`, or two coordinators, still reserve against their own views.

### Local and remote together

The scheduler's local workers and remote workers draw from one queue in slot order. A slot a remote worker gives back (refused, rejected, lost, or an `aborted_infra` attempt that still has retries) is queued again in its place, and the experiment's run waits until no slot is pending and none is out on a remote lease before it ends and analyses. If the run is stopped while leases are out, those leases end, their store leases are released, their slots count as pending and their late uploads are refused.

Every lease, refusal, rejection, release and loss is a line of `<runs root>/.remote/<exp id>.jsonl`, and the live view gets an event for each. The run summary adds `remote` (accepted remote runs, also counted in `sealed`) and `remote_rejected`.

Several bench processes may share one gateway safely: the server, runners on the same machine, parallel CI jobs or two terminals. Each process claims its key slots at the fault proxy before it mints a key, so no process can take or rebind a slot another process's run holds; when every slot is held elsewhere, a run waits up to two minutes and then fails with the holder named. This needs a fault proxy with slot claims: with an older proxy image, or with direct providers, slots are reserved per process only and `doctor` says so (`docs/DESIGN.md` 13.3, "Key slot ownership across bench processes").

### What remote execution does not guarantee

- **A worker's honesty.** The seal proves that the folder arrived as the worker sealed it and that it belongs to the leased slot and attempt. It does not prove the worker ran the agent faithfully: a worker with the token can fabricate a folder consistent with its own seal. Admit only machines you control, as you would for local runs.
- **Same images.** Image ids are local and are not compared; the same commit with the same image build inputs is what makes a worker's runs comparable. `doctor` on the worker reports missing images.
- **One coordinator.** Leases are exact within the coordinator's store; workers do not coordinate with each other except through it.

## Remote agents: adapter `remote_http`

An agent that is a deployed service is configured with `adapter: remote_http` and a `remote` block (`schemas.RemoteAgent`). The bench does not control that service: it sends the session, waits for the answer and grades it. Three optional channels make more of the run measurable. Each needs something from the operator and each has its own evidence strength.

```yaml
id: support-bot
adapter: remote_http
image: bench/coding-core-base:1   # the launcher image: needs python3
model: claude-sonnet-4-5          # recorded; sent only with model_traffic gateway
remote:
  url: https://agent.example.com/v1/answer
  auth_env: SUPPORT_BOT_TOKEN     # host environment variable holding the bearer token
  timeout_s: 300
  model_traffic: gateway          # channel 1; default external
  tools: expose                   # channel 2; default off
  artifacts: inline               # the response may carry artifact files; default none
  sign_env: SUPPORT_BOT_SIGNING   # optional HMAC secret for request signing
budgets: {wall_s: 600, cpus: 1, mem_gb: 1}
gateway: {provider: anthropic, max_retries: 0}
```

### The three channels

| Channel | Config | What the operator must do (deployment on another machine) | What becomes measurable | Strength |
|---|---|---|---|---|
| 1. Model traffic through this gateway | `remote.model_traffic: gateway` | Make the gateway reachable from the deployment: a reverse tunnel or a published, TLS-fronted address, and set `gateway.public_url` to that address. The deployment must use the request's `gateway.key` as its API key for every model call. | `tokens`, `cost_usd`, model turns, retries and fallbacks, on the run key only | trusted (gateway rows) |
| 2. Tools through this bench | `remote.tools: expose` | Make the run's tool gate reachable: either a reverse tunnel to `remote_tools.listen` and `remote_tools.public_url` set to the tunnel's address, or a reachable interface in `remote_tools.listen` with `{port}` in `remote_tools.public_url`. The deployment must call the URLs in the request's `tools` block with their bearer token. | every call to the run's proxied tool and MCP services and external APIs, in the tool ledger like a local agent's; `state` tasks become runnable; browser and computer-use services work the same way | trusted (tool proxy ledger) |
| 3. Self-reported telemetry | none; the deployment adds fields to its answer | Nothing. | `reported_tokens`, `reported_cost_usd`, agent-reported timeline events, self-declared `version` and `build`, inline artifacts for `artifact` tasks | agent_reported (the agent's own word) |

Without channel 1, `tokens`, `cost_usd` and `steps` stay unknown with the external traffic reason, whatever the agent reports. Without channel 2, no tool call of the deployment is seen and only `answer` (and, with `artifacts: inline`, `artifact`) tasks run.

Which tasks a remote agent can run (`utils.RemoteOutputAllowed`, checked when the experiment is planned and again when the adapter plans a run):

| Output kind | Allowed when | Why |
|---|---|---|
| `answer` | always | the answer comes back in the response |
| `state` | `tools: expose` and the task has a proxied service or external API | the deployment changes the services only through the published tool proxy |
| `artifact` | `artifacts: inline` | the files arrive only inline in the response; a response without them leaves the artifact missing, which the grade reports |
| `diff` | never | the workspace is not beside a remote agent |

`agent check` with a remote agent adds a row, "remote agent: measured channels", that names which channels were active in the check run and what is therefore trusted, agent-reported or unknown; `evidence.json` carries the same summary under `remote`.

### The request

The run's container is the bench's own launcher (`adapters/remotehttp.py`); nothing of the agent runs in it. It sends:

```
POST <remote.url>
Authorization: Bearer <value of $auth_env>      (only when auth_env is set)
X-Heimdall-Timestamp: <unix seconds>            (only when sign_env is set)
X-Heimdall-Signature: sha256=<hex>              (only when sign_env is set)
Content-Type: application/json

{"instruction": "...", "session_id": "...", "session_index": 1, "output": "answer",
 "files": {"notes/chat.txt": "..."},                         (small text files of the workspace, when present)
 "gateway": {"url": "...", "key": "...", "model": "..."},   (only with model_traffic gateway)
 "tools": {"notes": {"url": "https://.../r/<run id>/s/notes/mcp", "protocol": "mcp", "token": "..."},
           "weather": {"url": "https://.../r/<run id>/x/weather", "protocol": "external", "token": "..."}}}
                                                             (only with tools expose)
```

and waits up to `timeout_s` (default: the wall budget less 5 s). `session_id` is the session's id in a multi-session task (the run id otherwise) and `session_index` is 1-based. `output` is the task's output kind. `files` holds up to 100 small UTF-8 files of the workspace (256 KiB in all, hidden folders skipped); past those caps it is left out.

**Request signing.** With `remote.sign_env` naming a host environment variable (at least 16 characters), every request carries `X-Heimdall-Timestamp` and `X-Heimdall-Signature: sha256=<hex HMAC-SHA256 of "<timestamp>.<raw body>">`. The deployment recomputes the HMAC over the exact bytes it received, compares in constant time and refuses a timestamp more than a few minutes old. That proves the caller holds the secret; it does not hide the body, so keep TLS on the URL.

**Org secrets and other headers.** On a hosted server the host environment is the server's, not the org's, so use `remote.auth_secret` and `remote.sign_secret` instead of `auth_env` and `sign_env`: they name secrets from Settings > Secrets, resolved per run like any `{{secret.NAME}}`. `remote.auth_header` sends the token in another header (for example `X-API-Key`, raw, without `Bearer`). With `tools: expose`, `remote.tool_allow` lists the only tool, MCP and external API ids the deployment is sent. The "Connect your HTTP endpoint" form on Agents > New agent writes all of these, and its "Test my endpoint" button (`POST /api/bench/agents/test-endpoint`) sends one sample task with the same body, auth and signature and shows the answer and a trace.

**Secrets.** The bearer token, the run key, the tool token and the signing secret reach the launcher only through its environment (`BENCH_REMOTE_TOKEN`, `BENCH_REMOTE_RUN_KEY`, `BENCH_REMOTE_TOOLS_TOKEN`, `BENCH_REMOTE_SIGN_SECRET`), which `plan.json` redacts; they are never in argv or files, and the launcher never prints them.

### The answer

The answer is a JSON object with a string `answer` (another JSON value is written as JSON), or a `text/plain` body; it is written to the task's answer path. For a `state` or `artifact` task the `answer` field is optional. A non-2xx status, an answer task without an answer, `"status": "failed"`, or a timeout ends the session as the agent's failure.

An endpoint that is down is not the agent's failure. A connection that cannot be opened (refused, DNS, TLS, 20 second connect timeout), one closed before any response byte, and a `502`, `503` or `504` answer are retried 3 times, 2, 4 and 8 seconds apart (`BENCH_REMOTE_RETRIES`, `BENCH_REMOTE_BACKOFF_S` on the launcher). When every attempt fails that way, the launcher prints an `endpoint_unreachable` line and exits 75, and the run is sealed as `aborted_infra` with an `agent endpoint` failure: never scored, and the slot gets a new run (three attempts in all). The OpenAI-compatible launcher does the same on every turn; when earlier turns were answered the run is `infra_error` instead (still never scored). Any other status, including `500`, is the agent's answer.

The JSON object may also carry what the deployment says about itself. All of it is agent-reported: shown, never trusted, never deciding an outcome.

```json
{"answer": "...",
 "usage": {"input_tokens": 1200, "output_tokens": 300, "total_tokens": 1500, "model": "...", "cost_usd": 0.004},
 "events": [{"type": "tool_call", "name": "search", "input": {"q": "..."}}],
 "artifacts": {"out/report.csv": "<base64>"},
 "version": "2.3.1", "build": "abc123"}
```

- `usage` fills `reported_tokens` (`total_tokens`, else input plus output) and `reported_cost_usd`, summed over sessions. These are separate metrics with `source: agent_reported`. `tokens` and `cost_usd` never read them: with external traffic they stay unknown with the external traffic reason, and the spend cap keeps charging the per-run estimate.
- `events` is a list of native agent events (`spec/agent-event.schema.json`), a JSON lines string of them, or an OTLP JSON trace (`resourceSpans`; each span becomes one event). The launcher prints each as an `@@heimdall-event ` stdout line, where the agent event collector (`docs/agent-events.md`) normalizes it into an agent-reported timeline row. At most 500 events of 8 KiB each; the rest are counted as dropped. Strings that look like keys are redacted.
- `artifacts` is `{path: base64}` or `[{"path", "content_base64"}]`, honoured only with `remote.artifacts: inline`: each file is written into the workspace at its relative path, where the artifact export picks it up like a local agent's file. Absolute paths, `..`, hidden segments, symlinks, more than 50 files or more than 16 MiB in all are refused and noted. The files are the deployment's output, graded like any agent's.
- `version` and `build` are recorded as self-declared, capped and stripped of control characters.

The launcher sums all of this in one `remote_report` stdout line; `evidence.json` holds it under `remote.reported`.

**Metrics.** Two metrics hold the self-report. Both are lower-is-better (`verdict/direction.go`), carry `source: agent_reported` and evidence level `native`, and are kept apart from `tokens` and `cost_usd`:

| Metric | Kind | Better | Value | Unknown when | Not applicable when |
|---|---|---|---|---|---|
| `reported_tokens` | count | lower | `total_tokens`, else `input_tokens` plus `output_tokens`, summed over sessions | the remote agent reported no token count | the agent is not a remote agent |
| `reported_cost_usd` | usd | lower | `cost_usd`, summed over sessions | the remote agent reported no cost | the agent is not a remote agent |

**Async agents.** A `202` answer is followed on the same host: the status URL is the `Location` header, else `status_url` in the body, else `<remote.url>/<id>` for an `id` in the body. A status URL on another host, scheme or port is refused (the run's egress allows only the URL's host and port). The launcher polls with `GET`, the same bearer token, every `Retry-After` seconds (1 to 30, default 2), until the answer is no longer `202` and its `status` is not `queued`, `pending` or `running`, or the timeout passes. The final document is read like a direct answer.

### Channel 2 in detail: the published tool gate

With `tools: expose` and a task that has proxied services or external APIs, the environment starts, after the tool proxy, a gate container `bench-rt-<run id>` (the tool proxy image, `bifrost-bench toolproxy gate`):

- It is the only container of the run with a published port. It sits on its own bridge network `bench-rt-<run id>` for that port and joins the run's internal service network, where it forwards to `http://toolproxy:9200` only. The tool proxy keeps no route out.
- It publishes on `remote_tools.listen` (default `127.0.0.1:0`: loopback, a free port per run). The deployment is told `<base>/r/<run id>/s/<id>/<mcp path>` for an MCP service, `<base>/r/<run id>/s/<id>` for an HTTP tool service and `<base>/r/<run id>/x/<id>` for an external API, where `<base>` is `remote_tools.public_url` (with `{port}` replaced) or `http://<listen host>:<port>`.
- Every request needs `Authorization: Bearer <token>`. The engine mints the token per run (32 random bytes), keeps it in memory only and hands it to the launcher through redacted env; the gate holds only its sha256 and compares in constant time. No token or a wrong one answers 401, another run id 404, a call after the run's deadline (wall budget plus 10 minutes) 410. The gate drops the token and cookies before forwarding, caps request bodies at 8 MiB and requests at 20 per second (burst 40); the tool proxy's own caps apply behind it.
- Hidden services are never published: they have no proxy route. Browser and computer-use services are MCP services and are published the same way, so a remote agent can drive the bench's browser or desktop.
- Every call lands in `tools.jsonl` exactly like a local agent's. `remote_tools.json` in the run folder records where the gate was published and until when, never the token. `Destroy` removes the gate and its network with the run.

The gate needs the tool proxy image rebuilt from this tree (`images.toolproxy`, default `bench/bifrost-bench:dev`). `bifrost-bench doctor` shows a `remote tools` row: where a gate would be published, and a warning when that is beyond loopback.

### Published through the server (hosted)

On the hosted stack nothing above is needed for the shared worker. Caddy forwards `https://<domain>/gw/...` and `https://<domain>/rt/...` to the shared worker's public ingress (`remote_tools.ingress_listen`, `bench/publicingress`), and compose sets `gateway.public_url` to `https://<domain>/gw` and `remote_tools.public_url` to `https://<domain>/rt` from `HEIMDALL_DOMAIN`. The ingress answers only for live runs on that worker:

- `/gw/<path>` reaches the gateway only on its model and MCP routes (`/v1/`, `/openai/`, `/anthropic/`, `/genai/`, `/bedrock/`, `/cohere/`, `/litellm/`, `/langchain/`, `/pydanticai/`, `/mcp`) and only with the virtual key of a live run (`Authorization: Bearer`, `x-api-key`, `x-bf-vk`, `x-goog-api-key` or `?key=`). The admin API, metrics and the A2A agent gateway are never published. The key is dropped from the ingress when the run ends and Bifrost revokes it.
- `/rt/r/<run id>/...` reaches that run's tool gate only with the run's tool token, while the run is live. Gates then publish no host port: they join the `heimdall-rtgate` network (`remote_tools.ingress_network`) and the worker reaches them by name.
- Each run may make 600 requests a minute (burst 120), gateway and tools together; an address that fails authentication 30 times in a minute is refused for the rest of that minute. Bodies are capped at 32 MiB. A refused call never says whether the key, run or token exists.

The planner refuses a run whose deployed agent is on the internet and needs what its worker does not publish (`model_traffic: gateway` without `gateway.public_url`, `tools: expose` without `remote_tools.public_url`), with that reason, instead of running it and scoring 0. Endpoints on private or compose addresses share the worker's networks and are not refused. An organization's own worker can publish the same way: set `remote_tools.ingress_listen` and both public URLs, and put its own TLS proxy in front.

### Two machines: what the operator does

The bench host runs `bifrost-bench`; the deployment runs elsewhere.

1. **Reach the deployment.** `remote.url` must resolve and be reachable from the bench host; the per-run egress forwarder connects there and nowhere else.
2. **Channel 1.** Expose the gateway to the deployment, for example `ssh -R 18081:127.0.0.1:<gateway port> deploy-host` from the bench host (the deployment then calls `http://127.0.0.1:18081`), or a TLS reverse proxy in front of the gateway. Set `gateway.public_url` in `deploy/bench.yaml` to the address the deployment uses (`http://127.0.0.1:18081` in the tunnel example). The deployment must send its model calls to `gateway.url` from the request with `gateway.key` as its API key; any call with another key is not the run's and is not counted.
3. **Channel 2.** Pick one:
   - **Tunnel, one run at a time.** Set `remote_tools.listen: 127.0.0.1:9400` and `remote_tools.public_url: http://127.0.0.1:19400`, and keep `ssh -R 19400:127.0.0.1:9400 deploy-host` open. A fixed port serves one run at a time: a second concurrent remote-tools run fails to publish and ends `aborted_infra` with the reason, so run such experiments with concurrency 1.
   - **Reachable interface, concurrent runs.** Set `remote_tools.listen` to an address of the bench host the deployment can route to (a VPN or private LAN address, never a public one without a firewall), port 0, and `remote_tools.public_url: http://<that address>:{port}`. Each run gets its own port.
   The bearer token is the gate's only authentication and the gate has no TLS of its own: on anything but a tunnel or a private network put a TLS reverse proxy in front and point `public_url` at it.
4. **Channel 3.** Nothing on the bench side; the deployment adds `usage`, `events`, `version` and `build` (and `artifacts` with `artifacts: inline`) to its answer.
5. **Signing (optional).** Export the secret on the bench host under the name in `remote.sign_env`, give the deployment the same secret, and have it verify `X-Heimdall-Signature`.

### The one route out

The launcher stays on the internal evaluation network like any agent. The plan asks for egress to exactly the URL's host and port (`RunPlan.Egress`); the environment then starts a per-run forwarder container (`bench-eg-<run id>`, the launcher image, nobody, every capability dropped, read-only root) on a fresh outbound network, joins it to the evaluation network, and forwards TCP to that one host and port and nowhere else. The launcher connects to the forwarder while keeping the URL's host in the `Host` header and as the TLS server name, so TLS is verified end to end against the real host. The forwarder is recorded in `egress.json` in the run folder, the egress gate's result names the allowed destination, and the forwarder and its network are removed when the agent exits (the janitor removes a leftover by its run label).

### Model traffic

- **`external` (the default).** The deployment calls its model providers itself, out of the bench's sight. The run's gateway log is marked incomplete with that reason, so `tokens`, `cost_usd` and `steps` are unknown, never zero, and the spend cap counts the run at the per-run estimate. Set `budget.reserve_usd` to a realistic per-run cost. A self-reported `usage` fills only `reported_tokens` and `reported_cost_usd`.
- **`gateway`.** The deployment sends its model calls through this bench's gateway with the run's key, so they are measured like any agent's. The request carries a `gateway` block: `url` is `gateway.public_url` when set, else the gateway as agents reach it on the evaluation network (`gateway.agent_url`), `key` the run's virtual key, `model` the agent config's model. The key is revoked when the run ends and carries the per-run budget backstop, but it is sent to the deployment, so use `gateway` only with a deployment you control.

### What stays impossible

- **The deployment's own tools, files and internal steps.** A remote agent's local tool calls, file writes, subprocesses and reasoning happen on a machine the bench does not see. Only what crosses the gateway (channel 1) or the tool gate (channel 2) is observed; everything else is at best agent-reported (channel 3). No process or workspace observer runs for a remote agent.
- **Tokens and cost without channel 1.** They are only self-reported, in separate metrics, and never verified.
- **Which build answered.** Nothing proves the deployment is the version its owner claims. `version` and `build` in the answer are recorded as self-declared and nothing more. Pin what you can on your side (an immutable deployment URL per version, the signing secret per deployment).
- **Calls outside the run's key or token.** A model call with another key, or a tool the deployment runs by itself, is not the run's and is not counted.
