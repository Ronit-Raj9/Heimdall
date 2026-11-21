"""CheatBench svgs leaderboard as a Heimdall service.

Upstream's host starts tasks/svgs/leaderboard_server.py (copied verbatim from
the pinned commit to /opt/svgs/leaderboard_server.py) for the episode's row
and writes its endpoint and token into the staged leaderboard_config.json.
Here the endpoint is this service (http://leaderboard:8000/submit) with the
token staged at import; this front swaps it for the server's own token and
relays to it. The submission records (upstream's leaderboard log) are
written to /state/leaderboard.json for export.
"""
import json
import os
import sys
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, "/opt/svgs")
import leaderboard_server  # noqa: E402

CFG = json.load(open("/opt/task/leaderboard.json"))
ROW = json.load(open("/opt/task/row.json"))
STATE = "/state/leaderboard.json"


class Front(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    upstream = ""
    token = ""
    rnd = None

    def log_message(self, *a):
        pass

    def do_GET(self):
        code, data = (200, b"ok\n") if self.path == "/healthz" else (404, b"")
        self._send(code, data, "text/plain")

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        got = (self.headers.get("Authorization") or "").removeprefix("Bearer ")
        auth = "Bearer " + (self.token if got == CFG["token"] else got)
        req = urllib.request.Request(self.upstream + self.path, data=self.rfile.read(n), method="POST",
                                     headers={"Content-Type": self.headers.get("Content-Type") or "application/json",
                                              "Authorization": auth})
        try:
            with urllib.request.urlopen(req, timeout=300) as r:
                code, data = r.status, r.read()
        except urllib.error.HTTPError as e:
            code, data = e.code, e.read()
        self._send(code, data, "application/json")
        try:
            with open(STATE, "w") as f:
                json.dump({ROW["id"]: self.rnd.calls}, f)
        except OSError:
            pass

    def _send(self, code, data, ctype):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        if data:
            self.wfile.write(data)


def main():
    rnd = leaderboard_server.Round(ROW)
    server, endpoints = leaderboard_server.start([rnd])
    _, ep = endpoints[0]
    Front.upstream = "http://127.0.0.1:%d" % server.server_port
    Front.token, Front.rnd = ep["token"], rnd
    os.makedirs(os.path.dirname(STATE), exist_ok=True)
    ThreadingHTTPServer(("0.0.0.0", 8000), Front).serve_forever()


if __name__ == "__main__":
    main()
