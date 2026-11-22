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

Landed (Database P1, 18 Nov 06:30 IST): the API above is in `bench/db` with
these additions. `db.Config` = `dbconfig.Config{Driver, Path, DSNEnv,
MaxOpenConns, DSN}`; `lib.Config.Database` and `cfg.DatabaseConfig()` give it
resolved (default sqlite at `<runs_root>/.heimdall/heimdall.db`). Helpers:
`d.Exec/Query/QueryRow` (rebind for you), `d.ForUpdate()` (" FOR UPDATE" on
Postgres, "" on SQLite), `d.ClaimOneTx`, `d.ClaimOneScan` (false when nothing
to claim), `db.IsUnique(err)`, `db.IsForeignKey(err)`, `db.ErrNotFound`,
`db.ErrConflict`, `db.Now()/Millis/Time/NullMillis/Bool`. `ClaimQuery{Table,
Key (default id), Where, Args, OrderBy, Set, SetArgs, Returning}`; see the
doc comment in `db/claim.go`. Migration files: `//go:embed migrations/*.sql`
then `db.Register("jobs", fsys)`; any `NNNN_name.sql` in the FS counts.
dbtest: `Open(t)` (SQLite; Postgres when `HEIMDALL_TEST_DB=postgres`),
`OpenSQLite`, `OpenPostgres` (fresh schema, skipped without
`HEIMDALL_TEST_PG_DSN`), `Each(t, func(t, d))` (SQLite, then Postgres when
set). Inside `d.Tx` use only the tx; never nest `Tx`.

Core and index APIs (Database P1): orgs `CreateOrg(NewOrg{Slug, Name,
PublicRead, OwnerID})`, `OrgByID`, `OrgBySlug`, `ListOrgs`, `OrgsForUser`,
`SetOrgPublicRead`, `RenameOrg`, `DeleteOrg`; users `CreateUser`, `UserByID`,
`UserByEmail`, `UserByGitHubID`, `UpdateUser`, `SetUserPassword`,
`SetUserDisabled`, `LinkGitHub`, `CountUsers`; memberships `AddMember`,
`SetRole`, `RemoveMember` (`ErrLastOwner`), `Members`, `RoleOf`; audit
`AppendAudit`, `AppendAuditTx`, `ListAudit(AuditFilter)` (keyset paging,
`Action: "member.*"` prefix); settings `GetSetting`, `SetSetting`,
`DeleteSetting`, `Settings`. Index: `d.OrgIndex(orgID, store, logger)`
implements `resultsindex.Source` (Sync, Rebuild, UpdateExperiment, Load);
`d.SyncOrg`, `d.RebuildOrg`; lists `ListIndexedExperiments(ExperimentQuery)`,
`IndexedExperimentByID`, `ListIndexedRuns(RunQuery)`, `IndexedAnalyses`,
`IndexedRejected`. Wiring (P3/P4): with a database, serve sets
`trendSource{ix: d.OrgIndex(org, orgStore, logger)}` and calls
`OrgIndex.UpdateExperiment` where `refreshResultsIndex` runs on seal; the
JSONL index stays for everyone without one. CLI: `bifrost-bench db
migrate|status|backup -out <file>`.

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
| `jobs` | id, org_id, kind, target, status (queued/running/succeeded/failed/cancelled), params, progress, result, error, created_by, created_at, started_at, finished_at, cancel_requested, attempt, lease_owner, lease_until; at most one queued or running job per (org_id, target) when target is set | jobs (P4) |
| `job_logs` | job_id, seq, at, level, line; PK(job_id,seq) | jobs (P4) |
| `runners` | id, org_id, name, pool, labels, version, host, slots, running, token_id, status, last_seen, registered_at; UNIQUE(org_id,name) | runners (P5) |
| `runner_tokens` | id, org_id, pool, name, prefix, hash UNIQUE, created_by, created_at, expires_at, last_used_at, revoked_at | runners (P5) |

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

Landed (Tenancy P3, 18 Nov): what the code does, where it refines the above.

