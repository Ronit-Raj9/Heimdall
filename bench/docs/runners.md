# Runners

On the hosted platform (`auth.mode: hosted`) the server process never runs
an agent. Agents run on **runners** (called workers in the web app):
processes that lease slots from the server, run them with the normal
pipeline, and upload the sealed run folders. The server coordinates,
verifies and stores. An organization has two kinds:

- **The shared worker.** Run by the server operator on the server's machine
  (compose profile `shared-runner`, [hosting.md](hosting.md) section 9) and
  available to every organization, with the built-in agents and benchmarks
  and server-approved images. Nothing to set up. Uncapped by default; the
  operator can set per-organization limits.
- **Your own runners.** Any Linux machine or VM with Docker that you
  control, registered with a runner token of your organization. Use one for
  your own Docker images, private networks, GPUs or more slots.

Either way, runs use **your organization's own provider keys**: the keys you
added in [Settings > Model keys](/settings?tab=keys), stored encrypted on the server and handed to the
runner per lease (below). There is no fallback to a key of the server or of
the runner: a run on a provider your organization has no working key for is
refused before it starts.

A runner is the remote worker of [remote-execution.md](remote-execution.md)
with a per-organization token instead of the shared `BENCH_WORKER_TOKEN`.

Local mode is unchanged: `serve` with `BENCH_WORKER_TOKEN` opens experiments
to `bifrost-bench runner start` (or its old name `worker`) exactly as before,
pools are ignored, and the runner admin routes answer 503.

Code: `engine/remote.go`, `engine/remotescope.go` (hosted scope checks,
whoami, forget), `engine/remotehub.go` (one coordinator per organization),
`engine/remotespend.go` (runner spend cap), `runners/` (tables, tokens, runner
rows), `handlers/workers.go` (runner protocol), `handlers/runners.go` (admin
API), `cmd/bifrost-bench/runner.go` (CLI), `cmd/bifrost-bench/runnerhosted.go`
(hosted wiring), `deploy/runner/` (install script).

## Runner tokens and pools

A runner token is `hmr_<8 characters>_<43 characters>`: 32 random bytes,
shown once when an admin creates it, stored only as its sha256. It is scoped
to **one organization and one pool**, may expire (`expires_in_days`) and is
revoked at once by an admin. The auth middleware hands every `hmr_` bearer to
the runners area (`auth.RegisterRunnerVerifier`), which answers a runner
principal `{kind: runner, org_id, pool, token_id}` or 401; an unknown,
malformed, revoked or expired token all get the same 401.

An experiment picks its pool in YAML:

```yaml
runner:
  pool: gpu
```

The field is optional and additive (`omitempty`): an experiment without it
encodes and hashes exactly as before (`TestExperimentRunnerHash`, and every
recorded manifest re-hashes in `TestRecordedHashesUnchanged` and
`TestMatrixHashStability`). In hosted mode an experiment without a pool runs
on pool `default`. With it set, the pool is part of the manifest hash, so a
runner replanning the experiment reproduces it.

## What changes in the protocol

The worker API of docs/remote-execution.md stays as it is; hosted mode adds
an identity to every call.

| | Local mode | Hosted mode |
|---|---|---|
| Credential | `Bearer <BENCH_WORKER_TOKEN>` | `Bearer hmr_...` (runner token) |
| Coordinator | one, over the runs root | one per organization (`engine.RemoteHub`), over `runs/orgs/<org_id>/` |
| Which experiments a runner sees | every attached experiment | its organization's, whose pool is its token's pool |
| Uploads go to | the runs root | the organization's runs root; staging in its `.remote/` |
| Local slots on the server | yes (scheduler concurrency) | none: the launcher runs with `RunOptions.RemoteOnly` |

Additions, all backward compatible:

- `POST /workers/register` takes optional `pool` and `labels`. Hosted: an
  empty pool takes the token's; another pool is refused with 403.
- `GET /workers/whoami` answers `schemas.RunnerWhoami`: mode, org, pool,
  token id, the runners registered with this token, and how many attached
  experiments it may lease now. `runner status` calls it.
- 403 (`schemas.ErrWorkerScope`): a token of another organization, a pool the
  token does not have, or a call for a runner registered with another token.
  The worker stops at registration on 403, like on 401.
- A runner's lease, heartbeat, upload, complete and release calls must carry
  the token it registered with. Another token of the same organization can
  not act for it; a token of another organization never reaches the
  coordinator (the tenancy dispatcher routes by the token's organization,
  and the coordinator checks again).
