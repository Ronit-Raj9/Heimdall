# Script control client for tau2 tasks, embedded in each controls/*.sh by
# `bifrost-bench import tau2`. It runs in the agent's place, reads a plan
# (JSON on stdin) and drives the task's MCP server with raw JSON-RPC over
# HTTP: no MCP library, so the server is tested exactly as an agent sees it.
# Plan steps: {"say": text} calls respond_to_user, {"call": name,
# "arguments": {...}} calls a domain tool, {"echo_user": true} repeats the
# customer's last reply back to them.
import json
import os
import sys
import urllib.request

plan = json.load(sys.stdin)
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
url = (servers.get(plan.get("server", "tau2")) or {}).get("url") or "http://toolproxy:9200/s/tau2/mcp"
state = {"id": 0, "session": None, "last_reply": ""}


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
    return text


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                   "clientInfo": {"name": "tau2-control", "version": "1"}})
post({"jsonrpc": "2.0", "method": "notifications/initialized"})
names = {t["name"] for t in rpc("tools/list", {})["tools"]}
if "respond_to_user" not in names:
    sys.exit("server has no respond_to_user tool")
for step in plan["steps"]:
    if "say" in step:
        state["last_reply"] = call("respond_to_user", {"message": step["say"]})
    elif step.get("echo_user"):
        state["last_reply"] = call("respond_to_user", {"message": state["last_reply"]})
    else:
        call(step["call"], step.get("arguments") or {})
