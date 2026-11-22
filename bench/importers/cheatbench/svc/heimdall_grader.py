"""CheatBench grader tool as a Heimdall MCP service.

Upstream runs the task's grader.py inside the episode and serves it over
streamable-HTTP MCP with core/agents/runner_support.py (serve_grader), after
core/grader_guard.py (install_grader_guard). Both files are copied verbatim
from the pinned upstream commit next to this one; this front only puts that
server on the service port at /mcp, which the bench's tool proxy reaches.
Nothing of the grader's logic is changed.
"""
import json
import os
import sys
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, "/opt/heimdall")
from grader_guard import install_grader_guard  # noqa: E402
from runner_support import serve_grader  # noqa: E402

TASK = "/opt/task"
WORKDIR = os.environ.get("CHEATBENCH_WORKDIR", "/workspace")


def load():
    row = json.load(open(os.path.join(TASK, "row.json")))
    staged = {}
    for dest in (row.get("files") or {}):
        staged[dest] = os.path.join(WORKDIR, dest)
    for dest in (row.get("files_abs") or {}):
        staged[dest] = dest
    ns = {"__name__": "task_grader"}
    install_grader_guard(open(os.path.join(TASK, "grader.py")).read(), ns)
    return serve_grader(ns, row, staged, WORKDIR)


class Front(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    upstream = ""

    def log_message(self, *a):
        pass

    def _relay(self, method):
        if self.path == "/healthz":
            self._send(200, b"ok\n", {"Content-Type": "text/plain"})
            return
        if not self.path.startswith("/mcp"):
            self._send(404, b"", {})
            return
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else None
        req = urllib.request.Request(self.upstream, data=body, method=method)
        for k in ("Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version"):
            if self.headers.get(k):
                req.add_header(k, self.headers[k])
        try:
            with urllib.request.urlopen(req, timeout=600) as r:
                data, code, hdrs = r.read(), r.status, r.headers
        except urllib.error.HTTPError as e:
            data, code, hdrs = e.read(), e.code, e.headers
        out = {k: hdrs[k] for k in ("Content-Type", "Mcp-Session-Id") if hdrs.get(k)}
        self._send(code, data, out)

    def _send(self, code, data, headers):
        self.send_response(code)
        for k, v in headers.items():
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        if data:
            self.wfile.write(data)

    def do_POST(self):
        self._relay("POST")

    def do_GET(self):
        self._relay("GET")

    def do_DELETE(self):
        self._relay("DELETE")


def main():
    server = load()
    Front.upstream = server.url
    port = int(os.environ.get("CHEATBENCH_PORT", "8000"))
    sys.stderr.write(f"cheatbench grader {server.grader_server_name}/{server.grader_tool_name} on :{port}\n")
    ThreadingHTTPServer(("0.0.0.0", port), Front).serve_forever()


if __name__ == "__main__":
    main()
