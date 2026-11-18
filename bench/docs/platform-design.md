# Hosted platform: design contract

Status: in progress (18 Nov 2026). This file fixes the names, shapes and
ownership the hosted-platform work agrees on. Where it and the code differ,
the code wins and this file is corrected.

## Goal

Heimdall can be hosted as a multi-user service: teams sign in, belong to
organizations, and run benchmarks on runners they own, while the hosted
server stores results, renders the UI and coordinates. The local
single-user mode keeps working exactly as today.

## Rules that do not bend

1. **Local mode is unchanged.** `auth.mode: local` (the default) behaves as
   today: one implicit organization `local`, the write token guards writes,
   runs live in `runs/<exp>`. Every existing test, CLI command and recorded
   bundle keeps working. Hosted behaviour is switched on only by
   `auth.mode: hosted`.
2. **Sealed run folders stay the evidence.** The database indexes them and
   holds operational state (users, tokens, jobs, runners, audit). A verdict,
   leaderboard or release never reads a result from the database; it reads
   sealed files, as today. The index can be dropped and rebuilt from files.
3. **Isolation by construction.** In hosted mode every request is resolved
   to one organization before any handler runs, and the handlers it reaches
   were built over that organization's store only. A handler can not see
   another organization's data because it holds no reference to it. Every
   row of tenant data carries `org_id`, and every query on it filters by it.
4. **Strangers' code never runs on the hosted server.** Agents run on
   runners. The hosted server runs no agent container except on its own demo
   runner, which serves only the demo organization's suites under a spend
   cap.
5. **Secrets are stored hashed or not at all.** Passwords: argon2id.
   Session ids, API tokens, runner tokens, invitation tokens: sha256 of a
   32-byte random value; shown once. Provider keys never reach the hosted
   server; they live on runners.
6. **Every write is attributable.** Each state-changing request writes one
   audit row: who (user, API token or runner), what, which target, when,
   from where.
7. **Additive schemas.** New JSON fields are omitempty; manifests, locks,
   seals and recorded runs keep their hashes (the existing hash tests stay
   green).

## Configuration (lib/config.go, additive)

```yaml
auth:
  mode: local                 # local | hosted
  public_url: https://heimdall.example.com   # hosted: used for OAuth callbacks and links
  session_ttl_h: 168
  signup: invite              # invite | open | closed
  github:                     # optional OAuth login
    client_id_env: HEIMDALL_GITHUB_CLIENT_ID
    client_secret_env: HEIMDALL_GITHUB_CLIENT_SECRET
database:
  driver: sqlite              # sqlite | postgres
  path: .heimdall/heimdall.db # sqlite, relative to runs_root
  dsn_env: HEIMDALL_DATABASE_URL   # postgres
storage:
  root: runs                  # hosted: per-org folders under runs/orgs/<org_id>/
  s3:                         # optional mirror for backup and archive
    endpoint_env: HEIMDALL_S3_ENDPOINT
    bucket: heimdall-evidence
    access_key_env: HEIMDALL_S3_ACCESS_KEY
    secret_key_env: HEIMDALL_S3_SECRET_KEY
demo:
  org: demo                   # slug of the public read-only organization, empty for none
```

Secrets are always named by environment variable, never written in the file.

## Database (package `bench/db`, owner P1)

Two drivers behind `database/sql`: SQLite (`modernc.org/sqlite`, pure Go,
so `CGO_ENABLED=0` builds keep working) and Postgres
(`github.com/jackc/pgx/v5/stdlib`). Both are new dependencies, approved for
this work. SQL is written once in a portable subset:

- ids: `TEXT` (prefixed random ids: `org_`, `usr_`, `tok_`, `rnr_`, `job_` ...)
- times: `BIGINT` unix milliseconds UTC
- booleans: `INTEGER` 0/1
- JSON: `TEXT`
- placeholders: write `?`; `db.Rebind` turns them into `$1..$n` for Postgres
- upsert: `INSERT ... ON CONFLICT (...) DO UPDATE`; `RETURNING` allowed

Package API (P1 lands this first, within the first hour):

```go
type DB struct{ /* *sql.DB, dialect */ }
func Open(ctx context.Context, cfg Config) (*DB, error)
func (d *DB) Driver() string                       // "sqlite" | "postgres"
func (d *DB) SQL() *sql.DB
func (d *DB) Rebind(query string) string
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error
func (d *DB) ClaimOne(ctx context.Context, q ClaimQuery) (*sql.Row, error) // job/lease claim: SKIP LOCKED on Postgres, BEGIN IMMEDIATE on SQLite
func Register(set string, migrations fs.FS)        // called from init() of each owning package
func (d *DB) Migrate(ctx context.Context) error    // applies every registered set, "core" first, then others in name order
func NewID(prefix string) string
// package dbtest
func Open(t *testing.T) *db.DB   // SQLite in t.TempDir(); also Postgres when HEIMDALL_TEST_PG_DSN is set
```

