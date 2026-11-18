#!/usr/bin/env python3
"""Acme Brew Club helpdesk: an MCP server over orders, subscriptions and the support policy.

Part of Heimdall's support suite template (`bifrost-bench suite init
-template support`) and the example project examples/acme-helpdesk. Acme Brew
Club, its customers, orders and subscriptions are fictional. Python standard
library only, so the image needs nothing but python3 and never reaches the
network.

It speaks the Model Context Protocol over streamable HTTP: JSON-RPC 2.0
messages POSTed to /mcp (initialize, ping, tools/list, tools/call), each
answered with one application/json response; notifications are acknowledged
with 202. GET /healthz answers 200 when the server is up.

The agent talks to the customer through a second MCP server, the user
simulator (simuser.py, same image), with respond_to_user. This server holds
the account data and the actions. The tools enforce what a real backend
would (an order that shipped cannot change address, a refund cannot exceed
what was paid) but not the support policy: whether a refund, a credit or an
escalation is right is the agent's decision, which is what the tasks measure.

State: the task's seed database (SEED_PATH) is loaded at start and written to
STATE_PATH at once and after every change, with an action log; the bench
exports it after the agent exits and the hidden verifier reads it at
/grader/state/helpdesk/db.json.

Commands:
  python3 server.py           serve (PORT, default 8000)
  python3 server.py ready     exit 0 when the local server answers /healthz
"""

import datetime
import json
import os
import sys
import tempfile
import threading
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SEED_PATH = os.environ.get("SEED_PATH", "/seed/db.json")
STATE_PATH = os.environ.get("STATE_PATH", "/state/db.json")
PORT = int(os.environ.get("PORT", "8000"))
MCP_PATH = os.environ.get("MCP_PATH", "/mcp")
SERVER_NAME = "acme-helpdesk"
SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ["2025-06-18", "2025-03-26", "2024-11-05"]
MAX_BODY = 1 << 20
SUB_ACTIONS = ["pause", "resume", "cancel"]


class ToolError(Exception):
    """An error the agent sees as a tool result with isError true."""


# ---------------------------------------------------------------- Tools ----


def find(db, table, key, value, what):
    for row in db[table]:
        if row.get(key) == value:
            return row
    raise ToolError(f"no {what} {value!r}")


def pence(value, what):
    if not isinstance(value, int) or isinstance(value, bool) or value <= 0:
        raise ToolError(f"{what} must be a positive integer (pence)")
    return value


def text_arg(args, key):
    value = str(args.get(key) or "").strip()
    if not value:
        raise ToolError(f"{key} must not be empty")
    return value


def add_months(day, months):
    d = datetime.date.fromisoformat(day)
    m = d.month - 1 + months
    y, m = d.year + m // 12, m % 12 + 1
    last = [31, 29 if y % 4 == 0 and (y % 100 != 0 or y % 400 == 0) else 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][m - 1]
    return datetime.date(y, m, min(d.day, last)).isoformat()


def get_policy(db, args):
    return {"today": db["today"], "currency": db["currency"], "policy": db["policy"]}


def find_customer(db, args):
    email = text_arg(args, "email").lower()
    c = next((c for c in db["customers"] if c["email"].lower() == email), None)
    if c is None:
        raise ToolError(f"no customer with email {args['email']!r}")
    return get_customer(db, {"customer_id": c["id"]})


def get_customer(db, args):
    c = find(db, "customers", "id", text_arg(args, "customer_id"), "customer")
    orders = [o["id"] for o in db["orders"] if o["customer_id"] == c["id"]]
    subs = [s["id"] for s in db["subscriptions"] if s["customer_id"] == c["id"]]
    return dict(c, order_ids=orders, subscription_ids=subs)


def get_order(db, args):
    return find(db, "orders", "id", text_arg(args, "order_id"), "order")


def get_subscription(db, args):
    return find(db, "subscriptions", "id", text_arg(args, "subscription_id"), "subscription")


def issue_refund(db, args):
    order = find(db, "orders", "id", text_arg(args, "order_id"), "order")
    amount = pence(args.get("amount_pence"), "amount_pence")
    reason = text_arg(args, "reason")
    if order["status"] not in ("delivered", "partially_refunded"):
        raise ToolError(f"order {order['id']} is {order['status']}; only delivered orders can be refunded")
    left = order["total_pence"] - order["refunded_pence"]
    if amount > left:
        raise ToolError(f"order {order['id']} has {left} pence left to refund; asked for {amount}")
    refund = {"id": f"R-{len(db['refunds']) + 1:03d}", "order_id": order["id"], "amount_pence": amount, "reason": reason, "at": db["today"]}
    db["refunds"].append(refund)
    order["refunded_pence"] += amount
    order["status"] = "refunded" if order["refunded_pence"] == order["total_pence"] else "partially_refunded"
    return {"refund": refund, "order_status": order["status"], "refunded_pence": order["refunded_pence"]}


def apply_credit(db, args):
    c = find(db, "customers", "id", text_arg(args, "customer_id"), "customer")
    amount = pence(args.get("amount_pence"), "amount_pence")
    credit = {"id": f"C-{len(db['credits']) + 1:03d}", "customer_id": c["id"], "amount_pence": amount,
              "reason": text_arg(args, "reason"), "at": db["today"]}
    db["credits"].append(credit)
    return {"credit": credit}


