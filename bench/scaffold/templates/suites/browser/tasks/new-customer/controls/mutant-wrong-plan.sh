#!/bin/sh
# Mutant control: a plausible wrong action, creating the customer on the Team
# plan instead of Business, through the same browser and form. Admission
# requires it to fail, which shows the checks catch a near miss.
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

call("browser_navigate", {"url": APP + "/customers/new"})
snap = snapshot()
call("browser_fill_form", {"fields": [
    {"name": "Name", "type": "textbox", "target": ref(snap, "textbox", "Name"), "value": "Mira Okafor"},
    {"name": "Email", "type": "textbox", "target": ref(snap, "textbox", "Email"), "value": "mira.okafor@lumen.example"},
    {"name": "Plan", "type": "combobox", "target": ref(snap, "combobox", "Plan"), "value": "Team"},
    {"name": "Notes", "type": "textbox", "target": ref(snap, "textbox", "Notes"), "value": "Signed at the March trade fair"},
]})
call("browser_click", {"element": "Create customer button", "target": ref(snap, "button", "Create customer")})
if "Page Title: Customer C-" not in snapshot():
    sys.exit("the form did not lead to the new customer's page")
print("created Mira Okafor (Team) through the browser")
PY
