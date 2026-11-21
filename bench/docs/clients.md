# API clients and the OpenAPI description

Teams drive Heimdall from code and CI through the same HTTP API the UI uses. This guide covers the machine-readable description of that API and the two thin clients over it. Everything client-side lives in `bench/clients/` and can be removed by deleting that folder; the only Go additions are `handlers/openapi.go`, its test and one registration line in `server/server.go`.

For the public, versioned API (`/api/v1`, its token scopes and deprecation policy) and the SDKs' `V1Client` (0.2.0), see `docs/api-v1.md`; for the MCP server, `docs/mcp-server.md`. This guide covers the internal `/api/bench` clients.

| Piece | Where | What |
|---|---|---|
| API description | `GET /api/bench/openapi.json` | OpenAPI 3.1, built from the routes the server actually registered |
| Python client | `bench/clients/python/` | package `heimdall-bench`, import `heimdall`; standard library only, Python 3.9+ |
| TypeScript client | `bench/clients/typescript/` | package `@heimdall/bench-client`; zero runtime dependencies, global `fetch`, ESM, Node 18+ and browsers |
| CI gate examples | `bench/clients/examples/ci_gate.py`, `ci-gate.ts` | plan, run, wait, fail the job when the release policy blocks |
| Contract test | `bench/clients/contract_test.go` | serves the recorded bundle with the real handlers and runs both clients against it |

## The OpenAPI description

`curl -s http://127.0.0.1:8090/api/bench/openapi.json` returns an OpenAPI 3.1 document. It is built on the first request from two sources:

- The routes come from the router itself (`router.List()`), read after every handler has registered, so the document lists exactly what this server serves. Only `/api/` routes are described; the UI routes are not.
- Each route's summary, tag, auth, query parameters and body types come from one table, `handlers.APIRoutes` in `handlers/openapi.go`. The request and response schemas are derived from the Go types by reflection (JSON tags, `omitempty` for optional keys, pointers as nullable, `time.Time` as `date-time`), so they cannot drift from the structs. Two types with their own JSON encoding (`TaskSelector`, `MatrixModel`) have hand-written schemas.

Every JSON response schema is `allOf` the payload and `ModeField` (`mode`: `live`, `recorded` or `synthetic`), because the server adds `mode` to every JSON answer. Raw answers (a run file, an artifact, the evidence zip, the plain patch) carry the mode in the `X-Bench-Mode` header. The document itself is plain OpenAPI, so its mode is in that header and in the root extension `x-bench-mode`, not in a `mode` key that would make the document invalid. Write routes declare the `writeToken` bearer scheme and the worker routes the `workerToken` scheme.

A registered route missing from the table still appears, as a bare operation tagged `undocumented`, and is listed under `x-bench-undocumented`. `TestOpenAPICoversEveryRoute` (`handlers/openapi_test.go`) builds the real server and fails, naming each route, while that list is not empty; `TestAPIRoutesAreRegistered` fails on a table entry for a route the server no longer serves. Adding a route therefore means adding one line to `APIRoutes`:

```go
{Method: "GET", Path: APIPrefix + "/things/{id}", Tag: "things", Summary: "One thing", Response: schemas.Thing{}},
```

`TestOpenAPISchemasMatchContract` also checks the derived schema of every type that has a contract key list (`handlers/testdata/contract/<Type>.keys`): required keys must agree.

## Mode, auth, errors

