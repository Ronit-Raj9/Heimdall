"""Launcher of a remote_http agent (adapters/remotehttp.go).

The agent is a deployed service. This program runs in the agent container,
posts the session to the service and writes the answer it returns to the
answer path. It is bench code: the container holds nothing of the agent's.

Request: POST <BENCH_REMOTE_URL> with a JSON body
  {"instruction", "session_id", "session_index", "output", "files"?,
   "gateway"?, "tools"?}
and "Authorization: Bearer <token>" when the agent config names auth_env or
auth_secret (with auth_header set, "<auth_header>: <token>" instead).
With sign_env or sign_secret set, "X-Heimdall-Timestamp: <unix seconds>" and
"X-Heimdall-Signature: sha256=<hex HMAC-SHA256 of '<timestamp>.<body>'>".

Answer: a JSON object with a string "answer", or a text/plain body. The JSON
object may also carry what the agent reports about itself (agent-reported,
never trusted): "usage" {input_tokens, output_tokens, total_tokens, model,
cost_usd}, "events" (native agent events or an OTLP JSON trace), "artifacts"
({path: base64} or [{path, content_base64}], only with remote.artifacts
inline), "version" and "build". A 202 answer is polled on the same host until
it is no longer queued, pending or running.

The connection goes to BENCH_REMOTE_CONNECT (the run's egress forwarder) when
it is set; the Host header and the TLS server name stay the URL's, so TLS is
verified against the real host end to end.
"""

import base64
import hashlib
import hmac
import http.client
import json
import os
import re
import socket
import sys
import time
import urllib.parse

MAX_ANSWER_BYTES = 16 << 20
MAX_FILES = 100
MAX_FILES_BYTES = 256 << 10
MAX_EVENTS = 500
MAX_EVENT_BYTES = 8 << 10
MAX_ARTIFACTS = 50
MAX_ARTIFACT_BYTES = 16 << 20
MAX_STR = 256
PENDING = ("queued", "pending", "running")
EVENT_PREFIX = "@@heimdall-event "
SECRET_RE = re.compile(r"(?i)(sk-[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._~+/=-]{8,}|(?:api[_-]?key|token|secret|password)[\"']?\s*[:=]\s*[\"']?[^\s\"',}]{4,})")


def emit(event):
    print(json.dumps(event, sort_keys=True), flush=True)


def fail(code, message):
    emit({"type": "error", "message": message})
    print("remote_http: " + message, file=sys.stderr, flush=True)
    sys.exit(code)


def clean(value, limit=MAX_STR):
    """An inert, capped, redacted display string, or "" for a non-string."""
    if not isinstance(value, str):
        return ""
    value = "".join(c for c in value if c.isprintable())
    value = SECRET_RE.sub("[redacted]", value)
    return value[:limit]


def connect_to(addr, timeout):
    """Open a TCP connection, retrying while the forwarder starts."""
    last = None
    for _ in range(20):
        try:
            return socket.create_connection(addr, timeout)
        except (ConnectionRefusedError, socket.gaierror) as e:
            last = e
            time.sleep(0.25)
    raise last


class Conn(http.client.HTTPConnection):
    def __init__(self, host, port, addr, timeout):
        super().__init__(host, port, timeout=timeout)
        self.addr = addr

    def connect(self):
        self.sock = connect_to(self.addr, self.timeout)


class TLSConn(http.client.HTTPSConnection):
    def __init__(self, host, port, addr, timeout):
        super().__init__(host, port, timeout=timeout)
        self.addr = addr

    def connect(self):
        sock = connect_to(self.addr, self.timeout)
        self.sock = self._context.wrap_socket(sock, server_hostname=self.host)


def workspace_files(root, skip):
    """Small text files of the workspace (a session's files), or None when
    there are more than the caps allow."""
    out, total = {}, 0
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if not d.startswith("."))
        for name in sorted(filenames):
            path = os.path.join(dirpath, name)
            rel = os.path.relpath(path, root)
            if name.startswith(".") or os.path.abspath(path) == skip or os.path.islink(path):
                continue
            try:
                with open(path, "rb") as f:
                    data = f.read(MAX_FILES_BYTES + 1)
                text = data.decode("utf-8")
            except (OSError, UnicodeDecodeError):
                continue
            total += len(data)
            if len(out) >= MAX_FILES or total > MAX_FILES_BYTES:
                return None
            out[rel] = text
    return out


