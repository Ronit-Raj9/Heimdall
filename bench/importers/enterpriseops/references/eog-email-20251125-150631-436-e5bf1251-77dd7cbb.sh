#!/bin/sh
# Reference for EnterpriseOps-Gym email task_20251125_150631_436_e5bf1251_77dd7cbb
# (written by hand from the task's checks): POP on for all mail with archive,
# then remove INBOX from inbox messages labelled Work.
exec python3 - <<'PY'
import json, os, urllib.request
url = json.loads(os.environ["BENCH_MCP_SERVERS"])["email"]["url"]
sid = {}
def rpc(method, params=None, i=[0]):
    i[0] += 1
    h = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    if sid.get("s"): h["mcp-session-id"] = sid["s"]
    req = urllib.request.Request(url, data=json.dumps({"jsonrpc": "2.0", "id": i[0], "method": method, "params": params or {}}).encode(), headers=h)
    with urllib.request.urlopen(req, timeout=120) as r:
        if r.headers.get("mcp-session-id"): sid["s"] = r.headers["mcp-session-id"]
        body = r.read().decode()
    if body.lstrip().startswith("event:") or "\ndata:" in body or body.startswith("data:"):
        body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
    return json.loads(body)
def call(name, **args):
    out = rpc("tools/call", {"name": name, "arguments": args})
    text = out["result"]["content"][0]["text"]
    print(name, text[:200])
    try: return json.loads(text)
    except ValueError: return text
rpc("initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}})
call("update_pop_settings", userId="me", accessWindow="allMail", disposition="archive")
labels = call("list_labels", userId="me")
byname = {l.get("name"): l["id"] for l in labels.get("labels", [])}
work, inbox = byname["Work"], byname["INBOX"]
msgs = call("list_messages", userId="me", labelIds=[inbox, work])
ids = [m["id"] for m in msgs.get("messages", [])]
if ids:
    call("batch_modify_messages", userId="me", ids=ids, removeLabelIds=[inbox])
PY