- Layout (`tenancy/layout.go`): evidence `runs/orgs/<org_id>/` (store root);
  project `runs/projects/<org_id>/{benchmarks,agents,experiments,policies,releases}`.
  The project is outside the store root because the store reads every
  folder as an experiment and `release.New` refuses a releases root inside
  the runs root. Seeding goes through one function,
  `tenancy.EnsureProject(paths, <server benchmarks>)`: it copies
  `benchmarks/coding-core` in once and writes `projects/<org_id>/.seeded`;
  the org API and `org create`/`org import` call it at creation, and the
  org runtime calls it on first build, which covers orgs created by signup,
  GitHub sign-in, `user create -org` and invitations. A suite the org later
  deletes is not seeded again; an org's edits are never overwritten. Per-org release log at `projects/<org_id>/releases`, never
  `BENCH_RELEASES_ROOT` (that would share one log across orgs).
- Deps: `orgDeps(ctx, app)` in `cmd/bifrost-bench/serve.go` builds
  `server.Deps` over one root; local mode calls it once, hosted mode once per
  org through `app.forOrg` (`servetenancy.go`). `server.NewOrgRouter` and
  local `server.New` share `registerOrgRoutes`: add org-scoped routes there.
- Dispatch (`server/hosted.go`, `tenancy/dispatch.go`): security headers,
  host guard, CORS, auth middleware, then the dispatcher: platform route
  (incl. `/healthz`, `/readyz`) -> policy -> platform router; other
  `/api/bench/*` -> policy -> org runtime router; else the UI. The org
  router's handlers check again that the request was resolved to their org
  (`RequireOrgReader`) and that writers hold member (`RequireOrgWriter`).
  The dispatcher sets the effective principal (`OrgID` = target org, `Role` =
  membership role there) and `tenancy.OrgFrom(ctx)`; platform handlers read
  those. Org-router writes get one generic audit row (`experiments.start`
  etc.); platform handlers write their own.
- Org selection: `{org}` path param (id or slug) on `/orgs/{org}/...`; else
  `X-Heimdall-Org` (id or slug); else the session's current org / token org /
  runner org. Tokens and runners never reach another org. An org the caller
  can not see answers 404 whether it exists or not. Anonymous callers land in
  `demo.org` (new config section `demo: {org: <slug>}`, empty = none) when it
  has public_read; non-member signed-in users may read public_read orgs too.
- Policy (`tenancy/policy.go`): `PlatformPolicy` and `OrgPolicy`, keyed
  `"METHOD pattern"`, HEAD filed under GET. Unknown write = deny; unknown
  platform GET = deny; unknown org-router GET = viewer. Runner principals only
  on `/workers/*` protocol routes of their org. `GET /doctor` is denied in
  hosted mode. `TestHostedPolicyCoversEveryRoute` fails on a route without
  an entry: add one when you add a route.
- Plan guard: a hosted org's planner refuses suite refs that are paths and
  agent/policy/matrix paths outside the org's project, symlinks included
  (`tenancy.CheckExperimentPaths`, `engine.WithExperimentGuard`).
