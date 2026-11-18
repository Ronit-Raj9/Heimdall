# Multi-container tasks (docker compose)

A task's environment can be several containers: an app, a database, a cache,
a fake third-party API. The task names a docker compose file, and the bench
maps the supported subset of that file onto its own model, so a compose task
runs with the same isolation, evidence and grading as a single-container
task: a frozen manifest, a per-run private network with no route out, a
clean-room or snapshot verifier, sealed run folders, admission controls and a
lock.

The bench never runs `docker compose` and does not use compose-go. The file
is read with `gopkg.in/yaml.v3` by `bench/compose/`, every key is mapped,
reported as changed or ignored, or refused with a reason, and the result is
started by the same code that starts any task's services
(`environments/services.go`).

Status (18 Nov 2026): built and checked with scripted controls through the
real pipeline and real Docker; no real model has run a compose task. What was
verified and how is listed under [What was checked](#what-was-checked).

## How a compose file becomes a run

```yaml
# task.yaml
compose: stack/compose.yaml   # relative to the task folder
```

When the suite loads (every `suite lint`, `lock`, `admit`, `plan` and `run`
loads it), the compose file is parsed and:

| Compose | Bench |
|---|---|
| The workspace service (below) | The task's own image (`Task.Environment`): its `build` or `image`, `working_dir` as `workspace_path`, `user: root` as `environment.user: root`, its `environment` as `environment.env` |
| Every other service | A service (`ServiceSpec`) on the run's private network, reachable by its compose name, started before the agent in dependency order |
| `depends_on` conditions, `healthcheck` | Start order and the readiness check (below) |
| Named volumes | One fresh docker volume per run, shared by the services that mount it, removed with the run |
| Bind mounts inside the task folder | Files copied into the container before it starts |
| `ports`, `expose` | Internal ports only; a published host port is dropped, with a note |
| `x-heimdall` | Bench settings (below) |

The mapping happens before anything else reads the task, so the lock, the
manifest and the run see ordinary services and an ordinary task image. A
task.yaml may also list `services:` of its own (an MCP tool server, say);
they start after the compose services. What task.yaml's `environment:` sets
wins over the workspace service (the Harbor importer uses this: the
workspace image stays the importer's rewritten Dockerfile).

### The workspace service

Exactly one service is the container the agent works in. It is, in order:

1. the service marked `x-heimdall: {workspace: true}`;
2. else a service named `main`, Harbor's convention (harbor 0.23.0,
   `harbor/constants.py` `MAIN_SERVICE_NAME = "main"`; Harbor merges a task's
   compose file over its own base file, which defines `main`);
3. else a service named `client`, Terminal-Bench 1's convention (harbor
   0.23.0, `harbor/mappers/terminal_bench.py` takes `client`, else the first
   service, as the main service);
4. else none: when task.yaml's `environment` names an image or Dockerfile,
   the agent runs in it and every service of the file runs beside it;
   otherwise the task is refused with "no workspace service".

Lint notes when a convention, not a marker, picked the workspace.

The agent's container is still the bench's: its own command, a hardened
container (no added capabilities, no new privileges, a tmpfs home), the
agent's budgets, the observers, the workspace volume seeded from
`working_dir` in the image with a git baseline. So for the workspace
service:

- `build` or `image`: the task image. A `build` is tagged
  `bench/task-<suite>-<task>:<content hash>` like any task image.
- `working_dir`: the folder the workspace is seeded from. Without it, the
  last literal `WORKDIR` of the Dockerfile's final stage; without either the
  task is refused (the agent needs to know where to work).
- `environment`, `env_file`: set in the agent's container, in a scripted
  control's and in the verifier's, under the plan's own variables, which win.
  Names starting `BENCH_`, `AGENT_` or `OTEL_` are refused: they are the
  bench's.
- `user: root` (or `0`, `0:0`): the agent runs as root in the task image, as
  `environment.user: root` does (docs/scenarios-design.md). Any other user is
  changed to uid 1000, with a note.
- `depends_on`: every service the workspace waits for is up (healthy, or
  finished for `service_completed_successfully`) before the agent starts.
- A bind mount of the build context onto `working_dir` (the usual
  `.:/app` of a development compose file) is dropped with a note: the code
  must be in the image. Any other mount on the workspace is refused: the
  agent's container mounts only the bench's workspace, so shared data goes
  through a service or the Dockerfile.
