# Recorded internet: package mirrors and web downloads

Many real agent tasks expect the agent to install things: `pip install`, `npm install`, `apt-get install`, `go mod download`, a `git clone` or a `curl` of a release file. Bench runs have no internet, so before this feature such tasks were skipped at import (Terminal-Bench 2.0 lost 35 of 89 tasks, SkillsBench 1.1 lost 14 of 87).

The recorded internet gives such a task an `egress` service: one forward proxy that the agent, the scripted controls and (when needed) the verifier reach through `HTTP_PROXY` and `HTTPS_PROXY`. What the proxy serves is recorded once, at admission, and pinned in the suite lock. Every later run gets the same bytes. Nothing leaves the bench.

Package `bench/egress`, command `bifrost-bench egress`.

## How it works

1. **Import.** `bifrost-bench import harbor ... -set egress=record` imports a task whose reference solution or test script downloads at run time instead of skipping it. The task gets:
   - an `egress` service in `task.yaml` (role `egress`, image `bench/egress:1`, port 3128);
   - `egress/ca.pem`: a certificate authority made for this task (certificate and key). Only the egress service gets this file;
   - `environment/heimdall-egress-ca.crt`: the certificate alone. The Dockerfile copies it to `/etc/heimdall-egress/ca.crt`, writes `/etc/heimdall-egress/bundle.crt` (the image's roots plus it) and appends it to the system bundles;
   - `egress/cache.tar`: empty until it is recorded;
   - the tag `egress:recorded`, and lines in `IMPORT.md` saying all of this.
2. **Record.** `bifrost-bench egress record -suite <dir>` builds each task image, starts the proxy in `record` mode on a network with internet, runs `controls/reference.sh` in the task container with the proxy variables set, then runs the suite's verifier command in the same container. Every response is stored, and the recording is packed into `egress/cache.tar`. The packing is deterministic: same recording, same bytes. Each task's output goes to `<suite>/.egress-record/<task>.log`, and its request log to `<task>.requests.jsonl`.
3. **Lock.** `bifrost-bench suite lock`. The cache tar and the CA are service files, so their sha256 values are part of the task's `fixtures_sha256`. A changed recording is a changed task. Never run a plain `suite lock` after admission: it resets the admissions.
4. **Admit and run.** The proxy runs in `replay` mode. Each request is looked up by method, normalised URL and the request body's sha256. If that misses, a request with a body falls back to the same method and URL, so a client that puts a nonce in a POST body still replays. A hit is answered with the recorded status, headers and body, byte for byte. A miss gets a `404` with `X-Heimdall-Egress: miss`. Either way the proxy writes a row to `/egress/log/requests.jsonl`. That log is exported after the run as `state/egress/requests` and sealed with the run, so a reviewer can count the `miss` rows (egress_unmatched).

HTTPS works because the proxy ends each `CONNECT` tunnel itself, with a certificate for the requested host signed by the task's CA. Plain HTTP (apt's usual sources) goes through as absolute-URL proxy requests. The proxy stores bodies exactly as the upstream sent them (no decompression), so a gzip body replays as gzip.

## What the agent gets

When a run has an egress service, the environment adds these variables to the agent's container and to the scripted controls. Variables the plan already sets are kept as they are:

