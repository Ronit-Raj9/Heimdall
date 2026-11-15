# Agent steps and execution evidence

`steps` counts model turns. That says how often the agent asked the model,
not what it did between turns: a turn can run five commands, edit three
files and call two tools, and a framework can act with no model-requested
tool call at all. This page defines the numbers that count those actions,
the witnesses each number rests on, how much each witness can be trusted,
and what no observer outside an agent can ever see.

Code: `schemas/steps.go` (records and constants), `environments/procwatch.py`
(the process observer), the metrics in `metrics/`, the timeline in
`evidence.json` and `steps.jsonl`. API: `GET /api/bench/runs/{id}/steps`.

## The numbers

Existing metrics keep their names. `steps` is still model turns, so runs
recorded before this page compare as before; only `tool_calls_executed`
changed, as a fix (version 1.1.0).

| Metric | What it counts | Witness | Evidence level | Unknown when |
|---|---|---|---|---|
| `steps` ("Steps (model turns)") | Logical model requests the model answered. Retries and fallbacks of one request count once; a request that only ever failed is not a turn | Gateway | gateway | The gateway log is missing or incomplete |
| `model_attempts` | Physical upstream attempts on message rows: every attempt in Bifrost's attempt trail plus every fallback row (a row without a trail is one attempt). The retries `steps` collapses stay visible here | Gateway | gateway | The gateway log is missing or incomplete |
| `tool_calls` | Tool calls the model requested, by distinct tool-call id | Gateway (agent stream when the log is incomplete) | gateway, or native | Neither a complete log nor parsed stream tool calls |
| `tool_calls_executed` (1.1.0) | Tool calls with an execution witness, deduplicated: each requested call with a witness counts once; tool proxy ledger rows (tool listings excluded), shim calls and agent-stream tool results that match no requested call (and, for a shim call, no command) count one each | Tool proxy, shim, process observer, workspace observer, agent stream | boundary when any trusted witness contributed, native when only agent-stream tool results did | Nothing was counted while calls were requested, or no witness source was available |
| `tool_calls_unwitnessed` | Requested calls with no witness at all (`no_witness`) | Gateway, checked against every witness | gateway | The gateway log is missing or incomplete, the run has no step timeline, or no witness source was available (no observer ran, no ledger, no shim log, no agent stream) |
| `commands_executed` | Command trees the process observer saw: a process whose parent is an agent runtime process (or a parent the observer never saw) counts once, together with all its descendants. `bash -c "ls \| grep x"` is 1 | Process observer | boundary | The process observer was switched off, has no status, or was missing in any session; not applicable to remote agents |
| `files_changed_events` | Workspace observer events after the exclusions below, dropped ones included: the count is exact (recorded plus dropped) | Workspace observer | boundary | The workspace observer was switched off, has no status, or was missing in any session; not applicable to remote agents |
| `agent_steps` | Every agent action with a trusted or native witness, each action once (rule below) | All of them | boundary, or native when an agent-stream witness was used | The gateway log is missing or incomplete, the run has no step timeline, or no witness beyond the gateway exists and no observer ran |

**Versions.** `tool_calls_executed` is version 1.1.0: on a run with a step
timeline it counts the timeline as above. A run without one (recorded before
the timeline existed) is computed as version 1.0.0 did: shim calls, else
agent-stream tool results.

Unknown is never shown as zero. A run whose observers were off has unknown
`commands_executed`, `files_changed_events` and, unless another witness
saw something, unknown `agent_steps`.

### Agent runtime processes

`commands_executed` and `agent_steps` count what the agent started, not the
agent itself. The agent runtime processes of a session are the container's
PID 1 and every process that started before the session's first model
response ended and was still running at the session's last model response:
the CLI, its interpreter, a language server it keeps open. Without gateway
timings, they are the processes that started within 2 s of the first process
and were still running at the end. A process whose parent is a runtime
process, or whose parent the observer never saw, roots a command tree.

## Witnesses

Every row of the timeline names the witness it came from (`source`).

