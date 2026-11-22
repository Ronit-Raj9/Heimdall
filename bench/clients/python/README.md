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

## Public API v1 (0.2.0)

`heimdall.V1Client` speaks the versioned public API (`/api/v1`, `docs/api-v1.md`): agents and versions, compatibility, benchmarks, evaluations, estimates, report cards, the agent leaderboard, runs and trace export. On a hosted server pass an organization API token; its scope (`read`, `evaluate`, `admin`) must cover the calls (`evaluate` for `evaluate_and_wait`).

```python
import os
from heimdall import V1Client

hd = V1Client("https://heimdall.example.com", token=os.environ["HEIMDALL_TOKEN"], require_live=True)
print(hd.compatibility("support-bot")["benchmarks"])
est = hd.estimate_evaluation({"name": "nightly", "mode": "score", "pack": "support",
                              "arms": [{"label": "A", "agent": "support-bot", "model": "gpt-5.1"}]})
print(est["runs"], est["est_cost_usd"])

result = hd.evaluate_and_wait({"name": "nightly", "mode": "score", "pack": "support",
                               "arms": [{"label": "A", "agent": "support-bot", "model": "gpt-5.1"}]},
                              timeout=4 * 3600, on_status=lambda ev: print(ev["status"]))
card = result.report                        # heimdall.ReportCard (dataclasses)
print(result.succeeded, card.score("A"))
for b in card.benchmarks:
    print(b.benchmark, b.arms["A"].score, b.arms["A"].ci_low, b.arms["A"].ci_high)

open("trace.jsonl", "wb").write(hd.trace(hd.runs(limit=1)["runs"][0]["run_id"], format="jsonl").data)
```

`evaluate_and_wait` cancels the evaluation when it times out (`cancel_on_timeout=True`) and raises `HeimdallTimeout`; a failed or cancelled evaluation still returns, so check `result.succeeded`. `ReportCard` carries `arms`, `capabilities`, `benchmarks` (with the A/B `verdict` on two-arm evaluations), `reliability` per fault, `safety`, `overall` and `server_mode`.

Benchmarks as code (`docs/datasets-as-code.md`): `create_benchmark(id)`, `set_grader(id, "answer_match" | "judge", judge)`, `upsert_tasks(id, rows, mode, match, source, dry_run)` (each change a new version; the personal data policy runs first), `dataset_tasks(id)`, `delete_task(id, task)`, `dataset_versions(id)`, `dataset_version(id, n)`, `data_policy(id)`, `set_data_policy(id, pii_mode, pii_kinds, access, users)` and `import_from_platform({...})` (LangSmith, Langfuse, Braintrust, Datadog, with the org's secrets).

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

## Reporting agent events

`heimdall.events` lets an agent under test report its own internal steps (plans, tool calls and results, subagents, handoffs, memory and retrieval) in the format of `bench/spec/agent-event.schema.json`. The bench shows them as agent-reported evidence beside what it observed itself; they never raise a trusted metric or decide an outcome.

```python
from heimdall import events

plan = events.emit("plan", "fix the failing test", output="run the tests, read the failure, patch")
call = events.emit("tool_call", "run_shell", parent_id=plan, tool_call_id="call_1", input={"command": "pytest -q"})
events.emit("tool_result", "run_shell", parent_id=call, tool_call_id="call_1", output="1 failed", status="error")

with events.step("subagent_start", "reviewer", parent_id=plan) as sub:   # timed; status error if the block raises
    sub.output = "the fixture is stale"
```

Delivery follows the variables the bench sets in the agent container: `BENCH_EVENTS_URL` (a POST of JSON lines, 2 second timeout), else `BENCH_EVENTS_FILE` (one JSON object appended per line). A failed POST falls back to the file when both are set, and the URL is then skipped for 30 seconds. With neither set every call is a no-op that still returns an id, and nothing ever raises into the agent. `emit` returns the event id (a uuid4 in hex unless you pass `id`); nest events by passing it as `parent_id`. `input` and `output` take any JSON value and are cut to 8 KiB of text. `events.stdout_line(...)` returns the `@@heimdall-event ` line for an agent that can only print. Full example: `bench/clients/examples/agent_events.py`.

## Tests

```sh
cd bench/clients/python
python3 -m unittest            # unit tests against a local fake server; live tests skip
```

`tests/test_live_server.py` runs only with `HEIMDALL_URL` set; `bench/clients/contract_test.go` sets it against a real recorded server. `heimdall/models.py` is generated: `python3 scripts/gen_models.py --url http://127.0.0.1:8090/api/bench/openapi.json`.
