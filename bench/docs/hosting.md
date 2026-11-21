# Hosting Heimdall

From a fresh Linux VM to a working Heimdall at `https://<your domain>`: teams
sign in, belong to organizations and run benchmarks on runners they own; the
hosted server stores sealed results, renders the UI and coordinates. Design:
`docs/platform-design.md`. Files: `deploy/hosted/`.

```
browser / runner --HTTPS--> Caddy (80, 443) --> heimdall:8090 --> Postgres
                            only published       server, UI        internal network
                            service              embedded          not published
```

The server runs no agent: runners do, on machines you control, with provider
keys that never reach the server. The only exception is the optional demo
runner, which serves the public demo organization under a spend cap.

## 1. Machine

| | Minimum | Comfortable |
|---|---|---|
| CPU, RAM | 2 vCPU, 2 GB | 4 vCPU, 8 GB |
| Disk | 20 GB | 100 GB+ SSD; sealed runs grow by roughly 2 to 3 MB per run (318 runs took 745 MB) |
| OS | Any 64-bit Linux with Docker Engine 24+ and the Compose plugin v2.20+ (tested: Docker 29.9, Compose 5.6) | Ubuntu 24.04 LTS or Debian 12 |

The demo runner (section 9) runs agent containers on the same machine: add
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
   Postgres and gateway secrets, your domain and ACME e-mail. Review the file:
   signup policy (`HEIMDALL_SIGNUP`: `invite` by default, `open`, `closed`),
   GitHub, S3 mirror.
2. builds the server image from this checkout (`deploy/hosted/Dockerfile.server`:
   `npm ci` and the UI build, then a static Go binary with the UI embedded, on
   a non-root Alpine runtime; about 5 minutes the first time)
3. starts Postgres and applies the database migrations (`bifrost-bench db migrate`)
4. creates the first owner with `bifrost-bench user create` and prints the
   generated password once. Sign in and change it under Settings, Account
   (`POST /api/bench/auth/password`). The change ends every other session of
   that user; the session that made it stays signed in.
5. creates the public read-only `demo` organization and imports sealed runs
   into it with `bifrost-bench org import` (seals are verified on the way
   in). Default source: `testdata/recorded` from the checkout. For the full
   set, copy a runs folder to the VM and pass `--demo-runs /path/to/runs`;
   `--no-demo` skips the demo.
6. starts Caddy, the server and Postgres, waits until all are healthy

Create the owner with `--owner-email` (it runs `bifrost-bench user create`,
password on stdin, before the site takes traffic). Without it, the first
account to sign up on the empty install becomes the owner, with signup
`invite` or `open` alike: whoever reaches the server first. With `closed`, no
web signup works at all and accounts come only from `user create`. Later
signups follow the policy (`invite`: only with an invitation link).

The demo organization gets the `--owner-email` account as its owner. A demo
created by a run without `--owner-email` has no admin, and nobody can create
its runner token. To repair it, run bootstrap again with `--owner-email`:
when the demo already exists it adds that account as an owner
(`bifrost-bench org member add -org demo -email <email> -role owner`, a no-op
for an existing member). The same command run by hand, `$C run --rm
--no-deps heimdall org member add -org demo -email you@example.com -role
owner`, repairs any organization left without an admin.

Open `https://heimdall.example.com/`, sign in (your organization was created
with the account), invite your team (Settings > Members).

Each organization has a project folder in the data volume,
`/var/lib/heimdall/runs/projects/<org id>/` (suites, agent configs,
experiments, policies), next to its sealed runs in `runs/orgs/<org id>/`. An
organization created in the UI or with `org create` starts with the
`coding-core` suite. There is no upload for agent configs yet; copy one in
through the container:

```sh
docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env exec -T heimdall \
    sh -c 'cat > /var/lib/heimdall/runs/projects/<org id>/agents/my-agent.yaml' < agents/my-agent.yaml
```

The runners need the same file at the same path in their checkout, since
they plan every experiment again and refuse one that comes out different.

