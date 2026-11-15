#!/usr/bin/env python3
"""Acme Tea support desk: an MCP server over a ticket and orders database.

Part of the Heimdall example project examples/acme-support. Acme Tea, its
customers, orders and tickets are fictional. Python standard library only,
so the image needs nothing but python3 and never reaches the network.

It speaks the Model Context Protocol over streamable HTTP: JSON-RPC 2.0
messages POSTed to /mcp (initialize, ping, tools/list, tools/call), each
answered with one application/json response; notifications are acknowledged
with 202. GET /healthz answers 200 when the server is up. The server is the
mcp template's (`bifrost-bench suite init -template mcp`) with the inventory
tools replaced by the support desk's.

State: the task's seed database (SEED_PATH, copied in by the task's
`files:`) is loaded at start and written to STATE_PATH at once and after
every change; the bench exports it after the agent exits and the hidden
verifier reads it at /grader/state/desk/db.json.

The tools enforce what a real desk backend would (an order that shipped
cannot change address, a refund cannot exceed what was paid, tickets merge
only within one customer), but not the refund policy: deciding whether a
refund is allowed is the agent's job, which is what the tasks measure.

Commands:
  python3 server.py           serve (PORT, default 8000)
  python3 server.py ready     exit 0 when the local server answers /healthz
"""

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
SERVER_NAME = "acme-desk"
SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ["2025-06-18", "2025-03-26", "2024-11-05"]
MAX_BODY = 1 << 20
TICKET_STATUSES = ["open", "pending", "solved"]


class ToolError(Exception):
    """An error the agent sees as a tool result with isError true."""


# ---------------------------------------------------------------- Tools ----


def find(db, table, key, value, what):
    for row in db[table]:
        if row.get(key) == value:
            return row
    raise ToolError(f"no {what} {value!r}")


def summary(ticket):
    return {k: ticket.get(k) for k in ("id", "customer_id", "subject", "status", "created_at", "order_id", "merged_into")}


def search_tickets(db, args):
    status = args.get("status")
    email = (args.get("customer_email") or "").strip().lower()
    query = (args.get("query") or "").strip().lower()
    customer = None
    if email:
        customer = next((c for c in db["customers"] if c["email"].lower() == email), None)
        if customer is None:
            return {"tickets": []}
    out = []
    for t in db["tickets"]:
        if status and t["status"] != status:
            continue
        if customer and t["customer_id"] != customer["id"]:
            continue
        text = " ".join([t["subject"]] + [m["text"] for m in t["messages"]]).lower()
        if query and query not in text:
            continue
        out.append(summary(t))
    return {"tickets": out}


def get_ticket(db, args):
    return find(db, "tickets", "id", args["ticket_id"], "ticket")


def get_customer(db, args):
    if args.get("customer_id"):
        c = find(db, "customers", "id", args["customer_id"], "customer")
    elif args.get("email"):
        email = args["email"].strip().lower()
        c = next((c for c in db["customers"] if c["email"].lower() == email), None)
        if c is None:
            raise ToolError(f"no customer with email {args['email']!r}")
    else:
        raise ToolError("give customer_id or email")
    orders = [o["id"] for o in db["orders"] if o["customer_id"] == c["id"]]
    tickets = [t["id"] for t in db["tickets"] if t["customer_id"] == c["id"]]
    return dict(c, order_ids=orders, ticket_ids=tickets)


def get_order(db, args):
    return find(db, "orders", "id", args["order_id"], "order")


def get_refund_policy(db, args):
    return {"today": db["today"], "currency": db["currency"], "policy": db["policy"]}


def issue_refund(db, args):
    order = find(db, "orders", "id", args["order_id"], "order")
    amount = args["amount_cents"]
    reason = str(args.get("reason") or "").strip()
    if not isinstance(amount, int) or isinstance(amount, bool) or amount <= 0:
        raise ToolError("amount_cents must be a positive integer (pence)")
    if not reason:
        raise ToolError("reason must not be empty")
    if order["status"] not in ("delivered", "partially_refunded"):
        raise ToolError(f"order {order['id']} is {order['status']}; only delivered orders can be refunded")
    left = order["total_cents"] - order["refunded_cents"]
    if amount > left:
        raise ToolError(f"order {order['id']} has {left} cents left to refund; asked for {amount}")
    refund = {"id": f"R-{len(db['refunds']) + 1:03d}", "order_id": order["id"], "amount_cents": amount,
              "reason": reason, "at": db["today"]}
    db["refunds"].append(refund)
    order["refunded_cents"] += amount
    order["status"] = "refunded" if order["refunded_cents"] == order["total_cents"] else "partially_refunded"
    return {"refund": refund, "order_status": order["status"], "refunded_cents": order["refunded_cents"]}