| Source | Trust | What it records | Where it runs |
|---|---|---|---|
| `gateway` | trusted | Model turns, physical attempts, retries, fallbacks, and the tool calls the model requested (name, arguments hash, target) | Bifrost's request log, outside the agent's reach |
| `tool_proxy` | trusted | Every MCP, HTTP and external API tool call actually made through the run's tool proxy (`tools.jsonl`) | Per-run proxy container ([tool-proxy.md](tool-proxy.md)) |
| `process_observer` | trusted | The processes the agent started, with argv, working directory and exact start time; misses of very short ones are bounded and reported | Helper container in the agent's PID namespace |
| `workspace_observer` | trusted | Create, modify, delete and rename in the workspace volume | Helper container with the workspace mounted read-only |
| `network_observer` | trusted | DNS queries, connection attempts, plain HTTP request lines. Not in the timeline: netwatch records carry no timestamps, so they cannot be placed in a turn | The netwatch helper; it stays `agent check` only ([adding-an-agent.md](adding-an-agent.md)) |
| `shim` | boundary | Calls to the bench's tool shims (`pytest` and others under a probe) | Inside the agent's container, so the agent could tamper with its log |
| `agent_stream` | agent-reported | The parsed native stream of Codex, Claude Code and OpenCode: tool calls, tool results, messages | Written by the agent itself; the weakest witness |

The two observers are new and universal: they run beside every local agent
container, whatever the framework, with no adapter code.

### Process observer

A trusted helper container joins the agent container's PID namespace
(`--pid=container:<agent>`). It runs as root with every capability dropped
except `CAP_SYS_PTRACE` (needed to read another process's working directory
and the exit code of a zombie), with no network, a read-only root file
system, pids and memory limits, and labels that tie it to the run, so
`Destroy` and the janitor remove it with the run's other containers. Its
script (`environments/procwatch.py`) arrives on stdin, so its own argv is
just `python3 -u -`. The docker flags (`environments/observers.go`):
`--user 0 --cap-drop ALL --cap-add SYS_PTRACE --security-opt
no-new-privileges --network none --read-only --pids-limit 8 --memory 128m
--log-driver none`. The bench creates the observer container right after
the agent container, before the agent starts, and starts it the moment the
agent's namespace exists (a start that comes too early fails with "is not
running" and is retried every 5 ms), so the gap is one `docker start`. It
stops it after the agent ends.

It polls `/proc` every 20 ms (`observers.interval_ms`) and deduplicates
processes by (pid, kernel start time), so a reused pid is a new process. For
each process it records:

- pid, ppid, uid, `comm`;
- argv, capped at 64 arguments of 2 KiB each (`argv_truncated` marks a cut),
  with secrets redacted by the bench: the bench's redaction rules
  (`utils/redact.go`) plus the value of the run's gateway key. Redaction is
  done by the bench when it reads the observer's output (`comm` and the
  working directory are redacted the same way); the observer never holds the
  key;
- the working directory;
- the start time from the kernel, exact to the clock tick even when the
  process was found late;
- the end time: the first poll that no longer saw the process, so an upper
  bound;
- the exit code, only when the process was caught as a zombie (reaped
  between two polls, it has none). A zombie killed by a signal gets exit code
  128 plus the signal number, as a shell reports it;
- `running_at_end` when it was still running when the session ended; its end
  time is then the agent's end.

**Coverage is bounded, never hidden.** Linux hands out PIDs in order within
a PID namespace and counts them in `/proc/sys/kernel/ns_last_pid`. The
observer reads that counter and reports per session `pids_allocated` (the
value of `ns_last_pid`: every PID handed out in the agent's namespace since
it was created, threads included) against `pids_seen` (how many of them it
saw as a process or a thread). The difference bounds the processes it
missed. The observer joins a moment after the agent starts, so the agent's
first moments can be missed too: in three real-Docker runs of
`TestProcessObserverStartupGap` (a command every 100 ms from the agent's
first moment) the first command caught started 200 to 500 ms in, on a busy
host. Because the counter starts at the namespace's creation, those PIDs are
in it as well. An agent that exits before the observer can join (well under
a second) has its process evidence marked missing with the reason "the agent
ended before the process observer could join it". If the observer
never reported the counter, the coverage note says the miss count is not
bounded.

**Why it polls.** An unprivileged exec event stream does not exist on Linux.
The proc connector needs `CAP_NET_ADMIN`, fanotify exec events need
`CAP_SYS_ADMIN`, and eBPF tracing needs host-level capabilities. Inotify on
the agent's binaries through `/proc/1/root` was tested and receives no
events through overlayfs. Polling with a bounded, reported miss count is the
honest option that needs no extra privilege on the agent's side.

### Workspace observer

