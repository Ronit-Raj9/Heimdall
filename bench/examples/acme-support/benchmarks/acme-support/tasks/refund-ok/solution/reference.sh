#!/bin/sh
# Reference control: does the task the way a correct support agent would,
# through the same desk MCP server the agent gets (BENCH_MCP_SERVERS names
# its URL). Admission runs it in the agent's place with the services up and
# requires the verifier to pass it three times in a row.
exec python3 -I - <<'PY'
import json, os, sys, urllib.request

SERVICE = "desk"
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


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "control", "version": "1"}})
rpc("notifications/initialized", notify=True)
ticket = call("get_ticket", {"ticket_id": "T-1001"})
order = call("get_order", {"order_id": ticket["order_id"]})
policy = call("get_refund_policy", {})
kettle = next(i for i in order["items"] if i["sku"] == "KT-12")
assert policy["today"] <= "2026-04-07" and not kettle["final_sale"]
amount = kettle["unit_cents"] * 1
call("issue_refund", {"order_id": order["id"], "amount_cents": amount, "reason": "Kettle arrived dented (ticket T-1001)"})
call("reply_to_ticket", {"ticket_id": "T-1001", "status": "solved",
     "message": "Sorry about the dented kettle, Hannah. I have refunded GBP 42.00 for it to your original payment method."})
print("refunded 4200 on A-1001, T-1001 solved")
PY
