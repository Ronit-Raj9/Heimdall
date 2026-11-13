#!/usr/bin/env python3
"""Minimal coding agent for Bifrost Bench: a template to copy for your own agent.

It follows the three rules every bench agent follows:
  1. Read the task from the instruction file (BENCH_INSTRUCTION_FILE).
  2. Edit /workspace, then exit. The bench grades the diff, not the output.
  3. Send every model call to BENCH_BASE_URL with BENCH_API_KEY, the run's
     Bifrost virtual key. Nothing else is reachable from the container.

It speaks the OpenAI chat completions format to Bifrost's /openai/v1 route, so
any provider Bifrost has configured can serve it (model ids are written
provider/model, e.g. anthropic/claude-sonnet-4-5). Standard library only.

stdout is one JSON line per step, then the final message as plain text, so the
bench can read a success claim from the tail of stdout without a parser. To
build your own agent, copy this file, keep the env contract, change the loop.
"""
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request

BASE_URL = os.environ["BENCH_BASE_URL"].rstrip("/")
API_KEY = os.environ["BENCH_API_KEY"]
MODEL = os.environ["BENCH_MODEL"]
INSTRUCTION_FILE = os.environ.get("BENCH_INSTRUCTION_FILE", "/tmp/bench/instruction.md")
SESSION_ID = os.environ.get("BENCH_SESSION_ID", "")
MAX_TURNS = int(os.environ.get("BENCH_MAX_TURNS", "40"))
SHELL_TIMEOUT_S = int(os.environ.get("BENCH_SHELL_TIMEOUT_S", "120"))
WORKSPACE = "/workspace"
NOTES_FILE = "AGENTS.md"  # the project notes file this agent reads (notes.project_file)
OUTPUT_MAX = 8000  # characters of tool output sent back to the model

TOOLS = [
    {"type": "function", "function": {
        "name": "run_shell",
        "description": "Run a shell command in /workspace and return its exit code and output.",
        "parameters": {"type": "object", "properties": {"command": {"type": "string"}}, "required": ["command"]},
    }},
    {"type": "function", "function": {
        "name": "write_file",
        "description": "Write a whole file. The path is relative to /workspace.",
        "parameters": {"type": "object", "properties": {"path": {"type": "string"}, "content": {"type": "string"}},
                       "required": ["path", "content"]},
    }},
]

SYSTEM = (
    "You are a coding agent working in the repository at /workspace. Use run_shell to inspect files and run "
    "tests, and write_file to change files. When the task is finished, reply with a short summary and no tool call."
)


def emit(obj):
    print(json.dumps(obj, ensure_ascii=False), flush=True)


def chat(messages):
    """One model call. Retries are left to the gateway's policy, so a fault reaches the agent."""
    body = json.dumps({"model": MODEL, "messages": messages, "tools": TOOLS}).encode()
    headers = {"Authorization": "Bearer " + API_KEY, "Content-Type": "application/json"}
    if SESSION_ID:
        headers["x-bf-session-id"] = SESSION_ID
        headers["x-bf-session-affinity"] = "off"
    req = urllib.request.Request(BASE_URL + "/chat/completions", data=body, headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=600) as resp:
        return json.load(resp)


def run_shell(command):
    try:
        # The run key stays out of the commands' environment: the model's commands never need it.
        env = {k: v for k, v in os.environ.items() if k != "BENCH_API_KEY"}
        p = subprocess.run(command, shell=True, cwd=WORKSPACE, capture_output=True, text=True,
                           timeout=SHELL_TIMEOUT_S, env=env)
        out = f"exit {p.returncode}\n{p.stdout}{p.stderr}"
    except subprocess.TimeoutExpired:
        out = f"timed out after {SHELL_TIMEOUT_S}s"
    return out[-OUTPUT_MAX:]


def write_file(path, content):
    full = os.path.realpath(os.path.join(WORKSPACE, path))
    if full != WORKSPACE and not full.startswith(WORKSPACE + "/"):
        return f"refused: {path} is outside {WORKSPACE}"
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "w") as f:
        f.write(content)
    return f"wrote {len(content)} characters to {path}"


def call_tool(name, raw_args):
    try:
        args = json.loads(raw_args or "{}")
        if name == "run_shell":
            return run_shell(args["command"])
        if name == "write_file":
            return write_file(args["path"], args["content"])
        return f"unknown tool {name}"
    except (ValueError, KeyError, TypeError, OSError) as e:
        return f"tool error: {e}"


def initial_messages():
    with open(INSTRUCTION_FILE) as f:
        task = f.read()
    system = SYSTEM
    notes = os.path.join(WORKSPACE, NOTES_FILE)
    if os.path.isfile(notes):
        with open(notes) as f:
            system += f"\n\nProject notes from {NOTES_FILE}:\n" + f.read()
    return [{"role": "system", "content": system}, {"role": "user", "content": task}]


def main():
    messages = initial_messages()
    final = ""
    for turn in range(1, MAX_TURNS + 1):
        try:
            resp = chat(messages)
        except (urllib.error.URLError, OSError, ValueError) as e:
            detail = e.read().decode(errors="replace")[:500] if isinstance(e, urllib.error.HTTPError) else ""
            emit({"type": "error", "turn": turn, "error": str(e), "detail": detail})
            print(f"Stopped: model call failed on turn {turn}: {e}")
            return 1
        msg = resp["choices"][0]["message"]
        calls = msg.get("tool_calls") or []
        emit({"type": "step", "turn": turn, "text": msg.get("content") or "", "usage": resp.get("usage"),
              "tool_calls": [{"id": c["id"], "name": c["function"]["name"]} for c in calls]})
        messages.append({"role": "assistant", "content": msg.get("content"), **({"tool_calls": calls} if calls else {})})
        if not calls:
            final = msg.get("content") or ""
            break
        for c in calls:
            result = call_tool(c["function"]["name"], c["function"].get("arguments"))
            emit({"type": "tool_result", "turn": turn, "id": c["id"], "name": c["function"]["name"],
                  "output": result[:200]})
            messages.append({"role": "tool", "tool_call_id": c["id"], "content": result})
    else:
        print(f"Stopped: reached the turn cap of {MAX_TURNS} without a final answer.")
        return 0
    emit({"type": "final", "turns": turn})
    print(final)
    return 0


if __name__ == "__main__":
    sys.exit(main())
