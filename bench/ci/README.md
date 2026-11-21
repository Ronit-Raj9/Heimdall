# bench/ci: the agent release gate

Runs `bifrost-bench check` on a pull request and fails the build on a bad agent. The full guide is [`../docs/ci.md`](../docs/ci.md).

| File | What |
|---|---|
| `check.sh` | The gate. `recorded` mode replays a sealed experiment from the bundle you name (no default) on a temp copy; `live` mode runs an experiment file against a running stack. Exits with `check`'s code |
| `action.yml` | Composite action: set up Go, build the binary (`GOWORK=off`), verify the bundle's seals, run `check.sh`, upload the report |
| `ci.go`, `ci_test.go` | The seal guard: verifies every seal of the recorded bundle and proves a tampered run fails |

The workflow is `.github/workflows/bench-check.yml` at the repo root.

## Run it locally

From `bench/`:

```bash
# Recorded: no Docker, no gateway, no key. Builds the binary itself.
BENCH_CI_DATA_MODE=synthetic ci/check.sh recorded
echo "exit $?"

# Live: the stack must be up (make up) and its tokens in the environment.
BENCH_CI_ENV_FILE=deploy/.env ci/check.sh live experiments/codewins.reduced.yaml

# The seal guard.
GOWORK=off go test ./ci/
```

The report lands in `$RUNNER_TEMP/bench-check/report.md` (outside Actions: `${TMPDIR:-/tmp}/bench-check/report.md`; set `BENCH_CI_OUT` to move it), with `report.json` beside it.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Pass: no listed verdict, no policy block |
| 1 | Regressed (when `regressed` is in fail-on) |
| 2 | Inconclusive (when `inconclusive` is in fail-on) |
| 3 | Evaluation invalid (when `invalid` is in fail-on) |
| 4 | Infrastructure error: build failed, gateway down, store unreadable |
| 5 | Release policy decision `block` (with `-policy`) |
| 64 | Usage error: bad mode, missing experiment, bundle or policy file |

## Inputs

`check.sh` reads `BENCH_CI_*` variables; the header of `check.sh` lists every one. The action maps its inputs onto them:

| Action input | Variable | Default |
|---|---|---|
| `mode` | `BENCH_CI_MODE` | none: required, `recorded` or `live` |
| `experiment` | `BENCH_CI_EXPERIMENT` | none (live needs it) |
| `exp-id` | `BENCH_CI_EXP_ID` | the only experiment in the bundle |
| `bundle` | `BENCH_CI_BUNDLE` | none: recorded mode needs your own bundle folder |
| `data-mode` | `BENCH_CI_DATA_MODE` | `recorded` |
| `policy` | `BENCH_CI_POLICY` | `policies/default.yaml` when it exists and `check` has `-policy`; `none` turns it off |
| `fail-on` | `BENCH_CI_FAIL_ON` | `regressed,invalid` |
| `go-version` | none | the `go` line of `bench/go.mod` |

Outputs: `verdict`, `action`, `policy-decision`, `exit-code`, `report`.