def update_subscription(db, args):
    sub = find(db, "subscriptions", "id", text_arg(args, "subscription_id"), "subscription")
    action = args.get("action")
    if action not in SUB_ACTIONS:
        raise ToolError("action must be one of " + ", ".join(SUB_ACTIONS))
    if sub["status"] == "cancelled":
        raise ToolError(f"subscription {sub['id']} is cancelled")
    if action == "pause":
        months = args.get("months")
        if not isinstance(months, int) or isinstance(months, bool) or not 1 <= months <= 3:
            raise ToolError("months must be 1, 2 or 3 for a pause")
        sub["status"], sub["paused_until"] = "paused", add_months(db["today"], months)
    elif action == "resume":
        sub["status"], sub["paused_until"] = "active", None
    else:
        sub["status"], sub["paused_until"], sub["next_charge"] = "cancelled", None, None
    return sub


def update_shipping_address(db, args):
    order = find(db, "orders", "id", text_arg(args, "order_id"), "order")
    if order["status"] != "processing":
        raise ToolError(f"order {order['id']} is {order['status']}; only an order still processing can change address")
    addr = {k: str(args.get(k) or "").strip() for k in ("line1", "line2", "city", "postal_code", "country")}
    for k in ("line1", "city", "postal_code", "country"):
        if not addr[k]:
            raise ToolError(f"{k} must not be empty")
    addr["one_line"] = ", ".join(v for k, v in addr.items() if v)
    order["shipping_address"] = addr
    return {"order_id": order["id"], "shipping_address": addr}


def escalate_to_human(db, args):
    c = find(db, "customers", "id", text_arg(args, "customer_id"), "customer")
    esc = {"id": f"E-{len(db['escalations']) + 1:03d}", "customer_id": c["id"], "reason": text_arg(args, "reason"),
           "summary": text_arg(args, "summary"), "at": db["today"]}
    db["escalations"].append(esc)
    return {"escalation": esc, "message": "A human colleague will contact the customer within one working day."}


