#!/bin/sh
# Mutant: turns on line numbers and the toolbar instead of the status bar.
# The toolbar pushes the text down, so the line-number region, the
# status-bar region and the screen SSIM must fail it.
exec python3 -I - toolbar <<'PY'
import json, os, sys, time, urllib.request

SERVICE = "desktop"
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
    with urllib.request.urlopen(req, timeout=90) as resp:
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


def call(tool, args=None, tries=3):
    """Calls a desktop tool; a tool error is retried after a pause."""
    for attempt in range(tries):
        result = rpc("tools/call", {"name": tool, "arguments": args or {}})
        if not result.get("isError"):
            return result["content"]
        print(f"{tool} failed (try {attempt + 1})", file=sys.stderr)
        time.sleep(1)
    sys.exit(f"{tool} kept failing")


def menu(item_y, item_x=200):
    """Opens the View menu and clicks the item at item_y. The layout is
    fixed: one maximized window on a 1280 x 800 screen, View at (163, 35)."""
    call("click", {"x": 163, "y": 35})
    call("wait", {"ms": 600})
    call("click", {"x": item_x, "y": item_y})
    call("wait", {"ms": 600})


rpc("initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "control", "version": "1"}})
rpc("notifications/initialized", notify=True)
call("screenshot")
LINE_NUMBERS, TOOLBAR, STATUSBAR = 111, 162, 187
steps = sys.argv[1]
menu(LINE_NUMBERS)
menu(TOOLBAR)
PY
