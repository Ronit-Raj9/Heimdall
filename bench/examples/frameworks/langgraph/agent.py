#!/usr/bin/env python3
"""A small coding agent built with LangGraph.

It is written the way the LangGraph docs build a tool-calling agent: a
StateGraph over MessagesState with a model node, a ToolNode and
tools_condition, and a ChatOpenAI model from langchain-openai with the tools
bound to it. Nothing here knows about Heimdall or imports it.

The model endpoint comes from the standard OpenAI variables, read by
ChatOpenAI and the openai client under it: OPENAI_BASE_URL and
OPENAI_API_KEY. Point them at any OpenAI-compatible server (the bench points
them at its Bifrost gateway) and the agent runs unchanged.

Usage: fw-agent <task-file>
Env:   OPENAI_BASE_URL, OPENAI_API_KEY      the endpoint and its key
       AGENT_MODEL       model id sent to the endpoint (default gpt-5.4-mini)
       AGENT_MAX_STEPS   step cap: model turns (default 40)
       AGENT_SESSION_ID  optional; sent as the x-bf-session-id header
       BENCH_GATEWAY_HEADERS optional JSON object; extra headers on every model call
Prints the final answer last on stdout. Exit 0 when the run ends (solved or
not, step cap included), 1 when the model call fails.
"""
import json
import os
import subprocess
import sys

from langchain_core.messages import HumanMessage, SystemMessage
from langchain_core.tools import tool
from langchain_openai import ChatOpenAI
from langgraph.errors import GraphRecursionError
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode, tools_condition

WORKSPACE = "/workspace"
MODEL = os.environ.get("AGENT_MODEL", "gpt-5.4-mini")
MAX_STEPS = int(os.environ.get("AGENT_MAX_STEPS", "40"))
SESSION_ID = os.environ.get("AGENT_SESSION_ID", "")
COMMAND_TIMEOUT_S = 120
OUTPUT_MAX = 8000
SYSTEM = (
    "You are a coding agent working in the repository at /workspace. Use the tools to look at files, "
    "change them and run the tests. When the task is finished, reply with a short summary and no tool call."
)


def _inside(path):
    full = os.path.realpath(os.path.join(WORKSPACE, path or "."))
    if full != WORKSPACE and not full.startswith(WORKSPACE + "/"):
        raise ValueError(f"{path} is outside {WORKSPACE}")
    return full


@tool
def list_files(path: str = ".") -> str:
    """List the files under a directory of the repository (relative to /workspace)."""
    try:
        root = _inside(path)
        out = []
        for d, dirs, files in os.walk(root):
            dirs[:] = sorted(x for x in dirs if x != ".git")
            out += [os.path.relpath(os.path.join(d, f), WORKSPACE) for f in sorted(files)]
        return "\n".join(out[:500]) or "(no files)"
    except (OSError, ValueError) as e:
        return f"error: {e}"


@tool
def read_file(path: str) -> str:
    """Read a text file of the repository (path relative to /workspace)."""
    try:
        with open(_inside(path), errors="replace") as f:
            return f.read()[:20000]
    except (OSError, ValueError) as e:
        return f"error: {e}"


@tool
def write_file(path: str, content: str) -> str:
    """Write a whole file (path relative to /workspace), creating folders as needed."""
    try:
        full = _inside(path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w") as f:
            f.write(content)
        return f"wrote {len(content)} characters to {path}"
    except (OSError, ValueError) as e:
        return f"error: {e}"


@tool
def run_command(command: str) -> str:
    """Run a shell command in /workspace (for example the tests) and return its exit code and output."""
    env = {k: v for k, v in os.environ.items() if k != "OPENAI_API_KEY"}  # the key never reaches the model's commands
    try:
        p = subprocess.run(command or "true", shell=True, cwd=WORKSPACE, capture_output=True, text=True,
                           timeout=COMMAND_TIMEOUT_S, env=env)
        return f"exit {p.returncode}\n{p.stdout}{p.stderr}"[-OUTPUT_MAX:]
    except subprocess.TimeoutExpired:
        return f"timed out after {COMMAND_TIMEOUT_S}s"


def system_prompt():
    notes = os.path.join(WORKSPACE, "AGENTS.md")
    if os.path.isfile(notes):
        with open(notes, errors="replace") as f:
            return SYSTEM + "\n\nProject notes from AGENTS.md:\n" + f.read()
    return SYSTEM


def build_graph():
    headers = {"x-bf-session-id": SESSION_ID, "x-bf-session-affinity": "off"} if SESSION_ID else {}
    # BENCH_GATEWAY_HEADERS: the run's extra gateway headers (heimdall plugin fault plan and run tags, cache headers).
    headers.update(json.loads(os.environ.get("BENCH_GATEWAY_HEADERS") or "{}"))
    headers = headers or None
    tools = [list_files, read_file, write_file, run_command]
    model = ChatOpenAI(model=MODEL, default_headers=headers).bind_tools(tools)

    def call_model(state: MessagesState):
        return {"messages": [model.invoke(state["messages"])]}

    graph = StateGraph(MessagesState)
    graph.add_node("agent", call_model)
    graph.add_node("tools", ToolNode(tools))
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", tools_condition)
    graph.add_edge("tools", "agent")
    return graph.compile()


def text_of(message):
    content = message.content
    if isinstance(content, list):
        content = "".join(p.get("text", "") if isinstance(p, dict) else str(p) for p in content)
    return content or ""


def main():
    with open(sys.argv[1] if len(sys.argv) > 1 else "/tmp/bench/instruction.md") as f:
        task = f.read()
    app = build_graph()
    try:
        # Each step is a model node then a tool node, so the cap on graph steps is twice the turn cap.
        state = app.invoke({"messages": [SystemMessage(system_prompt()), HumanMessage(task)]},
                           {"recursion_limit": 2 * MAX_STEPS + 1})
    except GraphRecursionError:
        print(f"Stopped: reached the step cap of {MAX_STEPS} model turns without a final answer.")
        return 0
    except Exception as e:  # a failed model call ends the run; the bench reads the exit code
        print(f"Stopped: model call failed: {type(e).__name__}: {e}", file=sys.stderr)
        print(f"Stopped: model call failed: {type(e).__name__}")
        return 1
    print(text_of(state["messages"][-1]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
