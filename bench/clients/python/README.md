# heimdall-bench

Thin Python client for the Heimdall bench API (`/api/bench`). Standard library only (`urllib`), Python 3.9 or newer, typed with TypedDicts generated from the server's own OpenAPI description.

```sh
pip install ./bench/clients/python        # or put bench/clients/python on PYTHONPATH
```

```python
import os
from heimdall import Client

c = Client("http://127.0.0.1:8090", token=os.environ.get("BENCH_WRITE_TOKEN"))
plan = c.plan(open("experiments/smoke.yaml").read())   # YAML text, or a dict sent as JSON
started = c.run_experiment(open("experiments/smoke.yaml").read())
status = c.wait_for_experiment(started["exp_id"], timeout=3600)
decision, action, policy = c.check(started["exp_id"])
print(status["mode"], decision, action, policy and policy["decision"])
```

## Mode on every response

Every JSON method returns the server's object as a dict with a top-level `mode`: `live`, `recorded` or `synthetic`. Only `live` is a real measurement. Raw downloads (`run_patch`, `run_file`, `release_evidence`) return a `RawResponse` whose `mode` comes from the `X-Bench-Mode` header. `mode_of(response)` reads either. `Client(..., require_live=True)` raises `ModeError` on any answer that is not live, so a CI job cannot pass on a recorded demo.

Agent images can use `heimdall.FaultSession` for adapter fault probes. It is
pass-through when `BENCH_FAULT_*` is absent and fails closed when a configured
controller cannot be reached.

## Auth

`token` is sent as `Authorization: Bearer <token>`. The server requires its write token (`BENCH_WRITE_TOKEN`, printed once at start when unset) on every POST route: plan, create, start, audit and releases. Read routes need no token. The remote worker routes use a separate bearer (`BENCH_WORKER_TOKEN`) and are not wrapped here.

## Methods

| Area | Methods |
|---|---|
| Health | `health`, `doctor`, `catalog`, `openapi` |
| Suites, agents | `suites`, `suite_task(suite, task)`, `agents` |
| Experiments | `experiments`, `experiment(id)` (run status), `plan(exp)`, `create_experiment(exp)`, `start_experiment(id)`, `run_experiment(exp)`, `events(id, since)`, `grid(id)` |
| Analysis | `verdict(id, grade_source)`, `analysis(id)` (alias `diagnostics`), `triage(id)`, `audit(rows, alpha)` |
| Runs | `runs(exp_id)` (from the grid: the API has no run list route), `run`, `run_events`, `run_diff`, `run_patch`, `run_receipts`, `run_file`, `pair` |
| Releases | `leaderboard(**params)`, `releases`, `release(id)`, `create_release(exp_id, approver, note, override)`, `release_evidence(id)` |
| Other | `imports`, `workers` |
| CI | `wait_for_experiment(id, timeout, interval, on_status)`, `check(id) -> CheckResult` |

`check` returns a `CheckResult` that unpacks as `(decision, action, policy_result)` and also has `mode`, `policy_decision`, `blocked` (policy says block), `live` and the full `verdict` payload.

## Errors, timeouts, retries

A refused request raises `HeimdallError` with `status`, `type` (the server's error type: `not_found`, `unauthorized`, `unavailable` ...), `message` (the server's message) and `mode`. `HeimdallTimeout` covers a request with no answer within `timeout` seconds (default 30) and a `wait_for_experiment` that runs out. Reads retry `read_retries` times (default 2) on connection errors, 502 and 504. Writes are never retried: a start or a release is sent once.

## CI example

```yaml
# .github/workflows/release-gate.yml
jobs:
  gate:
    runs-on: self-hosted          # the bench server listens on loopback
    steps:
      - uses: actions/checkout@v4
      - run: python3 bench/clients/examples/ci_gate.py experiments/smoke.yaml
        env:
          HEIMDALL_URL: http://127.0.0.1:8090
          BENCH_WRITE_TOKEN: ${{ secrets.BENCH_WRITE_TOKEN }}
```

`examples/ci_gate.py` plans (and stops on planner errors), creates and starts the experiment, waits, then exits 1 when the release policy blocks, 2 when the experiment did not finish, 3 when the server is not live, 4 when a request failed. `--exp <id>` gates an experiment that already ran; `--allow-recorded` accepts a recorded server for demos.

The core of it:

```python
c = Client(url, token=token, require_live=True)
plan = c.plan(text)
if plan["errors"]:
    sys.exit(4)
exp_id = c.run_experiment(text)["exp_id"]
if c.wait_for_experiment(exp_id)["state"] != "done":
    sys.exit(2)
result = c.check(exp_id)
sys.exit(1 if result.blocked else 0)
```

## Tests

```sh
cd bench/clients/python
python3 -m unittest            # unit tests against a local fake server; live tests skip
```

`tests/test_live_server.py` runs only with `HEIMDALL_URL` set; `bench/clients/contract_test.go` sets it against a real recorded server. `heimdall/models.py` is generated: `python3 scripts/gen_models.py --url http://127.0.0.1:8090/api/bench/openapi.json`.
