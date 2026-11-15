#!/usr/bin/env python3
"""Agent events: report an agent's own steps to the Heimdall bench.

    BENCH_EVENTS_FILE=/tmp/events.jsonl python3 agent_events.py && cat /tmp/events.jsonl

Inside a bench run the bench sets BENCH_EVENTS_URL or BENCH_EVENTS_FILE in the
agent container; with neither set every call below is a no-op, so the same
code runs anywhere. The bench shows these events as agent-reported evidence,
next to what it observed itself. spec/agent-event.schema.json is the format.
"""

import os
import sys

try:
    from heimdall import events
except ImportError:  # running from the repository without installing heimdall-bench
    sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "python"))
    from heimdall import events


def main() -> int:
    # The three lines an agent adds: a plan, then each tool call with its result.
    plan = events.emit("plan", "fix the failing test", output="1. run the tests 2. read the failure 3. patch")
    call = events.emit("tool_call", "run_shell", parent_id=plan, tool_call_id="call_1", input={"command": "pytest -q"})
    events.emit("tool_result", "run_shell", parent_id=call, tool_call_id="call_1", output="1 failed", status="error")

    # A subagent nests under its start event; step() times a block and reports it when the block ends.
    with events.step("subagent_start", "reviewer", parent_id=plan) as sub:
        with events.step("retrieval", "search_docs", parent_id=sub.id, input={"query": "assertion error"}) as hit:
            hit.output = ["docs/testing.md"]
        sub.output = "the fixture is stale"
    events.emit("subagent_end", "reviewer", parent_id=sub.id, status="ok")

    # An agent that can only print writes the same event to stdout instead.
    print(events.stdout_line("message", "final", parent_id=plan, output="patched the fixture"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