def answer_of(doc, raw, content_type, required):
    """The answer text, or None when the task needs none and there is none."""
    if content_type.startswith("text/plain"):
        return raw.decode("utf-8", "replace")
    if not isinstance(doc, dict):
        fail(1, "the agent's answer is neither a JSON object nor text/plain")
    if "answer" not in doc:
        if required:
            fail(1, 'the agent\'s JSON answer has no "answer" field')
        return None
    value = doc["answer"]
    return value if isinstance(value, str) else json.dumps(value)


def count(v):
    """A non-negative integer token count, or None."""
    if isinstance(v, bool) or not isinstance(v, (int, float)) or v < 0 or v > 1e12:
        return None
    return int(v)


def usage_of(u):
    out = {}
    if not isinstance(u, dict):
        return out
    for k in ("input_tokens", "output_tokens", "total_tokens"):
        n = count(u.get(k))
        if n is not None:
            out[k] = n
    cost = u.get("cost_usd")
    if not isinstance(cost, bool) and isinstance(cost, (int, float)) and 0 <= cost < 1e6:
        out["cost_usd"] = float(cost)
    if clean(u.get("model")):
        out["model"] = clean(u.get("model"))
    return out


def otlp_events(doc):
    """Native events from an OTLP JSON trace: one per span."""
    out = []
    for rs in doc.get("resourceSpans") or []:
        for ss in (rs or {}).get("scopeSpans") or []:
            for sp in (ss or {}).get("spans") or []:
                if not isinstance(sp, dict):
                    continue
                attrs, kind = {}, "custom"
                for a in sp.get("attributes") or []:
                    if not isinstance(a, dict) or len(attrs) >= 32:
                        continue
                    v = a.get("value") or {}
                    val = v.get("stringValue", v.get("intValue", v.get("doubleValue", v.get("boolValue"))))
                    if isinstance(val, (str, int, float, bool)):
                        attrs[clean(a.get("key"), 128)] = clean(val) if isinstance(val, str) else val
                if attrs.get("heimdall.event.type"):
                    kind = attrs["heimdall.event.type"]
                ev = {"type": kind, "name": clean(sp.get("name")), "id": clean(sp.get("spanId"), 128),
                      "parent_id": clean(sp.get("parentSpanId"), 128), "attributes": attrs}
                for src, dst in (("startTimeUnixNano", "start_time"), ("endTimeUnixNano", "end_time")):
                    try:
                        ev[dst] = int(sp.get(src)) // 1000000
                    except (TypeError, ValueError):
                        pass
                code = ((sp.get("status") or {}).get("code"))
                ev["status"] = "error" if code in (2, "STATUS_CODE_ERROR") else "ok"
                out.append(ev)
    return out


def pass_events(events, report):
    """Prints each reported event as a prefixed stdout line, where the
    bench's agent-event collector reads it as agent-reported."""
    if isinstance(events, dict) and "resourceSpans" in events:
        events = otlp_events(events)
    if isinstance(events, str):
        parsed = []
        for line in events.splitlines():
            try:
                parsed.append(json.loads(line))
            except ValueError:
                report["events_dropped"] = report.get("events_dropped", 0) + 1
        events = parsed
    if not isinstance(events, list):
        if events is not None:
            report.setdefault("notes", []).append("events is not a list, a JSON lines string or an OTLP trace; dropped")
        return
    kept = 0
    for ev in events:
        line = json.dumps(ev, sort_keys=True) if isinstance(ev, dict) else ""
        if not line or kept >= MAX_EVENTS or len(line) > MAX_EVENT_BYTES:
            report["events_dropped"] = report.get("events_dropped", 0) + 1
            continue
        print(EVENT_PREFIX + SECRET_RE.sub("[redacted]", line), flush=True)
        kept += 1
    if kept:
        report["events"] = kept


def safe_rel(path):
    if not isinstance(path, str) or not path or path.startswith("/") or "\\" in path or "\x00" in path:
        return None
    norm = os.path.normpath(path)
    if norm != path or norm in (".", "..") or norm.startswith("../") or any(p.startswith(".") for p in norm.split("/")):
        return None
    return norm


