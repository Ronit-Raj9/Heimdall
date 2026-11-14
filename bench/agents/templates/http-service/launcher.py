#!/usr/bin/env python3
"""Launcher for an agent that is a server (template "http-service").

The bench starts this file as the agent's command. It starts the server
(AGENT_SERVER_CMD) in the run container, waits for AGENT_HEALTH_URL to answer
200, POSTs the task to AGENT_RUN_URL as JSON

    {"instruction", "workspace", "base_url", "api_key", "model", "session_id"}

and waits for the answer. A 200 or 201 answer is the result. A 202 answer
with {"id": ...} is polled at AGENT_STATUS_URL ("{id}" in it is replaced by
the id; otherwise "/<id>" is appended) until its status is no longer
"queued", "pending" or "running". A result is {"status": "completed" or
"failed", "final_message": "..."}. The launcher prints the final message last
on stdout, where the bench reads the completion claim, stops the server and
exits 0 when the status is completed (or absent on a 200), 1 otherwise. The
server's own output goes to stderr. Standard library only.
"""
import json
import os
import shlex
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

SERVER_CMD = os.environ["AGENT_SERVER_CMD"]
HEALTH_URL = os.environ["AGENT_HEALTH_URL"]
RUN_URL = os.environ["AGENT_RUN_URL"]
STATUS_URL = os.environ.get("AGENT_STATUS_URL", "")
STARTUP_TIMEOUT_S = float(os.environ.get("AGENT_STARTUP_TIMEOUT_S", "60"))
RUN_TIMEOUT_S = float(os.environ.get("AGENT_RUN_TIMEOUT_S", "840"))
POLL_S = 2.0
PENDING = ("queued", "pending", "running", "in_progress")


def log(msg):
    print("launcher: " + msg, file=sys.stderr, flush=True)


def start_server():
    # The server gets the key with each task, not in its environment.
    env = {k: v for k, v in os.environ.items() if k != "BENCH_API_KEY"}
    return subprocess.Popen(shlex.split(SERVER_CMD), env=env, stdout=sys.stderr, stderr=sys.stderr,
                            cwd=os.environ.get("BENCH_WORKSPACE", "/workspace"))


def stop_server(proc):
    if proc.poll() is not None:
        return
    proc.send_signal(signal.SIGTERM)
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()


def request(url, body=None, timeout=10.0):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"},
                                 method="POST" if data is not None else "GET")
    with urllib.request.urlopen(req, timeout=max(timeout, 1.0)) as resp:
        raw = resp.read()
        return resp.status, (json.loads(raw) if raw.strip() else {})


def wait_healthy(proc, deadline):
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"server exited with code {proc.returncode} before it was healthy")
        try:
            if request(HEALTH_URL, timeout=2)[0] == 200:
                return
        except (urllib.error.URLError, OSError, ValueError):
            pass
        time.sleep(0.5)
    raise RuntimeError(f"server not healthy at {HEALTH_URL} after {STARTUP_TIMEOUT_S:.0f}s")


def task():
    with open(os.environ.get("BENCH_INSTRUCTION_FILE", "/tmp/bench/instruction.md")) as f:
        instruction = f.read()
    return {
        "instruction": instruction,
        "workspace": os.environ.get("BENCH_WORKSPACE", "/workspace"),
        "base_url": os.environ["BENCH_BASE_URL"],
        "api_key": os.environ["BENCH_API_KEY"],
        "model": os.environ["BENCH_MODEL"],
        "session_id": os.environ.get("BENCH_SESSION_ID", ""),
    }


def run(deadline):
    status, body = request(RUN_URL, task(), timeout=deadline - time.monotonic())
    if status == 202:
        if not STATUS_URL or "id" not in body:
            raise RuntimeError("server answered 202 but AGENT_STATUS_URL or the id is missing")
        url = STATUS_URL.replace("{id}", str(body["id"])) if "{id}" in STATUS_URL else STATUS_URL.rstrip("/") + "/" + str(body["id"])
        while str(body.get("status", "running")).lower() in PENDING:
            if time.monotonic() + POLL_S > deadline:
                raise RuntimeError(f"run still {body.get('status')} after {RUN_TIMEOUT_S:.0f}s")
            time.sleep(POLL_S)
            _, body = request(url, timeout=min(30.0, deadline - time.monotonic()))
    return body


def main():
    start = time.monotonic()
    proc = start_server()
    try:
        wait_healthy(proc, start + STARTUP_TIMEOUT_S)
        body = run(start + RUN_TIMEOUT_S)
    except (RuntimeError, urllib.error.URLError, OSError, ValueError, KeyError) as e:
        log(str(e))
        print(f"Stopped: {e}")
        return 1
    finally:
        stop_server(proc)
    final = body.get("final_message") or body.get("output") or body.get("message") or ""
    print(final if isinstance(final, str) else json.dumps(final))
    return 0 if str(body.get("status", "completed")).lower() == "completed" else 1


if __name__ == "__main__":
    sys.exit(main())