Configuration lives in
`deploy/hosted/bench.hosted.yaml` (mounted read-only; secrets are only ever
named by environment variable) and `deploy/hosted/.env`. Site-specific compose
changes go in `deploy/hosted/docker-compose.override.yml`, which the scripts
pick up.

## 6. Runners

A runner is a machine you control that runs the agent containers and
uploads sealed runs. An org admin creates a runner token (`hmr_...`, one org,
one pool, shown once) in Settings > Runners, which also shows the install
command. On the runner machine (Ubuntu or Debian, Docker installed):

```sh
curl -fsSLO https://heimdall.example.com/api/bench/runners/install.sh
sudo bash install.sh --server https://heimdall.example.com --token-file ./runner-token \
    --ref <the server's commit or tag> --repo <repository URL> --pool default --slots 2
```

It installs `bifrost-bench runner start` as the `heimdall-runner` systemd
service with its own Bifrost gateway stack. Provider keys go only in that
machine's `deploy/.env`. Details, flags, upgrades and troubleshooting:
`deploy/runner/README.md`.

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
```

It takes a database backup first, tags the image IDs of the running server
and demo-runner containers `:previous` (a service with no running container
keeps its existing `:previous`, so a retried upgrade never moves the rollback
tag onto a half-finished build; a stack with its own image tag,
`HEIMDALL_IMAGE=heimdall/server:<tag>`, gets `:<tag>-previous` instead, so
two stacks on one Docker host never move each other's rollback image),
rebuilds, applies migrations and recreates what changed, then waits for
health. Rollback steps are in the script's header (`upgrade.sh --help`):
check out the previous commit, retag `:previous` as `:local`,
`up -d --no-build`. Migrations only add tables and columns, so the previous
version runs on the migrated database; if not, restore the dump. Upgrade
runners to the same commit (`deploy/runner/README.md`, Upgrade).

## 9. Demo runner (optional)

Runs the demo organization's experiments on this machine, in compose profile
`demo-runner`: its own Bifrost gateway, fault proxy and Postgres, and a
runner container that starts agent containers through the host's Docker
socket (which is root-equivalent: keep it to the demo org).

1. Sign in as the owner, open the `demo` organization, Settings > Runners,
   create a token for pool `demo`. Put it in `.env` as
   `HEIMDALL_DEMO_RUNNER_TOKEN`; set `HEIMDALL_DEMO_SPEND_CAP_USD` (default
   5, per UTC day) and, for a real model, a spend-limited `OPENAI_API_KEY` or
   `ANTHROPIC_API_KEY` (empty runs the scripted model at no cost).
2. Build the task and agent images the demo experiments use, e.g.
   `docker build -t bench/coding-core-base:1 benchmarks/coding-core/base` and
   `docker build -t bench/agent-custom-python:1 agents/images/custom-python`.
3. `sudo install -d -o 10001 -g 10001 -m 750 /var/lib/heimdall-runner`, then
   `deploy/hosted/bootstrap.sh --demo-runner`.

The checkout and `/var/lib/heimdall-runner` are mounted at their host paths,
so paths the runner hands to `docker run -v` mean the same on the host.

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
then removes everything. `--demo-runner` also starts the demo-runner profile
with a runner token created through the API, waits until the server lists
the runner, and runs a two-slot smoke experiment (toy agent, scripted model,
no spend) on it end to end: lease, run, upload, seal, analysis, the verdict
read over HTTPS (with the runner-side `gateway_hygiene` and
`gateway_versions` checks), then a regrade job. It fails unless both slots
finish with none failed, the verdict decision is not `invalid`, every named
instrument is `pass`, `warn` or `not_applicable`, and the regrade yields a
nonempty id whose verdict records it as `grade_source`. It needs the
`bench/coding-core-base:1` image on the machine. `--s3` adds a local S3 endpoint (SeaweedFS, pinned by digest)
as the evidence mirror and checks that `backup.sh` uploads a database dump
and mirrors the demo experiments; it is also the way to try a mirror
configuration before pointing it at a real bucket. It uses its own compose
project (a new name per run, `heimdall-smoke-<time>-<pid>`; a `SMOKE_PROJECT`
that already has containers, volumes or networks is refused before anything
starts, so teardown only ever removes what that run created) and image tags
(`heimdall/server:smoke`), so it is safe beside production. Run it before
upgrading production.

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
- [ ] Signup is `invite` or `closed` unless you mean to run an open service.
- [ ] Rotate secrets: the Postgres password (`ALTER USER heimdall PASSWORD
      '<new>'` in `$C exec postgres psql -U heimdall`, then `.env`, then
      `upgrade.sh --no-pull`), the GitHub client secret (on GitHub, then
      `.env`), API and runner tokens (revoke and recreate in Settings).
      Sessions expire after `session_ttl_h`; `bifrost-bench user
      set-password` (password recovery) ends every session of that user.
- [ ] Backups scheduled, copied off the machine, and a restore tried once.
- [ ] Provider keys only on runners, never in this `.env` (the demo runner's
      optional key excepted, spend-limited).
- [ ] Upgrade regularly; base images are pinned by digest and move with the
      repository.

Containers run as non-root where the image allows (server and runner as uid
10001), with read-only root filesystems, all capabilities dropped (Caddy
keeps `NET_BIND_SERVICE`) and `no-new-privileges`. Caddy sets HSTS
(one year), `nosniff`, `no-referrer` and a restrictive `Permissions-Policy`,
caps request bodies at 2 MiB and hides `/readyz`; the server adds its CSP,
`X-Frame-Options: DENY` and same-origin policies on every answer.

## 12. Not yet supported

- SSO / SAML / OIDC other than GitHub OAuth.
- Two-factor authentication.
- Choice of data residency or region per organization: everything lives on
  this machine (and the S3 mirror you configure).
- High availability: one server process and one Postgres; scale up, not out.
- Email delivery: invitations are links an admin copies and sends.

## Shared runner pool and guardrails

New organizations can run the built-in agents and suites without bringing
their own runner. The shared runner replans every experiment with the
server's checkout, so it runs only what the built-in templates reproduce.

| Variable | Where | What |
|---|---|---|
| `HEIMDALL_SHARED_RUNNER_SECRET` | server and `shared-runner` | Secret the shared runner fetches its assignments with (`GET /api/bench/admin/pool/assignments`). Empty turns the shared pool off. Generate with `openssl rand -hex 32`. |
| `HEIMDALL_MASTER_KEY` | server | Base64 32-byte key. The per-org shared pool runner tokens are kept in the keystore under it. |
| `HEIMDALL_SERVER_ADMINS` | server | Comma-separated emails of server admins: quotas, image approval, kill switch. |
| `HEIMDALL_SHARED_RUNTIME` | `shared-runner` | `runc` (default) or `runsc`. One shared runner serves one runtime; a `runsc` runner needs a Docker daemon whose default runtime is gVisor (set `DOCKER_HOST`). |
| `HEIMDALL_SHARED_SLOTS` | `shared-runner` | Slots per organization, capped by its max concurrent runs. |

Start it with `docker compose --profile shared-runner up -d`.

Guardrails (server admin, `/api/bench/admin/...`):

- Per-org limits: `GET/PUT /admin/quotas/{org_id}` (concurrent runs, runs per
  day, spend per day, image size, shared pool on or off, custom images,
  runtime `runc` or `runsc`). With the shared runner secret set, the shared
  pool is on for orgs that have no row.
- Custom images: members request one with `POST /api/bench/images`. It must be
  pinned by digest (`name@sha256:<64 hex>`) and fit the org's image size
  limit. A server admin approves it with `POST /admin/images/{id}/approve`.
- Kill switch: `POST /admin/killswitch {"on": true}` empties the shared
  runner's assignments and stops every coordinator from handing out new
  leases; slots already running finish.