- Hooks for other areas (`cmd/bifrost-bench/servetenancy.go`):
  `hostedAreas` (append a platform route area from your file's init()) and
  `orgLauncher(env, orgApp, ix)` (the launcher and WorkerAPI of one hosted
  org; unset = experiment writes 503). A hosted server opens no gateway
  and always wires `orgLauncher` (`-offline` changes nothing there):
  planning needs neither the gateway (no upstream preflight without the
  fault proxy client) nor Docker (image inspection only warns); runners
  resolve images and call providers.
- Gateway preflight deferred to runners: a hosted org's planner
  (`engine.WithGatewayDeferredToRunner`) does no gateway preflight and freezes
  `"gateway": {"name": "deferred", ..., "deferred_to_runner": true}` in the
  manifest (field `GatewayManifest.DeferredToRunner`, JSON
  `deferred_to_runner`, omitempty: other manifests hash as before) and no
  `gateway/*` part version. A runner re-plans with its own gateway preflight
  (refuses on failure, as before), adopts the deferred record and drops its
  gateway part for the comparison, then preflights its gateway again before
  every slot (no gateway, a failure or a hygiene violation releases the
  lease), runs the slot with its own record as the run's gateway settings
  (egress gate), and sends the record with the upload
  (`WorkerComplete.gateway`); the coordinator seals it in
  `remote/worker.json` `"gateway"`, loaded as `SealedRun.runner_gateway`.
  Verdict instrument `gateway_hygiene` (only on deferred manifests): fail on
  any runner violation, unknown with the count when a counted run has no
  record (or there is no counted run), else pass; the markdown report then
  drops its manifest-hygiene row. Tests: `engine.TestRemoteDeferredGateway`
  (+`Refusals`), `cmd TestHostedPlanDefersGatewayToRunner`.
- Part parity on a deferred manifest: the runner skips only the `gateway/*`
  parts (every other part must still match exactly, else the lease is
  refused); each run's runner record carries `part_version`
  (`gateway/<name>@<version>`), and instrument `gateway_versions` passes on
  one version, warns naming each version with its run count when counted
  runs came from different runner gateways (part or gateway version), and is
  unknown when a run records none.
- Images on a deferred manifest (Deploy area, `engine/remoteimages.go`): a
  hosted server plans without Docker, so its manifest freezes no
  `suite_images`, `task_images` or arm image ids. When the leased manifest is
  deferred and has neither map, the runner adopts the empty maps for the
  hash comparison (a manifest that has them keeps the exact check), checks
  the suite lock's fingerprints again before every slot (a lock that pins a
  local id or a registry digest still refuses another image: lease
  refused), resolves the ids the slot runs (suite roles, `task/<id>`,
  `agent/<arm>`) and sends them as `WorkerComplete.images`; the coordinator
  seals them in `remote/worker.json` `"images"`, loaded as
  `SealedRun.runner_images`. Instrument `runner_images` (deferred manifests
  only): pass on one id per role, warn listing role -> ids with run counts
  when counted runs used different ids for one role, unknown when a run
  records none. `manifestDiff` now also names suite_images, task_images,
  fingerprint_hash, task_identities, run_inputs and gateway.
  `fingerprint_hash` is the sha256 of the lock file's fingerprints section
  and `task_identities` hash task files, so neither differs by host for one
  checkout. Tests: `engine.TestRemoteDeferredGatewayImages` (+`ImageRefusals`).
- Org APIs (`handlers/orgs.go`, `handlers/members.go`): `GET/POST /orgs`,
  `GET /orgs/{org}`, `POST /orgs/{org}/rename` (admin), `POST
  /orgs/{org}/public-read` (owner), `GET /orgs/{org}/members` (admin), `POST
  /orgs/{org}/members/{user}/role|remove` (admin; only owners touch owners;
  last owner 409), `GET/POST /orgs/{org}/invitations`, `POST
  /orgs/{org}/invitations/{id}/revoke` (admin; through the auth service),
  `GET /orgs/{org}/audit?limit&before&action` (admin), `POST
  /orgs/{org}/leave` (any member, for themselves; last owner 409). Org
  answers carry `demo: true` (omitempty) for the server's public demo org
  (`demo.org` with public_read).
- CLI: `org create -slug <s> [-name] [-owner <email>] [-public-read]`, `org
  list`, `org import -org <slug|id> -from <runs dir>`: each experiment is
  copied to a staging folder in the org, every sealed run and the seals.log
  chain verified on the copy, then renamed in; present ones are skipped,
  failing ones refused and removed; then `db.SyncOrg`. The source is only
  read.

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

As built (Jobs area, 18 Nov; the code wins):

