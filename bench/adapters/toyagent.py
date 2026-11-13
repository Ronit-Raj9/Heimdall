#!/usr/bin/env python3
"""Toy agent for Bifrost Bench SYNTHETIC smoke runs.

Makes three Anthropic Messages API calls through the gateway with the run's
virtual key, prints every response as one stream-json line in the shape Claude
Code uses (so the claudecode parser reads it), then applies the task's
reference patch and claims success. If a call fails it stops, prints the error
the way Claude Code does and says it could not finish. Standard library only.

Configuration comes from the environment the scripted adapter sets:
BENCH_GATEWAY_URL, BENCH_RUN_KEY, BENCH_MODEL, BENCH_SESSION,
BENCH_INSTRUCTION, BENCH_PATCH, BENCH_WORKSPACE.
"""

import json
import os
import subprocess
import sys
import urllib.error
import urllib.request

OUTAGE_TEXT = "The model is unavailable, so I could not finish."
SUCCESS_TEXT = "I applied the fix to the workspace. All tests pass."
PATCH_FAILED_TEXT = "I could not apply the fix to the workspace."

TOOLS = [
    {
        "name": "read_file",
        "description": "Read a file from the workspace.",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
        },
    }
]

FOLLOW_UPS = [
    "Explain the fix you would make, in one sentence.",
    "Confirm the change is complete.",
]


def env(name, default=None):
    value = os.environ.get(name, default)
    if value is None:
        raise SystemExit("missing environment variable " + name)
    return value


GATEWAY = env("BENCH_GATEWAY_URL").rstrip("/")
KEY = env("BENCH_RUN_KEY")
MODEL = env("BENCH_MODEL")
SESSION = env("BENCH_SESSION", "")
INSTRUCTION = env("BENCH_INSTRUCTION", "/tmp/bench/instruction.md")
PATCH = env("BENCH_PATCH", "/tmp/bench/reference.diff")
WORKSPACE = env("BENCH_WORKSPACE", "/workspace")


def emit(obj):
    sys.stdout.write(json.dumps(obj, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def call_model(messages, tools):
    body = {"model": MODEL, "max_tokens": 1024, "messages": messages}
    if tools:
        body["tools"] = tools
    request = urllib.request.Request(
        GATEWAY + "/anthropic/v1/messages",
        data=json.dumps(body).encode("utf-8"),
        method="POST",
        headers={
            "content-type": "application/json",
            "anthropic-version": "2023-06-01",
            "x-api-key": KEY,
            "x-bf-session-id": SESSION,
            "x-bf-session-affinity": "off",
        },
    )
    with urllib.request.urlopen(request, timeout=120) as response:
        return json.loads(response.read().decode("utf-8"))


def error_kind(status):
    """Map an HTTP status to Claude Code's SDKAssistantMessageError names."""
    if status in (401, 403):
        return "authentication_failed"
    if status == 404:
        return "model_not_found"
    if status == 429:
        return "rate_limit"
    if status == 529:
        return "overloaded"
    if status == 400:
        return "invalid_request"
    if status is not None and status >= 500:
        return "server_error"
    return "unknown"


def fail(turn, status, detail):
    """Report a failed model call the way Claude Code does, then stop."""
    status_text = str(status) if status is not None else "connection error"
    emit(
        {
            "type": "assistant",
            "message": {
                "id": "msg_synthetic_%d" % turn,
                "type": "message",
                "role": "assistant",
                "model": "<synthetic>",
                "content": [{"type": "text", "text": "API Error: %s %s" % (status_text, detail[:300])}],
                "stop_reason": "stop_sequence",
                "usage": {"input_tokens": 0, "output_tokens": 0},
            },
            "error": error_kind(status),
            "parent_tool_use_id": None,
            "session_id": SESSION,
        }
    )
    emit(
        {
            "type": "result",
            "subtype": "success",
            "is_error": True,
            "num_turns": turn,
            "result": OUTAGE_TEXT,
            "session_id": SESSION,
        }
    )
    print(OUTAGE_TEXT, file=sys.stderr)
    sys.exit(1)


def run_tool(block):
    """Run the toy's one tool: read a workspace file, capped at 4 KB."""
    rel = str(block.get("input", {}).get("path", ""))
    full = os.path.normpath(os.path.join(WORKSPACE, rel))
    if full != WORKSPACE and not full.startswith(WORKSPACE.rstrip("/") + "/"):
        return "path is outside the workspace", True
    try:
        with open(full, "r", encoding="utf-8", errors="replace") as handle:
            return handle.read(4096), False
    except OSError as exc:
        return str(exc), True


def answer_tools(content):
    """Return tool_result blocks for every tool_use block in content."""
    results = []
    for block in content:
        if block.get("type") != "tool_use":
            continue
        text, is_error = run_tool(block)
        results.append(
            {"type": "tool_result", "tool_use_id": block.get("id", ""), "content": text, "is_error": is_error}
        )
    return results


def main():
    with open(INSTRUCTION, "r", encoding="utf-8") as handle:
        instruction = handle.read()
    emit({"type": "system", "subtype": "init", "cwd": WORKSPACE, "model": MODEL, "tools": ["read_file"], "session_id": SESSION})

    messages = [{"role": "user", "content": instruction}]
    # Only the first call offers the tool. Later calls repeat the definition
    # only when the history holds tool_use blocks, which the API requires.
    tools = TOOLS
    used_tools = False
    for turn in range(1, 4):
        try:
            response = call_model(messages, tools)
        except urllib.error.HTTPError as exc:
            fail(turn, exc.code, exc.read().decode("utf-8", errors="replace"))
        except (urllib.error.URLError, OSError, ValueError) as exc:
            fail(turn, None, str(exc))
        emit({"type": "assistant", "message": response, "parent_tool_use_id": None, "session_id": SESSION})

        content = response.get("content") or [{"type": "text", "text": "(no content)"}]
        messages.append({"role": "assistant", "content": content})
        results = answer_tools(content)
        if turn == 3:
            break
        if results:
            used_tools = True
            emit({"type": "user", "message": {"role": "user", "content": results}, "parent_tool_use_id": None, "session_id": SESSION})
            messages.append({"role": "user", "content": results + [{"type": "text", "text": FOLLOW_UPS[turn - 1]}]})
        else:
            messages.append({"role": "user", "content": FOLLOW_UPS[turn - 1]})
        tools = TOOLS if used_tools else None

    applied = subprocess.run(
        ["git", "-c", "safe.directory=*", "apply", "--whitespace=nowarn", PATCH],
        cwd=WORKSPACE,
        capture_output=True,
        text=True,
    )
    if applied.returncode != 0:
        emit({"type": "result", "subtype": "error_during_execution", "is_error": True, "num_turns": 3, "result": PATCH_FAILED_TEXT, "errors": [applied.stderr.strip()[:500]], "session_id": SESSION})
        sys.exit(1)
    emit({"type": "result", "subtype": "success", "is_error": False, "num_turns": 3, "result": SUCCESS_TEXT, "session_id": SESSION})


if __name__ == "__main__":
    main()
