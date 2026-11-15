#!/usr/bin/env python3
"""A local stand-in for the fictional weather API, for trying live and record
mode by hand without calling any real third-party service.

It serves the answers of a cassette (default: the drone-window task's) over
plain HTTP on the host, under the base path /v2, and checks the API key the
tool proxy injects: with ACME_WEATHER_KEY set, a call without the header
X-Api-Key equal to it gets 401. It prints one line per call (method, path,
whether the key was right), never the key itself.

    ACME_WEATHER_KEY=demo-key python3 tools/standin.py --port 8765

Then point a scratch copy of the task at it (base_url:
http://<this host's LAN address>:8765/v2), and run an experiment with
external: record or external: live; docs/external-apis.md has the steps.
Standard library only.
"""

import argparse
import base64
import json
import os
import sys
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_CASSETTE = os.path.join(HERE, "..", "benchmarks", "acme-external", "tasks", "drone-window",
                                "cassettes", "weather.cassette.jsonl")


def load(path):
    answers = {}
    with open(path) as f:
        for line in f:
            if line.strip():
                e = json.loads(line)
                answers.setdefault((e["method"], e["path"], e.get("query", "")), e)
    return answers


def normalize(query):
    return urllib.parse.urlencode(sorted(urllib.parse.parse_qsl(query, keep_blank_values=True)))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8765)
    ap.add_argument("--bind", default="0.0.0.0")
    ap.add_argument("--base", default="/v2")
    ap.add_argument("--cassette", default=DEFAULT_CASSETTE)
    args = ap.parse_args()
    answers = load(args.cassette)
    key = os.environ.get("ACME_WEATHER_KEY", "")

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            u = urllib.parse.urlsplit(self.path)
            key_ok = not key or self.headers.get("X-Api-Key") == key
            print("%s %s key=%s" % (self.command, self.path, "ok" if key_ok else "missing or wrong"), flush=True)
            if not key_ok:
                return self.send(401, {"Content-Type": "application/json"}, b'{"error": "bad api key"}')
            path = u.path[len(args.base):] if u.path.startswith(args.base + "/") else None
            e = answers.get(("GET", path, normalize(u.query))) if path else None
            if e is None:
                return self.send(404, {"Content-Type": "application/json"}, b'{"error": "not found"}')
            body = base64.b64decode(e["body"]) if e.get("base64") else e["body"].encode()
            self.send(e.get("status", 200), e.get("headers") or {}, body)

        def send(self, status, headers, body):
            self.send_response(status)
            for k, v in headers.items():
                self.send_header(k, v)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass

    print("weather stand-in on %s:%d%s, %d answers, key check %s" % (args.bind, args.port, args.base, len(answers),
                                                                  "on" if key else "off"), flush=True)
    ThreadingHTTPServer((args.bind, args.port), Handler).serve_forever()


if __name__ == "__main__":
    sys.exit(main())