- **Mode.** Both clients return the server's object with its `mode`; raw downloads expose the header's mode. `require_live=True` (Python) or `{ requireLive: true }` (TypeScript) turns every non-live answer into a `ModeError`, so a CI job cannot pass on a recorded demo. `serve -offline` never answers live (a live bench.yaml is served as recorded), and the verdict route carries the analysis's own mode, so a smoke experiment on a live server answers synthetic.
- **Auth, local mode** (`auth.mode` unset, the default). Every POST route requires the write token, `Authorization: Bearer <BENCH_WRITE_TOKEN>` (`lib.RequireWriteToken`; the server prints a generated token once when the variable is unset), and the worker routes require `BENCH_WORKER_TOKEN`. Read routes need nothing beyond the loopback host check.
- **Auth, hosted mode** (`auth.mode: hosted`, `docs/auth.md`). There is no shared write token. Reads and writes need a signed-in session (cookie plus CSRF value) or an API token (`Authorization: Bearer <token>`, created under `/api/bench/auth/tokens`), and each route checks the caller's role in the current organization. Sign-in, signup, `auth/config` and invitation lookups are open to anyone. Hosted runners use their own runner tokens (`docs/auth.md`, `docs/remote-execution.md`).
- The clients take one optional bearer token and send it on every request: the local write token, or a hosted API token. The worker and runner routes are not wrapped.
- **Errors.** The server's one error shape `{"mode", "error": {"message", "type"}}` becomes `HeimdallError` with `status`, `type`, the server message and `mode`. A recorded server answers plan, create and start with `503 unavailable` (no launcher).
- **Timeouts and retries.** Every request has a timeout (30 s default). Reads retry on connection errors, 502 and 504; writes are never retried.

## Method surface

| Need | Python | TypeScript | Route |
|---|---|---|---|
| Suites | `suites()`, `suite_task(s, t)` | `suites()`, `suiteTask(s, t)` | `GET /suites`, `/suites/{id}/tasks/{task}` |
| Agents | `agents()` | `agents()` | `GET /agents` |
| Experiments | `experiments()`, `experiment(id)` | `experiments()`, `experiment(id)` | `GET /experiments`, `/experiments/{id}` |
| Plan | `plan(exp)` | `plan(exp)` | `POST /experiments/plan` |
| Start a run | `create_experiment`, `start_experiment`, `run_experiment` | `createExperiment`, `startExperiment`, `runExperiment` | `POST /experiments`, `/experiments/{id}/start` |
| Run status | `experiment(id)`, `events(id, since)` | `experiment(id)`, `events(id, since)` | `GET /experiments/{id}`, `/events` |
| Runs | `runs(exp)`, `run`, `run_diff`, `run_patch`, `run_receipts`, `run_events`, `run_file`, `pair` | same in camelCase | `GET /experiments/{id}/grid`, `/runs/{id}/...`, `/pairs/{id}` |
| Run list (across experiments) | none yet: call the route directly | none yet | `GET /runs?exp=&suite=&arm=&task=&condition=&outcome=&track=&limit=&offset=` |
| Verdict, analysis | `verdict(id)`, `analysis(id)`, `triage(id)`, `audit(rows)` | `verdict`, `analysis`, `triage`, `audit` | `GET /experiments/{id}/verdict`, `/diagnostics`, `/triage`, `POST /audit` |
| Leaderboard | `leaderboard(**params)` | `leaderboard(params)` | `GET /leaderboard` |
| Releases | `releases`, `release`, `create_release`, `release_evidence` | same in camelCase | `/releases...` |
| Imports, workers | `imports()`, `workers()` | `imports()`, `workers()` | `GET /imports`, `/workers` |
| CI | `wait_for_experiment(id, timeout, interval)`, `check(id)` | `waitForExperiment(id, opts)`, `check(id)` | |

`runs(exp_id)` reads one experiment's grid and returns every slot that has a run. The server also has a cross-experiment run list, `GET /api/bench/runs` (filters above, paged), which the clients do not wrap yet. `check` returns the verdict's decision (`improved`, `regressed`, `inconclusive`, `invalid`), its action (`ship`, `hold`, `no_decision`, `fix_harness`) and the release policy result; it unpacks as `(decision, action, policy_result)` and has `blocked` (policy says `block`), `policy_decision` and `mode`.

## Types

- **Python** `heimdall/models.py` is generated from the OpenAPI document by `scripts/gen_models.py`: one TypedDict per component (required and optional keys kept apart in the Python 3.9 way), plus a `<Type>Response` that adds `mode` for every type a route answers with.
- **TypeScript** `src/types.ts` is a generated copy of the UI's `bench/ui/lib/types/bench.ts`, the hand-kept mirror of the Go types that the UI's own client reads; `scripts/sync-types.mjs` refreshes it. The UI's file is not changed and the UI gains no dependency.

