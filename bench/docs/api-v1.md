# Public API v1

`/api/v1` is the versioned, public Heimdall API: the part of the HTTP API that scripts, CI jobs, the SDKs and the MCP server build on. It is a stable subset of the internal `/api/bench` API the web UI uses. `/api/bench` may change in any release; `/api/v1` changes only under the policy below.

| Piece | Where |
|---|---|
| OpenAPI 3.1 document | `GET /api/v1/openapi.json` (no credential needed) |
| Route table | `handlers.V1Routes` in `bench/handlers/apiv1.go` |
| Python SDK | `bench/clients/python` (`heimdall-bench` 0.2.0, `heimdall.v1.V1Client`) |
| TypeScript SDK | `bench/clients/typescript` (`@heimdall/bench-client` 0.2.0, `V1Client`) |
| MCP server | `bifrost-bench mcp serve` (`docs/mcp-server.md`) |

## How it is served

Each v1 route is an alias of one bench route. The `V1Alias` middleware rewrites the request path (`/api/v1/benchmarks` to `/api/bench/suites`, say) before routing, so the v1 route runs the same handler, the same organization policy, the same role check and the same token scope check as its bench route. Every v1 answer carries `X-Heimdall-API-Version: 1`. A path under `/api/v1` that is not in the table answers 404, even when a bench route of that name exists.

| v1 route | Served by |
|---|---|
| `GET /api/v1/agents` | `GET /api/bench/agents` |
| `GET, POST /api/v1/agents/{id}/versions`, `GET /api/v1/agents/{id}/versions/{n}` | the same under `/api/bench` |
| `GET /api/v1/agents/{id}/compatibility` | the same |
| `GET /api/v1/agents/{id}/report-card` | the same |
| `GET /api/v1/connect/failures`, `POST /api/v1/connect/detect`, `POST /api/v1/connect/agents`, `POST /api/v1/connect/verify`, `GET /api/v1/connect/verify/{id}`, `POST /api/v1/connect/first-look` | the same (the Connect box: detect what was pasted, save it as an agent, verify it row by row with the agent.verify job, plan the first look) |
| `GET /api/v1/benchmarks` | `GET /api/bench/suites` |
| `GET /api/v1/benchmarks/catalog` | `GET /api/bench/benchmarks/catalog` |
| `GET /api/v1/benchmarks/packs` | `GET /api/bench/evaluation-packs` |
| `GET /api/v1/benchmarks/{id}/tasks/{task}` | `GET /api/bench/suites/{id}/tasks/{task}` |
| `GET, POST /api/v1/evaluations`, `GET /api/v1/evaluations/{id}`, `POST /api/v1/evaluations/{id}/cancel`, `POST /api/v1/evaluations/estimate` | the same |
| `GET /api/v1/evaluations/{id}/report`, `GET /api/v1/reports/{id}` | `GET /api/bench/evaluations/{id}/report` |
| `POST /api/v1/estimate` | `POST /api/bench/estimate` |
| `GET /api/v1/experiments/{id}/triage` | the same (failure groups behind an evaluation's benchmark) |
| `GET /api/v1/leaderboard/agents` | the same |
| `GET /api/v1/runs`, `GET /api/v1/runs/{id}`, `GET /api/v1/runs/{id}/steps` | the same |
| `GET /api/v1/runs/{id}/trace`, `GET /api/v1/runs/{id}/otlp` | the same (trace export: JSON, JSONL or OTLP) |

Every JSON answer is the payload plus a top-level `mode` (`live`, `recorded`, `synthetic`); only `live` is a real measurement. Errors are `{"mode", "error": {"message", "type"}}`.

## Credentials and token scopes

Local mode: writes need `Authorization: Bearer <BENCH_WRITE_TOKEN>`; reads need nothing beyond the loopback host check.

Hosted mode: send an organization API token, `Authorization: Bearer hm_...`, made under Settings, API tokens. A token is limited twice: by its role (viewer, member, admin, owner; never above its creator's) and by its scope:

| Scope | Allows |
|---|---|
| `read` | every read the role allows |
| `evaluate` | `read`, plus `POST /evaluations`, `/evaluations/estimate`, `/evaluations/{id}/cancel`, `/estimate`, registering an agent version, and creating, planning, starting, cancelling and resuming experiments |
| `admin` | everything the role allows |

A read on a route that needs role admin also needs scope `admin`. Each operation in the OpenAPI document names the scope it needs in `x-heimdall-token-scope`. A token created without a scope gets `admin`; tokens created before scopes existed are limited by their role alone. A refused scope answers 403 `forbidden` naming the scope required.

## Versioning and deprecation policy

1. **Additive changes only within v1.** A v1 release may add routes, optional request fields, response fields and enum values. Clients must ignore response fields they do not know.
2. **No silent breaking change.** Removing or renaming a route or field, making an optional request field required, or changing a field's meaning or type happens only in a new major version (`/api/v2`), never in v1.
3. **Deprecation before removal.** A v1 route is deprecated at least 6 months before it stops working. From the day it is deprecated, the route answers with `Deprecation: true` and a `Sunset: <HTTP date>` header, the OpenAPI document marks the operation `deprecated: true` with `x-heimdall-sunset`, and the release notes name the replacement.
4. **Overlap.** When `/api/v2` ships, `/api/v1` keeps working for at least 12 months.
5. **Security exception.** A route may change or close sooner when keeping it would expose customer data; the release notes say so.
6. **What is not covered.** `/api/bench` (the UI's internal API), the worker and runner routes, and the `x-` extensions of the OpenAPI document may change in any release.

Adding a route to v1 means one line in `V1Routes` plus a `RouteDoc` for its bench route; `TestV1RoutesAreServed` (`server/apiv1_test.go`) fails when the bench route is not registered, has no policy entry or no description.
