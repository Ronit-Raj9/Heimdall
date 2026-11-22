# Your API as a task service (OpenAPI mocks)

Agents that call internal systems need those systems in the task, without
the real one. Give Heimdall the system's OpenAPI 3 file and it writes a
stateful stand-in service into the benchmark: the same HTTP routes, one MCP
tool per operation, over starting data each task seeds, with the final data
exported for `checks.yaml` state checks (docs/declarative-checks.md).

## Create it

On the website: the benchmark's Build tab, "Your API (OpenAPI)": upload the
file, name the service, preview the operations, then create it. Over the
API: `POST /api/bench/suites/{id}/mocks` with `{"id", "spec", "file_name"?,
"replace"?, "dry_run"?}`. From the command line:

```console
$ bifrost-bench mock openapi -suite support -id crm -spec crm-openapi.yaml
wrote benchmarks/support/services/crm: 7 operations over 2 collections
```

It writes `services/<id>/` (the generic server, its Dockerfile, `mock.json`
derived from the spec, and the spec itself). The folder is part of the
benchmark's lock, so editing it changes the version. Each operation is
classified from its method and path:

| Kind | Route | What the mock does |
|---|---|---|
| list | `GET /orders` | the collection, filtered by query fields |
| get | `GET /orders/{id}` | one item, 404 when missing |
| create | `POST /orders` | adds the body, making an id when missing |
| update | `PUT`/`PATCH /orders/{id}` | merges the body into the item |
| delete | `DELETE /orders/{id}` | removes the item |
| action | anything else | recorded in `_actions`, answered `{"ok": true}` or by a fixture |

A nested path such as `/customers/{customer_id}/tickets` filters by, and
stamps, the parent field on the child collection.

## Use it in a task

The response (and the command) print the `services:` entry a task adds and
an empty seed. Put the account the task is about in the task's
`seed/<id>.json`, for example `{"customers": [{"id": "C-1", "tier": "gold"}],
"tickets": []}`, then check the final data:

```yaml
checks:
  - id: ticket-opened
    state: {file: crm/state.json, path: "$.tickets[?(@.customer_id=='C-1')]", length: 1}
```

The readiness check builds the service image from the folder like any other
service.

## Record and replay

Every request the mock answers is appended to `calls.jsonl`, exported with
the state. `bifrost-bench mock fixtures -suite <suite> -id <id> -run <run
folder>` adds a run's recorded calls of the service to `mock.json` as fixed
answers, matched first by method, path and a body subset; rebuild the image
afterwards. `MOCK_MODE=replay` in the service's `env:` answers only from
fixtures (an unmatched request gets 501), for a strict replay of a recorded
session.