A trusted helper container mounts the workspace volume read-only at `/w` and
watches it with inotify (through `ctypes`, with the `python3` of the helper
image), one watch per folder, added recursively as folders appear. It starts
before the agent and stops after the agent exits. The docker flags:
`--user 0 --cap-drop ALL --cap-add DAC_READ_SEARCH --security-opt
no-new-privileges --network none --read-only --pids-limit 8 --memory 128m
--log-driver none`, with the volume mounted `:ro`.

Events and their rules:

- `create`, `modify`, `delete`, `rename`, each with a timestamp, the path
  relative to the workspace root and the size after the change (when it
  could be read); `dir` marks a folder.
- `modify` is a write followed by a close, or a file still open 2 s after a
  write. A new file written and closed is one `create` with its final size,
  not a create plus modifies.
- A rename pairs the moved-from and moved-to halves by inotify cookie. A file
  moved out of the workspace is a `delete`; a file moved in is a `create`.
- Left out: everything under `.git/`, `__pycache__`, `.pytest_cache`,
  `.mypy_cache`, `.ruff_cache`, `node_modules`, `.cache`, and `*.pyc` files.

Caps: 20,000 events per session (the rest are counted in `dropped`, so
`files_changed_events` stays exact) and 16,384 watched folders (beyond that,
folders are not watched and the coverage note says so). A kernel queue
overflow is noted in the coverage too.

It sees only the workspace volume. Writes elsewhere (the agent's home
tmpfs, `/tmp`, the root file system of a snapshot-graded task) are not
observed. File reads are not recorded at all: a read changes nothing.

### Observer status

Neither observer ever fails a run. Each observer that is on writes one
`ObserverStatus` per session to `observers.json`:

| Field | Meaning |
|---|---|
| `observer` | `process` or `workspace` |
| `state` | `observed` (ran for the whole session), `off` (not applicable: a remote agent), `missing` (on, but could not start, or stopped more than 1.5 s before the agent ended) |
| `reason` | Why it is off or missing |
| `mode` | `proc_poll` or `inotify` |
| `interval_ms` | The process observer's poll interval |
| `observer_coverage` | What it could not see (queue overflow, watch limit reached) |
| `records`, `dropped` | Records kept, records dropped at a cap |
| `pids_allocated`, `pids_seen` | The process observer's coverage counter |

