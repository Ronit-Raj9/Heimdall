#!/bin/sh
# Mutant control: a plausible wrong action, recording the delivery on the 500 ml
# bottle (W-101) instead of the 750 ml one. Admission requires it to fail,
# which shows the checks catch a near miss. It runs in the agent's place
# with the services up.
exec python3 -I - <<'PY'
import json, os, sys, urllib.request

SERVICE = "inventory"
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
URL = (servers.get(SERVICE) or {}).get("url") or f"http://toolproxy:9200/s/{SERVICE}/mcp"
session = {}


def rpc(method, params=None, notify=False):
    msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
    if not notify:
        msg["id"] = rpc.next_id = getattr(rpc, "next_id", 0) + 1
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    headers.update(session)
    req = urllib.request.Request(URL, data=json.dumps(msg).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=30) as resp:
        if resp.headers.get("Mcp-Session-Id"):
            session["Mcp-Session-Id"] = resp.headers["Mcp-Session-Id"]
        body = resp.read().decode()
    if notify:
        return None
    if body.lstrip().startswith("data:") or "\ndata:" in body:
        body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
    answer = json.loads(body)
    if "error" in answer:
        sys.exit(f"{method}: {answer['error']}")
    return answer["result"]


def call(tool, args):
    result = rpc("tools/call", {"name": tool, "arguments": args})
    text = "".join(c.get("text", "") for c in result.get("content", []))
    if result.get("isError"):
        sys.exit(f"{tool}: {text}")
    return json.loads(text)


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}})
rpc("notifications/initialized", notify=True)
item = call("get_item", {"sku": "W-101"})
call("adjust_stock", {"sku": item["sku"], "delta": 12, "reason": "Delivery D-5531 from supplier"})
print("recorded 12 units of W-101 for delivery D-5531")
PY