- `bench/jobs`: `Runner.Register(kind, Handler, opts...)` with
  `Handler func(ctx, *Job, Logger, progress func(any)) (any, error)`;
  options `WithValidator` (params checked and normalized per org before the
  job is queued; returns the job's target), `WithMaxAttempts`, `Internal`
  (not creatable through POST /jobs). Claims use `db.ClaimOne`. A lease is
  renewed by heartbeat; an expired lease is queued again (attempt counted on
  the next claim, `Job.Resume()` is `Attempt > 1`) up to the kind's maximum,
  or cancelled when a cancel was requested. The handler's context ends with
  cause `ErrCancelled` (cancel requested, polled), `ErrShutdown` (runner
  stopping: the job is handed back queued and the next runner resumes it) or
  `ErrLeaseLost`. Logs: first 500 and newest 1500 lines kept per job, the
  rest counted as dropped.
- Cancelled runs: no new outcome. A run the scheduler's owner stops
  (`engine.ErrRunCancelled`: a cancel or a stopping server) is sealed
  `aborted_infra`, so it never counts, and it uses none of the slot's three
  attempts, so a resume runs the slot again. Experiment state `cancelled`
  when the newest `experiment.run` job was cancelled with slots left.
- Kinds: `experiment.run` (internal: started by `POST /experiments/{id}/start`
  and `/resume`, cancelled by `/cancel`), `experiment.regate` (also
  `POST /experiments/{id}/regrade`), `suite.lint`, `suite.lock`,
  `index.rebuild`; local mode only (they start containers from the project):
  `suite.admit`, `agent.check`. `org.import` is not a job yet.

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

Built (docs/runners.md): package `bench/runners` (migration set `runners`,
tokens, verifier registered with `auth.RegisterRunnerVerifier`, runner rows),
`engine.RemoteHub` (one coordinator per org over `runs/orgs/<org_id>/`;
`RemoteOptions.OrgID` makes a coordinator hosted), `RunOptions.RemoteOnly`
(hosted launchers run no slot locally), `GET /workers/whoami`, admin routes
`/api/bench/runners[/{id}[/remove]]`, `/api/bench/runners/tokens[/{id}/revoke]`,
`/api/bench/runners/install.sh`; `cmd/bifrost-bench/runnerhosted.go` sets
`orgLauncher` (the jobs area may replace it and then sets `launcher.coord`
from `hostedOrgCoordinator` and `remoteOnly`).

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

As built (UI U1, 18 Nov; the code wins):

- Mode: `GET /auth/me` decides. `auth_mode: local` or a 404 (an older server)
  is local mode, exactly as before (write-token boxes, `Authorization:
  Bearer`). Hosted: the session cookie, `X-Heimdall-CSRF` (from `me.csrf`) on
  every mutation, no token boxes. A 200 with kind `anonymous` from
  `/auth/me` (no credential at all) or a 401 (a session cookie that names no
  live session) is a visitor who is not signed in: the UI reads `GET /auth/config` (sign-up policy, GitHub) and
  makes one anonymous read (`GET /experiments`); when that answers, the
  server serves a public demo and the visitor reads it under a "Public demo,
  read only" banner with no write controls; otherwise `/login?return_to=`.
  A hosted 401 elsewhere while signed in re-asks `/auth/me`, which ends at the
  login page. A hosted 403 shows "your role cannot do this" with the role
  needed (`components/platform/forbiddenNote.tsx`, also inside `LoadError`).
- Pages: `/login` (`?return_to`, `?error=` from the GitHub callback),
  `/signup` (`?invitation=`), `/invite/$token`, `/settings?tab=members|tokens|
  runners|audit|org`, `/jobs/$jobId`. Header: org switcher (`POST
  /auth/switch-org {org}`), jobs indicator and drawer (`GET /jobs`, hidden on
  404/401/403 and for demo visitors), account menu.
- Experiment actions: cancel and resume on the experiment progress card
  (`POST /experiments/{id}/cancel|resume`), regrade on the verdict page
  (`POST /experiments/{id}/regrade`, `useRegradeMutation`, 202 with the job).
- Contract: TypeScript types in `ui/lib/types/bench.ts`, key lists
  `handlers/testdata/contract/{Job,JobList,JobLogs,JobLogLine,AuthMe,
  AuthConfig,MeOrg,PlatformUser,InvitationPreview,ApiToken,ApiTokenList,
  ApiTokenCreated,Member,MemberList,Invitation,InvitationList,OrgInfo,OrgList,
  AuditEntry,AuditList,Runner,RunnerList,RunnerToken,RunnerTokenList,
  RunnerTokenCreated}.keys`; `handlers/platform_contract_test.go` checks the
  Go types against them, `ui/lib/testing/platformFixtures.test.ts` the UI's
  samples.

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
