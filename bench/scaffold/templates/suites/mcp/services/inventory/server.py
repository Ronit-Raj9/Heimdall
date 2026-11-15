#!/usr/bin/env python3
"""Inventory MCP server: the tool service of a Heimdall mcp suite.

Written by `bifrost-bench suite init -template mcp`. Python standard library
only, so the image needs nothing but python3 and never reaches the network.

It speaks the Model Context Protocol over streamable HTTP: JSON-RPC 2.0
messages POSTed to /mcp (initialize, ping, tools/list, tools/call), each
answered with one application/json response; notifications are acknowledged
with 202. GET /healthz answers 200 when the server is up.

State: the seed file (SEED_PATH, copied in by the task's `files:`) is loaded
at start and written to STATE_PATH at once and after every change, so the
bench can export it after the agent exits (the task's `export:`) and the
hidden verifier can read it at /grader/state/<service>/<export>.

To make it yours, edit the "Tools" section: each tool is a name, a
description, a JSON Schema for its arguments and a Python function that gets
the database (a dict) and the arguments and returns a JSON-able result, or
raises ToolError for an error the agent should see. The protocol section
needs no change.

Commands:
  python3 server.py           serve (PORT, default 8000)
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

SEED_PATH = os.environ.get("SEED_PATH", "/seed/db.json")
STATE_PATH = os.environ.get("STATE_PATH", "/state/db.json")
PORT = int(os.environ.get("PORT", "8000"))
MCP_PATH = os.environ.get("MCP_PATH", "/mcp")
SERVER_NAME = "inventory"
SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ["2025-06-18", "2025-03-26", "2024-11-05"]
MAX_BODY = 1 << 20


class ToolError(Exception):
    """An error the agent sees as a tool result with isError true."""


# ---------------------------------------------------------------- Tools ----


def find_item(db, sku):
    for item in db["items"]:
        if item["sku"] == sku:
            return item
    raise ToolError(f"no item with sku {sku!r}")


def list_items(db, args):
    return {"items": [{"sku": i["sku"], "name": i["name"], "stock": i["stock"]} for i in db["items"]]}


def get_item(db, args):
    return find_item(db, args["sku"])


def adjust_stock(db, args):
    item = find_item(db, args["sku"])
    delta = args["delta"]
    reason = args["reason"].strip()
    if not isinstance(delta, int) or isinstance(delta, bool) or delta == 0:
        raise ToolError("delta must be a non-zero integer")
    if not reason:
        raise ToolError("reason must not be empty")
    if item["stock"] + delta < 0:
        raise ToolError(f"stock of {item['sku']} cannot go below zero (now {item['stock']})")
    item["stock"] += delta
    entry = {"seq": len(db["adjustments"]) + 1, "sku": item["sku"], "delta": delta, "reason": reason}
    db["adjustments"].append(entry)
    return {"item": item, "adjustment": entry}


TOOLS = [
    {
        "name": "list_items",
        "description": "List every item with its SKU, name and current stock.",
        "inputSchema": {"type": "object", "properties": {}},
        "handler": list_items,
    },
    {
        "name": "get_item",
        "description": "Get one item by SKU, with its stock and storage location.",
        "inputSchema": {
            "type": "object",
            "properties": {"sku": {"type": "string", "description": "Item SKU, e.g. W-100"}},
            "required": ["sku"],
        },
        "handler": get_item,
    },
    {
        "name": "adjust_stock",
        "description": "Add (positive delta) or remove (negative delta) units of an item and record why.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "sku": {"type": "string"},
                "delta": {"type": "integer", "description": "Units to add, or remove when negative"},
                "reason": {"type": "string", "description": "Why the stock changes, e.g. a delivery number"},
            },
            "required": ["sku", "delta", "reason"],
        },
        "handler": adjust_stock,
        "writes": True,
    },
]


# ------------------------------------------------------------- Protocol ----


class Store:
    """The database, loaded from the seed and saved after every change."""

    def __init__(self, seed_path, state_path):
        with open(seed_path, encoding="utf-8") as f:
            self.db = json.load(f)
        self.state_path = state_path
        self.lock = threading.Lock()
        self.save()

    def save(self):
        folder = os.path.dirname(self.state_path) or "."
        os.makedirs(folder, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=folder, prefix=".state-")
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(self.db, f, indent=2, sort_keys=True)
            f.write("\n")
        os.chmod(tmp, 0o644)
        os.replace(tmp, self.state_path)

    def call(self, tool, args):
        with self.lock:
            result = tool["handler"](self.db, args)
            if tool.get("writes"):
                self.save()
            return result


def missing_args(tool, args):
    required = tool["inputSchema"].get("required", [])
    return [name for name in required if name not in args]


def rpc_error(msg_id, code, message):
    return {"jsonrpc": "2.0", "id": msg_id, "error": {"code": code, "message": message}}


def rpc_result(msg_id, result):
    return {"jsonrpc": "2.0", "id": msg_id, "result": result}


def tool_result(payload, is_error=False):
    text = payload if isinstance(payload, str) else json.dumps(payload, sort_keys=True)
    return {"content": [{"type": "text", "text": text}], "isError": is_error}


def handle_message(store, msg):
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
        version = asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0]
        return rpc_result(msg_id, {
            "protocolVersion": version,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
        })
    if method == "ping":
        return rpc_result(msg_id, {})
    if method == "tools/list":
        listed = [{k: t[k] for k in ("name", "description", "inputSchema")} for t in TOOLS]
        return rpc_result(msg_id, {"tools": listed})
    if method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}
        tool = next((t for t in TOOLS if t["name"] == name), None)
        if tool is None:
            return rpc_error(msg_id, -32602, f"unknown tool: {name}")
        if not isinstance(args, dict):
            return rpc_result(msg_id, tool_result("arguments must be an object", True))
        missing = missing_args(tool, args)
        if missing:
            return rpc_result(msg_id, tool_result("missing arguments: " + ", ".join(missing), True))
        try:
            return rpc_result(msg_id, tool_result(store.call(tool, args)))
        except ToolError as e:
            return rpc_result(msg_id, tool_result(str(e), True))
        except (KeyError, TypeError, ValueError, AttributeError) as e:
            return rpc_result(msg_id, tool_result(f"bad arguments: {e}", True))
    return rpc_error(msg_id, -32601, f"method not found: {method}")


class Handler(BaseHTTPRequestHandler):
    server_version = "inventory-mcp/1.0"
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

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/healthz":
            self.send_json(200, {"ok": True, "server": SERVER_NAME})
        elif path == MCP_PATH:
            # No server-initiated stream: every answer comes on its POST.
            self.send_empty(405, {"Allow": "POST, DELETE"})
        else:
            self.send_json(404, {"error": "not found"})

    def do_DELETE(self):
        if self.path.split("?", 1)[0] == MCP_PATH:
            self.send_empty(200)
        else:
            self.send_json(404, {"error": "not found"})

    def do_POST(self):
        if self.path.split("?", 1)[0] != MCP_PATH:
            self.send_json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > MAX_BODY:
            self.send_json(400, rpc_error(None, -32600, "missing or oversized body"))
            return
        try:
            msg = json.loads(self.rfile.read(length))
        except ValueError:
            self.send_json(400, rpc_error(None, -32700, "parse error"))
            return
        batch = isinstance(msg, list)
        answers = [a for a in (handle_message(self.server.store, m) for m in (msg if batch else [msg])) if a is not None]
        headers = {}
        if any(isinstance(m, dict) and m.get("method") == "initialize" for m in (msg if batch else [msg])):
            headers["Mcp-Session-Id"] = uuid.uuid4().hex
        if not answers:
            self.send_empty(202, headers)
        else:
            self.send_json(200, answers if batch else answers[0], headers)

    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))


def serve():
    httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    httpd.store = Store(SEED_PATH, STATE_PATH)
    sys.stderr.write(f"{SERVER_NAME} MCP server on :{PORT}{MCP_PATH}, state in {STATE_PATH}\n")
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