def write_artifacts(arts, root, report):
    """Writes inline artifact files into the workspace, where the bench
    exports them like a local agent's. Unsafe paths and files past the caps
    are refused and noted."""
    if isinstance(arts, dict):
        arts = [{"path": k, "content_base64": v} for k, v in sorted(arts.items())]
    if not isinstance(arts, list):
        report.setdefault("notes", []).append("artifacts is not an object or a list; dropped")
        return
    total, written = 0, []
    for a in arts[:MAX_ARTIFACTS]:
        rel = safe_rel((a or {}).get("path")) if isinstance(a, dict) else None
        if rel is None:
            report.setdefault("notes", []).append("artifact with an unsafe path refused")
            continue
        try:
            data = base64.b64decode((a.get("content_base64") or ""), validate=True)
        except (ValueError, TypeError):
            report.setdefault("notes", []).append("artifact %s is not base64; refused" % clean(rel))
            continue
        total += len(data)
        if total > MAX_ARTIFACT_BYTES:
            report.setdefault("notes", []).append("artifacts past %d bytes refused" % MAX_ARTIFACT_BYTES)
            break
        dest = os.path.join(root, rel)
        parent = os.path.dirname(dest)
        os.makedirs(parent, exist_ok=True)
        if os.path.realpath(parent) != os.path.join(os.path.realpath(root), os.path.dirname(rel)).rstrip("/") or os.path.islink(dest):
            report.setdefault("notes", []).append("artifact %s leaves the workspace; refused" % clean(rel))
            continue
        with open(dest, "wb") as f:
            f.write(data)
        written.append(rel)
    if len(arts) > MAX_ARTIFACTS:
        report.setdefault("notes", []).append("more than %d artifacts; the rest refused" % MAX_ARTIFACTS)
    if written:
        report["artifacts"] = written


class Remote:
    """One remote agent endpoint: every request goes to its host and port."""

    def __init__(self, url):
        self.url = urllib.parse.urlsplit(url)
        if self.url.scheme not in ("http", "https") or not self.url.hostname:
            fail(2, "BENCH_REMOTE_URL is not an http(s) URL")
        self.port = self.url.port or (443 if self.url.scheme == "https" else 80)
        self.addr = (self.url.hostname, self.port)
        if os.environ.get("BENCH_REMOTE_CONNECT"):
            host, _, p = os.environ["BENCH_REMOTE_CONNECT"].rpartition(":")
            self.addr = (host, int(p))
        self.headers = {"Accept": "application/json, text/plain"}
        token = os.environ.get("BENCH_REMOTE_TOKEN", "")
        header = os.environ.get("BENCH_REMOTE_AUTH_HEADER", "")
        if token and header:
            self.headers[header] = token
        elif token:
            self.headers["Authorization"] = "Bearer " + token

    def same_origin(self, other):
        o = urllib.parse.urlsplit(urllib.parse.urljoin(urllib.parse.urlunsplit(self.url), other))
        port = o.port or (443 if o.scheme == "https" else 80)
        if (o.scheme, o.hostname, port) != (self.url.scheme, self.url.hostname, self.port):
            return None
        return (o.path or "/") + ("?" + o.query if o.query else "")

    def do(self, method, path, body, headers, timeout):
        cls = TLSConn if self.url.scheme == "https" else Conn
        conn = cls(self.url.hostname, self.port, self.addr, timeout)
        try:
            conn.request(method, path, body=body, headers=dict(self.headers, **headers))
            resp = conn.getresponse()
            raw = resp.read(MAX_ANSWER_BYTES + 1)
        except (OSError, http.client.HTTPException) as e:
            fail(1, "request to the agent failed: %s" % e)
        finally:
            conn.close()
        if len(raw) > MAX_ANSWER_BYTES:
            fail(1, "the agent's answer is larger than %d bytes" % MAX_ANSWER_BYTES)
        return resp, raw


def parse_json(raw):
    try:
        return json.loads(raw) if raw.strip() else {}
    except ValueError:
        return None


def poll(remote, resp, raw, deadline, report):
    """Follows a 202 answer on the same host until it is done."""
    doc = parse_json(raw) or {}
    target = resp.getheader("Location") or (doc.get("status_url") if isinstance(doc, dict) else None)
    if not target and isinstance(doc, dict) and "id" in doc:
        target = urllib.parse.urlunsplit(remote.url).rstrip("/") + "/" + urllib.parse.quote(str(doc["id"]), safe="")
    path = remote.same_origin(str(target)) if target else None
    if path is None:
        fail(1, "the agent answered 202 without a status URL on its own host (Location, status_url or id)")
    report["async"] = True
    polls = 0
    while True:
        wait = 2.0
        try:
            wait = min(30.0, max(1.0, float(resp.getheader("Retry-After") or 2)))
        except ValueError:
            pass
        if time.time() + wait > deadline:
            fail(1, "the agent's run was still pending at the timeout (%d polls)" % polls)
        time.sleep(wait)
        polls += 1
        resp, raw = remote.do("GET", path, None, {}, max(1.0, min(30.0, deadline - time.time())))
        doc = parse_json(raw)
        pending = resp.status == 202 or (isinstance(doc, dict) and str(doc.get("status", "")).lower() in PENDING)
        if not pending:
            emit({"type": "poll", "polls": polls, "status": resp.status})
            return resp, raw


