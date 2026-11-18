"""MCP relay for `bifrost-bench mcp scan`.

Runs in the trusted helper image inside the sandbox's network namespace, so
it reaches the MCP server on 127.0.0.1 while the server itself has no route
out. It reads one JSON request per line on stdin:

  {"n": 1, "url": "http://127.0.0.1:8080/mcp", "headers": {...}, "body": "...", "want": <id or null>, "timeout": 30}

POSTs the body and writes one JSON line per request on stdout:

  {"n": 1, "status": 200, "content_type": "...", "session": "...", "body": "...", "error": ""}

For an event-stream answer it stops reading once the event carrying the
JSON-RPC response with id `want` has arrived, so a server that keeps the
stream open does not stall the scan. Standard library only.
"""
import json
import sys
import urllib.error
import urllib.request

MAX_BODY = 8 << 20


def read_sse(resp, want):
    lines = []
    size = 0
    data = []
    while size < MAX_BODY:
        raw = resp.readline()
        if not raw:
            break
        size += len(raw)
        line = raw.decode("utf-8", "replace")
        lines.append(line)
        s = line.rstrip("\r\n")
        if s.startswith("data:"):
            data.append(s[5:].lstrip(" "))
        elif s == "" and data:
            try:
                msg = json.loads("\n".join(data))
            except ValueError:
                msg = None
            data = []
            msgs = msg if isinstance(msg, list) else [msg]
            if want is not None and any(isinstance(m, dict) and m.get("id") == want and ("result" in m or "error" in m) for m in msgs):
                break
    return "".join(lines)


def handle(req):
    out = {"n": req.get("n"), "status": 0, "content_type": "", "session": "", "body": "", "error": ""}
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    headers.update(req.get("headers") or {})
    r = urllib.request.Request(req["url"], data=req.get("body", "").encode("utf-8"), headers=headers, method="POST")
    try:
        resp = urllib.request.urlopen(r, timeout=req.get("timeout") or 30)
    except urllib.error.HTTPError as e:
        resp = e
    except Exception as e:  # connection refused while the server starts, timeouts
        out["error"] = "%s: %s" % (type(e).__name__, e)
        return out
    try:
        out["status"] = resp.status if hasattr(resp, "status") else resp.code
        ctype = resp.headers.get("Content-Type", "") or ""
        out["content_type"] = ctype
        out["session"] = resp.headers.get("Mcp-Session-Id", "") or ""
        if ctype.lower().startswith("text/event-stream"):
            out["body"] = read_sse(resp, req.get("want"))
        else:
            out["body"] = resp.read(MAX_BODY).decode("utf-8", "replace")
    except Exception as e:
        out["error"] = "%s: %s" % (type(e).__name__, e)
    finally:
        try:
            resp.close()
        except Exception:
            pass
    return out


def main():
    sys.stdout.write(json.dumps({"n": 0, "ready": True}) + "\n")
    sys.stdout.flush()
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
            out = handle(req)
        except Exception as e:
            out = {"n": None, "status": 0, "error": "%s: %s" % (type(e).__name__, e)}
        sys.stdout.write(json.dumps(out) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
