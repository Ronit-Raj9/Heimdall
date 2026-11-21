# Script control client for BALROG tasks, embedded in controls/reference.sh
# and controls/mutant1.sh by `bifrost-bench import balrog`. It runs in the
# agent's place, reads a plan (JSON on stdin: {"server": id, "actions": [...]})
# and plays it through the task's MCP server with raw JSON-RPC over HTTP,
# with no MCP library, so the server is tested exactly as an agent sees it.
import json
import os
import sys
import urllib.request

plan = json.load(sys.stdin)
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
server = plan.get("server", "balrog")
url = (servers.get(server) or {}).get("url") or "http://toolproxy:9200/s/" + server + "/mcp"
state = {"id": 0, "session": None}


def post(msg):
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    if state["session"]:
        headers["Mcp-Session-Id"] = state["session"]
    req = urllib.request.Request(url, data=json.dumps(msg).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=120) as r:
        if r.headers.get("Mcp-Session-Id"):
            state["session"] = r.headers.get("Mcp-Session-Id")
        body = r.read().decode()
        ctype = r.headers.get("Content-Type") or ""
    if "id" not in msg:
        return None
    if ctype.startswith("text/event-stream"):
        data = [ln[5:].strip() for ln in body.splitlines() if ln.startswith("data:")]
        body = data[-1] if data else "{}"
    return json.loads(body)


def rpc(method, params):
    state["id"] += 1
    doc = post({"jsonrpc": "2.0", "id": state["id"], "method": method, "params": params})
    if "error" in doc:
        sys.exit(f"{method}: {doc['error']}")
    return doc["result"]


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                   "clientInfo": {"name": "balrog-control", "version": "1"}})
post({"jsonrpc": "2.0", "method": "notifications/initialized"})
names = {t["name"] for t in rpc("tools/list", {})["tools"]}
if not {"observe", "act"} <= names:
    sys.exit(f"server lacks observe/act: {sorted(names)}")
rpc("tools/call", {"name": "observe", "arguments": {}})
for a in plan["actions"]:
    res = rpc("tools/call", {"name": "act", "arguments": {"action": a}})
    text = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
    print(f"{a} -> {text[:120]!r}", flush=True)
    if res.get("isError"):
        break
