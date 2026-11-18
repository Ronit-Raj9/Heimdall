# Heimdall visual capture (X1 vision grading, docs/visual-grading.md).
# Run by the bench, never by the agent, with `python3 -I -c` inside the run's
# desktop service container after the agent exited: it calls the desktop's
# own MCP screenshot tool at the endpoint in argv[1] (127.0.0.1 inside the
# container) and prints one JSON line: {"png": base64} or {"error": text}.
# Before the shot it parks the pointer in the bottom-right corner with the
# desktop's move tool, when it has one, so where the agent left the mouse
# does not change the pixels. Python standard library only.
import base64
import json
import struct
import sys
import urllib.request

URL = sys.argv[1]


def call(msg, session=None):
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    if session:
        headers["Mcp-Session-Id"] = session
    req = urllib.request.Request(URL, data=json.dumps(msg).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=60) as resp:
        body = resp.read().decode()
        sid = resp.headers.get("Mcp-Session-Id")
    if body.lstrip().startswith(("event:", "data:")):
        data = [line[5:].strip() for line in body.splitlines() if line.startswith("data:")]
        body = data[-1] if data else ""
    return sid, (json.loads(body) if body.strip() else None)


def main():
    sid, _ = call({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
        "protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "heimdall-visual", "version": "1"}}})
    call({"jsonrpc": "2.0", "method": "notifications/initialized"}, sid)
    first = shot(sid, 2)
    w, h = struct.unpack(">II", base64.b64decode(first)[16:24])
    _, moved = call({"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "move", "arguments": {"x": w - 1, "y": h - 1}}}, sid)
    parked = not (moved or {}).get("error") and not ((moved or {}).get("result") or {}).get("isError")
    png = shot(sid, 4) if parked else first
    sys.stdout.write(json.dumps({"png": png, "pointer_parked": parked}) + "\n")


def shot(sid, msg_id):
    _, answer = call({"jsonrpc": "2.0", "id": msg_id, "method": "tools/call", "params": {"name": "screenshot", "arguments": {}}}, sid)
    result = (answer or {}).get("result") or {}
    if (answer or {}).get("error") or result.get("isError"):
        raise RuntimeError("screenshot tool answered an error: " + json.dumps(answer)[:300])
    for item in result.get("content") or []:
        if item.get("type") == "image" and item.get("mimeType") == "image/png":
            return item["data"]
    raise RuntimeError("screenshot tool returned no png image")


try:
    main()
except Exception as e:  # one line for the bench, whatever broke
    sys.stdout.write(json.dumps({"error": str(e).splitlines()[0] if str(e) else type(e).__name__}) + "\n")
    sys.exit(3)
