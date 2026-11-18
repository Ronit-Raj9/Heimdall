#!/usr/bin/env python3
"""A small coding agent built with the OpenAI Agents SDK.

It is written the way the Agents SDK docs show: function tools from
@function_tool, one Agent with instructions and tools, and Runner.run_sync
with max_turns as the step cap. The model is an OpenAIResponsesModel (the
Responses API, the SDK's default) on an AsyncOpenAI client that reads
OPENAI_BASE_URL and OPENAI_API_KEY, so pointing those at any compatible
server (the bench points them at its Bifrost gateway) is all it takes. Nothing here knows about
Heimdall or imports it.

Tracing: the SDK exports traces to OpenAI by default. The image and the agent
YAML set OPENAI_AGENTS_DISABLE_TRACING=1, the SDK's documented switch.

Usage: fw-agent <task-file>
Env:   OPENAI_BASE_URL, OPENAI_API_KEY      the endpoint and its key
       AGENT_MODEL       model id sent to the endpoint (default gpt-5.4-mini)
       AGENT_MAX_STEPS   step cap: model turns (default 40)
       AGENT_SESSION_ID  optional; sent as the x-bf-session-id header
Prints the final answer last on stdout. Exit 0 when the run ends (solved or
not, step cap included), 1 when the model call fails.
"""
import os
import subprocess
import sys

from agents import Agent, MaxTurnsExceeded, OpenAIResponsesModel, Runner, function_tool
from openai import AsyncOpenAI

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


@function_tool
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


@function_tool
def read_file(path: str) -> str:
    """Read a text file of the repository (path relative to /workspace)."""
    try:
        with open(_inside(path), errors="replace") as f:
            return f.read()[:20000]
    except (OSError, ValueError) as e:
        return f"error: {e}"


@function_tool
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


@function_tool
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


def main():
    with open(sys.argv[1] if len(sys.argv) > 1 else "/tmp/bench/instruction.md") as f:
        task = f.read()
    headers = {"x-bf-session-id": SESSION_ID, "x-bf-session-affinity": "off"} if SESSION_ID else None
    # AsyncOpenAI reads OPENAI_BASE_URL and OPENAI_API_KEY; the headers only add the session id. The
    # model is an OpenAIResponsesModel, not a string: the SDK reads "anthropic/..." in a string as a
    # provider prefix and refuses it, while the model object sends the id to the endpoint verbatim.
    model = OpenAIResponsesModel(model=MODEL, openai_client=AsyncOpenAI(default_headers=headers))
    agent = Agent(name="coder", instructions=system_prompt(), model=model,
                  tools=[list_files, read_file, write_file, run_command])
    try:
        result = Runner.run_sync(agent, task, max_turns=MAX_STEPS)
    except MaxTurnsExceeded:
        print(f"Stopped: reached the step cap of {MAX_STEPS} model turns without a final answer.")
        return 0
    except Exception as e:  # a failed model call ends the run; the bench reads the exit code
        print(f"Stopped: model call failed: {type(e).__name__}: {e}", file=sys.stderr)
        print(f"Stopped: model call failed: {type(e).__name__}")
        return 1
    print(result.final_output)
    return 0


if __name__ == "__main__":
    sys.exit(main())
