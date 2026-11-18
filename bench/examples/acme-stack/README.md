# Acme stack example: tasks whose environment is a docker compose stack

This folder is a complete Heimdall (bifrost-bench) project for a team whose
service runs as several containers: an app, Postgres and Redis, started with
docker compose. Each task carries the team's own compose file, and the bench
maps it onto one run: the app is the agent's workspace image, the database
and the cache run beside it on the run's private internal network, and the
hidden verifier queries them by name after the agent exits. The guide is
[`docs/multi-container.md`](../../docs/multi-container.md). No Go code is
needed.

## What is fictional

Acme Shop, its catalogue, prices and code are invented for this example and
written from scratch; MIT licensed (`provenance.license` in every
`task.yaml`). The images are the official `python:3.12-slim`, `postgres:18`
and `redis:7-alpine`; the app image adds `psycopg[binary]==3.2.10` and
`redis==5.2.1` from PyPI at build time.

## Folder map

```
acme-stack/
  README.md                           this file
  .gitignore                          runs/ and checks/ stay local
  deploy/bench.yaml                   paths, gateway, networks, helper image bench/coding-core-base:1
  deploy/pricing-snapshot.json        model prices (copy of the bench's)
  policies/default.yaml               release policy (copy of the bench's)
  agents/oracle.yaml, agents/noop.yaml  the scripted controls (no model call)
  experiments/stack-smoke.yaml        free smoke check: oracle vs noop on every task
  benchmarks/acme-stack/
    suite.yaml                        verifier_command, reward, workdir /app
    tasks/<task>/
      task.yaml                       compose: stack/compose.yaml
      instruction.md
      stack/compose.yaml              the team's compose file: app (x-heimdall workspace), db, cache
      stack/app/                      the app: Dockerfile, acme/ package, migrations/
      stack/db/                       Postgres init scripts or the one-shot seed
      verifier/tests/verify.py        hidden verifier (reward file)
      verifier/requirements.md        each check and the instruction sentence behind it
      solution/reference.sh           reference, run in the agent's place
      controls/                       empty, exploit and mutant scripts
```

## The tasks

| Task | The agent must | The verifier checks |
|---|---|---|
| `fix-migration` | fix migration 002, which adds a `NOT NULL` column to a table that has rows, and apply it to the stack's database | the live database (002 applied, rows kept, each sku `SKU-<id>`, the column required), then the committed migrations on a fresh database holding a product the agent never saw (backfilled, required, unique) |
| `cache-invalidation` | make a price change visible at once, keeping the Redis cache for reads | on a fresh database and the verifier's own Redis database: the cache is still read, a change through the app shows at once, the other products' entries survive |
| `health-db` | make `GET /healthz` report the database: `200` up, `503` down, within seconds | the app's own server against the stack's database, then against a port that refuses connections. This stack also has a one-shot `seed` service that loads the catalogue before the agent starts (`service_completed_successfully`) |

Controls per task: the reference must pass three times; the empty control,
the exploit (fake reward files), the null agents and every mutant must fail.
Mutants: deleting the rows so the migration applies, fixing the file but not
applying it, leaving the column nullable; never reading the cache, flushing
the whole cache on a change, deleting the wrong key; always reporting up,
reporting down with status 200.

## Run it

From `bench/`, with the stack of `deploy/docker-compose.yml` up (the smoke
experiment mints a gateway key per run, so load the stack's `.env` in the
same shell):

```sh
B=./bin/bifrost-bench; C=examples/acme-stack/deploy/bench.yaml
$B suite lint  -config $C acme-stack
$B suite lock  -config $C acme-stack
$B suite admit -config $C acme-stack      # builds each app image (pip, with network), runs every control
$B run -config $C examples/acme-stack/experiments/stack-smoke.yaml
```

`suite lint` prints one warning for the compose mapping: fix-migration's
database publishes port 5432, which the bench keeps internal.

## Results

Checked on 18 Nov 2026 with real Docker on the development host, every run
scripted (no model call, $0):

- `suite admit`: all three tasks admitted, every control as expected. Per
  task: the reference passed 3 of 3; the empty control, every mutant (3, 3
  and 2), the exploit and the two null agents failed; the verifier joined
  only the run's internal network (`verifier_network_internal`). Each
  control is a full run of the stack (Postgres, Redis and, for `health-db`,
  the one-shot seed), 30 seconds to 4 minutes each on a shared, loaded
  machine.
- `run experiments/stack-smoke.yaml` (`e_stack_smoke_9pw6`): 6 sealed runs,
  0 errors, $0. Oracle (arm A) verified 3 of 3, noop (arm B) 0 of 3; the
  verdict reads INCONCLUSIVE, NO_DECISION (3 discordant tasks cannot reach
  significance; the smallest suite for that is 6 tasks) and the report is
  marked SYNTHETIC. Every run folder holds `services/db.log` and
  `services/cache.log` (and `services/seed.log` for `health-db`), sealed
  with the run; `check -exp` re-analyzed the sealed runs with the same
  verdict, and `GET /api/bench/runs/<id>/services` and
  `.../services/<id>/log` served them from a recorded server.

Admission found a bench bug on the way: a mutant that rewrote a migration
removed a line starting `-- ` (an SQL comment), which reads `--- ...` inside
the patch, and the diff export took it for a file header and failed. The
export now stops reading header lines at the first hunk
(`environments/diffallow.go`).

No real model has run these tasks.

## Limits

The limits of compose tasks are in
[`docs/multi-container.md`](../../docs/multi-container.md#limits). For this
example: the app image downloads two packages from PyPI when it is built,
so the first `suite admit` needs network. The verifier's code checks run on
a database it creates and drops itself, and on Redis database 9, so an agent
that writes into those during its run could still disturb them; the live
database checks of `fix-migration` read exactly what the agent left.
