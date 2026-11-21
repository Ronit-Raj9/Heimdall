#!/bin/sh
# Reference for EnterpriseOps-Gym task eog-calendar-20260109-043327-590-806f1a83-d690097e, written by hand from its checks (lane L14).
exec python3 - <<'PY'
import json, os, urllib.request
SERVERS = json.loads(os.environ["BENCH_MCP_SERVERS"])
_sid, _n = {}, [0]
def rpc(svc, method, params=None):
    _n[0] += 1
    h = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    if _sid.get(svc): h["mcp-session-id"] = _sid[svc]
    req = urllib.request.Request(SERVERS[svc]["url"], data=json.dumps({"jsonrpc": "2.0", "id": _n[0], "method": method, "params": params or {}}).encode(), headers=h)
    with urllib.request.urlopen(req, timeout=120) as r:
        if r.headers.get("mcp-session-id"): _sid[svc] = r.headers["mcp-session-id"]
        body = r.read().decode()
    if body.startswith("event:") or body.startswith("data:"):
        body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
    return json.loads(body)
_init = set()
def call(_svc, _tool, **args):
    svc, name = _svc, _tool
    if svc not in _init:
        rpc(svc, "initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}}); _init.add(svc)
    out = rpc(svc, "tools/call", {"name": name, "arguments": args})
    if "error" in out: raise SystemExit("%s: %s" % (name, out["error"]))
    text = out["result"]["content"][0]["text"]
    print(name, text[:300].replace("\n", " "))
    try: return json.loads(text)
    except ValueError: return text
OLD = "MegaCorp Headquarters, 123 Business Ave, New York, NY"
NEW = "TechCorp Main Campus - Building 1, Conference Room B"
NOTE = "Relocated due to unavailability at MegaCorp Headquarters."
cals = call("calendar", "get_calendar_list")
for c in cals.get("items", []):
    ev = call("calendar", "list_events", calendarId=c["id"], timeMin="2025-11-01T00:00:00Z", timeMax="2025-11-29T00:00:00Z", maxResults=250, singleEvents=True)
    for e in (ev.get("items", []) if isinstance(ev, dict) else []):
        if e.get("location") == OLD:
            desc = (e.get("description") or "").strip()
            call("calendar", "patch_event", calendarId=c["id"], eventId=e["id"], location=NEW, description=(desc + "\n\n" + NOTE).strip())
PY
