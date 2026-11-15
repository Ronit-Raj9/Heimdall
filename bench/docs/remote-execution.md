# Remote execution

Two features let an experiment run beyond one machine's agent containers:

- **Remote workers.** `bifrost-bench worker` runs on another machine, leases slots from a coordinator over HTTP, runs them with the normal pipeline and uploads each sealed run folder. The coordinator verifies the worker's seal before it accepts the run.
- **Remote agents.** Adapter `remote_http` runs an agent that is a deployed service: the run posts the instruction to the service's URL and grades the answer it returns.

Code: `engine/remote.go` (coordinator), `engine/remoteworker.go` (worker), `engine/remotearchive.go` (archive and seal checks), `engine/scheduler.go` (shared slot queue), `handlers/workers.go` (HTTP routes), `cmd/bifrost-bench/worker.go` (CLI), `adapters/remotehttp.go` and `adapters/remotehttp.py` (remote agents), `utils/remoteegress.go` (the remote agent's one route out).

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

`-slots` is how many slots the worker runs at once (default 1), `-id` names the worker (default a random id), `-config` is the worker's own bench config. The worker refuses a token file that others can read. Runs are sealed in the worker's own runs folder first, then uploaded.

### Reaching the coordinator

`serve` binds `server.listen` from `deploy/bench.yaml`, `127.0.0.1:8090` by default, and that default stays. Two ways for a worker to reach it:

- **SSH tunnel (recommended).** On the worker: `ssh -N -L 8090:127.0.0.1:8090 coordinator-host`, then `-server http://127.0.0.1:8090`. The token and the uploads travel inside SSH.
- **Explicit bind.** Set `server.listen` to a non-loopback address (and `server.allowed_hosts` to the name workers use). The server warns at start that it is not loopback.

The worker token is the worker API's only authentication. There is no TLS built into the server: on an explicit bind the token and every upload cross the network in clear text unless something wraps them (an SSH tunnel, a VPN, or a TLS reverse proxy in front of `serve`). Every worker call carries `Authorization: Bearer <token>`; the coordinator keeps only the token's sha256, compares in constant time, and never logs the token.

### The API

Every route is under `/api/bench/workers`. All but the list require the bearer token: a missing or wrong token answers 401, and every worker route answers 503 when worker mode is off.

| Method and path | Body | Answer |
|---|---|---|
| `GET /api/bench/workers` | | `{"enabled": bool, "workers": [WorkerInfo...]}`; read-only, for the UI |
| `POST /register` | `schemas.WorkerRegister`: worker id, host, build version, part versions, slots | `schemas.WorkerRegistered`: upload cap and chunk size |
| `POST /lease` | `{"worker_id"}` | 204 when there is no work now, else `schemas.WorkerLease`: lease id, slot, attempt, manifest hash, suite lock hash, part versions, heartbeat interval and stale threshold |
| `GET /manifests/{exp_id}?worker_id=` | | the experiment's manifest |
| `POST /leases/{id}/heartbeat` | `{"worker_id"}` | 204, or 410 when the lease is gone |
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

### What remote execution does not guarantee

- **A worker's honesty.** The seal proves that the folder arrived as the worker sealed it and that it belongs to the leased slot and attempt. It does not prove the worker ran the agent faithfully: a worker with the token can fabricate a folder consistent with its own seal. Admit only machines you control, as you would for local runs.
- **Same images.** Image ids are local and are not compared; the same commit with the same image build inputs is what makes a worker's runs comparable. `doctor` on the worker reports missing images.
- **One coordinator.** Leases are exact within the coordinator's store; workers do not coordinate with each other except through it.

## Remote agents: adapter `remote_http`

An agent that is a deployed service is configured with `adapter: remote_http` and a `remote` block (`schemas.RemoteAgent`):

```yaml
id: support-bot
adapter: remote_http
image: bench/coding-core-base:1   # the launcher image: needs python3
model: claude-sonnet-4-5          # recorded; sent only with model_traffic gateway
remote:
  url: https://agent.example.com/v1/answer
  auth_env: SUPPORT_BOT_TOKEN     # host environment variable holding the bearer token
  timeout_s: 300
  model_traffic: external         # or gateway
budgets: {wall_s: 600, cpus: 1, mem_gb: 1}
gateway: {provider: anthropic, max_retries: 0}
```

Only tasks whose output kind is `answer` can run against it: the agent is not beside the workspace, so the answer is all it produces. The adapter refuses any other task when it plans the run, before anything starts, so such a run ends `aborted_infra` with the reason; refusing the experiment at plan time is the planner's check.

### The request

The run's container is the bench's own launcher (`adapters/remotehttp.py`); nothing of the agent runs in it. It sends:

```
POST <remote.url>
Authorization: Bearer <value of $auth_env>      (only when auth_env is set)
Content-Type: application/json

{"instruction": "...", "session_id": "...", "session_index": 1,
 "files": {"notes/chat.txt": "..."},             (small text files of the workspace, when present)
 "gateway": {"url": "...", "key": "...", "model": "..."}}   (only with model_traffic gateway)
```

and waits up to `timeout_s` (default: the wall budget less 5 s). The answer is a JSON object with a string `answer` (another JSON value is written as JSON), or a `text/plain` body; it is written to the task's answer path. A non-2xx status, a body without an answer, or a timeout ends the session as the agent's failure. `session_id` is the session's id in a multi-session task (the run id otherwise) and `session_index` is 1-based, so a stateful deployment can tell a task's sessions apart. `files` holds up to 100 small UTF-8 files of the workspace (256 KiB in all, hidden folders skipped), such as a session's transcript; past those caps it is left out.

The token is read from the host environment when the run is planned and reaches the launcher only through its environment (`BENCH_REMOTE_TOKEN`), which the plan written to `plan.json` redacts; it is never in argv or files, and the launcher never prints it.

### The one route out

The launcher stays on the internal evaluation network like any agent. The plan asks for egress to exactly the URL's host and port (`RunPlan.Egress`); the environment then starts a per-run forwarder container (`bench-eg-<run id>`, the launcher image, nobody, every capability dropped, read-only root) on a fresh outbound network, joins it to the evaluation network, and forwards TCP to that one host and port and nowhere else. The launcher connects to the forwarder while keeping the URL's host in the `Host` header and as the TLS server name, so TLS is verified end to end against the real host. The forwarder is recorded in `egress.json` in the run folder, the egress gate's result names the allowed destination, and the forwarder and its network are removed when the agent exits (the janitor removes a leftover by its run label).

### Model traffic

- **`external` (the default).** The deployment calls its model providers itself, out of the bench's sight. The run's gateway log is marked incomplete with that reason, so `tokens`, `cost_usd` and `steps` are unknown, never zero, and the spend cap counts the run at the per-run estimate. Set `budget.reserve_usd` to a realistic per-run cost.
- **`gateway`.** The deployment sends its model calls through this bench's gateway with the run's key, so they are measured like any agent's. The request carries a `gateway` block: `url` is the gateway as agents reach it on the evaluation network (`gateway.agent_url`), `key` the run's virtual key, `model` the agent config's model. A deployment outside the host must reach the gateway at an address it can route to (a tunnel or a published port of the gateway, which the bench does not set up) and must use `key` as its API key for every model call of that request; calls made with any other key are not the run's and are not counted. The key is revoked when the run ends and carries the per-run budget backstop, but it is sent to the deployment, so use `gateway` only with a deployment you control.