def update_shipping_address(db, args):
    order = find(db, "orders", "id", args["order_id"], "order")
    if order["status"] != "processing":
        raise ToolError(f"order {order['id']} is {order['status']}; only an order still processing can change address")
    addr = {k: str(args.get(k) or "").strip() for k in ("line1", "line2", "city", "postal_code", "country")}
    for k in ("line1", "city", "postal_code", "country"):
        if not addr[k]:
            raise ToolError(f"{k} must not be empty")
    addr["one_line"] = ", ".join(v for k, v in addr.items() if v)
    order["shipping_address"] = addr
    return {"order_id": order["id"], "shipping_address": addr}


def merge_tickets(db, args):
    primary = find(db, "tickets", "id", args["primary_ticket_id"], "ticket")
    dup = find(db, "tickets", "id", args["duplicate_ticket_id"], "ticket")
    if primary["id"] == dup["id"]:
        raise ToolError("a ticket cannot be merged into itself")
    if primary["customer_id"] != dup["customer_id"]:
        raise ToolError("tickets of different customers cannot be merged")
    if dup["status"] == "merged" or primary["status"] == "merged":
        raise ToolError("one of these tickets is already merged")
    dup["status"] = "merged"
    dup["merged_into"] = primary["id"]
    primary["messages"].append({"from": "system", "at": db["today"], "text": f"Ticket {dup['id']} was merged into this ticket."})
    return {"primary": summary(primary), "merged": summary(dup)}


def reply_to_ticket(db, args):
    ticket = find(db, "tickets", "id", args["ticket_id"], "ticket")
    text = str(args.get("message") or "").strip()
    status = args["status"]
    if not text:
        raise ToolError("message must not be empty")
    if status not in TICKET_STATUSES:
        raise ToolError("status must be one of " + ", ".join(TICKET_STATUSES))
    if ticket["status"] == "merged":
        raise ToolError(f"ticket {ticket['id']} was merged into {ticket['merged_into']}; reply there")
    ticket["messages"].append({"from": "agent", "at": db["today"], "text": text})
    ticket["status"] = status
    return summary(ticket)


def obj(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


STR = {"type": "string"}

TOOLS = [
    {"name": "search_tickets", "handler": search_tickets,
     "description": "Find tickets by status (open, pending, solved, merged), customer email or a word in the subject or messages.",
     "inputSchema": obj({"status": STR, "customer_email": STR, "query": STR})},
    {"name": "get_ticket", "handler": get_ticket,
     "description": "Get one ticket with its whole message thread.",
     "inputSchema": obj({"ticket_id": {"type": "string", "description": "e.g. T-1001"}}, ["ticket_id"])},
    {"name": "get_customer", "handler": get_customer,
     "description": "Get a customer by id or email, with their order and ticket ids.",
     "inputSchema": obj({"customer_id": STR, "email": STR})},
    {"name": "get_order", "handler": get_order,
     "description": "Get one order: status, dates, items with unit prices in pence, shipping, total, refunded so far, shipping address.",
     "inputSchema": obj({"order_id": {"type": "string", "description": "e.g. A-1001"}}, ["order_id"])},
    {"name": "get_refund_policy", "handler": get_refund_policy,
     "description": "Get today's date and the support policy for refunds, address changes and duplicate tickets.",
     "inputSchema": obj({})},
    {"name": "issue_refund", "handler": issue_refund, "writes": True,
     "description": "Refund part or all of a delivered order to the customer's original payment method. Amount in pence.",
     "inputSchema": obj({"order_id": STR, "amount_cents": {"type": "integer"}, "reason": STR}, ["order_id", "amount_cents", "reason"])},
    {"name": "update_shipping_address", "handler": update_shipping_address, "writes": True,
     "description": "Change where an order that is still processing will be shipped.",
     "inputSchema": obj({"order_id": STR, "line1": {"type": "string", "description": "street and number"},
                         "line2": {"type": "string", "description": "flat, unit or floor (optional)"},
                         "city": STR, "postal_code": STR, "country": STR},
                        ["order_id", "line1", "city", "postal_code", "country"])},
    {"name": "merge_tickets", "handler": merge_tickets, "writes": True,
     "description": "Merge a duplicate ticket into a primary ticket of the same customer; the duplicate is closed as merged.",
     "inputSchema": obj({"primary_ticket_id": STR, "duplicate_ticket_id": STR}, ["primary_ticket_id", "duplicate_ticket_id"])},
    {"name": "reply_to_ticket", "handler": reply_to_ticket, "writes": True,
     "description": "Send a reply to the customer on a ticket and set its status (open, pending or solved).",
     "inputSchema": obj({"ticket_id": STR, "message": STR, "status": {"type": "string", "enum": TICKET_STATUSES}},
                        ["ticket_id", "message", "status"])},
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
    server_version = "acme-desk-mcp/1.0"
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
