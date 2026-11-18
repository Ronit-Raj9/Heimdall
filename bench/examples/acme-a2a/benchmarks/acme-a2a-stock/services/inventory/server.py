#!/usr/bin/env python3
"""Inventory HTTP tool server: the tool service of a Heimdall tool-use suite.

Written by `bifrost-bench suite init -template tool-use`. Python standard
library only, so the image needs nothing but python3 and never reaches the
network.

The agent reaches it through the per-run tool proxy at
http://toolproxy:9200/s/inventory/, which records every call in the run's
tool ledger and can inject faults. Endpoints (GET / lists them too):

  GET  /healthz              200 when the server is up
  GET  /                     the endpoint list, for the agent to discover
  GET  /items                every item: sku, name, stock
  GET  /items/<sku>          one item
  POST /items/<sku>/adjust   {"delta": n, "reason": "..."}: add or remove units

Errors answer 4xx with {"error": "..."}.

State: the seed file (SEED_PATH, copied in by the task's `files:`) is loaded
at start and written to STATE_PATH at once and after every change, so the
bench can export it after the agent exits (the task's `export:`) and the
hidden verifier can read it at /grader/state/<service>/<export>.

To make it yours, edit the "Routes" section: each route is a method, a path
pattern and a Python function that gets the database (a dict), the path
parameters and the JSON body, and returns a JSON-able result, or raises
ApiError with a status and a message.

Commands:
  python3 server.py           serve (PORT, default 8000)
  python3 server.py ready     exit 0 when the local server answers /healthz
"""

import json
import os
import re
import sys
import tempfile
import threading
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SEED_PATH = os.environ.get("SEED_PATH", "/seed/db.json")
STATE_PATH = os.environ.get("STATE_PATH", "/state/db.json")
PORT = int(os.environ.get("PORT", "8000"))
SERVER_NAME = "inventory"
MAX_BODY = 1 << 20


class ApiError(Exception):
    """An error answered with an HTTP status and {"error": message}."""

    def __init__(self, status, message):
        super().__init__(message)
        self.status = status


# --------------------------------------------------------------- Routes ----


def find_item(db, sku):
    for item in db["items"]:
        if item["sku"] == sku:
            return item
    raise ApiError(404, f"no item with sku {sku!r}")


def list_items(db, params, body):
    return {"items": [{"sku": i["sku"], "name": i["name"], "stock": i["stock"]} for i in db["items"]]}


def get_item(db, params, body):
    return find_item(db, params["sku"])


def adjust_stock(db, params, body):
    item = find_item(db, params["sku"])
    delta = body.get("delta")
    reason = str(body.get("reason") or "").strip()
    if not isinstance(delta, int) or isinstance(delta, bool) or delta == 0:
        raise ApiError(400, "delta must be a non-zero integer")
    if not reason:
        raise ApiError(400, "reason must not be empty")
    if item["stock"] + delta < 0:
        raise ApiError(409, f"stock of {item['sku']} cannot go below zero (now {item['stock']})")
    item["stock"] += delta
    entry = {"seq": len(db["adjustments"]) + 1, "sku": item["sku"], "delta": delta, "reason": reason}
    db["adjustments"].append(entry)
    return {"item": item, "adjustment": entry}


ROUTES = [
    ("GET", "/items", list_items, False, "List every item with its SKU, name and current stock."),
    ("GET", "/items/{sku}", get_item, False, "Get one item by SKU, with its stock and storage location."),
    ("POST", "/items/{sku}/adjust", adjust_stock, True,
     'Add or remove units of an item. Body: {"delta": <non-zero integer>, "reason": "<why, e.g. a delivery number>"}.'),
]


# -------------------------------------------------------------- Serving ----


def compile_route(pattern):
    return re.compile("^" + re.sub(r"\\{(\w+)\\}", r"(?P<\1>[^/]+)", re.escape(pattern)) + "$")


COMPILED = [(m, compile_route(p), fn, writes) for m, p, fn, writes, _ in ROUTES]


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


class Handler(BaseHTTPRequestHandler):
    server_version = "inventory-tools/1.0"
    protocol_version = "HTTP/1.1"

    def send_json(self, status, body):
        data = json.dumps(body, sort_keys=True).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def dispatch(self, method):
        path = self.path.split("?", 1)[0].rstrip("/") or "/"
        if method == "GET" and path == "/healthz":
            return self.send_json(200, {"ok": True, "server": SERVER_NAME})
        if method == "GET" and path == "/":
            return self.send_json(200, {"service": SERVER_NAME, "endpoints": [
                {"method": m, "path": p, "description": d} for m, p, _, _, d in ROUTES]})
        body = {}
        if method == "POST":
            length = int(self.headers.get("Content-Length") or 0)
            if length > MAX_BODY:
                return self.send_json(413, {"error": "body too large"})
            try:
                body = json.loads(self.rfile.read(length) or b"{}")
            except ValueError:
                return self.send_json(400, {"error": "body is not JSON"})
            if not isinstance(body, dict):
                return self.send_json(400, {"error": "body must be a JSON object"})
        allowed = False
        for m, rx, fn, writes in COMPILED:
            match = rx.match(path)
            if not match:
                continue
            if m != method:
                allowed = True
                continue
            store = self.server.store
            try:
                with store.lock:
                    result = fn(store.db, match.groupdict(), body)
                    if writes:
                        store.save()
            except ApiError as e:
                return self.send_json(e.status, {"error": str(e)})
            return self.send_json(200, result)
        if allowed:
            return self.send_json(405, {"error": f"{method} not allowed on {path}"})
        return self.send_json(404, {"error": f"no endpoint {method} {path}; GET / lists them"})

    def do_GET(self):
        self.dispatch("GET")

    def do_POST(self):
        self.dispatch("POST")

    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))


def serve():
    httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    httpd.store = Store(SEED_PATH, STATE_PATH)
    sys.stderr.write(f"{SERVER_NAME} tool server on :{PORT}, state in {STATE_PATH}\n")
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