- Upload verification is unchanged: archive size and sha256, safe extraction,
  the worker's seal verified with `store.Verify` rules, the run re-sealed
  into the organization's chain, the worker's seal kept as
  `remote/seal.json`.
- Lost runners, late uploads, refusals and the spend cap work exactly as in
  local mode.

Runner names (`-name`, the worker id) are unique per organization. A second
runner registering under a name takes over the registration and the first
one's leases are lost, as in local mode.

## Runner rows and states

Every hosted registration writes a `runners` row (name, pool, labels,
version, host, slots, token); lease and heartbeat calls refresh `last_seen`
and the running count (at most every 15 s unless something changed). The
admin list merges the rows with the coordinator's live view:

| State | Meaning |
|---|---|
| `running` | holds at least one lease |
| `idle` | registered, no lease |
| `lost` | silent for longer than the stale threshold (2 min); its leases were released and their slots re-queued |

`live` is true while the coordinator holds a registration (false after the
server restarted, until the runner calls again; it registers again by
itself on the 404).

## CLI

```sh
bifrost-bench runner start -server <url> -token-file <path> [-pool name] [-slots n] [-name name] \
    [-labels a,b] [-spend-cap-usd x] [-config deploy/bench.yaml] [-debug]
bifrost-bench runner status -server <url> -token-file <path> [-name name]
bifrost-bench worker ...          # alias of runner start, same flags (-id is -name)
```

- The token file must be mode 0600, as before.
- `-pool` may be left out: hosted runners take their token's pool.
- `-spend-cap-usd` is the runner's own cap per UTC day: before each lease the
  runner checks what it spent today, together with every other runner process sharing the runs root (observed cost, else the experiment's
  `budget.reserve_usd`, else 0.50 USD per run whose agent started) and stops
  leasing at the cap until the next UTC day. The total is kept in
  `<runs root>/.runner/spend.json`, so a restart does not reset it. Up to
  `-slots` runs already in flight may pass it.