## CI

Demo against the shipped smoke manifest. It sets `smoke: true`, so its verdict is synthetic even on a live server and a strict-live gate refuses it with exit 3; the demo therefore passes `--allow-recorded`:

```sh
HEIMDALL_URL=http://127.0.0.1:8090 BENCH_WRITE_TOKEN=... python3 bench/clients/examples/ci_gate.py experiments/smoke.yaml --allow-recorded
node --experimental-strip-types bench/clients/examples/ci-gate.ts experiments/smoke.yaml --allow-recorded
```

Production gate: point it at your own non-smoke experiment and leave `--allow-recorded` off, so anything but a live verdict fails the job:

```sh
HEIMDALL_URL=http://127.0.0.1:8090 BENCH_WRITE_TOKEN=... python3 bench/clients/examples/ci_gate.py experiments/my-release.yaml
```

Both examples plan (and stop on planner errors), create and start, wait, then exit 0 when the gate passes, 1 when the verdict is in `--fail-on` (default `regressed,invalid`, as `bifrost-bench check`) or the release policy blocks (`--fail-on-warn` also fails on warn; `--policy-only` ignores the verdict), 2 when the experiment did not finish, 3 when the server is not live, 4 when the plan was refused or a request failed. `--exp <id>` gates an experiment that already ran. Against the recorded bundle:

```text
$ python3 bench/clients/examples/ci_gate.py --exp e_pilot_mixed_nc93 --allow-recorded
server http://127.0.0.1:38617, mode recorded
verdict inconclusive, action no_decision, policy warn (mode recorded)
  unknown false-completion-ceiling: <= 0.05 5 observed runs of 5, fewer than min_n 10 (0 unknown); needs review: run more tasks or trials
gate passed
$ python3 bench/clients/examples/ci_gate.py --exp e_pilot_mixed_nc93
refusing to gate: server mode is 'recorded', not live: refusing to treat it as a real result (pass --allow-recorded for a demo)
```

The existing `bifrost-bench check` composite action (`docs/ci.md`) gates by re-analyzing run folders on the CI machine; these clients gate through a running server instead.

## Tests

```sh
cd bench && GOWORK=off go test ./handlers/ -run 'OpenAPI|APIRoutes'   # description covers every route
cd bench && GOWORK=off go test ./clients/                             # both clients against a real recorded server
cd bench/clients/python && python3 -m unittest                        # unit tests, fake server
cd bench/clients/typescript && node --experimental-strip-types --test test/client.test.ts
cd bench/clients/typescript && ../../ui/node_modules/.bin/tsc -p tsconfig.json --noEmit
```

`go test ./clients/` builds `bifrost-bench`, copies `testdata/recorded` into a temp runs root (the analyzer writes analysis folders there; the bundle is never touched), runs `serve -offline` in recorded mode on a free loopback port with a fresh write token and a clean environment, then for each language: checks the generated types are current, runs the unit tests and the live smoke test (`tests/test_live_server.py`, `test/live.test.ts`: every read route the client wraps, the mode on each answer, the 401 and 503 error shapes, and that each client route appears in `openapi.json`), runs the CI gate example (passes with `--allow-recorded`, exit 3 without), and for TypeScript runs `tsc` when `bench/ui` is installed. A missing `python3` or a node older than 22.6 skips that language; `-short` skips the test.

When the Go types or `bench.ts` change, the generated client types go stale and the contract test fails with the command to fix them. `HEIMDALL_REGENERATE=1 GOWORK=off go test ./clients/` rewrites both instead.

## Limits

- The clients wrap the experiment, run, analysis, release, catalog and listing routes. Streaming (`text/event-stream` on `/experiments/{id}/events`), artifact downloads and the worker protocol are described in the OpenAPI document but not wrapped; use the polling `events()` and the worker's own client.
- The TypeScript tests run `.ts` directly with Node's type stripping (22.6+); the package itself targets Node 18+ once built with `npm run build`.
- Neither package is published to a registry; install from the repository.
