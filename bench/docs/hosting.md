# Hosting Heimdall

From a fresh Linux VM to a working Heimdall at `https://<your domain>`: teams
sign up, belong to organizations and benchmark their own agents; the hosted
server stores sealed results, renders the UI and coordinates the workers that
run the agents. Files: `deploy/hosted/`.

```
browser / runner --HTTPS--> Caddy (80, 443) --> heimdall:8090 --> Postgres
                            only published       server, UI        internal network
                            service              embedded          not published
```

The server process runs no agent. Agents run on **workers** (runners): the
optional **shared worker** on this same machine, which every organization
can use (section 9), and each organization's own workers on machines it
controls (section 6).

What the server holds, and what it does not:

- **Provider keys.** Each organization adds its own keys in Models & keys.
  They are stored encrypted in the keystore under `HEIMDALL_MASTER_KEY` and
  are never shown again, not even to owners. A worker receives an
  organization's keys only while it holds a run of that organization, over
  the authenticated lease credentials route, and never writes them to the
  sealed evidence.
- **Own key required.** Runs never fall back to a key of the server or of
  the worker. An evaluation or experiment on a provider the organization has
  no working key for is refused before anything runs, with a pointer to
  Models & keys.
- **No public demo.** A fresh install has no demo organization, no
  anonymous access and no sample data. Every visitor signs in and sees only
  their own organization's work.

## 1. Machine

| | Minimum | Comfortable |
|---|---|---|
| CPU, RAM | 2 vCPU, 2 GB | 4 vCPU, 8 GB |
| Disk | 20 GB | 100 GB+ SSD; sealed runs grow by roughly 2 to 3 MB per run (318 runs took 745 MB) |
| OS | Any 64-bit Linux with Docker Engine 24+ and the Compose plugin v2.20+ (tested: Docker 29.9, Compose 5.6) | Ubuntu 24.04 LTS or Debian 12 |

The shared worker (section 9) runs agent containers on the same machine: add
2 vCPU and 4 GB per concurrent slot, or leave it off.

## 2. DNS and firewall

- Create an `A` record (and `AAAA` if the VM has IPv6) for your domain, e.g.
  `heimdall.example.com`, pointing at the VM. Caddy obtains the certificate on
  first start; this needs the record in place and port 80 reachable.
- Open inbound TCP 22 (SSH, ideally from your addresses only), TCP 80
  (ACME challenge and redirect) and TCP 443, plus UDP 443 for HTTP/3. Nothing
  else: Postgres and the server are on internal Docker networks and are not
  published.

```sh
sudo ufw default deny incoming && sudo ufw default allow outgoing
sudo ufw allow OpenSSH && sudo ufw allow 80/tcp && sudo ufw allow 443/tcp && sudo ufw allow 443/udp
sudo ufw enable
```

Docker publishes ports through its own iptables rules, ahead of ufw; that is
why only Caddy publishes anything. Use the cloud provider's firewall as well.

## 3. Install Docker and get the code

```sh
sudo apt-get update && sudo apt-get install -y git curl openssl ca-certificates
curl -fsSL https://get.docker.com -o get-docker.sh && sudo sh get-docker.sh
sudo usermod -aG docker "$USER" && newgrp docker     # or use sudo for every docker command
sudo git clone <repository URL> /opt/heimdall && sudo chown -R "$USER": /opt/heimdall
cd /opt/heimdall/bench
```

Check out the release you want to run (`git checkout <tag>`); runners must use
the same commit.

## 4. GitHub sign-in (optional)

Email and password work without it. For "Sign in with GitHub", create an
OAuth app at GitHub > Settings > Developer settings > OAuth Apps > New:

| Field | Value |
|---|---|
| Homepage URL | `https://heimdall.example.com` |
| Authorization callback URL | `https://heimdall.example.com/api/bench/auth/github/callback` |

Put the client id and a generated client secret in `deploy/hosted/.env`
(`HEIMDALL_GITHUB_CLIENT_ID`, `HEIMDALL_GITHUB_CLIENT_SECRET`) after step 5
creates it, then run `deploy/hosted/upgrade.sh --no-pull` (or `bootstrap.sh`
again) to restart with them. GitHub sign-in is on when both are set, off
otherwise.

## 5. Bootstrap

```sh
deploy/hosted/bootstrap.sh --domain heimdall.example.com --email ops@example.com \
    --owner-email you@example.com --owner-name "Your Name"
```

It is safe to run again; each step skips what is done. In order:

1. creates `deploy/hosted/.env` (mode 600) from `.env.example`, with random
   Postgres and gateway secrets, the keystore master key
   (`HEIMDALL_MASTER_KEY`, base64 of 32 random bytes), your domain and ACME
   e-mail. Review the file: signup policy (`HEIMDALL_SIGNUP`: `invite` by
   default, `allowlist` with `HEIMDALL_SIGNUP_DOMAINS`, `open`, `closed`;
   docs/auth.md), GitHub, S3 mirror, shared worker.
2. builds the server image from this checkout (`deploy/hosted/Dockerfile.server`:
   `npm ci` and the UI build, then a static Go binary with the UI embedded, on
   a non-root Alpine runtime; about 5 minutes the first time)
3. starts Postgres and applies the database migrations (`bifrost-bench db migrate`)
4. creates the first owner with `bifrost-bench user create` and prints the
   generated password once. Sign in and change it under Settings, Account
   (`POST /api/bench/auth/password`). The change ends every other session of
   that user; the session that made it stays signed in.
5. imports nothing: a production install holds real data only, and every
   organization route needs sign-in (`--demo-runs` and `--no-demo` are
   accepted and ignored, for older commands). Share links (`/s/<token>`)
   are the only pages a visitor opens without an account.
6. starts Caddy, the server and Postgres, waits until all are healthy

Create the owner with `--owner-email` (it runs `bifrost-bench user create`,
password on stdin, before the site takes traffic). Without it, the first
account to sign up on the empty install becomes the owner, with signup
`invite` or `open` alike: whoever reaches the server first. With `closed`, no
web signup works at all and accounts come only from `user create`. Later
signups follow the policy (`invite`: only with an invitation link).

An organization left without an admin is repaired with
`$C run --rm --no-deps heimdall org member add -org <org> -email
you@example.com -role owner`.

Open `https://heimdall.example.com/`, sign in (your organization was created
with the account), invite your team (Settings > Members).

Each organization has a project folder in the data volume,
`/var/lib/heimdall/runs/projects/<org id>/` (agent configs and their saved
versions, its own benchmarks, evaluations, experiments, policies), next to
its sealed runs in `runs/orgs/<org id>/`. Organizations add agents in the
browser: from a template, by editing the config (`PUT
/api/bench/agents/{id}/config`), or by uploading a config file (`POST
/api/bench/uploads/agent`). No shell access to the server is needed.

