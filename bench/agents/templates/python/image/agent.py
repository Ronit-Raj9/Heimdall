#!/usr/bin/env python3
"""Minimal coding agent for Bifrost Bench (template "python"). Standard library only.

The three rules every bench agent follows:
  1. Read the task from the instruction file (BENCH_INSTRUCTION_FILE).
  2. Edit /workspace, then exit. The bench grades the diff, not the output.
  3. Send every model call to BENCH_BASE_URL with BENCH_API_KEY, the run's
     Bifrost virtual key. Nothing else is reachable from the container.

It speaks OpenAI chat completions to Bifrost's /openai/v1 route; Bifrost
translates to whichever provider the run's key allows (model ids are written
provider/model, e.g. anthropic/claude-sonnet-4-5). stdout is one JSON line per
step, then the final message as plain text, so the bench reads the completion
claim from the end of stdout without a parser. Replace the loop with yours and
keep the env contract.

Optionally, the agent reports its own steps (tool calls, results, errors)
with report_event: the bench shows them as agent-reported evidence beside
what it observed itself (docs/agent-events.md). With no events variable set
it does nothing, so the agent runs the same with or without it.
"""
import datetime
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
import uuid

BASE_URL = os.environ["BENCH_BASE_URL"].rstrip("/")
API_KEY = os.environ["BENCH_API_KEY"]
MODEL = os.environ["BENCH_MODEL"]
INSTRUCTION_FILE = os.environ.get("BENCH_INSTRUCTION_FILE", "/tmp/bench/instruction.md")
SESSION_ID = os.environ.get("BENCH_SESSION_ID", "")
MAX_TURNS = int(os.environ.get("BENCH_MAX_TURNS", "40"))
SHELL_TIMEOUT_S = int(os.environ.get("BENCH_SHELL_TIMEOUT_S", "120"))
WORKSPACE = "/workspace"
NOTES_FILE = "AGENTS.md"  # notes.project_file in the agent YAML
OUTPUT_MAX = 8000

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


EVENTS_URL = os.environ.get("BENCH_EVENTS_URL", "")
EVENTS_FILE = os.environ.get("BENCH_EVENTS_FILE", "")


def report_event(type, name=None, **fields):
    """Report one of the agent's own steps (spec/agent-event.schema.json) and return its id.

    POSTs one JSON line to BENCH_EVENTS_URL, else (or when that fails) appends
    it to BENCH_EVENTS_FILE. Does nothing when the bench sets neither, and
    never raises. heimdall.events in bench/clients/python is the full version.
    """
    now = datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")
    event = dict({"type": type, "id": uuid.uuid4().hex, "start_time": now}, **fields)
    if name is not None:
        event["name"] = name
    if not (EVENTS_URL or EVENTS_FILE):
        return event["id"]
    for k in ("input", "output"):
        if k in event:
            v = event[k] if isinstance(event[k], str) else json.dumps(event[k], default=str)
            event[k] = v[:4000]
    line = (json.dumps(event, ensure_ascii=False, default=str) + "\n").encode()
    try:
        if EVENTS_URL:
            req = urllib.request.Request(EVENTS_URL, data=line, method="POST", headers={"Content-Type": "application/x-ndjson"})
            urllib.request.urlopen(req, timeout=2).close()
            return event["id"]
    except Exception:
        pass
    try:
        if EVENTS_FILE:
            with open(EVENTS_FILE, "ab") as f:
                f.write(line)
    except OSError:
        pass
    return event["id"]


def chat(messages):
    """One model call. Retries are left to the gateway, so a fault reaches the agent."""
    body = json.dumps({"model": MODEL, "messages": messages, "tools": TOOLS}).encode()
    headers = {"Authorization": "Bearer " + API_KEY, "Content-Type": "application/json"}
    if SESSION_ID:
        headers["x-bf-session-id"] = SESSION_ID
        headers["x-bf-session-affinity"] = "off"
    req = urllib.request.Request(BASE_URL + "/chat/completions", data=body, headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=600) as resp:
        return json.load(resp)


def run_shell(command):
    # The run key stays out of the commands' environment: the model's commands never need it.
    env = {k: v for k, v in os.environ.items() if k != "BENCH_API_KEY"}
    try:
        p = subprocess.run(command, shell=True, cwd=WORKSPACE, capture_output=True, text=True,
                           timeout=SHELL_TIMEOUT_S, env=env)
        out = f"exit {p.returncode}\n{p.stdout}{p.stderr}"
    except subprocess.TimeoutExpired:
        out = f"timed out after {SHELL_TIMEOUT_S}s"
    return out[-OUTPUT_MAX:]


def write_file(path, content):
    full = os.path.realpath(os.path.join(WORKSPACE, path))
    if not full.startswith(WORKSPACE + "/"):
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
    for turn in range(1, MAX_TURNS + 1):
        try:
            resp = chat(messages)
        except (urllib.error.URLError, OSError, ValueError) as e:
            emit({"type": "error", "turn": turn, "error": str(e)})
            report_event("error", "chat", output=str(e), status="error", attributes={"turn": turn})
            print(f"Stopped: model call failed on turn {turn}: {e}")
            return 1
        msg = resp["choices"][0]["message"]
        calls = msg.get("tool_calls") or []
        emit({"type": "step", "turn": turn, "usage": resp.get("usage"),
              "tool_calls": [c["function"]["name"] for c in calls]})
        messages.append({"role": "assistant", "content": msg.get("content"), **({"tool_calls": calls} if calls else {})})
        if not calls:
            print(msg.get("content") or "")
            return 0
        for c in calls:
            name, raw_args = c["function"]["name"], c["function"].get("arguments")
            call_id = report_event("tool_call", name, tool_call_id=c["id"], input=raw_args, attributes={"turn": turn})
            result = call_tool(name, raw_args)
            report_event("tool_result", name, parent_id=call_id, tool_call_id=c["id"], output=result)
            messages.append({"role": "tool", "tool_call_id": c["id"], "content": result})
    print(f"Stopped: reached the turn cap of {MAX_TURNS} without a final answer.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
