#!/bin/sh
# Reference for EnterpriseOps-Gym task eog-email-20260105-175342-189-e5bf1251-60089921, written by hand from its checks (lane L14).
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
aliases = call("email", "list_send_as_aliases", userId="me")
biz = [a for a in aliases.get("sendAs", []) if "business" in a.get("sendAsEmail", "")][0]
cur = call("email", "get_send_as_alias", userId="me", sendAsEmail=biz["sendAsEmail"])
call("email", "update_send_as_alias", userId="me", sendAsEmail=biz["sendAsEmail"],
     smtpMsa={"host": "smtp.freelancer.net", "port": 587, "securityMode": "starttls"},
     signature="<p>Eve Williams<br>Professional Web Development Services - React, Node.js & Full Stack Solutions<br>www.eve-webdev.com</p>",
     replyToAddress="eve@freelancer.net", displayName=cur["displayName"], isDefault=cur.get("isDefault", False),
     treatAsAlias=cur.get("treatAsAlias", True), verificationStatus=cur.get("verificationStatus", "accepted"))
PY
