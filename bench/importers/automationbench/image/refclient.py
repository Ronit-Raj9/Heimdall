# Reference client of `bifrost-bench import automationbench`: the start of every
# controls/reference.sh. It calls the task's MCP server (service "apps") the way
# an agent does; the task-specific steps follow it.
import json, os, urllib.request
SERVERS = json.loads(os.environ["BENCH_MCP_SERVERS"])
_sid, _n, _init = {}, [0], set()
def _rpc(method, params=None, svc="apps"):
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
def tool(name, **args):
    if "apps" not in _init:
        _rpc("initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}}); _init.add("apps")
    out = _rpc("tools/call", {"name": name, "arguments": args})
    if "error" in out: raise SystemExit("%s: %s" % (name, out["error"]))
    res = out["result"]; text = res["content"][0]["text"]
    print(name, args.get("method", ""), args.get("url", ""), text[:200].replace("\n", " "))
    if res.get("isError"): raise SystemExit("%s failed: %s" % (name, text[:500]))
    try: return json.loads(text)
    except ValueError: return text
def fetch(method, url, params=None, body=None):
    a = {"method": method, "url": url}
    if params is not None: a["params"] = json.dumps(params)
    if body is not None: a["body"] = json.dumps(body)
    out = tool("api_fetch", **a)
    if isinstance(out, dict) and isinstance(out.get("error"), (dict, str)):
        raise SystemExit("api_fetch %s %s: %s" % (method, url, json.dumps(out)[:500]))
    return out
def get(url, **params): return fetch("GET", url, params or None)
def post(url, body=None, **params): return fetch("POST", url, params or None, body)
def patch(url, body=None, **params): return fetch("PATCH", url, params or None, body)
def put(url, body=None, **params): return fetch("PUT", url, params or None, body)
def delete(url, **params): return fetch("DELETE", url, params or None)
def b64(text): return tool("base64_encode", text=text)
def send_email(to, subject, body, cc=None, frm=None):
    """Gmail messages.send with a raw RFC 2822 message."""
    hdr = "To: %s\r\n" % (", ".join(to) if isinstance(to, list) else to)
    if cc: hdr += "Cc: %s\r\n" % (", ".join(cc) if isinstance(cc, list) else cc)
    if frm: hdr += "From: %s\r\n" % frm
    raw = b64(hdr + "Subject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s" % (subject, body))
    return post("https://gmail.googleapis.com/gmail/v1/users/me/messages/send", {"raw": raw})
