#!/bin/sh
# Reference for EnterpriseOps-Gym task eog-teams-20251121-134729-533-464ee3e0-2dcf2712, written by hand from its checks (lane L14).
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
users = call("teams", "list_users", _filter="displayName eq 'Alice Johnson' or displayName eq 'James Wilson'")
by = {u["displayName"]: u for u in users["value"]}
alice, james = by["Alice Johnson"], by["James Wilson"]
teams = call("teams", "list_teams", _filter="displayName eq 'TechCorp Solutions Team'")
team = teams["value"][0]
call("teams", "create_call", source={"identity": {"user": {"id": james["id"], "displayName": "James Wilson"}}}, callbackUri="https://bench.invalid/callback", requestedModalities=["audio"],
     targets=[{"identity": {"user": {"id": alice["id"], "displayName": alice.get("displayName", "Alice Johnson")}}}],
     mediaConfig={"_odata_type": "#microsoft.graph.serviceHostedMediaConfig"})
call("teams", "create_channel", teamId=team["id"], displayName="Project Updates", membershipType="standard")
PY
