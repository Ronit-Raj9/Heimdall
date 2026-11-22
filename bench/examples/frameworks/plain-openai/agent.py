#!/usr/bin/env python3
"""A small coding agent with no framework: a tool loop on the OpenAI Python SDK.

The loop is the one the OpenAI function-calling guide shows: send the
messages and the tool schemas to client.chat.completions.create, run each
tool call the model asks for, append the results, repeat until the model
answers without a tool call. OpenAI() reads OPENAI_BASE_URL and
OPENAI_API_KEY, so pointing those at any OpenAI-compatible server (the bench
points them at its Bifrost gateway) is all it takes. Nothing here knows about
Heimdall or imports it.

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

import openai
from openai import OpenAI

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


def list_files(path="."):
    root = _inside(path)
    out = []
    for d, dirs, files in os.walk(root):
        dirs[:] = sorted(x for x in dirs if x != ".git")
        out += [os.path.relpath(os.path.join(d, f), WORKSPACE) for f in sorted(files)]
    return "\n".join(out[:500]) or "(no files)"


def read_file(path):
    with open(_inside(path), errors="replace") as f:
        return f.read()[:20000]


def write_file(path, content):
    full = _inside(path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "w") as f:
        f.write(content)
    return f"wrote {len(content)} characters to {path}"


def run_command(command):
    env = {k: v for k, v in os.environ.items() if k != "OPENAI_API_KEY"}  # the key never reaches the model's commands
    try:
        p = subprocess.run(command or "true", shell=True, cwd=WORKSPACE, capture_output=True, text=True,
                           timeout=COMMAND_TIMEOUT_S, env=env)
        return f"exit {p.returncode}\n{p.stdout}{p.stderr}"[-OUTPUT_MAX:]
    except subprocess.TimeoutExpired:
        return f"timed out after {COMMAND_TIMEOUT_S}s"


FUNCTIONS = {"list_files": list_files, "read_file": read_file, "write_file": write_file, "run_command": run_command}


def schema(name, description, **props):
    required = [k for k, v in props.items() if not v.pop("optional", False)]
    return {"type": "function", "function": {"name": name, "description": description, "parameters": {
        "type": "object", "properties": props, "required": required}}}


TOOLS = [
    schema("list_files", "List the files under a directory of the repository (relative to /workspace).",
           path={"type": "string", "optional": True}),
    schema("read_file", "Read a text file of the repository (path relative to /workspace).",
           path={"type": "string"}),
    schema("write_file", "Write a whole file (path relative to /workspace), creating folders as needed.",
           path={"type": "string"}, content={"type": "string"}),
    schema("run_command", "Run a shell command in /workspace (for example the tests) and return its exit code and output.",
           command={"type": "string"}),
]


def call_tool(name, raw_args):
    try:
        return FUNCTIONS[name](**json.loads(raw_args or "{}"))
    except (KeyError, TypeError, ValueError, OSError) as e:
        return f"error: {e}"


def system_prompt():
    notes = os.path.join(WORKSPACE, "AGENTS.md")
    if os.path.isfile(notes):
        with open(notes, errors="replace") as f:
            return SYSTEM + "\n\nProject notes from AGENTS.md:\n" + f.read()
    return SYSTEM


def main():
    with open(sys.argv[1] if len(sys.argv) > 1 else "/tmp/bench/instruction.md") as f:
        task = f.read()
    headers = {"x-bf-session-id": SESSION_ID, "x-bf-session-affinity": "off"} if SESSION_ID else {}
    # BENCH_GATEWAY_HEADERS: the run's extra gateway headers (heimdall plugin fault plan and run tags, cache headers).
    headers.update(json.loads(os.environ.get("BENCH_GATEWAY_HEADERS") or "{}"))
    headers = headers or None
    client = OpenAI(default_headers=headers)  # reads OPENAI_BASE_URL and OPENAI_API_KEY
    messages = [{"role": "system", "content": system_prompt()}, {"role": "user", "content": task}]
    for _ in range(MAX_STEPS):
        try:
            resp = client.chat.completions.create(model=MODEL, messages=messages, tools=TOOLS)
        except openai.OpenAIError as e:  # a failed model call ends the run; the bench reads the exit code
            print(f"Stopped: model call failed: {type(e).__name__}: {e}", file=sys.stderr)
            print(f"Stopped: model call failed: {type(e).__name__}")
            return 1
        msg = resp.choices[0].message
        turn = {"role": "assistant", "content": msg.content}
        if msg.tool_calls:
            turn["tool_calls"] = [{"id": c.id, "type": "function", "function": {
                "name": c.function.name, "arguments": c.function.arguments}} for c in msg.tool_calls]
        messages.append(turn)
        if not msg.tool_calls:
            print(msg.content or "")
            return 0
        for call in msg.tool_calls:
            result = call_tool(call.function.name, call.function.arguments)
            messages.append({"role": "tool", "tool_call_id": call.id, "content": result})
    print(f"Stopped: reached the step cap of {MAX_STEPS} model turns without a final answer.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