Each feature package owns its tables: it ships `migrations/NNNN_name.sql`
(portable SQL), registers them, and writes its own SQL against `*db.DB`.
`schema_migrations(set, version, applied_at)` tracks them.

Tables (owner in brackets):

| Table | Columns | Owner |
|---|---|---|
| `orgs` | id, slug UNIQUE, name, public_read, created_at | core (P1) |
| `users` | id, email UNIQUE, name, password_hash, github_id UNIQUE NULL, disabled, created_at | core (P1) |
| `memberships` | org_id, user_id, role (owner/admin/member/viewer), created_at; PK(org_id,user_id) | core (P1) |
| `audit_log` | id, org_id, actor_type (user/token/runner/system), actor_id, action, target_type, target_id, ip, at, details | core (P1) |
| `settings` | org_id, key, value; PK(org_id,key) | core (P1) |
| `idx_experiments` | org_id, exp_id, name, suite, arms, state, decision, action, cost_usd, created_at, frozen_at, finished_at, runner_pool, manifest_hash | index (P1) |
| `idx_runs` | org_id, run_id, exp_id, pair_id, slot_id, task, arm, condition, trial, outcome, score, cost_usd, tokens, wall_s, started_at, seal_hash, synthetic, agent_id, config_hash, model | index (P1) |
| `idx_analyses` | org_id, exp_id, analysis_id, decision, action, policy, created_at | index (P1) |
| `sessions` | id (sha256), user_id, org_id, csrf, created_at, expires_at, last_seen, ip, user_agent | auth (P2) |
| `api_tokens` | id, org_id, user_id NULL, name, prefix, hash, role, created_at, last_used_at, expires_at, revoked_at | auth (P2) |
| `invitations` | id, org_id, email, role, token_hash, created_by, created_at, expires_at, accepted_at | auth (P2) |
| `oauth_states` | state_hash, created_at, return_to | auth (P2) |
| `jobs` | id, org_id, kind, status (queued/running/succeeded/failed/cancelled), params, progress, result, error, created_by, created_at, started_at, finished_at, cancel_requested, attempt, lease_owner, lease_until | jobs (P4) |
| `job_logs` | job_id, seq, at, level, line; PK(job_id,seq) | jobs (P4) |
| `runners` | id, org_id, name, pool, labels, version, status, last_seen, registered_at | runners (P5) |
| `runner_tokens` | id, org_id, pool, prefix, hash, created_by, created_at, revoked_at | runners (P5) |

## Tenancy (package `bench/tenancy`, owner P3)

- `Principal{Kind user|token|runner|anonymous; UserID; OrgID; Role; TokenID}`
  is set on the request context by the auth middleware (P2) and read with
  `auth.PrincipalFrom(ctx)`.
- `tenancy.Registry` maps an org id to an org runtime: that org's
  `store.FSStore` rooted at `runs/orgs/<org_id>/` (local mode: the existing
  runs root, org `local`), its index, analyzer, launcher, and a router built
  from `server.Deps` for that org. Built lazily, cached, closed on eviction.
- Route dispatch in hosted mode: `/api/bench/auth/*` and `/api/bench/orgs/*`
  and platform routes (jobs, runners, tokens, audit, members) are served
  directly; every other `/api/bench/*` request is dispatched to the
  principal's org router. The org comes from the session's current org, an
  API token's org, or the `X-Heimdall-Org` header (checked against
  membership).
- Role policy table, one place (`tenancy/policy.go`): GET = viewer; POST that
  starts or records work (experiments, reviews, releases, estimates, plans,
  jobs) = member; members, invitations, tokens, runners, settings = admin;
  deleting or transferring the org = owner. The demo org with
  `public_read=1` lets anonymous principals read it and nothing else.
- `bifrost-bench org import -org <slug> -from <runs dir>` copies sealed runs
  into an org (seals verified on the way in): how the 318 real runs become
  the public demo.

## Auth (package `bench/auth`, owner P2)

