# Runners

On the hosted platform (`auth.mode: hosted`, docs/platform-design.md) agents
never run on the hosted server. Each organization runs them on **runners**:
machines it owns that lease slots from the server, run them with the normal
pipeline and their own provider keys, and upload the sealed run folders. The
server only coordinates, verifies and stores. A runner is the remote worker
of docs/remote-execution.md with a per-organization token instead of the
shared `BENCH_WORKER_TOKEN`.

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

**Provider keys stay on the runner.** They go in
`<prefix>/src/bench/deploy/.env` on the runner (`ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`, ...), where only the fault proxy container reads them. The
hosted server never sees them.

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
 "install": "curl -fsSLo install.sh https://heimdall.example.com/api/bench/runners/install.sh && sudo bash install.sh --server https://heimdall.example.com --token-file ./runner-token --pool gpu --name box-01"}
```

Removing a runner drops its row and registration and loses its leases at
once; a runner whose token is still valid registers again on its next call,
so revoke its token to keep it out. Revoking a token refuses its next call
and drops every runner registered with it.

## The demo runner

The public demo organization (`demo.org` in the config) runs on a normal
runner registered to it, in pool `demo`, under a spend cap. Nothing about it
is special to the server.

1. As an admin of the demo organization, create its token:
   `POST /api/bench/runners/tokens {"pool": "demo", "name": "demo-runner"}`
   (Settings > Runners in the UI, or `deploy/runner/demo-runner.sh`, which
   calls the API with an admin API token and writes the token file 0600).
2. Install it like any runner, on the hosted box itself or a spare one:
   `sudo bash install.sh --server <public url> --token-file ./runner-token --pool demo --name demo-runner --slots 1 --spend-cap-usd 5`.
3. Give the demo organization's experiments `runner: {pool: demo}` and a
   `budget.total_usd`. Three caps then hold: the experiment's spend cap on the
   server, the runner's own daily cap (`-spend-cap-usd`), and the provider
   budget of the keys in the runner's `deploy/.env`.

`deploy/runner/demo-runner.env.example` holds the settings
`demo-runner.sh` reads.

## What a runner token can not do

- Read or write anything but the worker routes: the tenancy policy refuses a
  runner principal on every other route.
- See another organization's experiments, leases or runs, or another pool's
  experiments of its own organization.
- Prove the runner ran the agent faithfully. As in docs/remote-execution.md,
  the seal proves the folder arrived as the runner sealed it, for the leased
  slot and attempt; admit only machines you control.
