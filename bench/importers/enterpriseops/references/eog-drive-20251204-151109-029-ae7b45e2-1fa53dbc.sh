#!/bin/sh
# Reference for EnterpriseOps-Gym task eog-drive-20251204-151109-029-ae7b45e2-1fa53dbc, written by hand from its checks (lane L14).
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
def perms(fid):
    r = call("drive", "list_permission", fileId=fid, supportsAllDrives=True)
    return (r.get("data") or r).get("permissions", [])
files = call("drive", "list_files", corpora="allDrives", includeItemsFromAllDrives=True)
items = files.get("files", []) if isinstance(files, dict) else []
spec = [f for f in items if f.get("name") == "Technical Spec.pdf"][0]
for p in perms(spec["id"]):
    if p.get("emailAddress") == "alex.rodriguez@company.com":
        call("drive", "delete_permission", fileId=spec["id"], permissionId=p["id"], supportsAllDrives=True)
call("drive", "create_permission", fileId=spec["id"], role="writer", type="user", emailAddress="sarah.johnson@company.com", supportsAllDrives=True)
for f in [f for f in items if f.get("name", "").startswith("Project Plan")]:
    for p in perms(f["id"]):
        if p.get("emailAddress") == "sarah.johnson@company.com" and p.get("role") == "commenter":
            call("drive", "delete_permission", fileId=f["id"], permissionId=p["id"], supportsAllDrives=True)
            call("drive", "create_permission", fileId=f["id"], role="writer", type="user", emailAddress="sarah.johnson@company.com", supportsAllDrives=True)
PY