Email and password (argon2id via `golang.org/x/crypto/argon2`), GitHub OAuth
(optional), sessions in an HttpOnly, Secure (when `public_url` is https),
SameSite=Lax cookie; every cookie-authenticated write also needs the
`X-Heimdall-CSRF` header equal to the session's csrf value and an allowed
`Origin`. API tokens `hm_<8 char prefix>_<secret>` and runner tokens
`hmr_...` as `Authorization: Bearer`. Login rate limit per IP and per email.
Routes under `/api/bench/auth/`: `signup`, `login`, `logout`, `me`,
`github/start`, `github/callback`, `invitations/{token}` (accept),
`switch-org`. Local mode: the auth middleware keeps today's write-token
check and sets a principal of org `local`, role owner.

## Jobs (package `bench/jobs`, owner P4)

One runner of jobs inside the server process, claiming queued jobs from the
database with a lease. Kinds register a handler `func(ctx, job, log) (result,
error)`. Kinds shipped: `experiment.run` (replaces the in-memory launcher
goroutine; resume after restart; cancel stops it and seals the in-flight
runs as cancelled), `suite.lint`, `suite.lock`, `suite.admit`,
`agent.check`, `index.rebuild`, `org.import`. Where a CLI command holds the
logic, it moves into a library function both call (package `bench/ops`).
API: `GET/POST /api/bench/jobs`, `GET /jobs/{id}`, `GET /jobs/{id}/logs`
(SSE and poll), `POST /jobs/{id}/cancel`. Local mode uses SQLite and the same
runner, so local runs get cancel and resume too.

## Runners (owner P5)

Extends the remote worker coordinator. In hosted mode a runner registers
with a runner token (`hmr_...`, scoped to one org and one pool), heartbeats,
leases slots only from its org's experiments whose `runner.pool` matches,
and uploads sealed runs, verified on arrival (existing behaviour). Experiment
YAML gains `runner: {pool: <name>}` (omitempty). CLI:
`bifrost-bench runner start -server <url> -token-file <path> [-pool] [-slots]`
(the existing `worker` command, renamed with an alias kept) and an install
script for a fresh Linux box. Admin API: create and revoke runner tokens,
list runners and their state. The demo runner is a normal runner registered
to the demo org.

## Evidence storage (owner P6)

Hosted layout `runs/orgs/<org_id>/<exp_id>/...`, same folder format as
today. Optional S3-compatible mirror: sealed experiment folders are copied
after sealing, verified by seal on restore; `bifrost-bench evidence backup |
restore | verify`. Retention setting per org (archive after N days to the
mirror). Database backup: `pg_dump` / SQLite online backup command.

## Deployment (owner P7)

`deploy/hosted/`: compose file with Caddy (automatic HTTPS for the domain),
Postgres, the Heimdall server image (UI embedded), optional demo runner with
the Bifrost stack; `.env.example`; backup and upgrade scripts; health and
readiness endpoints; `docs/hosting.md` (from a fresh VM to a working
instance in one page, plus a hardening checklist). A local smoke test brings
the whole stack up on spare ports with Caddy's internal CA.

## UI (owner U1)

Login, signup, invitation acceptance, org switcher, Settings (members and
roles, invitations, API tokens, runners with one-time token display and
install command, audit log, org settings), Jobs (drawer with running jobs,
job page with live logs and cancel), session-aware writes (no token boxes in
hosted mode; local mode unchanged), 401/403 handling, public demo banner.

## Ownership

| Owner | Packages and files |
|---|---|
| P1 Database | `bench/db/`, `bench/db/dbtest/`, core and index migrations, index sync (feeds from `store.SealedRuns` on seal and on rebuild), `cmd/bifrost-bench/db.go` (`db migrate|status|backup`) |
| P2 Auth | `bench/auth/`, `handlers/auth*.go`, `lib/config.go` auth section |
| P3 Tenancy | `bench/tenancy/`, `server/server.go` dispatch, `cmd/bifrost-bench/serve.go` wiring, `handlers/orgs.go`, `handlers/members.go`, `cmd/bifrost-bench/org.go` |
| P4 Jobs | `bench/jobs/`, `bench/ops/`, `handlers/jobs.go`, launcher integration in serve |
| P5 Runners | `engine/remote*.go`, `handlers/workers.go`, `handlers/runners.go`, `cmd/bifrost-bench/worker.go`, `runner.go`, `deploy/runner/` |
| P6 Evidence | `bench/evidencestore/`, `cmd/bifrost-bench/evidence.go` |
| P7 Deploy | `deploy/hosted/`, `Dockerfile` changes, `docs/hosting.md`, health endpoints |
| U1 UI | `bench/ui/` platform pages and shell changes |

Outside your area: additive edits only, re-read the file right before each
edit, mark the line with your area name.
