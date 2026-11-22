#!/usr/bin/env python3
"""Pull mode with a plain function: your agent runs here, Heimdall sends it turns.

Register an agent with adapter pull (``pull: {name: support-bot}``), start an
evaluation on it, then run this file where your agent lives:

    HEIMDALL_URL=https://heimdall.example.com HEIMDALL_TOKEN=hm_... \\
        OPENAI_API_KEY=sk-... python3 plain_function.py

The agent below is one OpenAI-compatible chat call per turn, with the task's
tools offered as function tools. Heimdall runs every tool call it returns
through the task's tools (so the evaluation records them) and sends the
results back as the next turn. The model endpoint is yours: OPENAI_BASE_URL
(default https://api.openai.com/v1), OPENAI_API_KEY, AGENT_MODEL.

Standard library only. Stop with Ctrl-C.
"""

import json
import os
import sys
import urllib.request

try:
    import heimdall
except ImportError:  # running from the repository without installing heimdall-bench
    sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "python"))
    import heimdall

AGENT = os.environ.get("HEIMDALL_AGENT", "support-bot")
BASE_URL = os.environ.get("OPENAI_BASE_URL", "https://api.openai.com/v1").rstrip("/")
MODEL = os.environ.get("AGENT_MODEL", "gpt-5.4-mini")
SYSTEM = "You are a helpful support agent. Use the tools when they help, then answer briefly."


def chat(messages, tools):
    body = {"model": MODEL, "messages": [{"role": "system", "content": SYSTEM}] + messages}
    if tools:
        body["tools"] = tools
    req = urllib.request.Request(BASE_URL + "/chat/completions", data=json.dumps(body).encode(), method="POST",
                                 headers={"Content-Type": "application/json",
                                          "Authorization": "Bearer " + os.environ["OPENAI_API_KEY"]})
    with urllib.request.urlopen(req, timeout=300) as resp:
        return json.load(resp)


def solve(turn):
    # turn.messages is the chat so far in OpenAI roles: the task (or the
    # customer's messages when Heimdall drives a chat), your earlier replies
    # and tool calls, and the tool results of a "tool_results" turn.
    doc = chat(turn.messages, turn.openai_tools())
    msg = doc["choices"][0]["message"]
    usage = doc.get("usage") or {}
    calls = [heimdall.ToolCall(id=c["id"], name=c["function"]["name"],
                               arguments=json.loads(c["function"].get("arguments") or "{}"))
             for c in msg.get("tool_calls") or []]
    return heimdall.Answer(text=msg.get("content") or "", tool_calls=calls,
                           usage={"input_tokens": usage.get("prompt_tokens", 0), "output_tokens": usage.get("completion_tokens", 0)})


if __name__ == "__main__":
    n = heimdall.serve(AGENT, solve, concurrency=int(os.environ.get("CONCURRENCY", "4")), log=lambda m: print(m, file=sys.stderr))
    print("handled %d turns" % n, file=sys.stderr)
