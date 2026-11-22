# Script control client for ToolBench-X tasks, embedded in each controls/*.sh
# by `bifrost-bench import toolbench-x`. It runs in the agent's place, reads a
# plan (JSON on stdin) and drives the task's MCP server with raw JSON-RPC over
# HTTP. Plan steps: {"call": name, "arguments": {...}, "retries": n} calls a
# tool (retrying a failed call up to n more times), {"submit": text} calls
# submit_answer.
import json
import os
import sys
import urllib.request

plan = json.load(sys.stdin)
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
url = (servers.get(plan.get("server", "toolbench")) or {}).get("url") or "http://toolproxy:9200/s/toolbench/mcp"
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


def call(name, arguments):
    res = rpc("tools/call", {"name": name, "arguments": arguments})
    text = "".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
    print(f"{name} -> {'error: ' if res.get('isError') else ''}{text[:200]}", flush=True)
    return bool(res.get("isError"))


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                   "clientInfo": {"name": "toolbench-x-control", "version": "1"}})
post({"jsonrpc": "2.0", "method": "notifications/initialized"})
names = {t["name"] for t in rpc("tools/list", {})["tools"]}
if "submit_answer" not in names:
    sys.exit("server has no submit_answer tool")
for step in plan["steps"]:
    if "submit" in step:
        call("submit_answer", {"answer": step["submit"]})
        continue
    for _ in range(1 + int(step.get("retries", 0))):
        if not call(step["call"], step.get("arguments") or {}):
            break
