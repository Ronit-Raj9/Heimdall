"""Front for one EnterpriseOps-Gym MCP server (bifrost-bench import enterpriseops).

Start: runs the upstream server (`python -m uvicorn main:app`) on its own port
inside this container, seeds the task database from /bench/task/seed.sql with
POST /api/seed-database (database id "bench"), then listens on :8080.

:8080 serves GET /healthz and the MCP endpoint /mcp only. Each request is
forwarded with the headers upstream's harness adds (x-database-id and the
task's context, keys without an x- prefix becoming x-<key with - for _>).
tools/list is cut to the task's selected tools and tools/call of any other
tool is refused, as upstream's oracle setting does. /api/* (seeding, SQL) is
never reachable from the agent.

`python /bench/bench_front.py export` runs the task's checks
(/bench/task/checks.json, upstream database_state verifiers) through the
upstream /api/sql-runner and prints the raw results as JSON; the verifier
compares them offline.
"""

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TASK = "/bench/task"
DB = "bench"
UP_PORT = int(os.environ.get("BENCH_UP_PORT", "8005"))
UP_APP = os.environ.get("BENCH_UP_APP", "main:app")
UP = "http://127.0.0.1:%d" % UP_PORT


def load(name, default):
    try:
        with open(os.path.join(TASK, name)) as f:
            return json.load(f)
    except FileNotFoundError:
        return default


CONTEXT = load("context.json", {})
ALLOWED = set(load("tools.json", []))


def ctx_headers():
    h = {"x-database-id": DB}
    for k, v in CONTEXT.items():
        key = k if k.lower().startswith("x-") else "x-" + k.lower().replace("_", "-")
        h[key] = str(v)
    return h


def up_request(method, path, body=None, headers=None, timeout=1200):
    h = {"Content-Type": "application/json"}
    h.update(headers or {})
    req = urllib.request.Request(UP + path, data=body, headers=h, method=method)
    return urllib.request.urlopen(req, timeout=timeout)


def wait_up(limit=120):
    end = time.time() + limit
    while time.time() < end:
        for p in ("/health", "/docs", "/"):
            try:
                up_request("GET", p, timeout=3).read()
                return
            except urllib.error.HTTPError:
                return
            except Exception:
                pass
        time.sleep(0.5)
    raise SystemExit("upstream server did not start")


def seed():
    with open(os.path.join(TASK, "seed.sql"), encoding="utf-8") as f:
        sql = f.read()
    body = json.dumps({"database_id": DB, "name": "bench", "description": "task seed", "sql_content": sql}).encode()
    out = json.loads(up_request("POST", "/api/seed-database", body).read())
    # Most servers answer {"success": true}; the drive server {"status": "success"}.
    if not (out.get("success") is True or out.get("status") == "success"):
        raise SystemExit("seed failed: %s" % out)


def export():
    results = []
    for c in load("checks.json", []):
        q = c.get("validation_config", {}).get("query", "")
        body = json.dumps({"query": q, "database_id": DB}).encode()
        try:
            r = json.loads(up_request("POST", "/api/sql-runner", body, ctx_headers(), timeout=60).read())
            results.append({"name": c.get("name"), "success": True, "result": r})
        except urllib.error.HTTPError as e:
            results.append({"name": c.get("name"), "success": False, "error": "HTTP %d: %s" % (e.code, e.read().decode(errors="replace")[:2000])})
        except Exception as e:  # noqa: BLE001
            results.append({"name": c.get("name"), "success": False, "error": str(e)})
    json.dump(results, sys.stdout)


def rpc_error(rid, msg):
    return {"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": msg}}


class Front(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        sys.stderr.write("front: " + fmt % args + "\n")

    def reply(self, code, body, ctype="application/json", extra=None):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            return self.reply(200, b'{"ok":true}')
        if self.path.split("?")[0] == "/mcp":
            return self.reply(405, b'{"error":"use POST"}')
        self.reply(404, b'{"error":"not found"}')

    def do_DELETE(self):
        if self.path.split("?")[0] != "/mcp":
            return self.reply(404, b'{"error":"not found"}')
        self.forward(b"")

    def do_POST(self):
        if self.path.split("?")[0] != "/mcp":
            return self.reply(404, b'{"error":"not found"}')
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        msg = None
        try:
            msg = json.loads(body or b"null")
        except ValueError:
            pass
        if ALLOWED and isinstance(msg, dict) and msg.get("method") == "tools/call":
            name = (msg.get("params") or {}).get("name")
            if name not in ALLOWED:
                return self.reply(200, json.dumps(rpc_error(msg.get("id"), "tool %r is not offered in this task" % name)).encode())
        self.forward(body, msg)

    def forward(self, body, msg=None):
        h = {k: v for k, v in self.headers.items() if k.lower() in ("accept", "mcp-session-id", "mcp-protocol-version", "content-type")}
        h.update(ctx_headers())
        try:
            r = up_request(self.command, self.path, body or None, h, timeout=600)
            code, data, rh = r.status, r.read(), r.headers
        except urllib.error.HTTPError as e:
            code, data, rh = e.code, e.read(), e.headers
        ctype = rh.get("Content-Type", "application/json")
        if ALLOWED and isinstance(msg, dict) and msg.get("method") == "tools/list":
            data = filter_tools(data, ctype)
        extra = {}
        if rh.get("mcp-session-id"):
            extra["mcp-session-id"] = rh["mcp-session-id"]
        self.reply(code, data, ctype, extra)


def filter_tools(data, ctype):
    def cut(obj):
        tools = ((obj or {}).get("result") or {}).get("tools")
        if isinstance(tools, list):
            obj["result"]["tools"] = [t for t in tools if t.get("name") in ALLOWED]
        return obj

    text = data.decode("utf-8", errors="replace")
    if "text/event-stream" in ctype:
        out = []
        for line in text.split("\n"):
            if line.startswith("data:"):
                try:
                    line = "data: " + json.dumps(cut(json.loads(line[5:])))
                except ValueError:
                    pass
            out.append(line)
        return "\n".join(out).encode()
    try:
        return json.dumps(cut(json.loads(text))).encode()
    except ValueError:
        return data


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "export":
        return export()
    os.chdir("/app")
    subprocess.Popen([sys.executable, "-m", "uvicorn", UP_APP, "--host", "127.0.0.1", "--port", str(UP_PORT)])
    wait_up()
    seed()
    ThreadingHTTPServer(("0.0.0.0", 8080), Front).serve_forever()


if __name__ == "__main__":
    main()