- `runner status` exits 0 when the server answers and accepts the token, and
  prints the org, pool, open experiments and this runner's state; 4 when the
  token is refused (revoked, expired, unknown or another organization's).

## Installing a runner

`deploy/runner/install.sh` sets up a fresh Ubuntu or Debian box as a systemd
service: it checks Docker, clones the project at the server's ref, builds
the binary (or downloads it with `--release-url`), writes the token to
`/etc/heimdall-runner/token` (0600), writes the unit, builds the images,
starts the local Bifrost stack from `deploy/docker-compose.yml`, starts the
runner and runs `runner status`. `--dry-run` prints every step and changes
nothing. Full flags and the upgrade and uninstall steps:
`deploy/runner/README.md`. The hosted server also serves the script at
`GET /api/bench/runners/install.sh`.

## Provider keys per lease

Provider keys live in the server's keystore, encrypted under
`HEIMDALL_MASTER_KEY`; nobody reads a value back over HTTP, owners included.
When a runner takes a lease it calls `GET
/api/bench/workers/leases/{lease_id}/credentials`. Only a runner token of the
lease's organization, from the runner that holds that lease, gets an answer
(`tenancy/policy_workercreds.go`, `handlers/workercreds.go`): the
organization's keys for the providers the run uses, for the length of that
run. The runner registers them with its Bifrost gateway for that
organization (`engine/remotecreds.go`), so every model call of the lease
(agent, judges, simulated users) goes agent, Bifrost, fault proxy, provider
on the organization's own keys. They are not written to the run folder or
the sealed evidence. Keys in a
runner machine's own `deploy/.env` are not used for hosted organizations.

## Admin API

Platform routes of the current organization; every one needs an admin
(`tenancy/policy.go`), and each write leaves one audit row. Local mode
answers 503.

| Route | Body | Answer |
|---|---|---|
| `GET /api/bench/runners` | | `{"runners": [RunnerView]}` |
| `GET /api/bench/runners/{id}` | | `RunnerView` |
| `POST /api/bench/runners/{id}/remove` | | `RunnerView` (audit `runner.remove`) |
| `GET /api/bench/runners/tokens` | | `{"tokens": [RunnerTokenView]}` |
| `POST /api/bench/runners/tokens` | `{"pool"?, "name"?, "expires_in_days"?}` | 201 `RunnerTokenCreated` (audit `runner_token.create`) |
| `POST /api/bench/runners/tokens/{id}/revoke` | | `RunnerTokenView` (audit `runner_token.revoke`) |
| `GET /api/bench/runners/install.sh` | | the install script (public) |

```jsonc
// RunnerView
{"id": "rnr_...", "org_id": "org_...", "name": "box-01", "pool": "gpu", "labels": ["gpu", "region=eu"],
 "version": "0.9.0", "host": "box-01", "state": "running", "slots": 4, "running": ["s_..."], "running_slots": 1,
 "completed": 12, "token_id": "rtk_...", "token_prefix": "ab12cd34", "last_seen": "2026-11-18T06:00:00Z",
 "registered_at": "2026-11-18T05:00:00Z", "live": true}
// RunnerTokenView
{"id": "rtk_...", "org_id": "org_...", "pool": "gpu", "name": "box-01", "prefix": "ab12cd34", "created_by": "usr_...",
 "created_at": "...", "expires_at": "...", "last_used_at": "...", "revoked_at": null, "state": "active"}   // active | revoked | expired
// RunnerTokenCreated: the only answer that holds the token
{"token": "hmr_ab12cd34_...", "info": RunnerTokenView,
 "command": "bifrost-bench runner start -server https://heimdall.example.com -token-file ~/.heimdall-runner-token -pool gpu -name box-01",
 "install": "curl -fsSLo install.sh https://heimdall.example.com/api/bench/runners/install.sh && sudo bash install.sh --server https://heimdall.example.com --token-file ~/.heimdall-runner-token --pool gpu --name box-01"}
```

Removing a runner drops its row and registration and loses its leases at
once; a runner whose token is still valid registers again on its next call,
so revoke its token to keep it out. Revoking a token refuses its next call
and drops every runner registered with it.

## The shared worker

The shared worker is a normal runner process with one difference: instead of
one runner token it holds `HEIMDALL_SHARED_RUNNER_SECRET` and fetches its
assignments from the server (`GET /api/bench/admin/pool/assignments`), one
per organization whose shared pool is on. For each it runs a worker loop
with that organization's token, so every lease, upload and key delivery is
scoped exactly as for an organization's own runner. It replans every
experiment with the server's checkout, so it runs only what the built-in
agent templates and the built-in library reproduce, plus images a server
admin approved for that organization. Setup and guardrails:
[hosting.md](hosting.md) section 9.

## Job kinds

Besides experiment slots, a runner takes runner jobs: hosted CLI jobs that
start containers (agent build, scans, checks) and From issues builds. Each
job has a kind, and a runner says at register which kinds it takes
(`JobKinds`; empty means every kind). The coordinator leases a job only to a
runner that takes its kind; a job of another kind stays queued for a runner
that takes it.

| Runner | Kinds it takes |
|---|---|
| An organization's own runner | every kind |
| The shared worker | `issuebench.work` only ("Build and test an issue on a worker") |

`issuebench.work` runs in the runner process, not as a `bifrost-bench`
subprocess: on the shared worker that keeps every Docker call under the
org's policy, and on an own runner it needs Docker and a reachable
`BENCH_GATEWAY_PUBLIC_URL` (the server's gateway, through Caddy `/gw`). The
lease carries a small request file and the job's secrets (a per-job model key,
the gateway URLs and, for a private repository only, the GitHub token), held
in memory only and redacted from every log line. Only result.json comes back.
Details: [tasks-from-your-repo.md](tasks-from-your-repo.md), "From issues on
the hosted site". With no runner online that takes a kind, a job of that kind
fails with `No worker online:`.

## What a runner token can not do

- Read or write anything but the worker routes: the tenancy policy refuses a
  runner principal on every other route.
- See another organization's experiments, leases or runs, or another pool's
  experiments of its own organization.
- Prove the runner ran the agent faithfully. As in docs/remote-execution.md,
  the seal proves the folder arrived as the runner sealed it, for the leased
  slot and attempt; admit only machines you control.

## Where runs go

Each organization has one setting for where its runs go (**Settings,
Workers, Runs go to**; `GET` and `PUT /api/bench/workers/target`):

| Target | Runs go to |
|---|---|
| `shared` | the shared worker (the default) |
| `platform:<modal\|daytona\|e2b>` | a fresh sandbox per run on a connected platform (next section) |
| `worker:<name>` | one of the organization's own workers (Runloop, Fly Machines, Kubernetes, a VM; section after next) |

`GET` lists every choice with `per_evaluation`: an evaluation may name one
of the organization's own workers in `worker` (`worker:<name>` or the bare
name) whatever the setting, but a platform only while it is the setting,
and `shared` not while a platform is (the platform's runs use the shared
worker's queue). The older `POST /api/bench/worker-platforms/default` and
`PUT /api/bench/workers/default` still work and set this same target: a
platform default clears an own worker default and the other way round.
Org admins set it; members read it.

## Cloud sandbox platforms (bring your own worker)

An organization can run its agents in its own Modal, Daytona or E2B account
instead of on the shared worker: **Settings, Workers, A fresh sandbox per run**.
The admin pastes the platform's API token; the server tests it with a cheap
authenticated read and keeps it only when the platform accepts it, as an org
secret in the keystore (AES-GCM, never returned, never logged; answers carry
its last four characters). A connected platform becomes where runs go with
`platform:<name>` (above); choosing the shared worker again, or
disconnecting the platform, goes back to the shared worker.

Code: `sandbox/` (platform clients, driver, connections), `handlers/workerplatforms.go`
(routes under `/api/bench/worker-platforms`), `cmd/bifrost-bench/workerplatforms_hosted.go`,
`cmd/bifrost-bench/runnerplatform.go`.

How a run gets there: the shared runner's assignments name the org's default
platform (`sharedpool.Assignment.Platform`: name, endpoint and a stamp, never
the token), and the token comes with that org's runner token claim
(`ClaimedToken.Platform`); an org with one is served even when its shared
pool is off, since the compute is its own. That
org's worker loop runs every run with the sandbox driver of its platform,
and its gateway preflight probes egress from a sandbox of that platform.

The sandbox driver is an `environments.Placement`: it runs the
docker_workspace plan unchanged (same images, workspace seed, launch, diff
export), so the manifest still freezes the Docker driver and the server's
hash and the runner's agree. Where the run actually ran is in the run
folder's `sandbox.json` (platform, sandbox id, registry image, egress rule,
isolation check). Per run:

