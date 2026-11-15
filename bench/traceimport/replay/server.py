#!/usr/bin/env python3
"""Replay tool server of a task drafted by `bifrost-bench task from-trace`.

Python standard library only, so the image needs nothing but python3 and
never reaches the network. It answers tool calls with the results a recorded
session got: the tool results are replayed, not live.

Seed: SEED_PATH (default /seed/tools.observed.json, copied in by the task's
`files:`) holds the recorded calls, in order, each with a name, arguments,
and a result or an error. A call is answered with the first unused recorded
call of the same name whose arguments are equal, else the first unused call
of that name, else the last call of that name again. A tool the session
never called is unknown.

State: every call the agent makes is appended to STATE_PATH (default
/state/calls.json) as {"seq", "tool", "arguments", "replayed", "exact",
"is_error"}, so the bench exports it after the agent exits and the hidden
verifier reads it at /grader/state/<service>/calls.json. "replayed" is the
seq of the recorded call that answered (null for an unknown tool) and
"exact" says whether its arguments matched.

Protocols, both on one port (PORT, default 8000):
  MCP over streamable HTTP: JSON-RPC 2.0 POSTed to MCP_PATH (default /mcp):
    initialize, ping, tools/list, tools/call; notifications get 202.
  Plain HTTP: GET / lists the tools; POST /tools/<name> with the arguments
    as a JSON object answers 200 {"result": ...} or 502 {"error": ...}.
  GET /healthz answers 200 when the server is up.

Commands:
  python3 server.py           serve
  python3 server.py ready     exit 0 when the local server answers /healthz
"""

import json
import os
import sys
import tempfile
import threading
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SEED_PATH = os.environ.get("SEED_PATH", "/seed/tools.observed.json")
STATE_PATH = os.environ.get("STATE_PATH", "/state/calls.json")
PORT = int(os.environ.get("PORT", "8000"))
MCP_PATH = os.environ.get("MCP_PATH", "/mcp")
SERVER_NAME = "trace-replay"
PROTOCOL_VERSIONS = ["2025-06-18", "2025-03-26", "2024-11-05"]
MAX_BODY = 1 << 20


class Replay:
    """The recorded calls, which of them were used, and the calls made."""

    def __init__(self, seed_path, state_path):
        with open(seed_path, encoding="utf-8") as f:
            seed = json.load(f)
        self.recorded = [c for c in seed.get("calls") or [] if isinstance(c, dict) and c.get("name")]
        self.names = []
        for c in self.recorded:
            if c["name"] not in self.names:
                self.names.append(c["name"])
        self.used = set()
        self.calls = []
        self.state_path = state_path
        self.lock = threading.Lock()
        self.save()

    def save(self):
        folder = os.path.dirname(self.state_path) or "."
        os.makedirs(folder, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=folder, prefix=".state-")
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump({"calls": self.calls}, f, indent=2, sort_keys=True)
            f.write("\n")
        os.chmod(tmp, 0o644)
        os.replace(tmp, self.state_path)

    def call(self, name, args):
        """Returns (text, is_error), or None for a tool never recorded."""
        with self.lock:
            same = [c for c in self.recorded if c["name"] == name]
            hit, exact = None, False
            for c in same:
                if c["seq"] not in self.used and c.get("arguments") == args:
                    hit, exact = c, True
                    break
            if hit is None:
                hit = next((c for c in same if c["seq"] not in self.used), same[-1] if same else None)
            if hit is not None:
                self.used.add(hit["seq"])
            is_error = bool(hit and hit.get("is_error"))
            self.calls.append({"seq": len(self.calls) + 1, "tool": name, "arguments": args,
                               "replayed": hit["seq"] if hit else None, "exact": exact, "is_error": is_error})
            self.save()
        if hit is None:
            return None
        if is_error:
            return hit.get("error") or hit.get("result") or "tool error", True
        return hit.get("result") or "", False

    def example(self, name):
        return next((c.get("arguments") for c in self.recorded if c["name"] == name), {})


def rpc_error(msg_id, code, message):
    return {"jsonrpc": "2.0", "id": msg_id, "error": {"code": code, "message": message}}


def rpc_result(msg_id, result):
    return {"jsonrpc": "2.0", "id": msg_id, "result": result}


