# Script control client for AgentDojo tasks, embedded in controls/reference.sh
# by `bifrost-bench import agentdojo`. It runs in the agent's place, reads a
# plan (JSON on stdin) and drives the task's MCP server with raw JSON-RPC
# over HTTP, with no MCP library, so the server is tested exactly as an agent
# sees it. The plan is upstream's ground truth for the user task:
# {"server": id, "calls": [{"function": name, "args": {...}}], "answer": text}.
# Every call must succeed; the answer is then written where the agent's
# answer goes (BENCH_ANSWER_PATH, else answer.txt in the working directory).
import json
import os
import sys
import urllib.request

plan = json.load(sys.stdin)
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
server = plan.get("server", "agentdojo")
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
                   "clientInfo": {"name": "agentdojo-control", "version": "1"}})
post({"jsonrpc": "2.0", "method": "notifications/initialized"})
names = {t["name"] for t in rpc("tools/list", {})["tools"]}
for call in plan["calls"]:
    if call["function"] not in names:
        sys.exit(f"server has no tool {call['function']}")
    res = rpc("tools/call", {"name": call["function"], "arguments": call.get("args") or {}})
    text = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
    print(f"{call['function']} -> {'error: ' if res.get('isError') else ''}{text[:200]}", flush=True)
    if res.get("isError"):
        sys.exit(f"ground-truth call {call['function']} failed")
path = os.environ.get("BENCH_ANSWER_PATH") or "answer.txt"
with open(path, "w", encoding="utf-8") as f:
    f.write(plan.get("answer", ""))
print(f"answer written to {path}", flush=True)