- `command`, `entrypoint`, `ports`, `healthcheck`, resource limits, tmpfs,
  hostname and aliases are ignored, each with a note.

### Readiness and start order

Services start one at a time, every dependency first (file order among
equals); a dependency cycle, a dependency on an undeclared or inactive
service, or a service that waits for the workspace is refused.

| Service | Ready when |
|---|---|
| has a `healthcheck` | its test passes, run inside the service container (`[CMD, ...]` as is; `[CMD-SHELL, cmd]` and a plain string in `/bin/sh -c`), polled every 0.5 s |
| no healthcheck, a port | the first internal port accepts a TCP connection (from a probe container on the run network) |
| neither | it is running |
| one-shot (some dependant waits with `service_completed_successfully`, or `x-heimdall: {run_once: true}`) | it exited with code 0 |

The time allowed for a healthcheck is what docker would take to call the
service unhealthy: `start_period + retries x (interval + timeout)`, kept
between 10 s and 10 minutes, or `x-heimdall: {ready_timeout_s: n}`. A
one-shot service has 5 minutes. Without a healthcheck the bench's default is
60 s.

This is stricter than docker compose in one way: a service with a
healthcheck is waited for until healthy even when its dependants only ask for
`service_started`. A service that never becomes ready, or a one-shot that
exits non-zero, ends the run as `aborted_infra` before the agent starts,
with the service's last log lines in the reason. `service_healthy` on a
service without a healthcheck is refused, as compose refuses it.

### Networks, isolation and egress

- Each run gets its own internal docker network (`--internal`: no route out)
  that the services and the agent join; two runs never see each other's
  services. Every network of the compose file becomes this one network, so
  services on different compose networks can reach each other (lint notes
  it).
- A service answers to its id (the compose name, lower case, `_` and `.` as
  `-`) and to its compose name, `hostname` and network `aliases`.
- `x-heimdall: {hidden: true}` keeps a service off the agent's network, as
  for any service; `role: tool|mcp` puts it behind the per-run tool proxy,
  which records every call (docs/tool-proxy.md).
- Services keep docker's default capabilities (database images switch users
  at start) with `no-new-privileges`, a 4 GiB memory cap and 1024 processes.
  `mem_limit`, `cpus`, `pids_limit` and `deploy.resources.limits` may lower
  these, never raise them.
- Image builds have no network unless the file says
  `x-heimdall: {build_network: true}` (most Dockerfiles install packages).
  The run itself never gets one.

### The verifier and the services

With `x-heimdall: {verifier_services: true}` at the top of the file (or
`verifier: true` on a service) the verifier joins the run's network while it
grades, after the agent has exited, so it can query the services by name:
the state the agent left (a migration applied, a row written), or fresh
databases of its own for checks the agent's leftovers must not influence
(the acme-stack verifiers create a database per check and use their own
Redis database index). The verifier also gets the workspace service's
environment, so the app's own settings work in it.

Before every grade the grader checks that each network it joins is internal
and refuses to grade otherwise; admission records this as
`verifier_network_internal` in place of `verifier_no_network`. This works in
both verifier modes: a clean-room verifier (pristine workspace plus the
agent's diff, in the task image) and a snapshot verifier (the frozen agent
container) both run in a fresh container on that network. A regrade after the
run's services were removed cannot reach them: it fails with that reason
instead of grading against nothing.

Under Harbor's conventions every service is reachable by the verifier,
because Harbor runs `test.sh` inside `main`, which sees every service.

### Images and the lock

- A service `build` (context and Dockerfile inside the task folder) is
  tagged `bench/svc-<suite>-<service>:<content hash>` and built when missing,
  like any service image; an `image` is pulled when missing.
- The suite lock binds the compose file (it is in the task's fixtures hash
  and its files fingerprint), every build context, and each service image
  under the role `service/<id>` beside the task image, with today's rules: a
  local build by tag only (its context is hashed), a pulled image by its
  registry digest where docker reports one apart from the local id, local ids
  only with `suite lock -pin-local-images`. Tasks without a compose file
  fingerprint exactly as before.

### Evidence

- Each compose service's output (stdout and stderr, docker's timestamps) is
  captured after the agent exits to `services/<id>.log` in the run folder,
  capped at 1 MiB, and sealed with the run. A log that cannot be read is
  logged and left out; it never fails the run.