1. One sandbox from the agent image, pulled by registry digest (an image
   that exists only on the runner is refused: push it first), with a hard
   lifetime of the wall budget plus 45 minutes, labelled with the run.
2. Egress: none for `network: none`, otherwise only the public gateway host
   (`gateway.public_url`, required; agent variables naming
   `gateway.agent_url` are rewritten to it). Daytona `domainAllowList` /
   `networkAllowList`, E2B `network.allowOut` with everything else denied,
   Modal `outbound_domain_allowlist` / `block_network`.
3. The workspace is seeded with the Docker driver's populate script from a
   tar of the task repo (the image needs `sh`, `tar` and `git`); before the
   agent starts, a check from inside the sandbox must fail to reach
   1.1.1.1:443, or the run fails as infrastructure.
4. The agent starts in the background as its uid (`setpriv`, or
   `chroot --userspec`), with its environment in a root-only file; the
   driver polls for its exit code under the wall budget and kills every
   agent process on expiry. Both streams are capped and redacted.
5. The diff is exported inside the sandbox after the agent stopped, then
   filtered on the runner; Destroy deletes the sandbox (idempotent, and it
   finds the sandbox from `sandbox.json` after a runner restart).

Refused at plan time, with the reason: services, external APIs, mailboxes,
compose, teams, sessions, memory, skills folders, remote_http and a2a
agents, snapshot grading, tasks with their own image, GPU tasks, realism
neutral, and any fault layer but the gateway. Observers and the event
collector do not run in a sandbox (recorded as off).

Platform APIs, all plain HTTP from Go except Modal:

| Platform | Token | API |
|---|---|---|
| Daytona | API key (bearer) | `https://app.daytona.io/api` sandbox create/get/delete; the sandbox toolbox for `process/execute` and `files/upload-v2` / `files/download`. Endpoint field: a self-hosted API URL. |
| E2B | team API key (`X-API-Key`) | `https://api.e2b.app`: template v3 + build `fromImage` (cached per image), `POST /v2/sandboxes`, `DELETE /sandboxes/{id}`; envd on `sandbox.e2b.app` for `process.Process/Start` (Connect streaming) and `/files`. Endpoint field: a self-hosted domain. |
| Modal | API token `ak-...:as-...` | Modal has no public REST sandbox API. The runner runs `sandbox/modal_helper.py` with the official `modal` Python package (`HEIMDALL_MODAL_PYTHON`, default `python3`) per call; the token reaches it only as `MODAL_TOKEN_ID` / `MODAL_TOKEN_SECRET`, with an empty `MODAL_CONFIG_PATH` so no local profile is used. Sandboxes run under the app `heimdall-sandboxes`. Endpoint field: an optional Modal environment name. |