def obj(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


STR = {"type": "string"}
INT = {"type": "integer"}

TOOLS = [
    {"name": "get_policy", "handler": get_policy,
     "description": "Get today's date and the support policy: refunds, subscriptions, credits, address changes, escalation.",
     "inputSchema": obj({})},
    {"name": "find_customer", "handler": find_customer,
     "description": "Find a customer by email, with their order and subscription ids.",
     "inputSchema": obj({"email": STR}, ["email"])},
    {"name": "get_customer", "handler": get_customer,
     "description": "Get a customer by id, with their order and subscription ids.",
     "inputSchema": obj({"customer_id": {"type": "string", "description": "e.g. U-101"}}, ["customer_id"])},
    {"name": "get_order", "handler": get_order,
     "description": "Get one order: status, dates, items with prices in pence, total, refunded so far, shipping address.",
     "inputSchema": obj({"order_id": {"type": "string", "description": "e.g. B-3001"}}, ["order_id"])},
    {"name": "get_subscription", "handler": get_subscription,
     "description": "Get one subscription: plan, price, status, last and next charge, pause date.",
     "inputSchema": obj({"subscription_id": {"type": "string", "description": "e.g. S-501"}}, ["subscription_id"])},
    {"name": "issue_refund", "handler": issue_refund, "writes": True,
     "description": "Refund part or all of a delivered order to the original payment method. Amount in pence.",
     "inputSchema": obj({"order_id": STR, "amount_pence": INT, "reason": STR}, ["order_id", "amount_pence", "reason"])},
    {"name": "apply_credit", "handler": apply_credit, "writes": True,
     "description": "Add store credit to a customer's account. Amount in pence.",
     "inputSchema": obj({"customer_id": STR, "amount_pence": INT, "reason": STR}, ["customer_id", "amount_pence", "reason"])},
    {"name": "update_subscription", "handler": update_subscription, "writes": True,
     "description": "Pause (1 to 3 months), resume or cancel a subscription.",
     "inputSchema": obj({"subscription_id": STR, "action": {"type": "string", "enum": SUB_ACTIONS}, "months": INT},
                        ["subscription_id", "action"])},
    {"name": "update_shipping_address", "handler": update_shipping_address, "writes": True,
     "description": "Change where an order that is still processing will be shipped.",
     "inputSchema": obj({"order_id": STR, "line1": {"type": "string", "description": "street and number"},
                         "line2": {"type": "string", "description": "flat, unit or floor (optional)"},
                         "city": STR, "postal_code": STR, "country": STR},
                        ["order_id", "line1", "city", "postal_code", "country"])},
    {"name": "escalate_to_human", "handler": escalate_to_human, "writes": True,
     "description": "Hand the customer to a human colleague, with the reason and a summary for them.",
     "inputSchema": obj({"customer_id": STR, "reason": STR, "summary": STR}, ["customer_id", "reason", "summary"])},
]


# ------------------------------------------------------------- Protocol ----


class Store:
    """The database, loaded from the seed and saved after every change."""

    def __init__(self, seed_path, state_path):
        with open(seed_path, encoding="utf-8") as f:
            self.db = json.load(f)
        self.state_path = state_path
        self.lock = threading.Lock()
        self.save()

    def save(self):
        folder = os.path.dirname(self.state_path) or "."
        os.makedirs(folder, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=folder, prefix=".state-")
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(self.db, f, indent=2, sort_keys=True)
            f.write("\n")
        os.chmod(tmp, 0o644)
        os.replace(tmp, self.state_path)

    def call(self, tool, args):
        with self.lock:
            result = tool["handler"](self.db, args)
            if tool.get("writes"):
                log = self.db.setdefault("actions", [])
                log.append({"seq": len(log) + 1, "tool": tool["name"], "arguments": args})
                self.save()
            return result


def missing_args(tool, args):
    required = tool["inputSchema"].get("required", [])
    return [name for name in required if name not in args]


def rpc_error(msg_id, code, message):
    return {"jsonrpc": "2.0", "id": msg_id, "error": {"code": code, "message": message}}


def rpc_result(msg_id, result):
    return {"jsonrpc": "2.0", "id": msg_id, "result": result}


def tool_result(payload, is_error=False):
    text = payload if isinstance(payload, str) else json.dumps(payload, sort_keys=True)
    return {"content": [{"type": "text", "text": text}], "isError": is_error}


def handle_message(store, msg):
    """Answers one JSON-RPC message; None for notifications and responses."""
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0":
        return rpc_error(None, -32600, "invalid request")
    method = msg.get("method")
    msg_id = msg.get("id")
    if method is None or "id" not in msg:
        return None
    params = msg.get("params") or {}
    if method == "initialize":
        asked = params.get("protocolVersion")
        version = asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0]
        return rpc_result(msg_id, {
            "protocolVersion": version,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
        })
    if method == "ping":
        return rpc_result(msg_id, {})
    if method == "tools/list":
        listed = [{k: t[k] for k in ("name", "description", "inputSchema")} for t in TOOLS]
        return rpc_result(msg_id, {"tools": listed})
    if method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}
        tool = next((t for t in TOOLS if t["name"] == name), None)
        if tool is None:
            return rpc_error(msg_id, -32602, f"unknown tool: {name}")
        if not isinstance(args, dict):
            return rpc_result(msg_id, tool_result("arguments must be an object", True))
        missing = missing_args(tool, args)
        if missing:
            return rpc_result(msg_id, tool_result("missing arguments: " + ", ".join(missing), True))
        try:
            return rpc_result(msg_id, tool_result(store.call(tool, args)))
        except ToolError as e:
            return rpc_result(msg_id, tool_result(str(e), True))
        except (KeyError, TypeError, ValueError, AttributeError) as e:
            return rpc_result(msg_id, tool_result(f"bad arguments: {e}", True))
    return rpc_error(msg_id, -32601, f"method not found: {method}")


class Handler(BaseHTTPRequestHandler):
    server_version = "acme-helpdesk-mcp/1.0"
    protocol_version = "HTTP/1.1"

    def send_json(self, status, body, headers=None):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(data)

    def send_empty(self, status, headers=None):
        self.send_response(status)
        self.send_header("Content-Length", "0")
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/healthz":
            self.send_json(200, {"ok": True, "server": SERVER_NAME})
        elif path == MCP_PATH:
            # No server-initiated stream: every answer comes on its POST.
            self.send_empty(405, {"Allow": "POST, DELETE"})
        else:
            self.send_json(404, {"error": "not found"})

    def do_DELETE(self):
        if self.path.split("?", 1)[0] == MCP_PATH:
            self.send_empty(200)
        else:
            self.send_json(404, {"error": "not found"})

    def do_POST(self):
        if self.path.split("?", 1)[0] != MCP_PATH:
            self.send_json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > MAX_BODY:
            self.send_json(400, rpc_error(None, -32600, "missing or oversized body"))
            return
        try:
            msg = json.loads(self.rfile.read(length))
        except ValueError:
            self.send_json(400, rpc_error(None, -32700, "parse error"))
            return
        batch = isinstance(msg, list)
        answers = [a for a in (handle_message(self.server.store, m) for m in (msg if batch else [msg])) if a is not None]
        headers = {}
        if any(isinstance(m, dict) and m.get("method") == "initialize" for m in (msg if batch else [msg])):
            headers["Mcp-Session-Id"] = uuid.uuid4().hex
        if not answers:
            self.send_empty(202, headers)
        else:
            self.send_json(200, answers if batch else answers[0], headers)

    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))


def serve():
    httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    httpd.store = Store(SEED_PATH, STATE_PATH)
    sys.stderr.write(f"{SERVER_NAME} MCP server on :{PORT}{MCP_PATH}, state in {STATE_PATH}\n")
    httpd.serve_forever()


def ready():
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/healthz", timeout=2) as r:
            return 0 if r.status == 200 else 1
    except OSError:
        return 1


if __name__ == "__main__":
    if sys.argv[1:] == ["ready"]:
        sys.exit(ready())
    serve()