- `x-heimdall: {export: [{id, argv | path}]}` on a service declares state
  read after the agent exits, exactly as `export:` on a task service
  (docs/scenarios.md): it lands in `state/<service>/<id>`, is sealed, and is
  mounted read-only in the verifier at `/grader/state/...`.
- The services' start and readiness are in the run record like any
  service's (`ServiceResult`).

## The supported subset

Top level: `services`, `volumes`, `networks`, `x-heimdall`; `version` and
`name` are ignored; other `x-*` keys are ignored, as compose ignores them.
YAML anchors, aliases and merge keys (`<<: *common`, or a list of them) are
resolved as compose reads them, explicit keys winning, up to 200,000 values
(a file of nested aliases stops there with an error).

Per service:

| Key | Supported as |
|---|---|
| `image` | the image; ignored when `build` is set |
| `build` | a string (context) or `context` and `dockerfile`, both inside the task folder |
| `command`, `entrypoint` | a list as is, or a string split like a shell (quotes, backslashes, no expansion); an empty `entrypoint` clears the image's |
| `environment` | a list of `KEY=VALUE` or a mapping |
| `env_file` | a path, a list, or entries with `path` and `required`, inside the task folder; `environment` wins over it |
| `depends_on` | a list, or a mapping with `condition` (`service_started`, `service_healthy`, `service_completed_successfully`); `required: false` is still started, with a note |
| `healthcheck` | `test`, `interval`, `timeout`, `retries`, `start_period`, `disable` |
| `volumes` | short (`src:dst[:ro]`) or long syntax: named volumes, anonymous volumes, binds inside the task folder (copied in), `tmpfs` |
| `tmpfs` | as is |
| `expose`, `ports` | internal tcp ports; published host ports dropped with a note |
| `working_dir`, `user` | as is (workspace: see above) |
| `hostname`, `networks.<n>.aliases` | extra names on the run network |
| `profiles` | a service with a profile is left out unless `x-heimdall: {profiles: [...]}` names it, as compose leaves it out without `--profile` |
| `mem_limit`, `cpus`, `pids_limit`, `deploy.resources.limits` | lower the bench's caps |
| `network_mode: bridge` | the run's network, with a note |

Interpolation follows compose: `$VAR`, `${VAR}`, `${VAR:-default}`,
`${VAR-default}`, `${VAR:?error}`, `${VAR?error}`, `${VAR:+value}`,
`${VAR+value}`, `$$`. Values come from the `.env` file beside the compose
file, inside the task folder. The host environment is never read: a variable
with no value and no default is refused, naming it, because docker compose
would take it from the shell of whoever runs the task.

`x-heimdall` per service: `workspace`, `verifier`, `role` (`data`, `tool`,
`mcp`), `hidden`, `proxy`, `run_once`, `ready_timeout_s`, `export`. At the top
level: `verifier_services`, `build_network`, `profiles`.

## Refused, with the reason

A refused setting is a load error naming its place in the file, so `suite
lint` lists every one and `plan` and `run` refuse the task before anything
starts.

| Setting | Why |
|---|---|
| `privileged`, `cap_add`, `devices`, `device_cgroup_rules`, `gpus` | they hand the container the host's devices or more privilege |
| `network_mode` other than bridge, `pid`, `ipc`, `uts`, `userns_mode`, `cgroup`, `cgroup_parent` | sharing the host's (or another container's) namespaces |
| `security_opt`, `sysctls`, `ulimits`, `oom_kill_disable`, `oom_score_adj`, `runtime`, `isolation` | they change the container's confinement |
| bind mounts outside the task folder, `~` paths, paths that leave it through `..` or a symlink | a run never sees host files |
| the docker socket, as a mount or `use_api_socket` | it controls the host |
| `extra_hosts`, `dns`, `dns_search`, `dns_opt`, `links`, `external_links`, `volumes_from` | they reach outside the run, or mount another container |
| `extends`, `include`, top-level `secrets` and `configs`, service `secrets` and `configs` | not supported; write the file out in full, mount files read-only |
| `external` volumes or networks, volume drivers and driver options | they live outside the run (a local driver option can bind a host folder) |
| `build.args`, `build.target`, `dockerfile_inline`, build `ssh`, `secrets`, `network: host`, remote build contexts | not supported yet, or reaching outside the task folder |
| more than one replica (`scale`, `deploy.replicas`) | one container per service |
| udp ports | readiness and the run network are tcp |
| a variable with no value and no default | compose would read the host environment |
| an unknown key | so a typo or a newer compose feature never runs half-mapped |

