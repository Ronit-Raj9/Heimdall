#!/bin/sh
# Mutant control: moves both files to the right folders but keeps the
# scanner's name for the invoice. Admission requires it to fail.
exec python3 -I - <<'PY'
import base64, json, os, struct, sys, time, urllib.request

SERVICE = "desktop"
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
URL = (servers.get(SERVICE) or {}).get("url") or f"http://toolproxy:9200/s/{SERVICE}/mcp"
SCREEN = (1280, 800)
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
    """Calls a desktop tool; a tool error (an injected fault, a busy
    display) is retried after a pause, as a careful agent would."""
    for attempt in range(tries):
        result = rpc("tools/call", {"name": tool, "arguments": args or {}})
        if not result.get("isError"):
            return result["content"]
        text = "".join(c.get("text", "") for c in result["content"])
        print(f"{tool} failed (try {attempt + 1}): {text}", file=sys.stderr)
        time.sleep(1)
    sys.exit(f"{tool} kept failing")


def look():
    """Takes a screenshot and checks it is a PNG of the whole screen: the
    layout the steps below rely on (one maximized window) is only valid at
    this size."""
    content = call("screenshot")
    png = base64.b64decode(next(c["data"] for c in content if c["type"] == "image"))
    if png[:8] != b"\x89PNG\r\n\x1a\n" or struct.unpack(">II", png[16:24]) != SCREEN:
        sys.exit(f"unexpected screenshot: {png[:24]!r}")


def key(combo, repeat=1, settle_ms=150):
    call("key", {"combo": combo, "repeat": repeat})
    call("wait", {"ms": settle_ms})


def type_text(text, settle_ms=200):
    for i in range(0, len(text), 1500):
        call("type", {"text": text[i:i + 1500]})
    call("wait", {"ms": settle_ms})


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}})
rpc("notifications/initialized", notify=True)
size = json.loads(call("screen_size")[0]["text"])
if (size["width"], size["height"]) != SCREEN:
    sys.exit(f"screen is {size}, want {SCREEN}")
look()

def move_selected(down, dest, dialog="F6"):
    # The file list has the focus. Home selects "..", each Down the next
    # entry (sorted by name). F6 is xfe's "Move to" (F5 "Copy to"): its
    # dialog takes a full destination path, which also renames.
    key("Home")
    key("Down", repeat=down)
    key(dialog, settle_ms=1200)
    key("ctrl+a")
    type_text(dest)
    key("Return", settle_ms=1500)


# Inbox, by name: .., IMG_2031.jpg, notes.txt, scan_0042.pdf
move_selected(3, "/home/desk/Documents/Invoices/2026/scan_0042.pdf")
move_selected(1, "/home/desk/Documents/Photos/office-party-2026.jpg")
look()
print("filed without renaming the invoice")
PY
