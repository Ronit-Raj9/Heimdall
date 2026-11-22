#!/usr/bin/env python3
"""Pull mode for a coding agent: repo in, patch out.

Register the agent with ``pull: {name: code-bot, repo: tarball}``. On a
coding task the first turn carries a signed, expiring link to the task's
repository (the base tree, never the hidden tests). This process downloads
it, lets a small tool-calling model loop edit the files locally, and answers
with ``git diff``: Heimdall applies the patch to the task's workspace and
grades it with the clean-room grader like a local agent's change.

    HEIMDALL_URL=... HEIMDALL_TOKEN=... OPENAI_API_KEY=... python3 coding_patch.py

Needs git. The model endpoint is yours: OPENAI_BASE_URL, OPENAI_API_KEY,
AGENT_MODEL. Standard library only. Replace edit_repo with your own agent.
"""

import json
import os
import subprocess
import sys
import tempfile
import urllib.request

try:
    import heimdall
except ImportError:  # running from the repository without installing heimdall-bench
    sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "python"))
    import heimdall

AGENT = os.environ.get("HEIMDALL_AGENT", "code-bot")
BASE_URL = os.environ.get("OPENAI_BASE_URL", "https://api.openai.com/v1").rstrip("/")
MODEL = os.environ.get("AGENT_MODEL", "gpt-5.4-mini")
MAX_STEPS = int(os.environ.get("AGENT_MAX_STEPS", "30"))
TOOLS = [
    {"type": "function", "function": {"name": "list_files", "description": "List the repository's files.",
                                      "parameters": {"type": "object", "properties": {}}}},
    {"type": "function", "function": {"name": "read_file", "description": "Read a file (path relative to the repository).",
                                      "parameters": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}}},
    {"type": "function", "function": {"name": "write_file", "description": "Replace a file's whole content.",
                                      "parameters": {"type": "object", "properties": {"path": {"type": "string"}, "content": {"type": "string"}},
                                                     "required": ["path", "content"]}}},
]


def git(repo, *args):
    return subprocess.run(["git", "-C", repo, *args], check=True, capture_output=True, text=True).stdout


def inside(repo, path):
    full = os.path.realpath(os.path.join(repo, path))
    if not full.startswith(os.path.realpath(repo) + os.sep):
        raise ValueError("%s is outside the repository" % path)
    return full


def run_tool(repo, name, args):
    try:
        if name == "list_files":
            return git(repo, "ls-files")[:20000]
        if name == "read_file":
            with open(inside(repo, args["path"]), encoding="utf-8", errors="replace") as f:
                return f.read()[:20000]
        if name == "write_file":
            path = inside(repo, args["path"])
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8") as f:
                f.write(args["content"])
            return "written"
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as e:
        return "error: %s" % e
    return "error: unknown tool %s" % name


def chat(messages):
    body = {"model": MODEL, "messages": messages, "tools": TOOLS}
    req = urllib.request.Request(BASE_URL + "/chat/completions", data=json.dumps(body).encode(), method="POST",
                                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + os.environ["OPENAI_API_KEY"]})
    with urllib.request.urlopen(req, timeout=300) as resp:
        return json.load(resp)["choices"][0]["message"]


def edit_repo(repo, instruction):
    messages = [{"role": "system", "content": "You are a coding agent. Change the repository's files to finish the task, then reply with a short summary."},
                {"role": "user", "content": instruction}]
    for _ in range(MAX_STEPS):
        msg = chat(messages)
        messages.append({k: v for k, v in msg.items() if k in ("role", "content", "tool_calls")})
        if not msg.get("tool_calls"):
            return msg.get("content") or ""
        for c in msg["tool_calls"]:
            out = run_tool(repo, c["function"]["name"], json.loads(c["function"].get("arguments") or "{}"))
            messages.append({"role": "tool", "tool_call_id": c["id"], "content": out})
    return "stopped after %d steps" % MAX_STEPS


def solve(turn):
    if not turn.repo:
        return "this agent only runs coding tasks"
    with tempfile.TemporaryDirectory(prefix="heimdall-repo-") as repo:
        turn.download_repo(repo)
        git(repo, "init", "-q")
        git(repo, "add", "-A")
        git(repo, "-c", "user.name=agent", "-c", "user.email=agent@localhost", "commit", "-qm", "base", "--allow-empty")
        summary = edit_repo(repo, turn.instruction)
        git(repo, "add", "-A")
        patch = git(repo, "diff", "--cached", "--binary")
    return heimdall.Answer(text=summary, patch=patch)


if __name__ == "__main__":
    heimdall.serve(AGENT, solve, concurrency=int(os.environ.get("CONCURRENCY", "2")), log=lambda m: print(m, file=sys.stderr))