def handle_message(replay, msg):
    """Answers one JSON-RPC message; None for notifications and responses."""
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0":
        return rpc_error(None, -32600, "invalid request")
    method = msg.get("method")
    msg_id = msg.get("id")
    if method is None or "id" not in msg:
        return None
    params = msg.get("params") or {}
    if method == "initialize":
        asked = params.get("protocolVersion")
        return rpc_result(msg_id, {
            "protocolVersion": asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0],
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": SERVER_NAME, "version": "1.0.0"},
        })
    if method == "ping":
        return rpc_result(msg_id, {})
    if method == "tools/list":
        tools = [{"name": n, "description": f"The {n} tool.", "inputSchema": {"type": "object"}} for n in replay.names]
        return rpc_result(msg_id, {"tools": tools})
    if method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}
        answer = replay.call(name, args)
        if answer is None:
            return rpc_error(msg_id, -32602, f"unknown tool: {name}")
        text, is_error = answer
        return rpc_result(msg_id, {"content": [{"type": "text", "text": text}], "isError": is_error})
    return rpc_error(msg_id, -32601, f"method not found: {method}")


class Handler(BaseHTTPRequestHandler):
    server_version = "trace-replay/1.0"
    protocol_version = "HTTP/1.1"

    def send_json(self, status, body, headers=None):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(data)

    def send_empty(self, status, headers=None):
        self.send_response(status)
        self.send_header("Content-Length", "0")
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()

    def read_json(self):
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > MAX_BODY:
            raise ValueError("missing or oversized body")
        return json.loads(self.rfile.read(length))

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        replay = self.server.replay
        if path == "/healthz":
            self.send_json(200, {"ok": True, "server": SERVER_NAME})
        elif path == MCP_PATH:
            self.send_empty(405, {"Allow": "POST, DELETE"})
        elif path in ("/", "/tools"):
            self.send_json(200, {"call": "POST /tools/<name> with the arguments as a JSON object",
                                 "tools": [{"name": n, "example_arguments": replay.example(n)} for n in replay.names]})
        else:
            self.send_json(404, {"error": "not found"})

    def do_DELETE(self):
        if self.path.split("?", 1)[0] == MCP_PATH:
            self.send_empty(200)
        else:
            self.send_json(404, {"error": "not found"})

    def do_POST(self):
        path = self.path.split("?", 1)[0]
        if path.startswith("/tools/"):
            self.post_tool(path[len("/tools/"):])
        elif path == MCP_PATH:
            self.post_mcp()
        else:
            self.send_json(404, {"error": "not found"})

    def post_tool(self, name):
        try:
            args = self.read_json()
        except ValueError as e:
            self.send_json(400, {"error": f"arguments: {e}"})
            return
        answer = self.server.replay.call(name, args)
        if answer is None:
            self.send_json(404, {"error": f"unknown tool: {name}"})
        elif answer[1]:
            self.send_json(502, {"error": answer[0]})
        else:
            self.send_json(200, {"result": answer[0]})

    def post_mcp(self):
        try:
            msg = self.read_json()
        except ValueError:
            self.send_json(400, rpc_error(None, -32700, "parse error"))
            return
        batch = isinstance(msg, list)
        msgs = msg if batch else [msg]
        answers = [a for a in (handle_message(self.server.replay, m) for m in msgs) if a is not None]
        headers = {}
        if any(isinstance(m, dict) and m.get("method") == "initialize" for m in msgs):
            headers["Mcp-Session-Id"] = uuid.uuid4().hex
        if not answers:
            self.send_empty(202, headers)
        else:
            self.send_json(200, answers if batch else answers[0], headers)

    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))


def serve():
    httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    httpd.replay = Replay(SEED_PATH, STATE_PATH)
    sys.stderr.write(f"{SERVER_NAME} on :{PORT}, {len(httpd.replay.recorded)} recorded calls, state in {STATE_PATH}\n")
    httpd.serve_forever()


def ready():
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/healthz", timeout=2) as r:
            return 0 if r.status == 200 else 1
    except OSError:
        return 1


if __name__ == "__main__":
    if sys.argv[1:] == ["ready"]:
        sys.exit(ready())
    serve()
