#!/bin/sh
# Mutant control (mutant-refund-only): a plausible wrong way to handle the chat. Admission requires the verifier to fail it.
# It talks to the customer (respond_to_user on the customer service) and
# works the helpdesk through the same MCP servers the agent gets
# (BENCH_MCP_SERVERS names their URLs).
exec python3 -I - <<'PY'
import json, os, sys, urllib.request

servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")


def url(service):
    return (servers.get(service) or {}).get("url") or f"http://toolproxy:9200/s/{service}/mcp"


class Client:
    def __init__(self, service):
        self.url, self.session, self.next_id = url(service), {}, 0
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
            body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
        answer = json.loads(body)
        if "error" in answer:
            sys.exit(f"{method}: {answer['error']}")
        return answer["result"]

    def call(self, tool, args):
        result = self.rpc("tools/call", {"name": tool, "arguments": args})
        text = "".join(c.get("text", "") for c in result.get("content", []))
        if result.get("isError"):
            sys.exit(f"{tool}: {text}")
        return text


desk, customer = Client("helpdesk"), Client("customer")


def say(message):
    reply = customer.call("respond_to_user", {"message": message})
    print("customer:", reply)
    return reply


def call(tool, args):
    return json.loads(desk.call(tool, args))
say("Hello, how can I help?")
call("issue_refund", {"amount_pence": 6500, "order_id": "B-3003", "reason": "faulty"})
say("I have refunded 65.00 for order B-3003.")
PY
