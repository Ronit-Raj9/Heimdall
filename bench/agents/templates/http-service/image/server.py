#!/usr/bin/env python3
"""Example agent server (template "http-service"), standard library only.

Stands in for your LangGraph, FastAPI or Agents SDK app: GET /health answers
200, POST /run takes {instruction, workspace, base_url, api_key, model,
session_id}, runs a small tool loop against base_url with api_key, and answers
{"status": "completed", "final_message": ...}. Replace it with your server.
"""
import json
import os
import sys
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[sys.argv.index("--port") + 1]) if "--port" in sys.argv else 8765
TOOL = {"type": "function", "function": {"name": "write_file", "description": "Write a file under the workspace.",
        "parameters": {"type": "object", "properties": {"path": {"type": "string"}, "content": {"type": "string"}},
                       "required": ["path", "content"]}}}


def solve(t):
    messages, root = [{"role": "user", "content": t["instruction"]}], os.path.realpath(t["workspace"])
    headers = {"Authorization": "Bearer " + t["api_key"], "Content-Type": "application/json"}
    if t.get("session_id"):
        headers["x-bf-session-id"] = t["session_id"]
    for _ in range(20):
        body = json.dumps({"model": t["model"], "messages": messages, "tools": [TOOL]}).encode()
        req = urllib.request.Request(t["base_url"].rstrip("/") + "/chat/completions", data=body, headers=headers)
        with urllib.request.urlopen(req, timeout=600) as r:
            msg = json.load(r)["choices"][0]["message"]
        calls = msg.get("tool_calls") or []
        messages.append({"role": "assistant", "content": msg.get("content"), **({"tool_calls": calls} if calls else {})})
        if not calls:
            return msg.get("content") or ""
        for c in calls:
            a = json.loads(c["function"].get("arguments") or "{}")
            path = os.path.realpath(os.path.join(root, str(a.get("path", ""))))
            ok = path.startswith(root + "/")
            if ok:
                os.makedirs(os.path.dirname(path), exist_ok=True)
                with open(path, "w") as f:
                    f.write(str(a.get("content", "")))
            messages.append({"role": "tool", "tool_call_id": c["id"], "content": "ok" if ok else "refused"})
    return "Stopped: turn cap reached."


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.reply(200 if self.path == "/health" else 404, {"status": "ok"})

    def do_POST(self):
        if self.path != "/run":
            return self.reply(404, {"error": "not found"})
        t = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        try:
            self.reply(200, {"status": "completed", "final_message": solve(t)})
        except Exception as e:  # report the failure to the launcher instead of dropping the connection
            self.reply(200, {"status": "failed", "final_message": f"agent error: {e}"})

    def reply(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):
        sys.stderr.write("server: " + fmt % args + "\n")


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
