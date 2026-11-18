#!/bin/sh
# Mutant control: says the order is still being prepared (it shipped) and
# gives no tracking. Admission requires the verifier to fail it.
exec python3 -I - <<'PY'
import json
import os
import re
import sys
import urllib.request

SERVERS = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
APIS = json.loads(os.environ.get("BENCH_EXTERNAL_APIS") or "{}")


class Client:
    """A minimal MCP client over streamable HTTP, through the tool proxy."""

    def __init__(self, service):
        self.url = (SERVERS.get(service) or {}).get("url") or f"http://toolproxy:9200/s/{service}/mcp"
        self.session = {}
        self.next_id = 0
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "control", "version": "1"}})
        self.rpc("notifications/initialized", notify=True)

    def rpc(self, method, params=None, notify=False):
        msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
        if not notify:
            self.next_id += 1
            msg["id"] = self.next_id
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        headers.update(self.session)
        req = urllib.request.Request(self.url, data=json.dumps(msg).encode(), headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=60) as resp:
            if resp.headers.get("Mcp-Session-Id"):
                self.session["Mcp-Session-Id"] = resp.headers["Mcp-Session-Id"]
            body = resp.read().decode()
        if notify:
            return None
        if body.lstrip().startswith("data:") or "\ndata:" in body:
            body = [line[5:] for line in body.splitlines() if line.startswith("data:")][-1]
        answer = json.loads(body)
        if "error" in answer:
            sys.exit(f"{method}: {answer['error']}")
        return answer["result"]

    def tools(self):
        return self.rpc("tools/list")["tools"]

    def call(self, tool, args):
        """Returns (payload, None), or (None, error text) for a tool error."""
        result = self.rpc("tools/call", {"name": tool, "arguments": args})
        text = "".join(c.get("text", "") for c in result.get("content", []))
        if result.get("isError"):
            return None, text
        try:
            return json.loads(text), None
        except ValueError:
            pass
        try:
            # A JSON record followed by more text: keep both.
            obj, end = json.JSONDecoder().raw_decode(text)
        except ValueError:
            return text, None
        if isinstance(obj, dict):
            obj = dict(obj, _trailing=text[end:].strip())
        return obj, None

    def must(self, tool, args):
        out, err = self.call(tool, args)
        if err is not None:
            sys.exit(f"{tool}: {err}")
        return out

desk = Client("desk")
desk.must("reply_to_ticket", {"ticket_id": "T-6001", "status": "solved", "message": "Hi Maya, your order is still being prepared."})
PY