Endpoint fields are organization input, so the hosted server and the shared
worker call platforms only over https and only at public addresses, checked
when each connection opens (a public name that resolves to a private,
loopback, link-local, carrier-grade NAT or cloud metadata address is
refused). A platform's error answer is reported by status only, never with
its body. An organization's own worker (`bifrost-bench runner`) runs inside
the organization's network and may use a private or plain http endpoint.

Real-platform tests are behind a build tag and skip without a token:
`go test -tags integration -run TestReal ./sandbox/` with `MODAL_TOKEN_ID` /
`MODAL_TOKEN_SECRET`, `DAYTONA_API_KEY` or `E2B_API_KEY`. Each creates one
alpine sandbox without network, runs a command, round-trips a file and
deletes it. A new platform implements `sandbox.Platform` (Check, Create,
Exec, Upload, Download, Destroy) and calls `sandbox.Register` in `init`.

## Bring your own worker (Runloop, Fly Machines, RunPod, Kubernetes, your own VM)

An organization can run its evaluations on compute it owns instead of the shared worker (package `byoworker`, routes in `handlers/workerconns.go`, Workers page). Every such worker is an ordinary runner with its own runner token and its own pool, named after the worker; only how it starts differs:

| Platform | Credential (keystore, encrypted) | What Heimdall does |
|---|---|---|
| Runloop | Runloop API key | `POST https://api.runloop.ai/v1/devboxes` with the one-line install as the devbox entrypoint (`--foreground --install-docker`), the runner settings in `environment_variables`; stop is `POST /v1/devboxes/{id}/shutdown`. Size `X_LARGE` by default, kept alive up to 48 hours. |
| Fly Machines | Fly token for an app the org created (`fly apps create`) | `POST https://api.machines.dev/v1/apps/{app}/machines` (image `ubuntu:24.04` unless set, `restart: always`, 4 performance CPUs and 8 GB by default); stop is `DELETE .../machines/{id}?force=true`. |
| RunPod | RunPod API key | `POST https://rest.runpod.io/v1/pods` with GPUs (`gpu_type`, default `NVIDIA L4`, `gpu_count` 1 to 8, `cloud_type` SECURE or COMMUNITY), image `nvidia/cuda:12.4.1-runtime-ubuntu22.04` unless set, the one-line install as `dockerStartCmd`, the runner settings in `env`; stop is `DELETE /v1/pods/{id}`. The runner registers the pod's GPUs, so the org's GPU slots go to it. A pod is a container: the agent containers need an image where Docker works. |
| Your Kubernetes cluster | A kubeconfig with inline certificates, or a service account token with the API server and its CA, scoped to one namespace | A token Secret and a one-replica Deployment of the runner image (`HEIMDALL_RUNNER_IMAGE` or the connection's image) in that namespace, running as service account `heimdall-runner`; each run is a pod of the Kubernetes driver in its own `heimdall-run-*` namespace. `GET /api/bench/workers/kubernetes-manifest?namespace=NS` serves the RBAC to apply first: a namespaced Role for the credential Heimdall holds, the driver ClusterRole for the runner, and a ValidatingAdmissionPolicy (Kubernetes 1.30+) that refuses any change the runner asks for outside `heimdall-run-*`. Admission does not cover reads, so use a cluster that holds nothing else sensitive. |
| Your own VM | none | The answer of the create call carries one line: `curl -fsSL https://<server>/api/bench/runners/install.sh \| sudo HEIMDALL_RUNNER_TOKEN=hmr_... HEIMDALL_POOL=<name> HEIMDALL_RUNNER_NAME=<name> sh`. |

The install script works as that one line: piped into `sh` it downloads itself again and runs that copy with bash; the server fills in its own address, repository and commit when it serves the script. It reads `HEIMDALL_SERVER`, `HEIMDALL_RUNNER_TOKEN` (then unsets it), `HEIMDALL_POOL`, `HEIMDALL_RUNNER_NAME`, `HEIMDALL_SLOTS` and `HEIMDALL_LABELS`, checks Docker and free disk (`--min-disk-gb`, default 20), and installs the systemd unit; `--foreground` runs the runner as its last step instead (a sandbox with no systemd), `--install-docker` installs git and Docker when missing.

An uploaded kubeconfig may not name a file, run an exec plugin, skip TLS verification or point at a loopback, private or link-local address. Platform API addresses are fixed in code; an organization never chooses where Heimdall sends its token.

Evaluations choose a worker with `worker` in the evaluation body (`worker:<name>` or a worker's name, or `shared`); without one they go where runs go (above). The child experiments of an own worker then carry `runner.pool: <name>`.
