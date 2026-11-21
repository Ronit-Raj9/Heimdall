"""Reference control for a CL-Bench stream, written by `bifrost-bench import clbench`.

Plays the stream through the clenv MCP endpoint in $BENCH_MCP_SERVERS. Each
turn it submits {"__reference__": <token>}; the service answers that token,
and only that token, with upstream's own reference policy (for
exploitable_poker: src/tasks/exploitable_poker/oracle.py
expert_exploit_action). The token lives in this control and the service's
environment, never in the agent's container. With BENCH_SESSION_INDEX set it
plays that session only, else every session in order.
"""

import json
import os
import sys
import urllib.request

TOKEN = "__TOKEN__"
SESSIONS = int("__SESSIONS__")


def endpoint() -> str:
    servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
    if "clenv" in servers:
        return servers["clenv"]["url"]
    return "http://clenv:8000/mcp"


class Client:
    def __init__(self, url: str) -> None:
        self.url = url
        self.sid = None
        self.n = 0
        self.call("initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "clbench-reference", "version": "1"}})
        self.post({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def post(self, msg):
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        if self.sid:
            headers["Mcp-Session-Id"] = self.sid
        req = urllib.request.Request(self.url, data=json.dumps(msg).encode(), headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=900) as resp:
            self.sid = resp.headers.get("Mcp-Session-Id") or self.sid
            body = resp.read().decode()
            ctype = resp.headers.get("Content-Type", "")
        if not body.strip():
            return None
        if "text/event-stream" in ctype:
            last = None
            for line in body.splitlines():
                if line.startswith("data:"):
                    last = json.loads(line[5:].strip())
            return last
        return json.loads(body)

    def call(self, method, params):
        self.n += 1
        out = self.post({"jsonrpc": "2.0", "id": self.n, "method": method, "params": params})
        if out is None or "error" in out:
            raise RuntimeError(f"{method}: {out}")
        return out["result"]

    def tool(self, name, args):
        res = self.call("tools/call", {"name": name, "arguments": args})
        if res.get("isError"):
            raise RuntimeError(f"{name}: {res}")
        sc = res.get("structuredContent")
        if isinstance(sc, dict):
            return sc.get("result", sc) if set(sc) == {"result"} else sc
        return json.loads(res["content"][0]["text"])


def main() -> int:
    c = Client(endpoint())
    idx = os.environ.get("BENCH_SESSION_INDEX")
    sessions = [int(idx)] if idx else list(range(1, SESSIONS + 1))
    for s in sessions:
        view = c.tool("start_session", {"session": s})
        if "error" in view:
            print(view, file=sys.stderr)
            return 1
        while view.get("status") == "awaiting_response":
            out = c.tool("submit", {"response": {"__reference__": TOKEN}})
            if "error" in out:
                print(out, file=sys.stderr)
                return 1
            view = out["next"]
        print(f"session {s}: {view.get('status')} {view.get('instances_completed')}/{view.get('instances_total')}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
