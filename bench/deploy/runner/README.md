# Heimdall runner: install on a fresh box

A runner is `bifrost-bench runner start` on a machine you control. It leases
slots from the hosted Heimdall server over HTTPS, plans each experiment again
with its own copy of the project (suites, agents, `deploy/bench.yaml` at the
server's commit) and refuses any slot whose plan differs, runs the agent
containers in its local Docker through a local Bifrost gateway stack, and
uploads the sealed run folders, which the server verifies on arrival
(`docs/remote-execution.md`). `install.sh` sets one up on Ubuntu or Debian as
a systemd service.

## Before you start

- Ubuntu or Debian (others only with `--force`), amd64 or arm64, systemd.
- Docker Engine with the compose plugin. The script does not install Docker:
  `curl -fsSL https://get.docker.com -o get-docker.sh && sudo sh get-docker.sh`.
- `curl`, `openssl`, `tar` (and `git` only for a git install):
  `sudo apt-get install -y git curl openssl ca-certificates`.
- A runner token. An org admin creates it in Settings > Runners (or the admin
  API). It looks like `hmr_<8 characters>_<secret>`, is scoped to one org and
  one pool, and is shown once.
- The commit the hosted server runs (`--ref`). The script your server serves
  at `/api/bench/runners/install.sh` already carries it. A runner on another
  commit refuses every slot, since its own plan does not match the server's.

## Install

```sh
curl -fsSLo install.sh https://heimdall.example.com/api/bench/runners/install.sh
sudo bash install.sh --dry-run --server https://heimdall.example.com --token-file ~/.heimdall-runner-token   # preview
sudo bash install.sh --server https://heimdall.example.com --token-file ~/.heimdall-runner-token
```

By default everything comes from the Heimdall server itself, at the commit
it runs: the project bundle `GET /downloads/bench-<commit>.tar.gz` and the
CLI binary `GET /downloads/bifrost-bench-linux-<amd64|arm64>`, each checked
against the `.sha256` file next to it before anything is installed. No git
repository and no Go toolchain are needed. If the server has no download for
its commit the script stops and says so.

To clone a repository and build from source instead (the way older installs
worked), name the repository: `--repo https://github.com/<owner>/<repo>.git
--ref <the server's commit>` (or `--source git`).

Pass the token with `--token-file PATH` or `--token-stdin` (pasted at a
hidden prompt, or piped). There is no flag that takes the token itself. With
`--token-stdin`, download the script first: `curl ... | sudo bash` would
leave no stdin for the token. Delete your copy of the token file afterwards.

What it does, in order: checks the OS, root, Docker, `docker compose` and the
tools; creates the system user (in group `docker`); downloads and checks
the server's project bundle into `<prefix>/src/bench` and its binary into
`bin/bifrost-bench` (in git mode: clones `--repo` at `--ref` into
`<prefix>/src` and builds `bin/bifrost-bench` with
`CGO_ENABLED=0 GOWORK=off go build`, installing Go into `<prefix>/go` only
when no new enough Go is found; the checksum is verified), or downloads the
binary with `--release-url`; creates `deploy/.env` with random gateway tokens like
`make env`; writes the token to `/etc/heimdall-runner/token`; writes
`/etc/systemd/system/heimdall-runner.service`; builds the images of the
Makefile's `images` target (not the web UI, which a runner does not need);
starts the gateway stack with
`docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --wait`;
enables and starts the service; then runs `runner status` as the runner user.

`--dry-run` prints every command with a `[dry-run]` prefix and changes
nothing; it needs neither root nor Docker. Every value is validated (pool and
name `^[A-Za-z0-9._-]{1,64}$`, slots 1 to 64) and nothing from the command
line is evaluated.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--server URL` | required | Hosted server, `https://host[:port][/path]`. Plain `http` only for `127.0.0.1` or `localhost` (an SSH tunnel). |
| `--token-file PATH` / `--token-stdin` | | The runner token. On an upgrade both may be left out: the installed token is kept. |
| `--pool NAME` | `default` | Runner pool; experiments with `runner: {pool: NAME}` run here. |
| `--slots N` | `1` | Slots run at once, 1 to 64. |
| `--name NAME` | host name | Runner name shown in the server's runner list. |
| `--labels a,b` | none | Comma-separated labels. |
| `--spend-cap-usd X` | none | Local spend cap for this runner. |
| `--debug` | off | Runner debug logging. |
| `--source server\|git` | `server` | `server`: the project bundle and binary from the server's `/downloads` at its commit. `git`: clone `--repo` and build from source. |
| `--ref REF` | the server's commit | Commit to install; must be the server's. In git mode also a branch or tag (default there `main`). |
| `--repo URL` | `https://github.com/maximhq/bifrost.git` | Repository holding `bench/`; passing it means `--source git`. Pass the repository the hosted server was built from. A private repository needs an SSH deploy key in `<prefix>/home/.ssh`. |
| `--release-url URL` | build from source | Prebuilt tarball containing `bin/bifrost-bench`; `{arch}` becomes `amd64` or `arm64`, e.g. `https://github.com/<owner>/<repo>/releases/download/<version>/heimdall-runner-linux-{arch}.tar.gz`. Use the release that matches `--ref`. |
| `--sha256 HEX` | none | Expected sha256 of that tarball. Without it the download is checked by TLS only. |
| `--prefix DIR` | `/opt/heimdall-runner` | Install root: `src/` (the project), `home/` (the user's home, Go caches), `go/` (Go, when installed). |
| `--user NAME` | `heimdall-runner` | System user the service runs as. |
| `--skip-images` | off | Do not build the task, bench and agent images. |
| `--skip-stack` | off | Do not start the gateway stack. |
| `--force` | off | Continue on an OS that is not Debian or Ubuntu. |
| `--dry-run` | off | Print the commands instead of running them. |

`HEIMDALL_GO_VERSION` in the environment overrides the pinned Go version used
when Go has to be installed.

## Provider keys

Provider keys go only in `<prefix>/src/bench/deploy/.env` on the runner
(`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` and the others listed
in `deploy/.env.example`), never on the hosted server. The fault proxy
container swaps them in for the slot placeholders, so agents, Bifrost and the
hosted server never see them; the runner process also reads `.env` (for the
gateway tokens and for providers run in direct mode). An empty key means the
proxy's scripted model (Anthropic, OpenAI) or a 503 for other providers. The
file is mode 0600, owned by the runner user. After editing it:

```sh
cd /opt/heimdall-runner/src/bench
sudo docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --wait
sudo systemctl restart heimdall-runner
```

## Upgrade

Run the installer again with the same flags (the token may be left out). A
copy downloaded from the server again installs the commit the server runs
now; with `--ref`, pass the server's new commit. It stops the service,
downloads the new bundle and binary (in git mode: fetches and checks out the
ref and rebuilds the binary), carries `deploy/.env`, `bin/`, `runs/`,
`releases/` and `.heimdall/` over to the new tree, rebuilds the images, brings the stack up again, rewrites
the unit and restarts the service. `deploy/.env` is kept as it is; the script
warns when `.env.example` gained keys that `.env` lacks. Slots that were
running when the service stopped are lost and go back to the server's queue
after the stale threshold.

## Uninstall

```sh
sudo systemctl disable --now heimdall-runner
sudo rm /etc/systemd/system/heimdall-runner.service && sudo systemctl daemon-reload
cd /opt/heimdall-runner/src/bench && sudo docker compose -f deploy/docker-compose.yml --env-file deploy/.env down -v
sudo rm -rf /etc/heimdall-runner /opt/heimdall-runner     # also deletes local run folders under src/bench/runs
sudo userdel heimdall-runner
```

Then revoke the token in Settings > Runners. The images (`bench/*`) stay
until `docker image rm`.

## Troubleshooting

Start with `runner status`; it checks that the server is reachable and the
token accepted, and prints the org, the pool and this runner's state:

```sh
cd /opt/heimdall-runner/src/bench
sudo -u heimdall-runner bin/bifrost-bench runner status -server https://heimdall.example.com -token-file /etc/heimdall-runner/token -name <name>
```

| Symptom | Check |
|---|---|
| Server not reachable | DNS, firewall, `curl -I https://heimdall.example.com`; a TLS error means a wrong host name or certificate. |
| Token refused (401) | Revoked, mistyped, or another org's token. Create a new one and re-run the installer with `--token-file`. |
| "token file is readable by others" | `sudo chmod 600 /etc/heimdall-runner/token && sudo chown heimdall-runner: /etc/heimdall-runner/token`. |
| Online but never gets work | The experiment's `runner.pool` is another pool, or the runner refused it: see the refusal reason in the server's runner list and in `journalctl -u heimdall-runner`. A version or manifest mismatch means `--ref` differs from the server's. |
| Runs end `aborted_infra` | `sudo docker compose -f deploy/docker-compose.yml --env-file deploy/.env ps`; missing images (`sudo docker images 'bench/*'`). |
| Service restarting | `sudo journalctl -u heimdall-runner -n 100`; `systemctl status heimdall-runner`. |

The installer exits 2 when everything was installed but the final
`runner status` failed; the service keeps retrying every 10 seconds.

## Security notes

- The token is stored only in `/etc/heimdall-runner/token` (directory 0700,
  file 0600, owned by the runner user); it is never in the unit file, in a
  command line or in the installer's output.
- Group `docker` is equivalent to root on this box: give the runner a machine
  of its own and admit only machines you control (a runner can upload any
  folder it seals; the server checks the seal, not how the agent ran).
- The unit runs with `NoNewPrivileges`, `ProtectSystem=full`, `PrivateTmp`
  and kernel protections, which keep the Docker socket usable.
