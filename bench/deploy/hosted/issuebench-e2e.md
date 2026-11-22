# From issues: real end to end run (`issuebench-e2e.sh`)

`deploy/hosted/issuebench-e2e.sh` runs From issues for real on a hosted-like
stack on this machine and prints `PASS` or `FAIL <reason>` (exit 0 or 1).
It costs real money on your provider key, so run it on purpose only.

## What it does

1. Starts the hosted stack with the `shared-runner` profile as its own
   compose project (`heimdall-ibe2e-<time>-<pid>`) on loopback ports 47543
   (HTTPS) and 47180 (HTTP), with Caddy's internal CA for
   `heimdall.localhost`. Images are tagged `heimdall/server:ib-e2e` and
   `heimdall/runner:ib-e2e`, so production tags are never replaced.
2. Signs up the owner, creates an organization and a one day API token.
3. Makes the owner a server admin (`bifrost-bench admin grant`), turns the
   shared pool on for the organization (`PUT /api/bench/admin/quotas/{id}`,
   runtime `runc` when `HEIMDALL_SHARED_ALLOW_RUNC=1`) and adds the provider
   key (`POST /api/bench/models/keys`).
4. Waits up to 10 minutes for `GET /api/bench/issuebench/readiness` to show
   `worker_online` and `gateway`. `--check-only` stops here.
5. Scans the repository (`POST /api/bench/issuebench/runs`), starts the run
   with consent and the builder model, and prints the run log and each
   issue's `status`, `stage` and `job_id` as they change.
6. Accepts every `ready` issue, waits for the benchmark, then follows the
   `suite.lint`, `suite.lock` and `suite.admit` jobs of that suite.
7. Prints a table (issue, status, cost, reason) and the verdict. `PASS`
   needs at least one emitted task whose `task.yaml` says `review: pending`
   and a `suite.admit` job that succeeded.

Everything is removed at the end (volumes included, plus containers and
networks labelled with the run's organization), also on failure, unless
`--keep` is given. A project name that already has containers, volumes or
networks is refused.

## Run it

Readiness only (no model calls, no key needed):

```sh
HEIMDALL_SHARED_ALLOW_RUNC=1 deploy/hosted/issuebench-e2e.sh --check-only
```

Full run:

```sh
export ISSUEBENCH_PROVIDER=anthropic ISSUEBENCH_MODEL=<model id>
export ISSUEBENCH_PROVIDER_KEY=...   # read from your secrets manager, not typed into history
export GITHUB_TOKEN=...              # any token that can read public repositories
deploy/hosted/issuebench-e2e.sh
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `ISSUEBENCH_PROVIDER`, `ISSUEBENCH_MODEL` | none | Builder model; required unless `--check-only` |
| `ISSUEBENCH_PROVIDER_KEY` | none | Key for that provider, added to the org on the models route |
| `GITHUB_TOKEN` | none | Token for the scan |
| `ISSUEBENCH_REPO` | `marshmallow-code/marshmallow` | Repository to scan |
| `ISSUEBENCH_LIMIT` | `3` | Issues to scan |
| `ISSUEBENCH_MAX_USD` | `3` | Dollar cap per build (`BENCH_ISSUEBENCH_MAX_USD`) |
| `ISSUEBENCH_TIMEOUT` | `4h` | Whole run (`3600`, `90m`, `4h`) |
| `HEIMDALL_SHARED_ALLOW_RUNC` | empty | `1` on a dev box without gVisor |
| `ISSUEBENCH_HTTPS_PORT`, `ISSUEBENCH_HTTP_PORT` | `47543`, `47180` | Loopback ports |
| `ISSUEBENCH_PROJECT` | generated | Compose project name |
| `ISSUEBENCH_IMAGE`, `ISSUEBENCH_RUNNER_IMAGE` | `heimdall/server:ib-e2e`, `heimdall/runner:ib-e2e` | Image tags |

Flags: `--check-only`, `--keep`, `--no-build` (use the images already
tagged), `--help`.

## Secrets

- The provider key goes to curl on stdin (jq reads it from the
  environment), never in argv or a file.
- The API token sits in a header file (mode 0600) in the run's work folder,
  removed at the end.
- `GITHUB_TOKEN` reaches the server container through the compose
  environment of this process (an override file that names the variable,
  not its value). The GitHub connection in Settings is OAuth only and no
  route stores a token, so the scan uses the server's `GITHUB_TOKEN`
  fallback. Readiness then shows `github: false`, which this script does
  not wait for.
- Every printed line has the provider key, GitHub token, API token,
  password, CSRF token and shared worker secret replaced with `[redacted]`,
  including the server and shared worker logs printed on failure.
- The generated env file holds the stack's own random secrets (mode 0600)
  and is never printed.

## Needs

Docker with the compose plugin, curl, jq, openssl, and free disk for the
server and runner images plus the issue environment images. gVisor
(`deploy/hosted/install-gvisor.sh`) or `HEIMDALL_SHARED_ALLOW_RUNC=1`.
