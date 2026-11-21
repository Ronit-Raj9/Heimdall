#!/bin/sh
# Reference for EnterpriseOps-Gym task eog-hr-20260109-133046-369-391f1eff-7e44fdca, written by hand from its checks (lane L14).
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
svcs = call("hr", "find_hr_services")
case = call("hr", "create_new_hr_case", status="draft", hr_service="35", opened_for="Karen Watkins", priority="moderate",
            short_description="Profile update: correct personal employee information", coe_type="Workforce Administration")
cid = None
if isinstance(case, dict):
    cid = case.get("hr_case_id") or (case.get("data") or {}).get("hr_case_id") or (case.get("case") or {}).get("hr_case_id")
call("hr", "send_notification", hr_case_id=int(cid or 61), email="karen.watkins@servicenow.com", type="update", status="draft")
PY
