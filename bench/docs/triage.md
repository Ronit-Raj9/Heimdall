# Failure triage

Triage answers "why did version B get worse?" with **observed patterns, not root causes**. Each pattern is a deterministic rule over a run's normalized events (`events.jsonl`, DESIGN.md 16.4) and its success claim. The triage screen counts, per arm, how many runs show each pattern and links every count to the runs and event ids behind it. Automatic root-cause localization is not built: decisive-step localization succeeds 14.2% of the time (DESIGN.md 2, 11). A pattern that is more common in B is a lead to read in the trace, never a verdict.

## Where it lives

| Part | Path |
|---|---|
| Detectors (per-run metrics) | `metrics/patternloop.go`, `metrics/patternunverified.go`, `metrics/patternignorederror.go`, shared rules in `metrics/patternevents.go` |
| API | `GET /api/bench/experiments/{id}/triage`, `handlers/triage.go`, returns `schemas.Triage` plus `mode` |
| Screen | `/triage?exp=<id>&pattern=<name>`, `ui/app/triage/`, `ui/components/triage/` |

The detectors are ordinary metrics: kind `rate`, lower is better, version 1.0.0, computed at the measure stage into each run's `metrics.json`. Each returns observed `1` when the pattern appears, observed `0` when the events exist and it does not, and `unknown` with a reason when there are no usable events. A run sealed before these metrics existed has no value for them and counts as unknown until it is measured again.

### Reason format

An observed `1` carries the triggering event ids (at most 10) and a one-line summary in `Value.Reason`:

```
events: r_abc:gateway:7,r_abc:gateway:9,r_abc:gateway:11; Bash "pytest -q" repeated 3 times in a row
```

The screen parses the part before the first `; ` as the event list. An observed `0` carries a short summary only.

## Which events a detector reads

`pattern_loop` and `pattern_unverified_finish` prefer gateway-sourced events (Bifrost's log is the witness of record, DESIGN.md 11) and fall back to the agent's own stream (`agent_stream`):

1. The gateway, when its log is complete and it recorded tool calls. Evidence level `gateway`.
2. Else the agent stream, when present (the gateway is missing, marked incomplete, or logged no tool calls while the stream has some, for example with content logging off). Evidence level `native`.
3. Else an incomplete gateway log. A pattern it shows counts; its absence is `unknown`, because the missing rows could hold the loop or the test run.
4. No gateway or agent-stream event at all: `unknown` ("no gateway or agent_stream events").

`pattern_ignored_error` reads only the agent stream: Bifrost records which tool results went back to the model, not whether they failed.

### Tool classes (by tool name, case-insensitive)

| Class | Rule |
|---|---|
| Command | name is `bash` or `exec`, or contains `shell`, `command` or `terminal` (Claude Code `Bash`, Codex `exec_command`) |
| Edit | not a command, not a todo tool, and the name contains `edit`, `write`, `patch`, `replace`, `create_file` or `insert` (`Edit`, `MultiEdit`, `Write`, `apply_patch`) |
| Read | a command whose program is `cat`, `head`, `tail`, `less`, `more`, `sed`, `awk`, `grep`, `rg`, `nl`, `wc`, `ls` or `find`; or a tool whose name contains `read`, `view`, `open`, `grep`, `glob`, `search`, `list` or `cat` |
| Test run | a command whose text contains `pytest`, `py.test`, `python -m pytest`, `unittest`, `npm test`, `go test` or `make test`; or a non-command tool whose name contains `test` |
| Same call | commands: the same command text (whitespace collapsed), whatever the other arguments; other tools: the same tool name and arguments hash, else the same name and target |

## The patterns

### `pattern_loop` (minimum evidence: gateway)

Observed 1 when the same call repeats **3 or more times in a row** (consecutive tool calls), or **4 or more times with no edit tool call between** them. An edit resets the between-edits count; the same edit repeated 3 times in a row is still a loop. The reason lists the repeated calls' event ids.

### `pattern_unverified_finish` (minimum evidence: gateway)

Observed 1 when the run ends with a success claim (`Claim.Made`) and **no test-run command comes after the last edit**. With no edit in the run, a test run anywhere counts as verification. No claim evidence is `unknown`; no success claim is `0`. The reason lists the last edit (when there is one) and the source's last event.

### `pattern_ignored_error` (minimum evidence: native)

Observed 1 when the run claims success and some failed tool result is **not answered by any of the next 2 tool calls**. A result failed when the parser flagged it (`IsError`) or its summary reports a non-zero exit (`exit 1: ...`, `Exit code 2`). The result is answered by:

- a retry: the same call, or any call with the same non-empty target;
- any edit;
- a read of a file the failed call named (a path in its target; a pytest node id `tests/test_x.py::test_y` names `tests/test_x.py`).

Failed model calls are not tool errors. The run ending right after the error leaves it unanswered. The reason lists each ignored result and the calls that followed it, with the first one's tool, target and output summary.

### `false_completion` (existing metric)

A success claim while the hidden verifier failed. Not applicable on passing runs. Triage shows it beside the three patterns because it is the outcome the patterns most often explain.

## Aggregation

For each pattern and arm, over the counted run of each counted, sealed slot:

- `num`: runs whose value is observed and 1;
- `den`: runs whose value is observed;
- `unknown`: runs whose value is unknown or missing.

A not_applicable value counts nowhere. Runs still in flight count nowhere. `runs[arm]` lists the run ids that show the pattern. The screen's difference column is B's share minus A's share in percentage points: descriptive only, with no test and no p-value. Patterns are not independent of task difficulty, and a handful of runs per arm makes any difference noisy.

## Honest limits

- **Observed, not caused.** A loop in a failed run does not mean the loop caused the failure. Read the linked trace.
- **Names, not semantics.** Classes come from tool names. A shell command that edits (`sed -i`, `echo >`, a Python script) is not an edit, so a loop around it can be reported and a later test run still counts. `str_replace_editor` with `view` counts as an edit. A custom agent with unusual tool names may need new rules.
- **Text matching.** `cat pytest.ini` contains `pytest` and counts as a test run (a false negative for unverified finish). A test runner not on the list (`tox`, `cargo test`, `bun test`, `npx jest`) is not one (a false positive). `go test` also matches `cargo test`.
- **Loops.** Polling a slow command or re-reading a file between thoughts can be deliberate. A loop with small argument changes (a different `-k` filter each time) is not caught.
- **Ignored errors.** An agent can answer an error in text, or by reading a different file, and still count as ignoring it. An expected failure (a test meant to fail first) counts as an error.
- **Gateway-only evidence** sees every tool call the model requested, with its name, target and arguments hash. It does not see tool outcomes, so `pattern_ignored_error` is unknown without a parsed agent stream; it also cannot see which commands actually ran. A test command the model requested but the harness refused still counts as a test run.
- **Claims.** Unverified finish and ignored error depend on the claim parser. A claim read from the tail of stdout is coarser than a native one.