## Changed or ignored, with a note

`restart` (a service that stops is an infrastructure error, never
restarted), `container_name`, `labels`, `stdin_open`, `tty`, `init`,
`logging`, `platform`, `pull_policy`, stop settings, reservations, CPU shares
and sets, `shm_size`, `cap_drop`, `read_only`, swarm `deploy` settings and
network addresses are ignored; published ports, merged networks, read-write
binds (copied in: writes stay in the container), optional dependencies and
the workspace's dropped settings are changed. `suite lint` prints each as a
warning against the compose file, for example:

```
warning tasks/fix-migration/stack/compose.yaml: services.db.ports[0]: changed: host port 5432 is not published; the service is reachable only inside the run, as db:5432
```

## Converting a team's compose file

```sh
bifrost-bench task init -suite acme-stack -id checkout-bug -from-compose ../shop/docker-compose.yml
```

copies the folder holding the compose file into the task's `stack/` folder
(without `.git`, `node_modules`, virtual environments, caches and symlinks,
at most 20,000 files and 256 MiB), writes `task.yaml` with `compose:`
pointing at the copy, drafts of the instruction, the hidden verifier
(`verifier/tests/verify.py`), the reference solution, a mutant, the empty and
exploit controls, adds the task to `suite.yaml`, scans the copy for secrets,
and prints what the mapping found. A refused setting does not stop the
write; the task does not load until it is fixed. Then:

1. Mark the agent's service `x-heimdall: {workspace: true}`, make sure its
   Dockerfile copies the code to `working_dir`, and remove what was refused.
2. Add `x-heimdall: {verifier_services: true, build_network: true}` when the
   verifier queries services and the builds install packages.
3. Replace each EDIT ME draft: the instruction as a teammate would write it,
   the verifier's checks, a reference that passes, mutants that look right
   and fail.
4. `bifrost-bench task validate <suite>/<id>`, `suite lock`, `task admit`.

The suite should use grader `verifier_command` with `result: reward`, and a
`workdir` equal to the workspace's `working_dir`, as
`examples/acme-stack/benchmarks/acme-stack/suite.yaml` does.

## Harbor (Terminal-Bench 2, SkillsBench) compose tasks

`bifrost-bench import harbor` used to skip every task with a compose file in
`environment/`. It now maps `environment/docker-compose.yaml`, the only name
Harbor reads (harbor 0.23.0, `environments/docker/docker.py`
`_environment_docker_compose_path`), under Harbor's conventions:

- `CONTEXT_DIR` is the environment folder, `TEST_DIR` is `/tests`,
  `MAIN_IMAGE_NAME`, `PREBUILT_IMAGE_NAME`, `CPUS` and `MEMORY` are Harbor's
  own and their settings are ignored (from harbor 0.23.0
  `environments/docker/compose_env.py` `ComposeInfraEnvVars`).
- Mounts of Harbor's log folders (`HOST_*_LOGS_PATH`, `HOST_ARTIFACTS_PATH`)
  are dropped: the bench gives the verifier its own `/logs/verifier`.
- SkillsBench files pass Claude Code's Vertex AI settings to `main`
  (`CLAUDE_CODE_USE_VERTEX`, `CLOUD_ML_REGION`, `ANTHROPIC_VERTEX_PROJECT_ID`,
  `GOOGLE_APPLICATION_CREDENTIALS`) and mount `~/.config/gcloud`; these
  configure the harness's own model access, which the bench replaces with the
  gateway, so they are dropped with a note. A provider key the task itself
  reads from the host (`OPENAI_API_KEY=${OPENAI_API_KEY}`) is refused.
- The workspace image stays the importer's rewritten `environment/Dockerfile`;
  every other service runs beside it and the snapshot verifier reaches all of
  them. Each change is listed in the task's `IMPORT.md`.
- A compose file under another name (`docker-compose.yml`, `compose.yaml`)
  is not read by Harbor, so it is not used here either; the task imports as
  one container, as it runs under Harbor, with a note in `IMPORT.md`.

