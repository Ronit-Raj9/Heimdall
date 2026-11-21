#!/bin/sh
# Reference for EnterpriseOps-Gym task eog-hybrid-20260116-080150-885-1628b966-5a58d8e3, written by hand from its checks (lane L14).
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
labels = call("email", "list_labels", userId="me")
inbox = {l.get("name"): l["id"] for l in labels.get("labels", [])}.get("INBOX", "INBOX")
existing = call("email", "list_filters", userId="me")
if "newsletter@marketing.com" not in json.dumps(existing):
    call("email", "create_filter", userId="me", criteria={"from": "newsletter@marketing.com", "to": "*", "subject": "null", "query": "null", "negatedQuery": "null", "hasAttachment": False, "excludeChats": False, "size": 0, "sizeComparison": "unspecified"}, action={"removeLabelIds": [inbox]})
call("hr", "create_new_hr_service", service_name="New HR Portal", fulfillment_type="service_activity")
PY