| Variable | Value |
|---|---|
| `HTTP_PROXY`, `HTTPS_PROXY`, `http_proxy`, `https_proxy` | `http://egress:3128` |
| `NO_PROXY`, `no_proxy` | `localhost`, `127.0.0.1`, `::1`, the tool proxy, every service id, and every host named by a URL in the plan's or the task's variables (the model gateway) |
| `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `PIP_CERT`, `GIT_SSL_CAINFO`, `CARGO_HTTP_CAINFO` | `/etc/heimdall-egress/bundle.crt` |
| `NODE_EXTRA_CA_CERTS` | `/etc/heimdall-egress/ca.crt` |
| `NODE_USE_ENV_PROXY`, `UV_NATIVE_TLS` | `1` |

A service with `verifier: true` (only an egress service may set it) is also joined to the verifier's container, on the run's internal service network, with the same proxy variables. That is how a `test.sh` that runs `uv pip install` or `git clone` at verify time is answered from the recording. The verifier gets no other route out.

## Run modes

Set per experiment with `egress:` (it overrides every egress service of the experiment's runs), or per service with `HEIMDALL_EGRESS_MODE` in its `env`:

| Mode | What happens | Repeatable |
|---|---|---|
| `replay` (default) | Answered from `egress/cache.tar` only; a miss is a 404 and a `miss` row | yes |
| `offline` | Every request is refused with 403 and an `refused` row | yes |
| `live-allowlisted` | A host on the allowlist is reached live (not recorded); any other host is refused. The allowlist is `HEIMDALL_EGRESS_ALLOW` (comma list; `*.example.com` covers subdomains), and by default the hosts the recording saw. Only in this mode is the egress service joined to a bridge network with a route out (`bench-enet-<run>`, labelled with the run so cleanup removes it); the agent never joins that network | no: labelled like `external: live`, and reports and the leaderboard say so |
| `record` | Forwarded and written to the record folder. Admission only (`egress record`); an experiment cannot set it | no |

## Commands

```sh
# once per machine that runs these tasks: the service image, from a static binary
CGO_ENABLED=0 go build -o bifrost-bench ./cmd/bifrost-bench
./bifrost-bench egress image                     # builds bench/egress:1 (scratch + this binary + root certificates)

# import, record, lock, admit
bifrost-bench import harbor -from datasets/terminal-bench-2 -suite tb2-egress -set egress=record -ids a,b,c
bifrost-bench egress record -suite benchmarks/tb2-egress -parallel 4
bifrost-bench suite lock tb2-egress
bifrost-bench suite admit tb2-egress

# what a recording holds
bifrost-bench egress show benchmarks/tb2-egress/tasks/<task>
```

`egress record` flags: `-tasks a,b` (default: every task with an egress service), `-parallel n`, `-image`, `-logs dir`, `-timeout-s n` (default the task's `agent_s`, at least 900), `-max-bytes n` (default 8 GiB per task), `-keep-existing` (skips a task whose cache already holds responses).

`egress serve` is the service's entry point: `-mode`, `-listen :3128`, `-cache /egress/cache.tar`, `-ca /egress/ca.pem`, `-log /egress/log/requests.jsonl`, `-allow`, `-record-dir`, `-max-bytes`. It also answers `GET /healthz` (the ready check), `GET /ca.crt` and `GET /stats` (the count of each outcome).

## Honest limits

- **The agent can tell.** Its HTTPS goes through a proxy with a certificate it can read (`/etc/heimdall-egress/ca.crt`). An eval-awareness check that looks for a MITM CA will see one (docs/eval-awareness.md).
- **Only what the reference fetched replays.** An agent that installs a different package, a newer version or another git ref gets a 404 for it. That is the point of a frozen internet: an agent that solves the task another way may need `egress: live-allowlisted`, and those results are not repeatable.
- **Recording is ground truth.** What the upstream served at record time is what every run gets. Record from admission with the reference pinned, and prefer lockfiles and `pip --require-hashes` in task authoring.
- **HTTP/1.1 only towards the client.** HTTP/2 is negotiated upstream only. No WebSocket, no `git://`, no SSH clones. Java tools that ignore the proxy variables (coursier, Maven) are not covered unless the task sets `JAVA_TOOL_OPTIONS` itself.
- **Compose tasks** with run-time downloads are still skipped (`-set egress=record` does not combine with compose services yet).
- **Docker driver only**, like every other service: the Kubernetes and sandbox drivers refuse tasks with services.
- **Per-task images.** The cache is copied into the service container at the start of each run, so a large cache (hundreds of MB) adds that copy to each run's start-up time.
