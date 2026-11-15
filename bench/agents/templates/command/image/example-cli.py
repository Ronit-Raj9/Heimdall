#!/usr/bin/env python3
"""example-cli: a stand-in for your command-line agent (template "command").

Replace it with your own CLI in the Dockerfile and argv. It shows the shape
the bench needs from any CLI: take the task file, the workspace and the model
as arguments, read the base URL and key from the environment variables most
SDKs read (OPENAI_BASE_URL, OPENAI_API_KEY), edit the workspace, print the
final message last and exit 0. Standard library only.

Optionally, a CLI reports its own steps as stdout lines that start with
"@@heimdall-event " (docs/agent-events.md); report_event shows how. The bench
shows them as agent-reported evidence beside what it observed itself.
"""
import argparse
import json
import os
import sys
import urllib.request

WRITE_FILE = {"type": "function", "function": {
    "name": "write_file", "description": "Write a whole file; the path is relative to the workspace.",
    "parameters": {"type": "object", "properties": {"path": {"type": "string"}, "content": {"type": "string"}},
                   "required": ["path", "content"]}}}


def report_event(type, name=None, **fields):
    """Print one of the CLI's own steps as a stdout line: "@@heimdall-event " and one JSON object.

    Printed only when the bench collects events for the run (it then sets
    BENCH_EVENTS_FILE or BENCH_EVENTS_URL), so stdout is unchanged otherwise.
    """
    if not (os.environ.get("BENCH_EVENTS_FILE") or os.environ.get("BENCH_EVENTS_URL")):
        return
    event = dict({"type": type}, **fields)
    if name is not None:
        event["name"] = name
    for k in ("input", "output"):
        if isinstance(event.get(k), str):
            event[k] = event[k][:4000]
    print("@@heimdall-event " + json.dumps(event, default=str), flush=True)


def chat(messages):
    headers = {"Authorization": "Bearer " + os.environ["OPENAI_API_KEY"], "Content-Type": "application/json"}
    if os.environ.get("BENCH_SESSION_ID"):
        headers["x-bf-session-id"] = os.environ["BENCH_SESSION_ID"]
    body = json.dumps({"model": ARGS.model, "messages": messages, "tools": [WRITE_FILE]}).encode()
    url = os.environ["OPENAI_BASE_URL"].rstrip("/") + "/chat/completions"
    with urllib.request.urlopen(urllib.request.Request(url, data=body, headers=headers), timeout=600) as r:
        return json.load(r)["choices"][0]["message"]


def write_file(path, content):
    full = os.path.realpath(os.path.join(ARGS.workspace, path))
    if not full.startswith(os.path.realpath(ARGS.workspace) + "/"):
        return "refused: outside the workspace"
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "w") as f:
        f.write(content)
    return "ok"


def main():
    with open(ARGS.instruction_file) as f:
        messages = [{"role": "user", "content": f.read()}]
    for _ in range(ARGS.max_turns):
        msg = chat(messages)
        calls = msg.get("tool_calls") or []
        messages.append({"role": "assistant", "content": msg.get("content"), **({"tool_calls": calls} if calls else {})})
        if not calls:
            print(msg.get("content") or "")
            return 0
        for c in calls:
            try:
                args = json.loads(c["function"].get("arguments") or "{}")
                result = write_file(args["path"], args["content"])
            except (ValueError, KeyError, TypeError, OSError) as e:
                result = f"tool error: {e}"
            report_event("tool_result", "write_file", tool_call_id=c["id"], output=result,
                         status="ok" if result == "ok" else "error")
            messages.append({"role": "tool", "tool_call_id": c["id"], "content": result})
    print("Stopped: turn cap reached without a final answer.")
    return 0


if __name__ == "__main__":
    p = argparse.ArgumentParser(prog="example-cli")
    p.add_argument("--instruction-file", required=True)
    p.add_argument("--workspace", required=True)
    p.add_argument("--model", required=True)
    p.add_argument("--max-turns", type=int, default=20)
    ARGS = p.parse_args()
    sys.exit(main())