A missing observer makes the metrics that need it unknown with its reason,
never zero. Remote agents (`remote_http`) get `off` for both: their actions
happen outside the bench, so there is nothing to observe, and the metrics
that need an observer are not applicable. An observer switched off in config
writes no status at all, like a run recorded before the observers existed;
the metrics that need it are then unknown ("switched off, or run recorded
before observers").

## The step timeline

A run writes these files into its folder, sealed with the run
(`processes.jsonl` and `fsevents.jsonl` when the observer ran):

| File | Contents |
|---|---|
| `steps.jsonl` | The unified timeline: one `StepRecord` per line, ordered by time |
| `processes.jsonl` | One `ProcessRecord` per process |
| `fsevents.jsonl` | One `FileEvent` per workspace change |
| `observers.json` | One `ObserverStatus` per observer and session |

A multi-session run also writes `processes.<n>.jsonl` and `fsevents.<n>.jsonl`
per session (n 1-based); the unindexed files hold every session in order.
`evidence.json` carries the same records (`processes`, `file_events`,
`observers`, `steps`).

- **API.** `GET /api/bench/runs/{id}/steps` answers `run_id`, `available`
  (false for a run recorded before the timeline, with empty lists), `steps`,
  `observers`, `truncated` (more than 20,000 rows or 16 MiB; the first rows
  are sent, never a cut row) and `skipped_lines` (lines of `steps.jsonl` that
  did not parse).
- **Run page.** The "Agent steps" section shows the timeline with a filter per
  source, so one witness can be hidden or shown at a time
  (`ui/components/runs/stepTimeline.tsx`).
- **Trace export.** The OTLP export adds commands and file changes as child
  spans of the model turn they follow ([trace-export.md](trace-export.md)).
- **Agent check.** `agent check` has a `process_observer` and a
  `workspace_observer` row that prove both observers saw the agent
  ([adding-an-agent.md](adding-an-agent.md#the-rows)).

### Rows

Each row has a `seq`, a time, a `kind` (`model_turn`, `model_attempt`,
`tool_request`, `tool_execution`, `command`, `file_change`, `agent_event`; the
schema also names `network`, which the timeline does not produce), a
`source`, a `proof` level and a `turn`. Rows are ordered by time; at equal
times model turns come first, then attempts and requests, then the rest.
Every string in a row (summary, argv, path) is agent-influenced display text.

- **Turn of a row.** Gateway rows carry their own logical step. Every other
  row gets the last turn whose response ended at or before the row's time,
  so 0 before the first response.
- **Turn window for linking.** A call requested in turn k is linked to
  witnesses from the end of response k minus 1 s to the start of request
  k + 1 plus 1 s; for the last turn the window has no end. The 1 s slack
  absorbs write and poll delays; every witness runs on the same host clock
  as the gateway.
- **Model rows.** One `model_turn` per logical model request (a request
  that never got an answer is a row marked as an error and is not counted),
  one `model_attempt` per retry after the first attempt and per fallback row,
  and one `tool_request` per requested call, at the time the response ended.

### Proof levels

`proof` is the strength of the row's own source, whatever the row is linked
to. How a row is linked shows in `witness_of` and `witnessed_by`, not in its
proof.

| Proof | Rows |
|---|---|
| `verified` | `process_observer`, `workspace_observer`, `tool_proxy` and `shim` rows: a record of the action itself |
| `strong` | `gateway` rows (`model_turn`, `model_attempt`, `tool_request`): the trusted record of what the model was asked and answered, not of what then ran |
| `partial` | `agent_stream` rows: the agent's own word |

The timeline does not use `inferred`.

### Linking requested calls to their execution

Every tool call the model requested (a `tool_request` row from the gateway)
is matched against the execution witnesses:

| Witness | Matches when |
|---|---|
| Tool proxy ledger row | Not yet linked, inside the turn window, and the tool name fits (the proxied tool's name, or a framework's prefixed form of it: `mcp__docs__search`, `docs.search`, `docs_search`); a row with an equal arguments hash wins over the first fitting row |
| Shim call | The tool-call id matches |
| Process observer command | A command root (not a runtime process), not yet linked, started inside the turn window, whose argv, whitespace normalized, contains the request's target (the command it names). Its descendants are linked to the same request |
| Workspace observer change | The request's target is a single path (no spaces or shell characters), and the changed path equals it relative to `/workspace` (or the target ends with `/` plus the changed path), inside the turn window |
| Agent-stream tool result | The tool-call id matches |

The request row lists its witnesses in `witnessed_by`; each witness row
points back with `witness_of` and is not counted again.

A shim call that matches no request is linked to a command with the same
program name started within 2 s of it (to that command's tool call when the
command witnesses one, else to the command tree), and is counted on its own
only when there is none. Tool proxy ledger rows and agent-stream tool results
that match no request are counted on their own.

Each requested call gets one execution state:

| State | Meaning |
|---|---|
| `executed_with_proof` | At least one witness other than the agent's own stream saw it run: a ledger row, a shim call, a command or a file change |
| `executed_agent_reported` | Only the agent stream says it ran |
| `no_witness` | Nothing shows it ran. Counted by `tool_calls_unwitnessed` |

A model can request a call the agent never runs (a refused or cancelled
call): `no_witness` is then the right answer, not an error.

**Conflicts are recorded, not hidden.** When witnesses disagree, the row
says so in `conflict`: the agent stream reports a tool call whose id the
complete gateway log never had, or the stream says a call succeeded where
the ledger says it failed. The disagreement stays on the row for the
reader. The rows that carry one:

- an `agent_event` row for a tool call the agent stream reports and the
  complete gateway log does not have;
- a `tool_request` row whose agent-stream result and ledger row disagree on
  whether it failed;
- a `tool_request` row for a shell-like tool (`bash`, `shell`, `exec`,
  `command`, `terminal` in its name) that only the agent stream says ran
  while the process observer was watching and saw no matching command.

## How `agent_steps` counts

`agent_steps` counts each agent action with a trusted or native witness
once:

1. model turns (gateway, the same count as `steps`);
2. plus requested tool calls with an execution witness, one each; the ledger
   row, shim call, command tree or file change linked to a call is part of
   that call and not counted again;
3. plus tool proxy ledger rows, shim calls and agent-stream tool results
   that match no request (a shim call that matches a command belongs to it);
4. plus command trees not linked to any tool call;
5. plus file changes not linked to a tool call and not inside the lifetime
   of a command tree already counted (on its own, or through the tool call it
   witnesses), from its start to 500 ms after its end, grouped as one action
   per distinct path per turn and session.

Its evidence level is the weakest witness used: native when a counted
agent-stream row or an `executed_agent_reported` call is in it, else
boundary. With only gateway rows and no observer that ran, it is unknown,
not a count of model turns. The `counted` rows of `steps.jsonl` are exactly
the actions summed (`evidence/steps.go`, `BuildTimeline`).

### Worked example: a tool call that runs tests

One turn of Codex requests `bash -lc "pytest -q"`. The shell starts, runs
pytest, which starts Python:

| seq | kind | source | turn | proof | row | counted |
|---|---|---|---|---|---|---|
| 1 | `model_turn` | gateway | 1 | strong | The response asks for call `c1`: `bash -lc "pytest -q"` | yes |
| 2 | `tool_request` | gateway | 1 | strong | `c1`, target `pytest -q`; execution `executed_with_proof`, `witnessed_by` [3] | yes |
| 3 | `command` | process_observer | 1 | verified | argv `bash -lc pytest -q`, parent the Codex runtime; `witness_of` 2 | no, part of `c1` |
| 4 | `command` | process_observer | 1 | verified | argv `/usr/bin/python3 /usr/local/bin/pytest -q`, parent row 3; `witness_of` 2 | no, same command tree |

This turn contributes **2 agent steps**: 1 model turn and 1 tool call
executed with proof, witnessed by the process observer. The Python child is
a descendant in the same command tree, so it is not counted. Pytest's writes
to `.pytest_cache` are excluded; a `junit.xml` it wrote to the workspace
would fall inside the counted command's lifetime and not count either. The
next model request, which sends the result back, is a turn of its own.

For this slice: `steps` 1, `model_attempts` 1, `tool_calls` 1,
`tool_calls_executed` 1, `tool_calls_unwitnessed` 0, `commands_executed` 1,
`agent_steps` 2, evidence level boundary.

### Second example: a framework that acts without tool calls

A text-action agent (the model writes a shell block in plain text and the
framework runs it) and its framework's own file writes leave no tool call in
the gateway log:

- turn 1: the response holds `ls | grep test`; the framework runs
  `bash -c "ls | grep test"`: one command tree (bash, ls, grep), linked to no
  tool call: 1 action;
- turn 2: the framework writes `src/app.py` itself with Python file I/O,
  twice: two `modify` events on one path in one turn, inside no command:
  1 action;
- turn 3: the final answer.

`tool_calls` 0, `commands_executed` 1, `files_changed_events` 2,
`agent_steps` 3 + 1 + 1 = 5. Without the observers this run would read 3
model turns and 0 tool calls, and its `agent_steps` would be unknown, not 3.

## Configuration

In the bench config (`deploy/bench.yaml`, `lib/config.go`); a key left out
keeps its default:

```yaml
observers:
  process: true      # the process observer, default on
  workspace: true    # the workspace observer, default on
  interval_ms: 20    # the process observer's poll interval
```

Both observers use the helper image (`images.helper`), which must have
`python3`. A shorter interval catches shorter processes at a higher CPU cost
(table below). The interval is recorded per session in `observers.json`;
keep it the same across the arms of one experiment, since it changes what
`commands_executed` can see. An observer switched off writes no status and
no records; its metrics read unknown, never zero.

## Security

- **The agent can see the process observer.** It shares the PID namespace,
  so `ps` in the agent's container lists `python3 -u -`. It sees nothing
  more: the script came in on stdin.
- **A non-root agent cannot stop it.** The observer runs under a different
  uid and the agent has no `CAP_KILL`, so it can neither signal nor trace the
  observer (verified in a real-Docker test).
- **A root agent can kill it.** A task whose agent runs as root (imported
  Harbor tasks, for example) holds `CAP_KILL`. The bench detects an observer
  whose output ended more than 1.5 s before the agent ended (a smaller gap is
  the agent's PID 1 exiting, which ends the namespace) and marks the process
  evidence `missing` with that reason; the commands are then unknown, never
  zero.
- **The agent cannot forge observer records.** The observer's output reaches
  the bench over the Docker attach pipe, not a file or socket in the agent's
  container, and its file descriptors under `/proc/<pid>/fd` need ptrace
  rights the agent lacks.
- **The observer costs the agent nothing of its limits.** It runs in its
  own cgroup, so it never counts against the agent's pids or memory limit,
  and it only reads `/proc`: it never touches the agent's processes.
- **The workspace observer is read-only.** It mounts the volume read-only
  and shares nothing else with the agent. The agent can flood it with
  changes: past the event cap, detail is dropped but the count stays exact;
  past the folder cap, the coverage note says what was not watched.
- **Argv can hold secrets.** The bench redacts them with its redaction rules
  and the run's gateway key before writing `processes.jsonl`; the observer
  never sees the key.

## Measured coverage and overhead

Measured on the development host on 15 Nov 2026, with real Docker and a
scripted agent that spawned 40 processes of each fixed duration. Each cell is
processes caught out of 40:

| Poll interval | 1 ms | 5 ms | 20 ms | 50 ms | 100 ms |
|---|---:|---:|---:|---:|---:|
| 10 ms | 4/40 | 23/40 | 40/40 | 40/40 | 40/40 |
| **20 ms (default)** | 3/40 | 12/40 | 39/40 | 40/40 | 40/40 |
| 50 ms | 0/40 | 0/40 | 1/40 | 40/40 | 40/40 |

CPU cost of the observer, as a share of one core: about 4.7% at 10 ms, about
3% at 20 ms (0.29 s of CPU over a 9.5 s run), about 2% at 50 ms. It runs in
its own cgroup and does not touch the agent's processes.

The real-Docker test `TestProcessObserverMissRate` (environments, tag
integration) repeats the measurement through the bench's own wiring, with
the workspace observer running too, the agent limited to one CPU and other
work on the host. Three runs at 20 ms caught 5, 0 and 6 of 40 1 ms
processes, 11, 0 and 11 of 40 5 ms ones, 40, 35 and 40 of 40 20 ms ones and
40, 39 and 40 of 40 50 ms ones; in each run the PID counter bounded the
misses (for example 101 of 174 PIDs seen, so at most 73 missed, against 63
actually missed). Agent wall time showed no effect beyond host noise: a
one-CPU agent doing 6 million loop iterations and 60 process spawns took
2.03 s and 1.58 s with the observer and 2.61 s and 1.70 s without it,
while the observer used 0.14 s of CPU in its own cgroup.

At the default interval, every process of 50 ms or longer was caught in
quiet runs (39 of 40 under load) and most of 1 ms and 5 ms were not. A test run, a build or a script usually
lasts longer than that; a very short helper inside a pipeline can be missed,
and each run's `pids_allocated` against `pids_seen` says how many PIDs went
unseen.

## Limits

- **Some actions cannot be seen from outside any agent.** Pure in-process
  reasoning and framework-internal function calls that touch no process, no
  file in the workspace, no network, no tool proxy and no model leave no
  trace. A file read is invisible too: inotify read events are not recorded,
  and a read changes nothing. This limit applies to every external
  benchmark, not just this one: anything that crosses no process, file,
  network or model boundary leaves nothing an outside observer can see, and
  only the agent's own report (a weaker witness) could show it.
- **Short processes can be missed.** A process shorter than the poll
  interval can start and end between two polls, and so can the agent's first
  few milliseconds before the observer starts. The miss is bounded per run by
  `pids_allocated` against `pids_seen`; the bound counts threads as well, so
  it is an upper bound on missed processes.
- **End times are upper bounds**, and exit codes exist only for processes
  caught as zombies.
- **The workspace observer sees one volume.** Writes outside the workspace
  are not observed, and a file written into a just-created folder before its
  watch exists can be missed.
- **Linking is by content where there is no id.** A ledger row, command or
  file change is linked to a requested call by tool name, target and turn
  window; two identical calls in one turn can be linked either way round. A
  row's proof stays its source's; the link shows only in `witness_of` and
  `witnessed_by`.
- **The shim log is inside the agent's container.** Its rows are `verified`
  and a shim call makes a request `executed_with_proof`, but the log lives
  inside the agent's container, so an agent that tampers with it could forge
  or hide shim calls.
- **No network rows.** The network observer runs only for `agent check`, and
  its records carry no timestamps, so connections are not in the timeline.
- **Root agents can stop the process observer.** The run then reports the
  process evidence as missing with the reason, never as zero commands.
- **Not for remote agents.** `remote_http` agents act outside the bench; both
  observers are off for them.