def main():
    remote = Remote(os.environ["BENCH_REMOTE_URL"])
    timeout = float(os.environ.get("BENCH_REMOTE_TIMEOUT_S", "600"))
    deadline = time.time() + timeout
    with open(os.environ["BENCH_REMOTE_INSTRUCTION"], encoding="utf-8") as f:
        instruction = f.read()
    answer_path = os.environ.get("BENCH_ANSWER_PATH", "answer.txt")
    output = os.environ.get("BENCH_REMOTE_OUTPUT", "answer")
    body = {
        "instruction": instruction,
        "session_id": os.environ.get("BENCH_REMOTE_SESSION_ID", ""),
        "session_index": int(os.environ.get("BENCH_REMOTE_SESSION_INDEX", "1")),
        "output": output,
    }
    files_root = os.environ.get("BENCH_REMOTE_FILES_DIR", "")
    if files_root and os.path.isdir(files_root):
        files = workspace_files(files_root, os.path.abspath(answer_path))
        if files is None:
            emit({"type": "note", "message": "workspace files left out: more than %d files or %d bytes" % (MAX_FILES, MAX_FILES_BYTES)})
        elif files:
            body["files"] = files
    if os.environ.get("BENCH_REMOTE_TRAFFIC") == "gateway":
        body["gateway"] = {
            "url": os.environ.get("BENCH_REMOTE_GATEWAY_URL", ""),
            "key": os.environ.get("BENCH_REMOTE_RUN_KEY", ""),
            "model": os.environ.get("BENCH_REMOTE_MODEL", ""),
        }
    if os.environ.get("BENCH_REMOTE_TOOLS"):
        tools, token = json.loads(os.environ["BENCH_REMOTE_TOOLS"]), os.environ.get("BENCH_REMOTE_TOOLS_TOKEN", "")
        body["tools"] = {k: dict(v, token=token) for k, v in sorted(tools.items())}
    payload = json.dumps(body).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    secret = os.environ.get("BENCH_REMOTE_SIGN_SECRET", "")
    if secret and len(secret) < 16:
        fail(2, "the signing secret is shorter than 16 characters")
    if secret:
        ts = str(int(time.time()))
        mac = hmac.new(secret.encode("utf-8"), ts.encode("ascii") + b"." + payload, hashlib.sha256).hexdigest()
        headers["X-Heimdall-Timestamp"] = ts
        headers["X-Heimdall-Signature"] = "sha256=" + mac
    path = (remote.url.path or "/") + ("?" + remote.url.query if remote.url.query else "")
    started = time.time()
    emit({"type": "request", "session_id": body["session_id"], "session_index": body["session_index"],
          "files": len(body.get("files", {})), "tools": len(body.get("tools", {})), "signed": bool(secret)})
    resp, raw = remote.do("POST", path, payload, headers, timeout)
    report = {}
    if resp.status == 202:
        resp, raw = poll(remote, resp, raw, deadline, report)
    emit({"type": "response", "status": resp.status, "bytes": len(raw),
          "latency_ms": int((time.time() - started) * 1000)})
    if resp.status // 100 != 2:
        fail(1, "the agent answered HTTP %d: %s" % (resp.status, clean(raw[:500].decode("utf-8", "replace"), 500)))
    content_type = resp.getheader("Content-Type", "application/json").lower()
    doc = None if content_type.startswith("text/plain") else parse_json(raw)
    if isinstance(doc, dict) and str(doc.get("status", "completed")).lower() == "failed":
        fail(1, "the agent reported its run failed: %s" % clean(str(doc.get("error", "")), 500))
    answer = answer_of(doc, raw, content_type, output == "answer")
    if isinstance(doc, dict):
        usage = usage_of(doc.get("usage"))
        if usage:
            report["usage"] = usage
        for k in ("version", "build"):
            if clean(doc.get(k)):
                report[k] = clean(doc.get(k))
        pass_events(doc.get("events"), report)
        if doc.get("artifacts") is not None:
            if os.environ.get("BENCH_REMOTE_ARTIFACTS") == "inline" and files_root:
                write_artifacts(doc["artifacts"], files_root, report)
            else:
                report.setdefault("notes", []).append("artifacts sent without remote.artifacts inline; ignored")
    if report:
        report["type"] = "remote_report"
        emit(report)
    if answer is not None:
        parent = os.path.dirname(answer_path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(answer_path, "w", encoding="utf-8") as f:
            f.write(answer)
    emit({"type": "result", "answer_bytes": len((answer or "").encode("utf-8"))})


if __name__ == "__main__":
    main()
