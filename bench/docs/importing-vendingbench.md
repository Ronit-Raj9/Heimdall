# Importing Prosus Vending Bench

`bifrost-bench import vendingbench` turns Prosus Vending Bench (https://github.com/ProsusAI/vending-bench, Apache-2.0, task version 3.0.0) into a suite. Upstream ships one Harbor task: the agent runs six vending machines in Amsterdam through MCP tools served by a `sim-server` sidecar, and the score is the completed run's final bank balance in EUR (0 when unfinished or bankrupt).

```sh
git clone --depth 1 https://github.com/ProsusAI/vending-bench datasets/vending-bench
bifrost-bench import vendingbench -from datasets/vending-bench -suite vending-bench
```

## Tasks

Ten tasks, matching upstream's published baseline matrix (`results/baselines/scripted-reference`): `vending-30d-seed1..5` (the default 30-day challenge) and `vending-365d-seed1..5` (`variants/full-year.toml`). The seed and config are written into the compose file (`VENDING_SEED`, `VENDING_CONFIG`). Agent timeout: 4 h for 30 days, 12 h for 365 days (upstream: 12 h).

## What changes

The harbor importer refuses three features this task uses, so the importer stages a rewritten copy per task and passes it to the harbor importer (`docs/importing-harbor.md` describes everything after that):

| Upstream | Here |
|---|---|
| `[[verifier.collect]]` hook POSTs `/verifier/finalize` in sim-server after the agent stops; `environment_mode = "separate"` | `test.sh` does the same POST first. Under Harbor conventions the snapshot verifier joins the run network after the agent has exited, so the sealing happens at the same moment; sim-server is never reachable by a later regrade |
| `[[environment.mcp_servers]]` in task.toml | `x-heimdall: {role: mcp, proxy: false}` on the sim-server service: the agent's CLI config names it (`docs/mcp-agents.md`) |
| `main` without `build` (Harbor's base file supplies it) | `build: .` |
| `solve.sh` installs apt packages, uv and fastmcp at run time | uv and a venv `/opt/vending-ref` with `fastmcp==2.11.3` are baked into the image; `solve.sh` runs the same `run_solution.py` from it |

The suite's `pass_threshold` is 1500 (the starting balance): a pass is a completed, profitable run. The reward recorded on every grade is the balance itself, so mean reward is comparable with upstream numbers for the same horizon.

## Limits

- The verifier is the snapshot verifier on the run network, not Harbor's separate container. The score itself comes from sim-server, which the agent cannot write to except through its tools.
- The baked venv with fastmcp is visible to the agent. It holds no task data.
- 365-day runs need thousands of tool calls; budget accordingly.
