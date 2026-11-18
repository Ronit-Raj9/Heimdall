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

Optionally, steps are reported as agent events (docs/agent-events.md): the
launcher reports its handoff to the server with report_event, and the server
inherits BENCH_EVENTS_URL and BENCH_EVENTS_FILE in its environment, so it can
report its own steps the same way (heimdall.events in bench/clients/python).
With neither variable set nothing is reported.
"""
import datetime
import json
import os
import shlex
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

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


def _env(name, default=None):
    """A bench variable: AGENT_<name> in a run with realism neutral, else
    BENCH_<name> (docs/eval-awareness.md). Without a default it must be set."""
    v = os.environ.get("AGENT_" + name) or os.environ.get("BENCH_" + name)
    if v:
        return v
    if default is None:
        raise KeyError("BENCH_" + name)
    return default


EVENTS_URL = os.environ.get("AGENT_EVENTS_URL") or os.environ.get("BENCH_EVENTS_URL", "")
EVENTS_FILE = os.environ.get("AGENT_EVENTS_FILE") or os.environ.get("BENCH_EVENTS_FILE", "")


def now_rfc3339():
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def report_event(type, name=None, **fields):
    """Report one agent step (spec/agent-event.schema.json) and return its id.

    POSTs one JSON line to BENCH_EVENTS_URL, else (or when that fails) appends
    it to BENCH_EVENTS_FILE. Does nothing when the bench sets neither, and
    never raises.
    """
    event = dict({"type": type, "id": uuid.uuid4().hex, "start_time": now_rfc3339()}, **fields)
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


def start_server():
    # The server gets the key with each task, not in its environment.
    env = {k: v for k, v in os.environ.items() if k not in ("AGENT_API_KEY", "BENCH_API_KEY")}
    return subprocess.Popen(shlex.split(SERVER_CMD), env=env, stdout=sys.stderr, stderr=sys.stderr,
                            cwd=_env("WORKSPACE", "/workspace"))


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
    with open(_env("INSTRUCTION_FILE", "/tmp/bench/instruction.md")) as f:
        instruction = f.read()
    return {
        "instruction": instruction,
        "workspace": _env("WORKSPACE", "/workspace"),
        "base_url": _env("BASE_URL"),
        "api_key": _env("API_KEY"),
        "model": _env("MODEL"),
        "session_id": _env("SESSION_ID", ""),
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
        handoff_start = now_rfc3339()
        body = run(start + RUN_TIMEOUT_S)
    except (RuntimeError, urllib.error.URLError, OSError, ValueError, KeyError) as e:
        log(str(e))
        report_event("error", "launcher", output=str(e), status="error")
        print(f"Stopped: {e}")
        return 1
    finally:
        stop_server(proc)
    final = body.get("final_message") or body.get("output") or body.get("message") or ""
    completed = str(body.get("status", "completed")).lower() == "completed"
    report_event("handoff", "agent-server", input={"run_url": RUN_URL}, output=final, status="ok" if completed else "error",
                 start_time=handoff_start, end_time=now_rfc3339())
    print(final if isinstance(final, str) else json.dumps(final))
    return 0 if completed else 1


if __name__ == "__main__":
    sys.exit(main())
