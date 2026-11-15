#!/bin/sh
# Reference control: does the task the way a correct agent would, through the
# same browser the agent gets. It speaks MCP (JSON-RPC over streamable HTTP) to
# the browser service at the URL BENCH_MCP_SERVERS names, which is the per-run
# tool proxy, so every step lands in tools.jsonl like an agent's: navigate,
# read the accessibility snapshot for element references, fill, select, click.
# Admission runs it in the agent's place with the services up and requires the
# verifier to pass it three times in a row. Python standard library only.
exec python3 -I - <<'PY'
import json, os, re, sys, urllib.request

SERVICE = "browser"
APP = "http://webapp:8000"
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
URL = (servers.get(SERVICE) or {}).get("url") or f"http://toolproxy:9200/s/{SERVICE}/mcp"
session = {}


def rpc(method, params=None, notify=False):
    """One JSON-RPC message to the browser's MCP endpoint (streamable HTTP)."""
    msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
    if not notify:
        msg["id"] = rpc.next_id = getattr(rpc, "next_id", 0) + 1
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    headers.update(session)
    req = urllib.request.Request(URL, data=json.dumps(msg).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=90) as resp:
        if resp.headers.get("Mcp-Session-Id"):
            session["Mcp-Session-Id"] = resp.headers["Mcp-Session-Id"]
        body = resp.read().decode()
    if notify:
        return None
    if "data:" in body and not body.lstrip().startswith("{"):
        body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
    answer = json.loads(body)
    if "error" in answer:
        sys.exit(f"{method}: {answer['error']}")
    return answer["result"]


def call(tool, args):
    """Call one browser tool and return the text of its result."""
    result = rpc("tools/call", {"name": tool, "arguments": args})
    text = "".join(c.get("text", "") for c in result.get("content", []) if c.get("type") == "text")
    if result.get("isError"):
        sys.exit(f"{tool}: {text}")
    return text


def snapshot():
    """The current page's accessibility snapshot, as the agent reads it."""
    return call("browser_snapshot", {})


def ref(snap, role, name):
    """The element reference of the first element with this role and name."""
    m = re.search(r'- %s "%s"[^\n]*?\[ref=(\w+)\]' % (re.escape(role), re.escape(name)), snap)
    if not m:
        sys.exit(f"no {role} named {name!r} on the page:\n{snap}")
    return m.group(1)


def table_rows(snap):
    """The texts of each table row's cells and links, in page order."""
    rows = []
    for line in snap.splitlines():
        if re.match(r"\s*- row\b", line):
            rows.append([])
        m = re.match(r'\s*- (?:cell|link) "([^"]*)"', line)
        if m and rows:
            rows[-1].append(m.group(1))
    return rows


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "control", "version": "1"}})
rpc("notifications/initialized", notify=True)

call("browser_navigate", {"url": APP + "/customers"})
snap = snapshot()
call("browser_type", {"element": "Search customers", "target": ref(snap, "textbox", "Search customers"), "text": "kim.osei@baobab.example", "submit": True})
found = [r[0] for r in table_rows(snapshot()) if any("kim.osei@baobab.example" in c for c in r)]
if not found:
    sys.exit("no customer found")
customer = found[0]
call("browser_navigate", {"url": APP + "/orders/A-2027"})
snap = snapshot()
call("browser_type", {"element": "Customer ID", "target": ref(snap, "textbox", "Customer ID"), "text": customer})
call("browser_click", {"element": "Move order button", "target": ref(snap, "button", "Move order")})
if "(" + customer + ")" not in snapshot():
    sys.exit("the order page does not show the new customer")
print("moved A-2027 to", customer, "through the browser")
PY
