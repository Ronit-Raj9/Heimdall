"""CheatBench creative_writings review endpoint as a Heimdall service.

Upstream's host binds core/review_server.py (ReviewServer, verbatim from the
pinned commit and copied beside this file as /opt/cheatbench/core) for each
episode, with the task's pairwise prompt and its grader model. Here the model
is the suite's pinned judge, reached through the gateway on the service's own
virtual key (BENCH_SVC_GATEWAY_URL, BENCH_SVC_GATEWAY_KEY, BENCH_SVC_MODEL).
The review logic, prompt, retries and conversation are upstream's. This file
only fixes the bearer token (staged in grading/.review), puts the endpoint on
the service port and relays the client's model calls to the gateway.
"""
import json
import os
import sys
import threading
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, "/opt/cheatbench")
CFG = json.load(open("/opt/task/review.json"))
GW_URL = os.environ.get("BENCH_SVC_GATEWAY_URL", "").rstrip("/")
GW_KEY = os.environ.get("BENCH_SVC_GATEWAY_KEY", "")
GW_MODEL = os.environ.get("BENCH_SVC_MODEL", "")
STATE = "/state/review.json"


class Relay(BaseHTTPRequestHandler):
    """OpenAI-compatible relay to the gateway: adds the virtual key and the
    gateway's model name; the client keeps upstream's request body."""

    def log_message(self, *a):
        pass

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n) or b"{}")
        if GW_MODEL:
            body["model"] = GW_MODEL
        req = urllib.request.Request(GW_URL + self.path, data=json.dumps(body).encode(), method="POST",
                                     headers={"Content-Type": "application/json", "x-bf-vk": GW_KEY})
        try:
            with urllib.request.urlopen(req, timeout=900) as r:
                code, data = r.status, r.read()
        except urllib.error.HTTPError as e:
            code, data = e.code, e.read()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


class Front(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    upstream = ""
    server_ref = None

    def log_message(self, *a):
        pass

    def do_GET(self):
        code, data = (200, b"ok\n") if self.path == "/healthz" else (404, b"")
        self._send(code, data, "text/plain")

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        req = urllib.request.Request(self.upstream + self.path, data=self.rfile.read(n), method="POST")
        for k in ("Content-Type", "Authorization"):
            if self.headers.get(k):
                req.add_header(k, self.headers[k])
        try:
            with urllib.request.urlopen(req, timeout=900) as r:
                code, data = r.status, r.read()
        except urllib.error.HTTPError as e:
            code, data = e.code, e.read()
        self._send(code, data, "application/json")
        try:  # the reviewer's record (upstream grader_state), exported after the run
            with open(STATE, "w") as f:
                json.dump(self.server_ref.state, f)
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
    relay = ThreadingHTTPServer(("127.0.0.1", 9000), Relay)
    threading.Thread(target=relay.serve_forever, daemon=True).start()
    os.environ["HEIMDALL_GW_KEY"] = GW_KEY or "unset"
    from core import review_server
    config = {k: v for k, v in CFG.items() if k != "token"}
    parts = review_server.parse_prompt(open("/opt/task/prompt.md", encoding="utf-8").read())
    config["system"], config["user_template"] = parts["system"], parts["user_template"]
    config["reference_sha256"] = review_server.norm_digest(open("/opt/task/gold.md", encoding="utf-8").read())
    config.update({"api_key_env": "HEIMDALL_GW_KEY", "api_base_url": "http://127.0.0.1:9000/v1"})
    server = review_server.start_review_server(config)
    server.token = CFG["token"]
    Front.upstream = "http://127.0.0.1:%d" % server.server_address[1]
    Front.server_ref = server
    os.makedirs(os.path.dirname(STATE), exist_ok=True)
    port = int(os.environ.get("CHEATBENCH_PORT", "8000"))
    sys.stderr.write(f"cheatbench review endpoint on :{port} model {config.get('model')}\n")
    ThreadingHTTPServer(("0.0.0.0", port), Front).serve_forever()


if __name__ == "__main__":
    main()
