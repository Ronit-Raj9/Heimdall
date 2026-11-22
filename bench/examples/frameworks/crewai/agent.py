#!/usr/bin/env python3
"""A small coding agent built with CrewAI.

It is written the way the CrewAI docs show: tools from the @tool decorator,
one Agent (role, goal, backstory, tools, llm, max_iter as the step cap), one
Task, and a sequential Crew started with kickoff(). The LLM is CrewAI's
native OpenAI provider with custom_openai=True, which the CrewAI docs give
for an OpenAI-compatible endpoint configured by environment: it reads
OPENAI_BASE_URL and OPENAI_API_KEY and sends the model id unchanged. Point
those at any compatible server (the bench points them at its Bifrost
gateway) and the agent runs unchanged. Nothing here knows about Heimdall or
imports it.

Telemetry: CrewAI sends anonymous telemetry, offers trace upload and checks
PyPI for a newer version. The image and the agent YAML switch all three off:
CREWAI_DISABLE_TELEMETRY, CREWAI_DISABLE_TRACKING, CREWAI_TRACING_ENABLED=false
and CREWAI_DISABLE_VERSION_CHECK.

Usage: fw-agent <task-file>
Env:   OPENAI_BASE_URL, OPENAI_API_KEY      the endpoint and its key
       AGENT_MODEL       model id sent to the endpoint (default gpt-5.4-mini)
       AGENT_MAX_STEPS   step cap: agent iterations (default 40)
       AGENT_SESSION_ID  optional; sent as the x-bf-session-id header
       BENCH_GATEWAY_HEADERS optional JSON object; extra headers on every model call
Prints the final answer last on stdout. Exit 0 when the run ends (solved or
not, step cap included), 1 when the model call fails.
"""
import json
import os
import subprocess
import sys

from crewai import LLM, Agent, Crew, Process, Task
from crewai.tools import tool

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


@tool("list_files")
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


@tool("read_file")
def read_file(path: str) -> str:
    """Read a text file of the repository (path relative to /workspace)."""
    try:
        with open(_inside(path), errors="replace") as f:
            return f.read()[:20000]
    except (OSError, ValueError) as e:
        return f"error: {e}"


@tool("write_file")
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


@tool("run_command")
def run_command(command: str) -> str:
    """Run a shell command in /workspace (for example the tests) and return its exit code and output."""
    env = {k: v for k, v in os.environ.items() if k != "OPENAI_API_KEY"}  # the key never reaches the model's commands
    try:
        p = subprocess.run(command or "true", shell=True, cwd=WORKSPACE, capture_output=True, text=True,
                           timeout=COMMAND_TIMEOUT_S, env=env)
        return f"exit {p.returncode}\n{p.stdout}{p.stderr}"[-OUTPUT_MAX:]
    except subprocess.TimeoutExpired:
        return f"timed out after {COMMAND_TIMEOUT_S}s"


def project_notes():
    notes = os.path.join(WORKSPACE, "AGENTS.md")
    if os.path.isfile(notes):
        with open(notes, errors="replace") as f:
            return "\n\nProject notes from AGENTS.md:\n" + f.read()
    return ""


def main():
    with open(sys.argv[1] if len(sys.argv) > 1 else "/tmp/bench/instruction.md") as f:
        task_text = f.read()
    headers = {"x-bf-session-id": SESSION_ID, "x-bf-session-affinity": "off"} if SESSION_ID else {}
    # BENCH_GATEWAY_HEADERS: the run's extra gateway headers (heimdall plugin fault plan and run tags, cache headers).
    headers.update(json.loads(os.environ.get("BENCH_GATEWAY_HEADERS") or "{}"))
    headers = headers or None
    llm = LLM(model=MODEL, custom_openai=True, default_headers=headers)
    coder = Agent(
        role="Coding agent",
        goal="Complete the task in the repository at /workspace so that its tests pass.",
        backstory=SYSTEM + project_notes(),
        tools=[list_files, read_file, write_file, run_command],
        llm=llm,
        max_iter=MAX_STEPS,
        allow_delegation=False,
        verbose=False,
    )
    job = Task(description=task_text, expected_output="A short summary of what was changed and the test result.",
               agent=coder)
    crew = Crew(agents=[coder], tasks=[job], process=Process.sequential, verbose=False)
    try:
        result = crew.kickoff()
    except Exception as e:  # a failed model call ends the run; the bench reads the exit code
        print(f"Stopped: model call failed: {type(e).__name__}: {e}", file=sys.stderr)
        print(f"Stopped: model call failed: {type(e).__name__}")
        return 1
    print(result.raw)
    return 0


if __name__ == "__main__":
    sys.exit(main())