Counted on 18 Nov 2026 against the local clones the importer was first run
on (`datasets/` of the main checkout: Terminal-Bench 2.0 at `2fd12b88`,
SkillsBench `main` at `f860ecf0`):

| Benchmark | Tasks with a compose file | Imported now | Still skipped, and why |
|---|---|---|---|
| Terminal-Bench 2.0 (89) | 0: no task ships a compose file in `environment/`, so none was skipped for it | 0 | none for compose |
| SkillsBench `tasks/` (87) | 3, previously skipped as "multi-container compose environment" | 1: `react-performance-debugging` (its file is `docker-compose.yml`, which Harbor does not read, so it runs as one container, as under Harbor) | `fix-visual-stability`: its compose file maps (main plus an `api` service; checked by `TestUpstreamSkillsBench`), but its `solve.sh` runs `npm install` and its `test.sh` too, so it is skipped for network, as single-container tasks are. `drone-planning-control`: `main` reads `ANTHROPIC_API_KEY` from the host: refused |
| SkillsBench `tasks-extra/` (14) | 6 | 0 | 5 read host variables in `task.toml` (`MODAL_TOKEN_ID`, `GH_TOKEN`, `OPENAI_API_KEY`, `HUGGINGFACE_API_TOKEN`), skipped by the existing check before the compose file is read; `pedestrian-traffic-counting`: its compose file reads `OPENAI_API_KEY`, `GEMINI_API_KEY` and `ANTHROPIC_API_KEY` from the host: refused |

So the compose mapping itself admits no new upstream task yet: every real
compose task found is held back by network use or host credentials, which
the bench refuses for single-container tasks too. The mapping was exercised
on the real files (and on the importer's fixtures, which do import with
their services), not on an admitted upstream task.

## Example: `examples/acme-stack`

Three tasks on Acme Shop (fictional), each with its own
`stack/compose.yaml`: the app (Python, `psycopg` and `redis` from PyPI) as
the workspace, `postgres:18` and `redis:7-alpine` beside it, healthchecks
on both.

| Task | The agent must | The verifier checks |
|---|---|---|
| `fix-migration` | fix migration 002 (a `NOT NULL` column added to a table with rows) and apply it to the stack's database | the live database (002 applied, rows kept, backfilled, required), and the committed migrations on a fresh database with data the agent never saw |
| `cache-invalidation` | make a price change visible at once without turning the Redis cache off | on a fresh database and its own Redis database: the cache is still read, a change shows at once, other products' entries survive |
| `health-db` | make `/healthz` report the database, 200 up or 503 down | the app's server against the stack's database, then against a port that refuses connections; this stack also runs a one-shot `seed` service (`service_completed_successfully`) |

Each task has a reference script, an empty control, an exploit and two or
three mutants (deleting the rows to make the migration apply, forgetting to
apply it, leaving the column nullable; never reading the cache, flushing the
whole cache, deleting the wrong key; always reporting up, reporting down with
200). See the example's README for the commands and the recorded results.

## What was checked

- `go test ./compose/`: parser, interpolation, value parsing, the refusal
  table (one row per refused setting, each asserting the place and the
  reason), mapping of a full stack, workspace conventions, Harbor
  conventions, profiles, `.env` and `env_file`. `TestUpstreamSkillsBench`
  maps SkillsBench's own files when `BENCH_SKILLSBENCH_DIR` names a clone.
- `go test ./suite/ -run Compose`: the loader maps `acme-stack`, refuses a
  privileged service at load with its place, lint reports changed settings,
  the lock hash moves when the compose file changes, `task init
  -from-compose` writes a task that loads.
- `go test ./environments/ ./graders/ -run Compose|ServiceLimits|WithTaskEnv`:
  the docker flags a compose service gets, the caps, the workspace
  environment, the verifier's networks and the refusal of a regrade without
  services.
- `go test ./importers/harbor/`: compose fixtures import (with Harbor's
  variables and log mount), a refused one is skipped with each place and
  reason, a `docker-compose.yml` imports as one container with a note.
- Real Docker, `go test -tags integration ./environments/ -run
  'TestComposeStackEndToEnd|TestComposeSnapshotAcrossServices'`: one
  `cache-invalidation` run of three containers (app, Postgres, Redis) with
  the reference passing and noop failing in the clean room, service logs
  written, the run network internal, nothing left behind; and a snapshot
  grade whose verifier reads a Redis service's exported state
  (`x-heimdall export`) and queries the live service by name.
