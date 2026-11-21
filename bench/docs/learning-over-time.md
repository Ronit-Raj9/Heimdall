# Learning over time and persistence

Most benchmarks score each task on its own, with a fresh agent. These options measure what happens across tasks and across time: does the agent get better as it remembers, does it notice that the world changed, can it pick up after a crash, and does it finish well when the money runs out.

## Stream mode: memory kept from task to task

```yaml
stream:
  memory: carry        # carry | fresh
  order: seeded        # suite (default) | seeded: one shuffle from the experiment seed, the same for every agent
  fresh_arm: B         # optional: an agent ordered the same way that never keeps memory (forward transfer control)
  replay: 2            # optional: run the first 2 tasks again at the end (backward transfer)
  threshold: 0.8       # optional: the success rate "tasks to reach target" waits for
```

The runs of each agent, fault and trial run one task after another. With `memory: carry` the agent's memory volumes (`/memory`, given as `BENCH_MEMORY_DIR`, and the agent's own `memory.paths`) are archived into `memory.tar` in the run folder after each run, sealed with it, and unpacked into the next run of the chain before it starts. `stream.json` in each run folder records its place, the run its memory came from and the archive hashes. A run whose previous run left no archive starts empty and says why. Stream runs only on local workers; adaptive trials and team tasks are refused.

An evaluation takes `"stream": {"memory": "carry"}`. In score mode it adds a visible control arm, `<agent> (no memory)`: the same agent and model, ordered the same way, always starting empty (it carries the `HEIMDALL_STREAM_CONTROL` marker env var, so it is the same agent version). It is the forward transfer control, the estimate counts its runs, the report card names it under Learning over time, and the leaderboards leave it out. An evaluation never takes `fresh_arm` itself.

The report card's **Learning over time** section shows, per benchmark:

| Column | Metric | Meaning |
| --- | --- | --- |
| Average success | `aulc` | Normalized area under the learning curve (first-pass runs). |
| Gain start to end | `final_gain` | Success in the last third of the sequence minus the first third. |
| Kept on old tasks | `bwt` | Replay success minus first-pass success on the same tasks (negative: forgetting). |
| Gain from memory | `fwt` | Success with memory minus the fresh control on the same tasks, after the first task. |
| Tasks to reach target | `episodes_to_threshold` | First place where the rolling mean over 3 tasks reaches the threshold. |

Trends include held-out runs when the server config sets `held_out.visible`.

## World clock: time passes between sessions

```yaml
# task.yaml of a multi-session task
world:
  start_date: 2026-01-05
  session_gap: 7d
  events:
    - {after_session: 1, service: shop, action: price_change}
```

Session n runs at `start_date + (n-1) x session_gap`, given to the agent as `BENCH_DATE`. Before each later session the engine POSTs `/_world/tick` to every task and suite service with a port, straight on the run network (never through the tool proxy, so the agent cannot call it), with `{"session": n, "date": ..., "actions": [...]}`. A service named by an event must answer 2xx or the run fails; other services may ignore the hook. Every tick is recorded in `world.json`. Graders then check that the agent used the new state, not what it remembered.

## Crash and resume: the `process` fault

```yaml
layer: process
apply:
  process: {at_model_call: 4}   # or after_s: 60; session: <id> on a multi-session task
```

The engine sends SIGKILL to the agent container once the run made that many model calls (counted in the fault proxy ledger) or after that many seconds, then starts the agent again on the same workspace and memory with `BENCH_RESUME=1` and the wall time it had left. Both outputs are kept as one stream; `process_kill.json` records the kill. Metric `resumed_without_redo` is 1 when no tool call that succeeded before the kill was repeated with the same arguments after it (a heuristic: an intended repeat read counts too).

## Context pressure: the `inflate` tool fault

`layer: tool`, `apply.tool: {mode: inflate, tokens: 20000}` appends that many tokens of plausible filler to real tool results. Pair it with a constraint stated early in the instruction and the existing violation gates.

## Budget disclosure and the budget squeeze

`budget: {disclose: true}` passes the agent `BENCH_BUDGET_USD` (the per-run cap, or the experiment total divided by its runs), `BENCH_DEADLINE` (RFC 3339) and `BENCH_MAX_TOOL_CALLS` when set.

The gateway fault `apply.fault: {mode: budget_squeeze, call: 5, grace_calls: 1}` lets calls up to 5 through, then one more with header `x-bench-budget-remaining-calls`, then answers 402 "budget exhausted" in the provider's own error format. Metrics:

- `graceful_finish`: the run passed, or stopped without claiming success and left partial work.
- `cost_normalized_success`: verified success divided by the run's cost in dollars (on every run).

The report card's **Persistence** section shows both, with `resumed_without_redo`.
