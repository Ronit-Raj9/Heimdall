#!/usr/bin/env python3
"""Pull mode with an OpenAI Agents SDK agent.

The Agents SDK runs tools inside its own loop, so here Heimdall's task tools
become FunctionTools whose bodies hand the call to Heimdall: the run stops
at the first tool call, the answer carries it, Heimdall runs it through the
task's tools (recorded in the evaluation) and the next turn brings the
result.
The SDK sees the whole chat each turn as Responses-style input items.

    pip install openai-agents
    HEIMDALL_URL=... HEIMDALL_TOKEN=... OPENAI_API_KEY=... python3 openai_agents_sdk.py

An Agents SDK app with its own tools (not the task's) keeps them: they run
in your process, and only the final text goes back to Heimdall.
Env: HEIMDALL_AGENT (default support-bot), AGENT_MODEL.
"""

import json
import os
import sys

try:
    from agents import Agent, FunctionTool, Runner
    from agents.exceptions import AgentsException
except ImportError:
    sys.exit("this example needs the OpenAI Agents SDK: pip install openai-agents")

try:
    import heimdall
except ImportError:  # running from the repository without installing heimdall-bench
    sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "python"))
    import heimdall

AGENT = os.environ.get("HEIMDALL_AGENT", "support-bot")
MODEL = os.environ.get("AGENT_MODEL", "gpt-5.4-mini")
INSTRUCTIONS = "You are a helpful support agent. Use the tools when they help, then answer briefly."


class ToolRequested(AgentsException):
    """Raised by a task tool: Heimdall runs the call, not this process."""

    def __init__(self, name, arguments):
        super().__init__("tool %s requested" % name)
        self.name, self.arguments = name, arguments


def heimdall_tool(spec):
    async def invoke(_ctx, args_json):
        raise ToolRequested(spec["name"], json.loads(args_json or "{}"))

    return FunctionTool(name=spec["name"], description=spec.get("description", ""),
                        params_json_schema=spec.get("input_schema") or {"type": "object", "properties": {}},
                        on_invoke_tool=invoke, strict_json_schema=False)


def to_input(messages):
    """OpenAI chat messages as Agents SDK (Responses) input items."""
    items = []
    for m in messages:
        role = m.get("role")
        if role == "tool":
            items.append({"type": "function_call_output", "call_id": m["tool_call_id"], "output": m.get("content") or ""})
            continue
        if m.get("content"):
            items.append({"role": role, "content": m["content"]})
        for c in m.get("tool_calls") or []:
            items.append({"type": "function_call", "call_id": c["id"], "name": c["function"]["name"],
                          "arguments": c["function"].get("arguments") or "{}"})
    return items


def solve(turn):
    agent = Agent(name=AGENT, instructions=INSTRUCTIONS, model=MODEL, tools=[heimdall_tool(t) for t in turn.tools])
    try:
        result = Runner.run_sync(agent, to_input(turn.messages))
    except ToolRequested as req:
        return heimdall.Answer(tool_calls=[heimdall.ToolCall(name=req.name, arguments=req.arguments)])
    return str(result.final_output or "")


if __name__ == "__main__":
    heimdall.serve(AGENT, solve, concurrency=int(os.environ.get("CONCURRENCY", "2")), log=lambda m: print(m, file=sys.stderr))