- Real Docker, `suite admit` of all three `acme-stack` tasks (every control
  as expected) and the $0 oracle-vs-noop experiment `e_stack_smoke_9pw6`
  through the full pipeline (oracle 3 of 3, noop 0 of 3, 6 sealed runs with
  their service logs): `examples/acme-stack/README.md`.
- `suite lint` and `plan` on a copy of `acme-stack` with `cap_add` and the
  docker socket added to a service: both refuse, naming
  `services.cache.cap_add` and `services.cache.volumes[0]` with their
  reasons, before anything starts.
- `task init -from-compose` on a sample team project: the folder is copied
  without `.git`, the refused settings (a host variable, a host mount,
  `privileged`, no workspace) and the changed ones are printed, and lint
  lists the same.

Not checked: a real model on a compose task; a compose task on remote
workers or in a team run; Kubernetes (see Limits).

## Limits

- One run network: compose network separation (a frontend that must not
  reach the database) is not kept.
- The agent's container cannot be reached by name by the services, and it
  mounts no compose volume or bind: shared state goes through a service.
- Bind mounts are copies made before start: a service never writes back into
  the task folder, and a live-reload setup sees no change.
- Readiness is stricter than compose (a healthcheck is always waited for),
  and a service is never restarted.
- Build arguments and targets are refused for now; bake them into the
  Dockerfile.
- Team runs (docs/multi-agent.md) have not been run with a compose file.
  The workspace service's environment is set for a single agent, a scripted
  control and the verifier; a team member's container does not get it.
- The Kubernetes driver refuses tasks with services, so a compose task with
  services is refused there; a compose file with only a workspace service
  would reach Kubernetes without its environment variables, which that
  driver does not apply yet.
- Only `x-heimdall` marks the verifier's access; a verifier that joins the
  run network can talk to every non-hidden service on it, not only the
  marked ones.
- Service logs are captured for compose services only, and only after the
  agent exits: a run aborted because a service never became ready keeps the
  last lines in its reason instead.

## Sources

Read on 18 Nov 2026 from the primary source, the `harbor` 0.23.0 package as
published on PyPI (Apache-2.0, by the Terminal-Bench authors), installed
locally:

- `harbor/constants.py`: `MAIN_SERVICE_NAME = "main"`.
- `harbor/environments/docker/docker.py`: the task's compose file is
  `environment/docker-compose.yaml` only, merged after Harbor's base
  `docker-compose-build.yaml` (service `main`, `build.context:
  ${CONTEXT_DIR}`, `command: sh -c "sleep infinity"`) or
  `docker-compose-prebuilt.yaml`.
- `harbor/environments/docker/compose_env.py`: `ComposeInfraEnvVars`
  (`MAIN_IMAGE_NAME`, `CONTEXT_DIR`, `PREBUILT_IMAGE_NAME`, `CPUS`, `MEMORY`
  and egress settings) and the legacy log mount variables
  `HOST_`/`ENV_` + `VERIFIER_LOGS`, `AGENT_LOGS`, `ARTIFACTS` + `_PATH`.
- `harbor/mappers/terminal_bench.py`: Terminal-Bench 1 tasks name the agent's
  service `client` (else the first service), and the mapper sets
  `TEST_DIR=/tests`.

Read on 18 Nov 2026 from docs.docker.com:

- `reference/compose-file/services/`: a healthcheck has the defaults of the
  Dockerfile `HEALTHCHECK` instruction; a test list starts with `NONE`,
  `CMD` or `CMD-SHELL`, a string means `CMD-SHELL`; `depends_on` conditions
  are `service_started`, `service_healthy` and
  `service_completed_successfully`, `required` defaults to true; a service
  with profiles starts only when one of them is active.
- `reference/dockerfile/` (`HEALTHCHECK`): `--interval` 30s, `--timeout` 30s,
  `--start-period` 0s, `--start-interval` 5s, `--retries` 3.

Not verified: how docker compose splits a `command` or `entrypoint` given as
a string (the reference page does not say); the bench splits it like a POSIX
shell without expansion, which matches the files seen here. Every other key
and the interpolation forms follow the Compose Specification as docker
compose implements it, from experience rather than a fresh reading, so a key
this mapping does not know is refused rather than guessed.