**Built-in library.** Every organization also sees the server's read-only
benchmark and agent library (`HEIMDALL_LIBRARY_DIR`, the checkout's
`benchmarks/` and `agents/` by default) without copying anything: public
benchmarks such as Terminal-Bench, SWE-bench Verified, tau2 and AgentDojo,
and the built-in agents (Codex, Claude Code, OpenCode, Gemini CLI,
mini-swe-agent). An organization's own benchmark or agent with the same id
wins over the library's.

Configuration lives in
`deploy/hosted/bench.hosted.yaml` (mounted read-only; secrets are only ever
named by environment variable) and `deploy/hosted/.env`. Site-specific compose
changes go in `deploy/hosted/docker-compose.override.yml`, which the scripts
pick up.

## 6. Organization workers

Each organization picks where its runs go on the Workers page (one setting,
`PUT /api/bench/workers/target`): the shared worker, a fresh sandbox per run
in its own Modal, Daytona or E2B account, or one of its own workers on
Runloop, Fly Machines, its Kubernetes cluster or its own VM. Details:
[runners.md](runners.md#where-runs-go).

An organization that wants its agents on its own machines connects its own
worker: any Linux machine or VM with Docker, in its own cloud account or
data center. An org admin creates a worker token (`hmr_...`, one org, one
pool, shown once) on the Workers page, which also shows the install command.
On the worker machine (Ubuntu or Debian, Docker installed):

```sh
curl -fsSLO https://heimdall.example.com/api/bench/runners/install.sh
sudo bash install.sh --server https://heimdall.example.com --token-file ./runner-token \
    --ref <the server's commit or tag> --repo <repository URL> --pool default --slots 2
```

It installs `bifrost-bench runner start` as the `heimdall-runner` systemd
service with its own Bifrost gateway stack. The worker receives the
organization's provider keys per run from the keystore (section intro), so
no key has to be copied to it; keys put in that machine's `deploy/.env`
are not used for hosted organizations. Details, flags, upgrades and
troubleshooting: [runners.md](runners.md) and `deploy/runner/README.md`.

Model keys: every organization's runs use only that organization's own keys
from Models & keys, which the server hands to the runner per lease. There is
no fallback to the server's or a runner's own provider keys, and no silent
fallback to the scripted practice model. Evaluations, QuickRun, experiments
and schedules are refused before launch (HTTP 409, with a link to `/models`)
when the organization has no working key for a model's provider, and with 503
when the key store cannot be read. A runner refuses a hosted lease whose keys
it cannot fetch. Practice runs (smoke or `scripted_upstream` experiments,
scripted controls) are labelled `practice` in `run.json` and are left out of
report cards, leaderboards and verdicts.

## 7. Backups

```sh
deploy/hosted/backup.sh --dir /var/backups/heimdall --keep-days 14
```

Writes `heimdall-db-<time>.dump` (`pg_dump`, custom format) to the folder
(mode 700). `.env` holds `HEIMDALL_MASTER_KEY`, which decrypts every stored
provider key, so it is never copied beside the dump in plain text: set
`HEIMDALL_BACKUP_PASSPHRASE_FILE` (a passphrase file kept elsewhere) for an
encrypted `heimdall-env-<time>.enc`, or pass `--env-dir DIR` (another folder,
another disk or a vault mount) for a plain copy there. With neither, the
backup warns and leaves `.env` out: keep it, or at least the master key, in
your secrets manager. `bootstrap.sh` generates the master key when it is
empty and never replaces one. Evidence: with the S3 mirror set
(`HEIMDALL_S3_BUCKET` and its keys in `.env`, or `HEIMDALL_S3_BUCKET` in the
shell, which wins over `.env` as it does for compose; create the bucket first, the
mirror never creates one, and keep it private) it runs
`bifrost-bench evidence backup` in the server container, which uploads a
second database dump to the bucket (`_backups/db/`) and mirrors every sealed
experiment, checking each seal (`docs/evidence-storage.md`; a configured
bucket with a server build lacking that command fails the backup rather
than writing a local-only one), and archives the
organization project folders (`runs/projects`: agent configs, suites,
experiment definitions, policies, release logs) and `releases/` to
`heimdall-projects-<time>.tar.gz`, since the mirror holds sealed experiments
only; without a mirror it archives the data volume to
`heimdall-evidence-<time>.tar.gz`. Archives are taken with the server
container paused (`docker compose pause`), so a seals.log and its run folders
always come from the same moment; requests wait while the tar runs. The
server also mirrors each experiment after it is sealed. Schedule it and
copy the folder off the machine:

```sh
echo '17 3 * * * root /opt/heimdall/bench/deploy/hosted/backup.sh >>/var/log/heimdall-backup.log 2>&1' | sudo tee /etc/cron.d/heimdall-backup
```

Sealed run folders are the evidence; the database holds users, tokens, jobs,
runners, audit and an index that can be rebuilt from the folders.

Restore on a new machine: sections 1 to 3 (DNS pointed at the new machine),
`.env` restored to `deploy/hosted/.env` (mode 600) from your secrets manager,
`--env-dir` or the encrypted copy
(`openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass file:<passphrase file> -in heimdall-env-<time>.enc`), then

```sh
cd /opt/heimdall/bench && C="docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env"
$C build heimdall
$C up -d --wait postgres
$C exec -T postgres pg_restore -U heimdall -d heimdall --clean --if-exists --no-owner < heimdall-db-<time>.dump
gunzip -c heimdall-evidence-<time>.tar.gz | $C run --rm --no-deps -T --entrypoint tar heimdall -C /var/lib/heimdall -xf -
#   or, with the S3 mirror, into the empty runs folder: $C run --rm --no-deps heimdall evidence restore -all
#   and then the project folders and release logs, which the mirror does not hold:
#   gunzip -c heimdall-projects-<time>.tar.gz | $C run --rm --no-deps -T --entrypoint tar heimdall -C /var/lib/heimdall -xf -
#   (database from the bucket instead of a local dump: evidence list, then evidence fetch-backup -name <file> -out <dir>)
$C up -d --wait
```

## 8. Upgrades

```sh
deploy/hosted/upgrade.sh                 # backup, git pull --ff-only, rebuild, migrate, restart
deploy/hosted/upgrade.sh --ref v1.2.0    # a specific tag or commit
deploy/hosted/upgrade.sh --drain-timeout 30m   # wait longer for runs in flight
```

Drain mode. After the new images are built and the migrations applied, the
script drains the running server: it stops handing out new leases, and the
script waits until no run is in flight (the `heimdall_leases_in_flight`
metric reaches 0) or `--drain-timeout` passes (default 10m). Only then are
the containers recreated, so the restart stops no run. Runs still in flight
at the timeout keep their leases and are re-attached by the new server
(durable leases, below). A failed upgrade turns drain mode off
again; `--no-drain` skips it. By hand, inside the server container:

```sh
docker compose exec heimdall bifrost-bench drain -config /srv/heimdall/deploy/bench.yaml -timeout 10m   # on, then wait
docker compose exec heimdall bifrost-bench drain -config /srv/heimdall/deploy/bench.yaml -status
docker compose exec heimdall bifrost-bench drain -config /srv/heimdall/deploy/bench.yaml -off           # leases again
```

The command reads the drain token the server writes at start to
`<runs root>/.drain.token` (mode 0600). A server admin can call the same
route, `GET` and `POST /api/bench/admin/drain` with `{"on": true}`. Drain
mode lives in the server process only: a restarted server always hands out
leases.

Durable leases. Runs still in flight at a restart (a drain timeout, a
crash) are not lost when the restart is shorter than the stale threshold (2
minutes): the server keeps every lease in the `remote_leases` table, and the
next process re-attaches it when the experiment resumes, so the run finishes
on its runner and is counted once (docs/remote-execution.md, Durable leases).

It takes a database backup first, tags the image IDs of the running server
and shared worker containers `:previous` (a service with no running container
keeps its existing `:previous`, so a retried upgrade never moves the rollback
tag onto a half-finished build; a stack with its own image tag,
`HEIMDALL_IMAGE=heimdall/server:<tag>`, gets `:<tag>-previous` instead, so
two stacks on one Docker host never move each other's rollback image),
rebuilds, applies migrations and recreates what changed, then waits for
health. Containers of services the compose file no longer has are removed
(the retired `demo-runner`); services of profiles not started on this host
are left alone. When the shared worker runs here, its profile is upgraded
too and `registry-sync.sh` refreshes the private registry. Rollback steps are in the script's header (`upgrade.sh --help`):
check out the previous commit, retag `:previous` as `:local`,
`up -d --no-build`. Migrations only add tables and columns, so the previous
version runs on the migrated database; if not, restore the dump. Upgrade
runners to the same commit (`deploy/runner/README.md`, Upgrade).

Each build also rebuilds the files the server offers at `/downloads` (the
command line tool, the Python and TypeScript SDKs, a source tarball) at the
checkout's commit (`docs/downloads.md`); CI jobs download the tool from there.

## 9. Shared worker (optional)

The shared worker lets every organization run the built-in agents and
benchmarks without connecting a worker of its own. It runs in compose
profile `shared-runner` on this machine: a worker container that starts
agent containers through the host's Docker socket, with its own Bifrost
gateway and fault proxy. It serves every organization whose shared pool is
on, which is every organization without its own quota row.

1. Set the secrets in `deploy/hosted/.env` (bootstrap generates both on a
   fresh install; add them with `openssl rand` on an older one):

| Variable | Where | What |
|---|---|---|
| `HEIMDALL_SHARED_RUNNER_SECRET` | server and `shared-runner` | Secret the shared worker fetches its assignments with (`GET /api/bench/admin/pool/assignments`, no tokens in the answer) and claims one runner token per org with (`POST /api/bench/admin/pool/assignments/{org}/token`; each claim revokes that org's earlier token and is audited in the org's log). Empty turns the shared pool off. Generate with `openssl rand -hex 32`. |
| `HEIMDALL_MASTER_KEY` | server | Base64 32-byte keystore key: provider keys are encrypted under it. `openssl rand -base64 32`. Losing it loses every stored key. Shared worker tokens are not stored readable: the server keeps only their hashes. |
| `HEIMDALL_GATEWAY_USAGE_SECRET` | server and `bifrost` | Shared secret for the heimdall Bifrost plugin's usage export: the plugin posts per-request usage rows to the server (`POST /api/bench/gateway/usage`, `Authorization: Bearer`). Only used with the opt-in custom Bifrost build: the default `deploy/bifrost/config.json` has no heimdall entry, since the stock `maximhq/bifrost` image has no heimdall plugin. `deploy/bifrost/config.plugin.json` enables the plugin and reads the secret as `env.HEIMDALL_GATEWAY_USAGE_SECRET`; `deploy/hosted/docker-compose.bifrost-plugin.yml` builds Bifrost from this repo (`deploy/bifrost/Dockerfile.plugin`) and mounts it (see "Custom Bifrost build with the Heimdall plugin" below). `bootstrap.sh` generates it when empty. `openssl rand -hex 32`. |
| `HEIMDALL_SERVER_ADMINS` | server | Comma-separated emails of server admins: quotas, image approval, kill switch. |
| `HEIMDALL_SHARED_RUNTIME` | `shared-runner` | `auto` (default): every container runs under gVisor (`--runtime runsc`), serving every org. `runsc`: gVisor, only orgs whose runtime is `runsc`. Without a `runsc` runtime on the Docker host the shared worker does not start (fail closed): install gVisor with `sudo sh deploy/hosted/install-gvisor.sh`. |
| `HEIMDALL_SHARED_ALLOW_RUNC` | `shared-runner` | Operator override, empty by default. `1` lets the shared worker start under runc when the host has no gVisor (or with `HEIMDALL_SHARED_RUNTIME=runc`); it logs a warning on every start and then serves only orgs a server admin set to runtime `runc`. New orgs default to gVisor (`runsc`) and wait for a worker that has it. |
| `HEIMDALL_BUILD_NETWORK` | `shared-runner` | The build network (fixed name, default `heimdall-buildnet`): the read-only registry, the shared worker and its image build sessions, nothing else. |
| `HEIMDALL_BUILDKIT_APPARMOR` | `shared-runner` | AppArmor profile of each build session's BuildKit container. Empty means unconfined. On hosts that restrict unprivileged user namespaces (Ubuntu 23.10 and later, `kernel.apparmor_restrict_unprivileged_userns=1`) rootless BuildKit cannot start and restarts in a loop; load the narrow `heimdall-buildkitd` profile once with `sudo sh deploy/hosted/install-buildkit-apparmor.sh`, set this to `heimdall-buildkitd` and run `upgrade.sh`. The profile grants user namespaces to that container only. |
| `HEIMDALL_SHARED_REGISTRY` | `shared-runner` | The private image registry org builds take their base images from (`registry:5000`, the read-only `registry` service of the profile; only that host is trusted over plain HTTP in `deploy/hosted/buildkitd.toml`). Empty leaves `FROM` lines alone, so `FROM bench/...` fails. |
| `HEIMDALL_REGISTRY_AUTH_DIR` | `registry`, `shared-runner` | Folder outside the checkout with the registry's credentials and digest manifest. Empty: `bootstrap.sh`, `upgrade.sh` or `registry-sync.sh` picks `/var/lib/heimdall-registry-auth` (as root) or `~/.config/heimdall/registry-auth`, generates the credentials there and writes the path to `.env`. Delete one identity's two files to rotate it. |
| `HEIMDALL_REGISTRY_DIR` | `registry` | Host folder for the registry's images, e.g. on the data disk. Empty: the `registry-data` Docker volume. |
| `HEIMDALL_SHARED_PLATFORM_IMAGES` | `shared-runner` | Comma-separated images every org may pull (a trailing `/` or `:` is a prefix). Otherwise an org pulls only its approved, digest-pinned images. Org Dockerfiles take those without a registry host (and `bench/...`) from the private registry. |
| `HEIMDALL_SHARED_GATEWAY_CONTAINER` | `shared-runner` | The gateway container every org network joins. Empty finds the compose project's `bifrost` service. |
| `HEIMDALL_GATEWAY_PUBLIC_URL`, `HEIMDALL_TOOLS_PUBLIC_URL` | server and `shared-runner` | Where deployed agents (HTTP, OpenAI-compatible, A2A) and cloud sandboxes (Modal, Daytona, E2B) reach the shared worker's model gateway and live runs' tool gates. Default `https://$HEIMDALL_DOMAIN/gw` and `https://$HEIMDALL_DOMAIN/rt`: Caddy forwards both to the worker's public ingress, which answers only for live runs, with the run's own key or token, rate limited per run (docs/remote-execution.md, "Published through the server"). Leave them at the default. |
| `HEIMDALL_RTGATE_NETWORK` | `shared-runner` | Internal network the worker reaches its runs' tool gates on (default `heimdall-rtgate`). |
| `HEIMDALL_SHARED_SLOTS` | `shared-runner` | Slots per organization, capped by its max concurrent runs. Default 4, the default max concurrent runs of an org. |
| `HEIMDALL_SHARED_ISSUEBENCH_CONCURRENCY` | `shared-runner` | From issues builds (job kind `issuebench.work`) at once on the shared worker, all organizations together. Default 1; a waiting job logs `waiting for a free build slot on this worker`. |
| `BENCH_ISSUEBENCH_EGRESS_ALLOW` | server and `shared-runner` | Extra hosts (comma separated) a From issues build sandbox may reach through its egress proxy, on top of `pypi.org`, `files.pythonhosted.org`, `deb.debian.org` and `security.debian.org`. Host names only, at most 32 hosts in all. |

2. Build the task and agent images the built-in benchmarks use, e.g.
   `docker build -t bench/coding-core-base:1 benchmarks/coding-core/base`.
3. `docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env --profile shared-runner up -d`.
4. `deploy/hosted/registry-sync.sh`: copies `bench/coding-core-base:1` and
   the `HEIMDALL_SHARED_PLATFORM_IMAGES` into the private registry (below).
   Run it again after rebuilding one of them; `upgrade.sh` runs it when the
   shared worker is up. With arguments it copies those images instead.

Each run uses the keys of the organization it belongs to, delivered for that
run only; the shared worker has no provider key of its own. The Docker
socket is root-equivalent, so the shared worker runs only built-in agent
templates and server-approved images: an organization's own Docker image
needs a server admin's approval (below) or a worker of its own.

Isolation between orgs on the shared host:

- Each org's agents run on their own internal network,
  `heimdall-evalnet-<org id>`, which only the gateway joins; one org's
  containers cannot reach another's.
- The shared worker mounts no server data. Each org's project (suites,
  agents, experiments, policies; never `.git`, `.heimdall/` state or the
  release log) is downloaded with that org's own runner token from
  `GET /api/bench/workers/project` before every lease.
- Agents run under gVisor (`runsc`). The shared worker does not start on a
  host without it unless the operator sets `HEIMDALL_SHARED_ALLOW_RUNC=1`
  (logged on every start), and then serves only orgs a server admin set to
  runtime `runc`. Install gVisor as root with
  `sudo sh deploy/hosted/install-gvisor.sh` (`--check` only reports); it
  registers runtime `runsc` with Docker, reloads Docker without restarting
  containers and runs a test container under it. The admin Organizations
  page shows each org's sandbox.
- Every org Dockerfile builds in a build session of its own: a fresh
  rootless BuildKit started for that build and removed afterwards (no Docker
  socket, no shared daemon, process table or cache with another org's
  build), one build at a time per org, with the org's own cache volume
  `heimdall-buildcache-<org id>`. BuildKit runs inside the network namespace
  of a small guard container (the runner image, `NET_ADMIN` only) whose
  egress rules (`deploy/hosted/build-egress.sh`) refuse cloud metadata
  (`169.254.169.254`, `168.63.129.16`), every private, link-local and
  reserved IPv4 range (other compose services, the Docker host, the VPC)
  and all IPv6 (`fd00:ec2::254`), and allow only the registry, the host's
  DNS servers and the public internet (package installs). A session killed
  before its cleanup is removed when the shared worker next starts.
- BuildKit cannot see images on the host daemon, so the bench's own images
  (`FROM bench/coding-core-base:1`) and the operator's platform images come
  from the `registry` service on the build network: read only (its storage
  is mounted read-only, deletes are off), pull credentials required, no host
  port, `registry:2` pinned by digest, storage in `HEIMDALL_REGISTRY_DIR` or
  the `registry-data` volume. Only `registry-sync.sh` writes: it starts
  `registry-writer` (same storage, sync identity, on an internal network
  nothing else joins) for the copy, removes it afterwards and records each
  pushed image's digest in the manifest (`HEIMDALL_REGISTRY_AUTH_DIR/runner/manifest`).
  The worker points each such `FROM` at the registry with a named build
  context pinned by that digest (`registry:5000/bench/coding-core-base:1@sha256:...`),
  so org Dockerfiles stay unchanged and a moved tag cannot change what a
  build starts from; an image missing from the manifest or the registry
  fails the build before it starts, naming the image and `registry-sync.sh`. Any other `FROM` must
  name an image BuildKit can pull itself. The built image is loaded into
  the host's Docker (`buildx --load`) for the run. Tags are
  namespaced per org (`org-<org id>/...`); an org cannot use or overwrite
  another org's images or a platform tag. After upgrading, remove images that
  orgs built under global tags before this change (`docker image ls`).
- Hosted CLI jobs (agent build and similar) do not run on the shared worker;
  they run on an org's own workers. The one exception is the job kind
  `issuebench.work` (From issues builds, below): the shared worker takes it
  and runs it in process, under the org's own Docker policy.
- Run one shared worker per runtime: a second one with the same secret claims
  the same orgs' tokens and revokes the first one's.

From issues builds on the shared worker (job kind `issuebench.work`, "Build
and test an issue on a worker"; the full flow is in
[tasks-from-your-repo.md](tasks-from-your-repo.md), "From issues on the
hosted site"):

- The server scans GitHub, runs the model checks and emits the tasks. Building
  and testing one issue is one runner job, leased only to a worker that takes
  that kind. The shared worker registers for `issuebench.work` only; an
  organization's own worker takes every kind.
- The job runs in the shared worker process, never as a `bifrost-bench`
  subprocess, so every Docker call goes through the org's policy: runtime
  `runsc`, labels `heimdall.org=<org id>`, images in `org-<org id>/...`.
- Each job gets its own internal network `heimdall-ib-<job>` with no route
  out, and an egress proxy container (`bench/egress:1`, mode
  `live-allowlisted`) that is the only way out, to the allowlist above. The
  shared worker builds `bench/egress:1` at start when it is missing, and
  removes leftover issuebench containers and networks of its own.
- Builds at once: `HEIMDALL_SHARED_ISSUEBENCH_CONCURRENCY` (default 1).
- gVisor and snapshots: the builder agent rolls its sandbox back with
  `docker commit`. With gVisor's default in-sandbox overlay a commit may miss
  files changed after start; the worker checks this once per runtime and, when
  commits drop changes, replays the builder's commands from the base image
  instead (logged once: `this worker's container runtime does not keep
  changes on docker commit; the builder replays commands instead`). Replay is
  correct but slower. To keep commits fast, register runsc with the flag
  `--overlay2=none` in `/etc/docker/daemon.json` (`"runtimeArgs":
  ["--overlay2=none"]`) and reload Docker; this is optional.
- The shared worker needs the `shared-runner` profile's Bifrost: the server
  mints each job's model key on that gateway. Without the profile, From issues
  builds can not run on the hosted site at all, and the readiness route says
  so.

Guardrails (server admin, `/api/bench/admin/...`):

- Per-org limits: `GET/PUT /admin/quotas/{org_id}` (concurrent runs, runs per
  day, spend per day, image size, shared pool on or off, custom images,
  runtime `runc` or `runsc`). The caps exist but are off by default: an
  organization without a row has the shared pool on and no run or spend cap.
  Set them per organization when you need them; once set, runs per day and
  spend per day are checked when a run starts and at every lease, over the
  org's sealed runs and their known spend that UTC day.
- Custom images: members request one with `POST /api/bench/images`. It must be
  pinned by digest (`name@sha256:<64 hex>`) and fit the org's image size
  limit. A server admin approves it with `POST /admin/images/{id}/approve`.
  It is approved automatically, with no admin, when the org's runs on the
  shared worker run under gVisor: the org's runtime is `runsc`, or a shared
  worker with `HEIMDALL_SHARED_RUNTIME=auto` (or `runsc`) on a host with
  gVisor asked for its assignments in the last 10 minutes. Such an image is
  never handed to a shared worker running `runc`. Automatic approval also
  turns custom images on for the org. Set `HEIMDALL_IMAGE_AUTO_APPROVE=off` on
  the server to require an admin for every image. `GET /api/bench/images`
  shows each request's `approval` (`automatic` or `admin`) and `next_step`;
  `GET /api/bench/pool` says which way this org's images are approved.
- Private registries: an org admin adds a login under Settings > Secrets
  (`POST /api/bench/registry-credentials`: Docker Hub, GHCR, ECR, GCR or
  Artifact Registry, ACR, or any other registry). The login names an org
  secret holding the password, token, ECR secret access key or Google service
  account JSON (user `_json_key`). The server resolves the logins for each
  lease (ECR: a short-lived token from `GetAuthorizationToken`, so the access
  key never leaves the server) and the worker uses one for a single
  `docker pull` of a missing agent or task image on that host, through a
  temporary Docker config it removes right after. The image must still be
  approved (or a platform image) on the shared worker; logins are never
  logged.
- Kill switch: `POST /admin/killswitch {"on": true}` empties the shared
  worker's assignments and stops every coordinator from handing out new
  leases; slots already running finish.

## 10. Operating it

```sh
C="docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env"
$C ps                                   # health of every service
$C logs -f --tail 100 heimdall caddy    # server log, access log (JSON, credentials masked)
$C exec heimdall wget -qO- http://127.0.0.1:8090/readyz   # database, migrations, runs root
curl -fsS https://heimdall.example.com/healthz             # public liveness, for uptime checks
$C exec heimdall bifrost-bench <command>                    # CLI against the live data
```

Metrics: set `HEIMDALL_METRICS_LISTEN=0.0.0.0:9090` in `.env` and the server
serves Prometheus metrics at `http://heimdall:9090/metrics` on the compose
network only (no port is published and the endpoint has no login, so never
publish it): `heimdall_http_request_duration_seconds` (API latency by method,
area and status class), `heimdall_job_queue_depth` and `heimdall_jobs` (by kind
and status; evaluation runs are `experiment.run`), `heimdall_leases_in_flight`,
`heimdall_lease_oldest_heartbeat_age_seconds`, `heimdall_workers` (by state),
`heimdall_workers_disk_low` and `heimdall_remote_runs_total` (uploaded runs
by result and outcome).

Monitoring (lane Z14): the `monitoring` compose profile adds Prometheus with
alert and SLO rules, a Grafana dashboard, an OpenTelemetry Collector and
Tempo for platform traces; `https://<domain>/status` is the public status
page with incident notes (`bifrost-bench status incident ...`). Setup,
metrics, SLOs, alert runbooks and the status page: docs/monitoring.md.

Runner disk: every runner (`runner start`, the shared runner) sweeps every 10
minutes: leftovers of crashed runs (the janitor), Heimdall task and agent
images (label `bifrost-bench=1`, tags under `bench/` or `org-<org id>/` only)
that no run used for 48 hours, never one a container uses, one listed in
`runner.disk.pinned_images`, or one any suite needs (task, base, service and
helper images of every suite, admitted or draft, under this checkout's
benchmarks, `HEIMDALL_LIBRARY_DIR` and each org project; when that set
cannot be read, nothing is removed). `runner.disk.image_gc` is `auto` by
default: on for a remote runner, off for a runner that shares its host with
a library (`HEIMDALL_LIBRARY_DIR` set); `on` or `off` force it, and the build cache of Heimdall's own buildx
builder (`runner.disk.builder` or `BUILDX_BUILDER`; with neither the shared
default builder is left alone). A run folder is deleted once the server
accepted its upload (`runner.disk.keep_run_folders` keeps it). Below
`runner.disk.min_free_gb` free (default 10, `HEIMDALL_RUNNER_MIN_FREE_GB`) the
runner takes no work and the server shows `disk` with `low: true` in its
workers list.

Provider concurrency: `HEIMDALL_PROVIDER_SLOTS` (default 32) is how many runs
may use one provider at once through the shared fault proxy, and per
organization key; the proxy's `/health` lists each provider's slots and live
claims under `providers`.

`/healthz` answers while the process serves. `/readyz` checks the database,
that migrations are current and that the runs root is writable; it is the
container health check and Caddy does not expose it. Container logs rotate
at 5 x 20 MB per service. The Caddy access log replaces invitation tokens
(`/invite/<token>`, `/api/bench/auth/invitations/<token>`) and the `code` and
`state` query values (the OAuth callback; the same names on other routes are
masked too) with `REDACTED`, and drops the `Referer` header.

`deploy/hosted/smoke.sh` brings the whole stack up as a separate compose
project on 127.0.0.1:47443 and :47080 with Caddy's internal CA for
`heimdall.localhost`, checks TLS, headers, redirect, body limit and internal
ports, fetches the UI, signs up and creates an organization through the API,
then removes everything. (`--demo-runner` is retired with the demo-runner
service and ignored: hosted installs have no demo.) `--s3` adds a local S3 endpoint (SeaweedFS, pinned by digest)
as the evidence mirror and checks that `backup.sh` uploads a database dump
and mirrors the demo experiments; it is also the way to try a mirror
configuration before pointing it at a real bucket. It uses its own compose
project (a new name per run, `heimdall-smoke-<time>-<pid>`; a `SMOKE_PROJECT`
that already has containers, volumes or networks is refused before anything
starts, so teardown only ever removes what that run created) and image tags
(`heimdall/server:smoke`), so it is safe beside production. Run it before
upgrading production.

### Settings reference

The settings of `deploy/hosted/.env` beyond the site, database and key store,
each with a one-line meaning (`.env.example` has the same list):

| Variable | Meaning |
|---|---|
| `HEIMDALL_SHARED_RUNNER_SECRET` | Secret of the shared worker (assignments and per-org token claims); empty turns the shared worker off. |
| `HEIMDALL_SHARED_RUNTIME` | Container runtime of the shared worker: `auto` or `runsc` (gVisor, required). |
| `HEIMDALL_SHARED_ALLOW_RUNC` | Operator override: `1` lets the shared worker run under runc without gVisor, for orgs set to runc only. Leave empty. |
| `HEIMDALL_SHARED_SLOTS` | Runs at once per organization on the shared worker, capped by its own limit (default 4). |
| `HEIMDALL_BUILD_NETWORK` | Network of the shared worker's image build sessions and read-only registry (default `heimdall-buildnet`). |
| `HEIMDALL_SHARED_REGISTRY` | The read-only private image registry (`registry:5000`) that org builds take bench and platform base images from, pinned by digest. |
| `HEIMDALL_REGISTRY_AUTH_DIR` | Folder outside the checkout with that registry's credentials and digest manifest; empty picks one and writes it here. |
| `HEIMDALL_LIBRARY_RUNS_ROOT` | Run store of the platform org whose reference runs give a new organization's first estimate its cost and time (docs/evaluations.md); empty uses each organization's own runs only. |
| `HEIMDALL_EVAL_BUDGET_MAX_USD` | Largest budget the evaluation wizard suggests (default 100 USD, lowered to an org's daily spend cap when one is set); a person can still type more. |
| `HEIMDALL_SHARED_HOST_SLOTS` | Runs at once on the shared worker's host, all organizations together, shared fairly between those with work; empty or 0 is one per 2 CPUs and 4 GB of memory. |
| `HEIMDALL_EXPERIMENT_LIMIT` | Evaluations running at once on the whole platform; empty or 0 is 64. |
| `HEIMDALL_EXPERIMENT_PER_ORG` | Evaluations running at once per organization; empty or 0 is 8. |
| `BENCH_ISSUEBENCH_PROVIDER`, `BENCH_ISSUEBENCH_MODEL` | Builder model for benchmarks made from GitHub issues (From issues) when the form names none; empty means the form must pick one. |
| `BENCH_ISSUEBENCH_MAX_USD` | Dollar cap of one build from issues; empty is 5 USD. |
| `BENCH_ISSUEBENCH_CHECK_MAX_USD` | Dollar cap of one model check job on a built benchmark; empty is 1 USD. |
| `BENCH_ISSUEBENCH_NETWORK` | Docker network of build from issues containers when the server builds them itself (not hosted); empty is Docker's default. On the hosted site builds run on a worker on a per-job internal network instead. |
| `BENCH_ISSUEBENCH_EGRESS_ALLOW` | Extra hosts (comma separated) a From issues build sandbox may reach through its egress proxy, on top of the Python package registries; empty adds none. |
| `HEIMDALL_SHARED_ISSUEBENCH_CONCURRENCY` | From issues builds at once on the shared worker; empty is 1. |
| `HEIMDALL_SHARED_BUILDKIT` | Address of the isolated BuildKit that builds images from org Dockerfiles. |
| `HEIMDALL_REGISTRY_DIR` | Host folder (on the data disk) for that registry's images; empty is a Docker volume. |
| `HEIMDALL_RUNNER_DIR` | Host folder of the shared worker's runs, releases and org projects; empty is `/var/lib/heimdall-shared-runner`. |
| `HEIMDALL_SHARED_PLATFORM_IMAGES` | Images every organization may pull, comma-separated. |
| `HEIMDALL_SHARED_GATEWAY_CONTAINER` | The model gateway container every org network joins; empty finds it by name. |
| `HEIMDALL_MODAL_PYTHON` | Python with the `modal` package, for organizations whose runs go to Modal (shared worker). |
| `HEIMDALL_SANDBOX_GPU` | Modal GPU type a shared runner declares for GPU tasks of organizations whose default worker is Modal (default `L4`; `docs/open-models.md`). |
| `HEIMDALL_PROVIDER_SLOTS` | Runs that may use one provider at once through the shared fault proxy and per organization key. |
| `HEIMDALL_RUNNER_MIN_FREE_GB` | Free disk a runner keeps; below it the runner takes no new work and reports disk low. |
| `HEIMDALL_METRICS_LISTEN` | Prometheus metrics address on the compose network; empty is off. Never publish it. |
| `HEIMDALL_SERVER_ADMINS` | Comma-separated emails of server admins: quotas, image approval, kill switch. An address counts only once verified (docs/auth.md, Server admins). |
| `HEIMDALL_SERVER_ADMIN_IDS` | Comma-separated user ids of server admins; `bifrost-bench admin grant -email X` does the same from the database. |
| `HEIMDALL_SIGNUP_DOMAINS` | Email domains that may sign up without an invitation under `HEIMDALL_SIGNUP=allowlist`. |
| `HEIMDALL_SMTP_URL` | Email through SMTP: `smtp://user:pass@host:587` (STARTTLS) or `smtps://user:pass@host:465`. Evaluation alerts, the weekly digest and account emails ([notifications.md](notifications.md)). |
| `HEIMDALL_RESEND_API_KEY` / `HEIMDALL_POSTMARK_TOKEN` | Email through Resend or Postmark instead of SMTP. The first transport set wins (SMTP, Resend, Postmark); none set writes emails to the server log only. |
| `HEIMDALL_MAIL_FROM` | From address of every email, e.g. `Heimdall <alerts@example.com>`; required with any transport. |
| `HEIMDALL_API_RATE_LIMITS` | API rate limits per token and per organization (`cred_burst,cred_per_sec,org_burst,org_per_sec`, or `off`); empty is the default. |
| `HEIMDALL_RUNNER_IMAGE` | Runner image Heimdall starts for an organization's own worker on Fly Machines or Kubernetes; empty is the published default. |
| `HEIMDALL_GATEWAY_LOGS_URL` | The shared gateway's log API the server lists, deletes and expires one organization's prompt logs through; empty with the shared worker on is `http://bifrost:8080`, `off` disables it. |
| `HEIMDALL_SECURITY_CONTACT` | Where security reports go (`mailto:` or `https:`), published at `/.well-known/security.txt`; empty points at the `/security` page. |
| `HEIMDALL_BILLING` | `on` enforces plan limits and sells Team through Stripe; empty meters and shows limits only (section 10, Billing). |
| `HEIMDALL_STRIPE_SECRET_KEY`, `HEIMDALL_STRIPE_WEBHOOK_SECRET`, `HEIMDALL_STRIPE_TEAM_PRICE` | Stripe secret key, webhook signing secret and Team price id; a live key needs `HEIMDALL_STRIPE_ALLOW_LIVE=1`. |
| `HEIMDALL_VOICE_RTC_URL`, `HEIMDALL_VOICE_RTC_SECRET`, `HEIMDALL_VOICE_SIP_ALLOW` | Real-time voice calls (compose profile `voice`): where runs reach the voice gateway, the secret per-run gateway tokens are signed with, and the E.164 prefixes Heimdall may dial. Empty URL: real-time calls are refused. Setup: docs/voice-realtime.md. |
| `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`, `LIVEKIT_PUBLIC_URL` | Heimdall's own LiveKit server (profile `voice`) and the URL teams' bots join. |
| `HEIMDALL_BACKUP_PASSPHRASE_FILE` | Passphrase file kept away from the backups; `backup.sh` then writes an encrypted copy of `.env` beside each dump. |
| `HEIMDALL_DOMAIN`, `HEIMDALL_ACME_EMAIL` | The public host name Caddy serves and gets a certificate for, and the ACME account address for expiry and revocation notices. |
| `HEIMDALL_PG_PASSWORD` | Password of the server's Postgres role, hex only (it goes inside the connection URL compose builds as `HEIMDALL_DATABASE_URL`). |
| `HEIMDALL_SIGNUP_PER_HOUR` | New self-serve accounts per hour across every address, also the burst; empty or 0 keeps the default (20 at once, then one per 3 minutes). Invitation sign-ups are not counted. |
| `HEIMDALL_CAPTCHA_SECRET`, `HEIMDALL_CAPTCHA_SITEKEY` | Optional hCaptcha on self-serve sign-up: the secret turns it on, the site key is what the sign-up page shows. Invitation sign-ups skip it. |
| `HEIMDALL_IMAGE_AUTO_APPROVE` | `off`: every image request waits for a server admin. Empty: images a gVisor shared worker can run are approved automatically (section 9). |
| `HEIMDALL_FAULT_MODE` | Where fault plans go, on the server and the shared worker: `proxy` (default, the fault proxy behind Bifrost) or `plugin` (headers to the heimdall Bifrost plugin, the proxy as fallback; needs the custom Bifrost build). |
| `HEIMDALL_PULL_SIGNING_KEY` | Signs run tokens and repository links of pull-mode agents (docs/pull-agents.md); the server and the shared worker share it. `bootstrap.sh` fills it when empty. |
| `HEIMDALL_BENCH_DIR` | Host path of this checkout's `bench` folder, which the shared worker mounts at the same path; the helper scripts fill it in. |
| `BIFROST_SETUP_TOKEN`, `BIFROST_PG_PASSWORD`, `BENCH_FAULTPROXY_TOKEN` | The shared worker's Bifrost stack: Bifrost's admin setup token, its Postgres password and the fault proxy's control token. Random values (`openssl rand -hex 24`). |
| `HEIMDALL_S3_BUCKET`, `HEIMDALL_S3_REGION`, `HEIMDALL_S3_ENDPOINT`, `HEIMDALL_S3_ACCESS_KEY`, `HEIMDALL_S3_SECRET_KEY` | Evidence mirror (section 7): empty bucket or keys, no mirror. Empty endpoint is AWS S3; MinIO, R2 and others take `https://host[:port]`. |
| `HEIMDALL_STATUS_PROBES` | Extra components on the public `/status` page: `id=url` pairs, comma separated. |
| `HEIMDALL_OTEL_ENDPOINT`, `HEIMDALL_OTEL_SAMPLER`, `HEIMDALL_GRAFANA_PASSWORD`, `HEIMDALL_GRAFANA_PORT`, `HEIMDALL_PROMETHEUS_RETENTION` | Monitoring profile (docs/monitoring.md): where traces go (empty: no traces) and how many are kept, Grafana's admin password and loopback port, Prometheus retention. |
| `HEIMDALL_LIBRARY_REGISTRY_TOKEN_DIR`, `HEIMDALL_LIBRARY_REGISTRY_ACCESS`, `HEIMDALL_LIBRARY_REGISTRY_DIR`, `HEIMDALL_LIBRARY_REGISTRY_PUBLISH` | Library registry override (docs/library-registry.md, "Library registry" below): token key folder, per-org access file, image folder and published address. |
| `HEIMDALL_VOICE_RTC_PUBLISH`, `HEIMDALL_VOICE_MAX_CALLS` | Voice gateway (profile `voice`): the host address its port is published on, where runs reach it (default `172.17.0.1:8790`) and calls at once (default 20). |

### Billing

Plans and billing (lane Y16) are off by default. Off, Heimdall still meters
each organization's shared worker minutes (the time its leases hold a slot
on the shared worker), runs, evaluations and storage per UTC day, and
Settings, Billing and Settings, Usage show them against the plan's limits;
nothing is enforced or charged.

| Plan | Seats | Shared worker minutes a month | Includes |
|---|---|---|---|
| Free | 3 | 300 | library benchmarks with the org's own key |
| Team, $39 per seat a month | 50 | 3000 | private benchmarks, the CI gate, scheduled checks and alerts |
| Enterprise (contact) | no limit | no limit | everything, own worker support, invoice billing |

To turn it on, in Stripe test mode first:

1. In the Stripe dashboard (test mode), create a product "Heimdall Team" with
   a recurring monthly price of $39 per unit; copy its `price_...` id.
2. Add a webhook endpoint `https://<domain>/api/bench/billing/webhook` for
   `checkout.session.completed`, `customer.subscription.created`,
   `customer.subscription.updated` and `customer.subscription.deleted`; copy
   its signing secret (`whsec_...`).
3. Turn on the customer portal (Settings, Billing, Customer portal).
4. In `deploy/hosted/.env`: `HEIMDALL_BILLING=on`,
   `HEIMDALL_STRIPE_SECRET_KEY=sk_test_...`,
   `HEIMDALL_STRIPE_WEBHOOK_SECRET=whsec_...`,
   `HEIMDALL_STRIPE_TEAM_PRICE=price_...`, optionally
   `HEIMDALL_SALES_CONTACT`. Then `upgrade.sh --no-pull`.
5. Live mode later: the live key, price and webhook secret, plus
   `HEIMDALL_STRIPE_ALLOW_LIVE=1`.

A server admin sets an organization's plan by hand (Enterprise contracts)
with `PUT /api/bench/admin/billing/{org_id}` and `{"plan":"enterprise"}`.
Paid seats follow the member count every hour (and on "Sync seats"),
prorated. `GET /api/public/pricing` serves the plans to the pricing page
without sign-in.
**Queue and hot paths.** Waiting evaluations take organizations in turn:
the next one to start belongs to the organization with the fewest running,
then the highest priority (an admin can move a queued job with
`POST /api/bench/jobs/{id}/priority`, -10 to 10), then the oldest. The shared
worker's host limit is shared the same way. While runs finish, the server
updates only the changed part of its index and checks only the new results
on their way to the evidence mirror; every result is checked again in full
when the evaluation ends, at every mirror sweep (`storage.sweep_interval_m`)
and by `bifrost-bench evidence sweep` in the nightly runbook.

`HEIMDALL_DEV` turns on the practice suite and examples for tests and
development; a production install never sets it.

## 11. Hardening checklist

- [ ] SSH: keys only (`PasswordAuthentication no`, `PermitRootLogin no` in
      `/etc/ssh/sshd_config`), from known addresses if possible.
- [ ] Automatic security updates: `sudo apt-get install -y unattended-upgrades && sudo dpkg-reconfigure -plow unattended-upgrades`.
- [ ] Optional: `fail2ban` for SSH. The server rate-limits logins per IP and
      per email itself.
- [ ] Firewall: only 22, 80, 443 inbound (section 2). Postgres is never
      published; do not add a `ports:` entry to it.
- [ ] `deploy/hosted/.env` is mode 600 and not committed; backups of it are
      as secret as it is.
- [ ] Signup is `invite`, `allowlist` or `closed` unless you mean to run an
      open service. Check the live value: `curl -s https://$HEIMDALL_DOMAIN/api/bench/auth/config`
      (a server admin's override from Admin, Sign-up wins over `.env`).
- [ ] Server admins are bound by user id: `bifrost-bench admin list` on the
      server shows each one and how it became admin.
- [ ] Rotate secrets: the Postgres password (`ALTER USER heimdall PASSWORD
      '<new>'` in `$C exec postgres psql -U heimdall`, then `.env`, then
      `upgrade.sh --no-pull`), the GitHub client secret (on GitHub, then
      `.env`), API and runner tokens (revoke and recreate in Settings).
      Sessions expire after `session_ttl_h`; `bifrost-bench user
      set-password` (password recovery) ends every session of that user.
- [ ] Backups scheduled, copied off the machine, and a restore tried once.
- [ ] No provider keys in this `.env`: organizations bring their own, and
      runs never fall back to a server key.
- [ ] `HEIMDALL_MASTER_KEY` kept in a secret manager, not beside the backups.
- [ ] Upgrade regularly; base images are pinned by digest and move with the
      repository.

Containers run as non-root where the image allows (server and runner as uid
10001), with read-only root filesystems, all capabilities dropped (Caddy
keeps `NET_BIND_SERVICE`) and `no-new-privileges`. Caddy sets HSTS
(one year), `nosniff`, `no-referrer` and a restrictive `Permissions-Policy`,
caps request bodies at 2 MiB and hides `/readyz`; the server adds its CSP,
`X-Frame-Options: DENY` and same-origin policies on every answer.

### Library registry (optional)

Library images can be pushed once to a registry and pinned by digest in
each benchmark's lock, so every worker pulls the same bytes instead of
building them; orgs pull with their own API or runner token, restricted
benchmarks only by the orgs the access file names. Setup, the Caddy route
and the sync and prefetch commands: `docs/library-registry.md`.

## 12. Not yet supported

- SSO / SAML / OIDC other than GitHub OAuth.
- Two-factor authentication.
- Choice of data residency or region per organization: everything lives on
  this machine (and the S3 mirror you configure).
- High availability: one server process and one Postgres; scale up, not out.
- Email delivery needs a transport: until `HEIMDALL_SMTP_URL` (or a Resend
  or Postmark key) and `HEIMDALL_MAIL_FROM` are set, emails (alerts, the
  weekly digest, email verification links) are only written to the server
  log ([notifications.md](notifications.md), docs/auth.md). Invitations are
  links an admin copies and sends.
- Master key rotation or a KMS: `HEIMDALL_MASTER_KEY` is one static key.

## Custom Bifrost build with the Heimdall plugin

The default stack runs the stock `maximhq/bifrost` image with `deploy/bifrost/config.json`, which has no heimdall plugin entry. To run the heimdall plugin (faults inside the gateway, run tags, usage export) opt in with the compose override:

```sh
cd bench/deploy/hosted
docker compose -f docker-compose.yml -f docker-compose.bifrost-plugin.yml --profile shared-runner up -d --build bifrost
```

The override builds Bifrost from this repo (`deploy/bifrost/Dockerfile.plugin`, a Go workspace build over the local core, framework, transports and plugin modules, since this fork's transports needs packages that are not published; context the repo root, plugins/heimdall compiled in) and mounts `deploy/bifrost/config.plugin.json` in place of `config.json`: the same config plus the `heimdall` plugin enabled, `allow_inline_plans` on and usage rows posted to `http://heimdall:8090` with `HEIMDALL_GATEWAY_USAGE_SECRET` (set on both the server and `bifrost`). Without the override nothing about the plugin is loaded. The full flow against a real bifrost-http build (`deploy/bifrost/e2e/run.sh` with `HTTP=1`) has not been run against the hosted stack yet.
