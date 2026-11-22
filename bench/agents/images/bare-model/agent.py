#!/usr/bin/env python3
"""Bare-model baseline for Bifrost Bench: the same model with no agent scaffolding.

One model call with the task text and nothing else: no tools, no loop, no
project notes, no retries of its own. Its answer is the final message (and
the answer file when the task asks for one). Comparing an agent with this
baseline answers "does the agent add anything over the model?".

It speaks the OpenAI chat completions format to Bifrost's /openai/v1 route
with the run's virtual key, like every bench agent. Standard library only.
"""
import json
import os
import sys
import urllib.error
import urllib.request


def _env(name, default=None):
    """The bench variable name; a run with realism neutral passes AGENT_<rest>, read first."""
    neutral = "AGENT_" + name[len("BENCH_"):]
    if neutral in os.environ:
        return os.environ[neutral]
    if default is None:
        return os.environ[name]
    return os.environ.get(name, default)


def main():
    base = _env("BENCH_BASE_URL").rstrip("/")
    key = _env("BENCH_API_KEY")
    model = _env("BENCH_MODEL")
    session = _env("BENCH_SESSION_ID", "")
    answer_path = _env("BENCH_ANSWER_PATH", "")
    with open(_env("BENCH_INSTRUCTION_FILE", "/tmp/bench/instruction.md")) as f:
        task = f.read()
    body = json.dumps({"model": model, "messages": [{"role": "user", "content": task}]}).encode()
    headers = {"Authorization": "Bearer " + key, "Content-Type": "application/json"}
    if session:
        headers["x-bf-session-id"] = session
        headers["x-bf-session-affinity"] = "off"
    req = urllib.request.Request(base + "/chat/completions", data=body, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            out = json.load(resp)
    except (urllib.error.URLError, OSError, ValueError) as e:
        detail = e.read().decode(errors="replace")[:500] if isinstance(e, urllib.error.HTTPError) else ""
        print(json.dumps({"type": "error", "error": str(e), "detail": detail}), flush=True)
        print(f"Stopped: the model call failed: {e}")
        return 1
    text = (out["choices"][0]["message"].get("content") or "").strip()
    print(json.dumps({"type": "step", "turn": 1, "usage": out.get("usage")}), flush=True)
    if answer_path:
        try:
            os.makedirs(os.path.dirname(answer_path) or ".", exist_ok=True)
            with open(answer_path, "w") as f:
                f.write(text)
        except OSError as e:
            print(json.dumps({"type": "error", "error": "answer file: " + str(e)}), flush=True)
    print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
