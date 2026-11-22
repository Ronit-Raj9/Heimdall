"""Launcher of a remote_http agent (adapters/remotehttp.go).

The agent is a deployed service. This program runs in the agent container,
posts the session to the service and writes the answer it returns to the
answer path. It is bench code: the container holds nothing of the agent's.

Request: POST <BENCH_REMOTE_URL> with a JSON body
  {"instruction", "session_id", "session_index", "output", "files"?,
   "gateway"?, "tools"?}
"gateway" is {"url", "key", "model", "headers"?}: "headers" are extra
request headers the service must send on each of its model calls.
For a conversation task (BENCH_REMOTE_CHAT) the body also carries
"messages" (the conversation so far, ending with the user's message;
"instruction" is that message), "turn" and "turns", and "attachments"
([{name, media_type, url} or {name, media_type, path, content_base64}]).
After each reply the next scripted user turn is appended and posted again
on the same session id; the last reply is the answer, or with grade_on
transcript the whole conversation as text.
and "Authorization: Bearer <token>" when the agent config names auth_env or
auth_secret (with auth_header set, "<auth_header>: <token>" instead).
With sign_env or sign_secret set, "X-Heimdall-Timestamp: <unix seconds>" and
"X-Heimdall-Signature: sha256=<hex HMAC-SHA256 of '<timestamp>.<body>'>".

Conversation (lane Y11, BENCH_CONV_USERS set): the bench drives a chat
task. Each message of the simulated customer is one request, with
"output": "conversation" and "conversation": {"turn", "message", "history",
"messages"? (the whole chat so far, OpenAI roles, with history client)};
the session_id is the conversation's. The "answer" is the agent's reply to
the customer. The agent needs no respond_to_user tool.

Repo in, patch out (BENCH_REPO=tarball, diff tasks): the request carries
"repo": {"url", "format": "tar.gz", "sha256", "bytes", "expires_at",
"files"}: a signed link to the workspace that expires with the run. The
answer carries "patch", a unified diff, which is checked (size, paths) and
applied to the workspace; the bench then grades the change like any
agent's.

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
MAX_ATTACHMENT_BYTES = 8 << 20
MAX_STR = 256
PENDING = ("queued", "pending", "running")
EVENT_PREFIX = "@@heimdall-event "
SECRET_RE = re.compile(r"(?i)(sk-[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._~+/=-]{8,}|(?:api[_-]?key|token|secret|password)[\"']?\s*[:=]\s*[\"']?[^\s\"',}]{4,})")


# Y03 (G1 P1-2): the customer's endpoint failing before it answers is
# infrastructure. RETRIES more attempts are made, BACKOFF_S apart (doubling);
# then the launcher prints an endpoint_unreachable line and exits
# EXIT_UNREACHABLE, and the bench seals the run as aborted_infra (never
# scored; the slot gets a new run, up to three attempts).
RETRIES = max(0, min(5, int(os.environ.get("BENCH_REMOTE_RETRIES", "3") or 3)))
BACKOFF_S = float(os.environ.get("BENCH_REMOTE_BACKOFF_S", "2") or 2)
RETRY_STATUS = (502, 503, 504)
EXIT_UNREACHABLE = 75


def emit(event):
    print(json.dumps(event, sort_keys=True), flush=True)


def fail(code, message):
    emit({"type": "error", "message": message})
    print("remote_http: " + message, file=sys.stderr, flush=True)
    sys.exit(code)


def unreachable(reason, attempts):
    emit({"type": "endpoint_unreachable", "reason": reason, "attempts": attempts})
    print("remote_http: the agent's endpoint did not answer after %d attempts: %s" % (attempts, reason), file=sys.stderr, flush=True)
    sys.exit(EXIT_UNREACHABLE)


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


CONNECT_TIMEOUT_S = 20


class Conn(http.client.HTTPConnection):
    def __init__(self, host, port, addr, timeout):
        super().__init__(host, port, timeout=timeout)
        self.addr = addr

    def connect(self):
        self.sock = connect_to(self.addr, min(self.timeout, CONNECT_TIMEOUT_S))
        self.sock.settimeout(self.timeout)


class TLSConn(http.client.HTTPSConnection):
    def __init__(self, host, port, addr, timeout):
        super().__init__(host, port, timeout=timeout)
        self.addr = addr

    def connect(self):
        sock = connect_to(self.addr, min(self.timeout, CONNECT_TIMEOUT_S))
        self.sock = self._context.wrap_socket(sock, server_hostname=self.host)
        self.sock.settimeout(self.timeout)


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

    def do(self, method, path, body, headers, timeout, deadline=None):
        """One request. With a deadline, a failure to connect (refused,
        DNS, TLS, connect timeout) and a 502, 503 or 504 answer are retried
        up to RETRIES times; when every attempt fails that way the endpoint
        is unreachable, which is the customer's infrastructure, never the
        agent's answer (exit EXIT_UNREACHABLE)."""
        attempts = (RETRIES + 1) if deadline else 1
        last = ""
        for attempt in range(attempts):
            if attempt:
                pause = min(BACKOFF_S * (2 ** (attempt - 1)), 30.0)
                if deadline and time.time() + pause >= deadline:
                    break
                emit({"type": "retry", "attempt": attempt + 1, "reason": last})
                time.sleep(pause)
            cls = TLSConn if self.url.scheme == "https" else Conn
            conn = cls(self.url.hostname, self.port, self.addr, timeout)
            try:
                try:
                    conn.connect()
                except (OSError, http.client.HTTPException) as e:
                    last = clean("connect: %s" % e, 200)
                    continue
                try:
                    conn.request(method, path, body=body, headers=dict(self.headers, **headers))
                    resp = conn.getresponse()
                except socket.timeout as e:
                    fail(1, "the agent did not answer within %ds: %s" % (int(timeout), e))
                except (OSError, http.client.HTTPException) as e:
                    # Closed or reset before a single response byte: the
                    # endpoint (or its load balancer) is down, not answering.
                    last = clean("no response: %s" % (e or type(e).__name__), 200)
                    continue
                try:
                    raw = resp.read(MAX_ANSWER_BYTES + 1)
                except (OSError, http.client.HTTPException) as e:
                    fail(1, "reading the agent's answer failed: %s" % e)
            finally:
                conn.close()
            if len(raw) > MAX_ANSWER_BYTES:
                fail(1, "the agent's answer is larger than %d bytes" % MAX_ANSWER_BYTES)
            if deadline and resp.status in RETRY_STATUS:
                last = "HTTP %d" % resp.status
                continue
            return resp, raw
        if attempts == 1:
            fail(1, "request to the agent failed: %s" % last)
        unreachable(last, attempts)


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


def load_chat():
    """The task's conversation (BENCH_REMOTE_CHAT), or None for a plain task."""
    path = os.environ.get("BENCH_REMOTE_CHAT", "")
    if not path:
        return None
    with open(path, encoding="utf-8") as f:
        doc = json.load(f)
    msgs = [{"role": m["role"], "content": m["content"]} for m in doc.get("messages") or []
            if isinstance(m, dict) and isinstance(m.get("content"), str)]
    if not msgs or msgs[-1]["role"] != "user":
        fail(2, "the task's conversation does not end with the user's message")
    return {"messages": msgs, "user_turns": [t for t in doc.get("user_turns") or [] if isinstance(t, str)],
            "attachments": doc.get("attachments") or [], "grade_on": doc.get("grade_on") or "final"}


def chat_attachments(items, root):
    """The conversation's attachments as sent to the agent: links as they are,
    files stored with the task inline as base64, up to MAX_ATTACHMENT_BYTES."""
    out, total = [], 0
    for a in items:
        if not isinstance(a, dict) or not a.get("name"):
            continue
        item = {"name": str(a["name"]), "media_type": str(a.get("media_type") or "application/octet-stream")}
        if a.get("url"):
            item["url"] = str(a["url"])
        elif a.get("path") and root:
            path = os.path.join(root, str(a["path"]))
            if os.path.islink(path) or not os.path.isfile(path):
                continue
            with open(path, "rb") as f:
                data = f.read(MAX_ATTACHMENT_BYTES + 1)
            total += len(data)
            if total > MAX_ATTACHMENT_BYTES:
                emit({"type": "note", "message": "attachment %s left out: over %d bytes in all" % (item["name"], MAX_ATTACHMENT_BYTES)})
                continue
            item["path"] = str(a["path"])
            item["content_base64"] = base64.b64encode(data).decode("ascii")
        else:
            continue
        out.append(item)
    return out


def render_chat(msgs):
    return "\n\n".join("%s: %s" % (m["role"], m["content"].strip()) for m in msgs) + "\n"


def sign_headers(payload):
    headers = {"Content-Type": "application/json"}
    secret = os.environ.get("BENCH_REMOTE_SIGN_SECRET", "")
    if secret and len(secret) < 16:
        fail(2, "the signing secret is shorter than 16 characters")
    if secret:
        ts = str(int(time.time()))
        mac = hmac.new(secret.encode("utf-8"), ts.encode("ascii") + b"." + payload, hashlib.sha256).hexdigest()
        headers["X-Heimdall-Timestamp"] = ts
        headers["X-Heimdall-Signature"] = "sha256=" + mac
    return headers, bool(secret)


def exchange(remote, body, deadline, report):
    """Posts one request body to the agent (following a 202) and returns
    (doc, raw, content_type) of a 2xx answer that did not report failure."""
    payload = json.dumps(body).encode("utf-8")
    headers, signed = sign_headers(payload)
    path = (remote.url.path or "/") + ("?" + remote.url.query if remote.url.query else "")
    started = time.time()
    req = {"type": "request", "session_id": body["session_id"], "session_index": body["session_index"],
           "files": len(body.get("files", {})), "tools": len(body.get("tools", {})), "signed": signed,
           "turn": (body.get("conversation") or {}).get("turn", 0), "repo": bool(body.get("repo"))}
    if "messages" in body:
        # Y13: a chat task's request carries the conversation so far.
        req["messages"] = len(body["messages"])
        req["turn"] = body.get("turn", 1)
    emit(req)
    resp, raw = remote.do("POST", path, payload, headers, max(1.0, deadline - time.time()), deadline)
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
    return doc, raw, content_type


def take_report(doc, report):
    """Folds what the agent reports about itself into report (agent-reported)."""
    if not isinstance(doc, dict):
        return
    usage = usage_of(doc.get("usage"))
    if usage:
        total = report.setdefault("usage", {})
        for k, v in usage.items():
            total[k] = total.get(k, 0) + v if isinstance(v, (int, float)) and k != "model" else v
    for k in ("version", "build"):
        if clean(doc.get(k)):
            report[k] = clean(doc.get(k))
    pass_events(doc.get("events"), report)


def conv_helpers():
    """heimdall_conv (adapters/heimdall_conv.py), beside this launcher."""
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    import heimdall_conv  # noqa: E402
    return heimdall_conv


def offer_repo(body, report):
    """Repo in: packs the workspace, stores it on Heimdall's pull endpoint and
    adds the signed, expiring link to the request."""
    hc = conv_helpers()
    root = os.environ.get("BENCH_REPO_DIR", "/workspace")
    try:
        data, files = hc.workspace_tarball(root, skip=[os.path.abspath(os.environ.get("BENCH_ANSWER_PATH", ""))])
        server = hc.Server(os.environ["BENCH_PULL_URL"], os.environ.get("BENCH_PULL_TOKEN", ""), os.environ.get("BENCH_PULL_CONNECT", ""))
        info = server.publish_repo(data)
    except (ValueError, OSError, KeyError) as e:
        fail(1, "could not offer the repository: %s" % clean(str(e), 500))
    body["repo"] = {"url": info["url"], "format": "tar.gz", "sha256": info.get("sha256", ""), "bytes": info.get("bytes", len(data)),
                    "expires_at": info.get("expires_at", ""), "files": files}
    report["repo"] = {"bytes": len(data), "files": files}


def take_patch(doc, report):
    """Patch out: applies the agent's unified diff to the workspace."""
    hc = conv_helpers()
    patch = doc.get("patch") if isinstance(doc, dict) else None
    if patch is None or patch == "":
        report.setdefault("notes", []).append("the agent returned no patch")
        return
    try:
        touched = hc.apply_patch(os.environ.get("BENCH_REPO_DIR", "/workspace"), patch)
    except ValueError as e:
        # A patch that is unsafe or does not apply is the agent's result: the
        # workspace stays as it was and the grader sees no change.
        report["patch"] = {"applied": False, "error": clean(str(e), 500)}
        emit({"type": "note", "message": "patch refused: " + clean(str(e), 500)})
        return
    report["patch"] = {"applied": True, "files": len(touched), "bytes": len(patch.encode("utf-8"))}


def main():
    if os.environ.get("BENCH_REMOTE_MAPPING") or os.environ.get("BENCH_REMOTE_AUTH"):
        if os.environ.get("BENCH_REMOTE_CHAT"):
            # TODO(Y13): multi-turn chat tasks on the mapped path; for now the
            # mapped request carries only the instruction (the last user message).
            emit({"type": "note", "message": "chat task on a mapped endpoint: only the instruction is sent, scripted user turns are not"})
        return main_mapped()  # Y10: the endpoint's own request and answer shape
    remote = Remote(os.environ["BENCH_REMOTE_URL"])
    timeout = float(os.environ.get("BENCH_REMOTE_TIMEOUT_S", "600"))
    deadline = time.time() + timeout
    with open(os.environ["BENCH_REMOTE_INSTRUCTION"], encoding="utf-8") as f:
        instruction = f.read()
    answer_path = os.environ.get("BENCH_ANSWER_PATH", "answer.txt")
    output = os.environ.get("BENCH_REMOTE_OUTPUT", "answer")
    chat = load_chat()  # Y13: chat-message task (messages, scripted user turns, attachments)
    body = {
        "instruction": instruction,
        "session_id": os.environ.get("BENCH_REMOTE_SESSION_ID", ""),
        "session_index": int(os.environ.get("BENCH_REMOTE_SESSION_INDEX", "1")),
        "output": output,
    }
    files_root = os.environ.get("BENCH_REMOTE_FILES_DIR", "")
    repo_mode = os.environ.get("BENCH_REPO") == "tarball"
    if files_root and os.path.isdir(files_root) and not repo_mode:
        files = workspace_files(files_root, os.path.abspath(answer_path))
        if files is None:
            emit({"type": "note", "message": "workspace files left out: more than %d files or %d bytes" % (MAX_FILES, MAX_FILES_BYTES)})
        elif files:
            body["files"] = files
    gateway = gateway_block()
    if gateway is not None:
        body["gateway"] = gateway
    if os.environ.get("BENCH_REMOTE_TOOLS"):
        tools, token = json.loads(os.environ["BENCH_REMOTE_TOOLS"]), os.environ.get("BENCH_REMOTE_TOOLS_TOKEN", "")
        body["tools"] = {k: dict(v, token=token) for k, v in sorted(tools.items())}
    report = {}
    if repo_mode:
        offer_repo(body, report)
    last_doc = None
    if os.environ.get("BENCH_CONV_USERS"):
        # Y11: the bench drives the conversation. Each user message is one
        # request; the agent's "answer" is its reply to the customer.
        hc = conv_helpers()
        history = os.environ.get("BENCH_CONV_HISTORY", "client")
        body["output"] = "conversation"

        def agent_turn(user_text, messages, turn):
            nonlocal last_doc
            req = dict(body)
            req["conversation"] = {"turn": turn, "message": user_text, "history": history}
            if history == "client":
                req["conversation"]["messages"] = messages
            doc, raw, ctype = exchange(remote, req, deadline, report)
            take_report(doc, report)
            last_doc = doc
            return answer_of(doc, raw, ctype, True)

        try:
            summary, answer, _ = hc.drive(agent_turn, emit, deadline)
        except hc.ConversationError as e:
            fail(1, str(e))
        report["conversation"] = {k: summary[k] for k in ("agent_turns", "ended_by", "user_server")}
        if output != "answer":
            answer = None
        doc = last_doc
    elif chat is not None:
        # Y13: a conversation task. The customer's messages as they are, then
        # each scripted user turn after the agent's reply, on one session id.
        transcript = list(chat["messages"])
        attachments = chat_attachments(chat["attachments"], files_root)
        turns = [None] + chat["user_turns"]
        answer, doc = "", None
        for n, turn in enumerate(turns, start=1):
            if turn is not None:
                transcript.append({"role": "user", "content": turn})
            req = dict(body, instruction=transcript[-1]["content"], messages=list(transcript), turn=n, turns=len(turns))
            if attachments:
                req["attachments"] = attachments
            doc, raw, content_type = exchange(remote, req, deadline, report)
            take_report(doc, report)
            reply = answer_of(doc, raw, content_type, True) or ""
            transcript.append({"role": "assistant", "content": reply})
            answer = reply
            if time.time() >= deadline and n < len(turns):
                fail(1, "timed out after %d of %d turns" % (n, len(turns)))
        emit({"type": "conversation", "turns": len(turns), "messages": len(transcript)})
        if chat["grade_on"] == "transcript":
            answer = render_chat(transcript)
    else:
        doc, raw, content_type = exchange(remote, body, deadline, report)
        answer = answer_of(doc, raw, content_type, output == "answer")
        take_report(doc, report)
    if isinstance(doc, dict):
        if doc.get("artifacts") is not None:
            if os.environ.get("BENCH_REMOTE_ARTIFACTS") == "inline" and files_root:
                write_artifacts(doc["artifacts"], files_root, report)
            else:
                report.setdefault("notes", []).append("artifacts sent without remote.artifacts inline; ignored")
    if repo_mode:
        take_patch(doc, report)
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


# Helpers of the mapped path (lane Y10): Heimdall's own body and answer
# shape, used when a mapping or auth block is set.
def heimdall_body(instruction, output, answer_path, files_root):
    """Heimdall's own request body."""
    body = {
        "instruction": instruction,
        "session_id": os.environ.get("BENCH_REMOTE_SESSION_ID", ""),
        "session_index": int(os.environ.get("BENCH_REMOTE_SESSION_INDEX", "1")),
        "output": output,
    }
    if files_root and os.path.isdir(files_root):
        files = workspace_files(files_root, os.path.abspath(answer_path))
        if files is None:
            emit({"type": "note", "message": "workspace files left out: more than %d files or %d bytes" % (MAX_FILES, MAX_FILES_BYTES)})
        elif files:
            body["files"] = files
    gateway = gateway_block()
    if gateway is not None:
        body["gateway"] = gateway
    tools = tools_block()
    if tools:
        body["tools"] = tools
    return body


def gateway_block():
    if os.environ.get("BENCH_REMOTE_TRAFFIC") != "gateway":
        return None
    block = {
        "url": os.environ.get("BENCH_REMOTE_GATEWAY_URL", ""),
        "key": os.environ.get("BENCH_REMOTE_RUN_KEY", ""),
        "model": os.environ.get("BENCH_REMOTE_MODEL", ""),
    }
    # Extra headers for every model call through the gateway (fault plan
    # and run tags of the heimdall plugin, cache headers); only when set.
    try:
        headers = json.loads(os.environ.get("BENCH_GATEWAY_HEADERS", "") or "{}")
    except ValueError:
        headers = {}
    if isinstance(headers, dict) and headers:
        block["headers"] = {str(k): str(v) for k, v in sorted(headers.items())}
    return block


def tools_block():
    if not os.environ.get("BENCH_REMOTE_TOOLS"):
        return {}
    tools, token = json.loads(os.environ["BENCH_REMOTE_TOOLS"]), os.environ.get("BENCH_REMOTE_TOOLS_TOKEN", "")
    return {k: dict(v, token=token) for k, v in sorted(tools.items())}


def mapped_sign_headers(payload):
    """The HMAC signature headers of a request body, {} without a secret."""
    secret = os.environ.get("BENCH_REMOTE_SIGN_SECRET", "")
    if not secret:
        return {}
    if len(secret) < 16:
        fail(2, "the signing secret is shorter than 16 characters")
    ts = str(int(time.time()))
    mac = hmac.new(secret.encode("utf-8"), ts.encode("ascii") + b"." + payload, hashlib.sha256).hexdigest()
    return {"X-Heimdall-Timestamp": ts, "X-Heimdall-Signature": "sha256=" + mac}


def read_answer(resp, raw, output, files_root):
    """Reads Heimdall's own answer shape: the answer text (or None) and the
    agent's self-report."""
    if resp.status // 100 != 2:
        fail(1, "the agent answered HTTP %d: %s" % (resp.status, clean(raw[:500].decode("utf-8", "replace"), 500)))
    content_type = (resp.getheader("Content-Type") or "application/json").lower()
    doc = None if content_type.startswith("text/plain") else parse_json(raw)
    if isinstance(doc, dict) and str(doc.get("status", "completed")).lower() == "failed":
        fail(1, "the agent reported its run failed: %s" % clean(str(doc.get("error", "")), 500))
    answer = answer_of(doc, raw, content_type, output == "answer")
    report = {}
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
    return answer, report


def write_answer(answer, answer_path, report):
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


# ---------------------------------------------------------------------------
# Request and response mapping (lane Y10; remotemap/ is the Go twin and the
# tests run both against the same endpoints). BENCH_REMOTE_MAPPING holds the
# mapping (schemas.RemoteMapping) with every {{secret.NAME}} written as
# {{env_secret.NAME}}, whose value is BENCH_REMOTE_SECRET_<NAME>.
# BENCH_REMOTE_AUTH holds the auth block (secret names only) and
# BENCH_REMOTE_CONNECT_MAP maps each host:port the run may reach to its
# forwarder port.

PH_RE = re.compile(r"\{\{\s*([A-Za-z_][A-Za-z0-9_.]*)\s*\}\}")
TOKEN_EARLY = 60
MAX_POLLS = 600
MAX_STREAM_EVENTS = 20000


def secret_value(name):
    v = os.environ.get("BENCH_REMOTE_SECRET_" + name, "")
    if not v:
        fail(2, "secret %s is not set: add it on Settings > Secrets" % name)
    return v


class Vars:
    def __init__(self, instruction, output):
        self.instruction = instruction
        self.output = output
        self.session_id = os.environ.get("BENCH_REMOTE_SESSION_ID", "")
        self.session_index = int(os.environ.get("BENCH_REMOTE_SESSION_INDEX", "1"))
        self.model = os.environ.get("BENCH_REMOTE_MODEL", "")
        self.start_id = ""
        self.reply_id = ""

    def lookup(self, name, secrets):
        if name == "instruction":
            return self.instruction
        if name == "messages":
            return [{"role": "user", "content": self.instruction}]
        if name == "tools":
            return tools_block()
        if name == "metadata":
            m = {"session_id": self.session_id, "session_index": self.session_index, "output": self.output}
            for k, env in (("task", "BENCH_REMOTE_TASK"), ("run", "BENCH_REMOTE_RUN")):
                if os.environ.get(env):
                    m[k] = os.environ[env]
            return m
        if name == "gateway":
            return gateway_block()
        if name == "session_id":
            return self.session_id
        if name == "session_index":
            return self.session_index
        if name == "output":
            return self.output
        if name == "model":
            return self.model
        if name == "uuid":
            return os.urandom(16).hex()
        if name == "start.id":
            return self.start_id
        if name == "reply.id":
            return self.reply_id
        if name.startswith("env_secret.") and secrets:
            return secret_value(name[len("env_secret."):])
        fail(2, "unknown placeholder {{%s}} in the mapping" % name)


def text_of(v):
    if v is None:
        return ""
    if isinstance(v, str):
        return v
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)) and not isinstance(v, bool):
        return json.dumps(v)
    return json.dumps(v, separators=(",", ":"), sort_keys=True)


def render_text(s, v, secrets=False, escape=None):
    def sub(m):
        t = text_of(v.lookup(m.group(1), secrets))
        return escape(t) if escape else t
    return PH_RE.sub(sub, s or "")


def path_escape(t):
    return urllib.parse.quote(t, safe="$&+=:@")


def render_node(n, v):
    if isinstance(n, str):
        m = PH_RE.fullmatch(n)
        if m:
            return v.lookup(m.group(1), False)
        return render_text(n, v)
    if isinstance(n, list):
        return [render_node(e, v) for e in n]
    if isinstance(n, dict):
        return {k: render_node(e, v) for k, e in n.items()}
    return n


def render_body(tmpl, v):
    try:
        doc = json.loads(tmpl)
    except ValueError as e:
        fail(2, "the request template is not valid JSON: %s" % e)
    return json.dumps(render_node(doc, v)).encode("utf-8")


# Paths: the JSONPath subset of remotemap/path.go.

def parse_path(src):
    s = src.strip()
    if s.startswith("$"):
        s = s[1:]
    segs, i = [], 0
    while i < len(s):
        c = s[i]
        if c == "." or (i == 0 and c != "["):
            if c == ".":
                i += 1
            j = i
            while j < len(s) and s[j] not in ".[":
                j += 1
            if j == i:
                fail(2, "path %r has an empty key" % src)
            segs.append(("wild",) if s[i:j] == "*" else ("key", s[i:j]))
            i = j
        elif c == "[":
            rest = s[i + 1:]
            if rest.startswith("*]"):
                segs.append(("wild",))
                i += 3
            elif rest[:1] in ("'", '"'):
                j = rest.find(rest[0], 1)
                if j < 0 or rest[j + 1:j + 2] != "]":
                    fail(2, "path %r has an unterminated key" % src)
                segs.append(("key", rest[1:j]))
                i += j + 3
            elif rest.startswith("?("):
                j = rest.find(")]")
                if j < 0:
                    fail(2, "path %r has an unterminated filter" % src)
                segs.append(parse_filter(rest[2:j], src))
                i += j + 3
            else:
                j = rest.find("]")
                try:
                    segs.append(("index", int(rest[:j].strip())))
                except ValueError:
                    fail(2, "path %r has a bad index" % src)
                i += j + 2
        else:
            fail(2, "path %r: unexpected %r" % (src, c))
    return segs


def parse_filter(expr, src):
    expr = expr.strip()
    op, left, right = "", expr, ""
    for o in ("==", "!="):
        k = expr.find(o)
        if k >= 0:
            op, left, right = o, expr[:k].strip(), expr[k + 2:].strip()
            break
    if not left.startswith("@.") or len(left) < 3:
        fail(2, "path %r: bad filter" % src)
    field = left[2:].split(".")
    value = None
    if op:
        if len(right) >= 2 and right[0] in "'\"" and right[-1] == right[0]:
            value = right[1:-1]
        elif right in ("true", "false"):
            value = right == "true"
        elif right == "null":
            value = None
        else:
            try:
                value = float(right)
            except ValueError:
                fail(2, "path %r: bad filter value" % src)
    return ("filter", field, op, value)


def filter_match(seg, v):
    _, field, op, value = seg
    for f in field:
        if not isinstance(v, dict) or f not in v:
            return False
        v = v[f]
    if not op:
        return v is not None
    if isinstance(value, float) and isinstance(v, (int, float)) and not isinstance(v, bool):
        eq = float(v) == value
    else:
        eq = v == value and type(v) is type(value)
    return eq if op == "==" else not eq


def path_eval(segs, doc):
    cur = [doc]
    for sg in segs:
        nxt = []
        for v in cur:
            if sg[0] == "key":
                if isinstance(v, dict) and sg[1] in v:
                    nxt.append(v[sg[1]])
            elif sg[0] == "index":
                if isinstance(v, list):
                    i = sg[1] + len(v) if sg[1] < 0 else sg[1]
                    if 0 <= i < len(v):
                        nxt.append(v[i])
            elif sg[0] == "wild":
                if isinstance(v, list):
                    nxt.extend(v)
                elif isinstance(v, dict):
                    nxt.extend(v[k] for k in sorted(v))
            else:
                if isinstance(v, list):
                    nxt.extend(e for e in v if filter_match(sg, e))
                elif isinstance(v, dict) and filter_match(sg, v):
                    nxt.append(v)
        cur = nxt
    return cur


def matches_text(ms, sep):
    return sep.join(text_of(m) for m in ms if m is not None)


# Auth.

def rsa_sign_sha256(pem_text, data):
    """PKCS#1 v1.5 SHA-256 signature with an RSA key in PEM (PKCS#8 or
    PKCS#1), in pure Python: the launcher runs on the bench's stdlib only."""
    lines = [l for l in pem_text.strip().splitlines() if not l.startswith("-----")]
    der = base64.b64decode("".join(lines))

    def read(b, i):
        tag, n = b[i], b[i + 1]
        i += 2
        if n & 0x80:
            k = n & 0x7F
            n = int.from_bytes(b[i:i + k], "big")
            i += k
        return tag, b[i:i + n], i + n

    _, seq, _ = read(der, 0)
    _, _, i = read(seq, 0)
    tag, val, j = read(seq, i)
    if tag == 0x30:  # PKCS#8: the RSAPrivateKey is in the octet string
        _, octet, _ = read(seq, j)
        _, seq, _ = read(octet, 0)
    ints, i = [], 0
    while i < len(seq) and len(ints) < 4:
        _, v, i = read(seq, i)
        ints.append(int.from_bytes(v, "big"))
    n, d = ints[1], ints[3]
    k = (n.bit_length() + 7) // 8
    t = bytes.fromhex("3031300d060960864801650304020105000420") + hashlib.sha256(data).digest()
    em = b"\x00\x01" + b"\xff" * (k - 3 - len(t)) + b"\x00" + t
    return pow(int.from_bytes(em, "big"), d, n).to_bytes(k, "big")


def b64url(b):
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode("ascii")


def sigv4_escape(s):
    return urllib.parse.quote(s, safe="-_.~")


def sigv4_sign(method, host, path_query, headers, body, keys):
    """Adds the AWS Signature Version 4 headers (remotemap/auth.go SignV4)."""
    t = time.gmtime()
    amz = time.strftime("%Y%m%dT%H%M%SZ", t)
    day = amz[:8]
    headers["X-Amz-Date"] = amz
    if keys.get("session_token"):
        headers["X-Amz-Security-Token"] = keys["session_token"]
    path, _, query = path_query.partition("?")
    canon_path = "/".join(sigv4_escape(seg) for seg in (path or "/").split("/")) if keys["service"] != "s3" else (path or "/")
    pairs = sorted(sigv4_escape(k) + "=" + sigv4_escape(v) for k, v in urllib.parse.parse_qsl(query, keep_blank_values=True))
    hs = {"host": host, "x-amz-date": amz}
    for k, v in headers.items():
        if k.lower() == "content-type":
            hs["content-type"] = v
    if keys.get("session_token"):
        hs["x-amz-security-token"] = keys["session_token"]
    names = sorted(hs)
    canon_headers = "".join(n + ":" + " ".join(hs[n].split()) + "\n" for n in names)
    signed = ";".join(names)
    canonical = "\n".join([method, canon_path, "&".join(pairs), canon_headers, signed, hashlib.sha256(body or b"").hexdigest()])
    scope = "%s/%s/%s/aws4_request" % (day, keys["region"], keys["service"])
    to_sign = "AWS4-HMAC-SHA256\n%s\n%s\n%s" % (amz, scope, hashlib.sha256(canonical.encode("utf-8")).hexdigest())

    def mac(k, m):
        return hmac.new(k, m.encode("utf-8"), hashlib.sha256).digest()

    key = mac(mac(mac(mac(("AWS4" + keys["secret_key"]).encode("utf-8"), day), keys["region"]), keys["service"]), "aws4_request")
    sig = hmac.new(key, to_sign.encode("utf-8"), hashlib.sha256).hexdigest()
    headers["Authorization"] = "AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s" % (keys["access_key"], scope, signed, sig)


class Auth:
    """The auth block of one run: tokens are fetched once and again shortly
    before they expire; SigV4 signs every request."""

    def __init__(self, spec, endpoints, header):
        self.spec, self.endpoints, self.header = spec, endpoints, header or "Authorization"
        self.token, self.expires, self.fetches = "", 0.0, 0
        self.type = spec.get("type", "")

    def tls_context(self):
        if not self.spec.get("client_cert_secret"):
            return None
        import ssl
        import tempfile
        ctx = ssl.create_default_context()
        if self.spec.get("ca_cert_secret"):
            ctx = ssl.create_default_context(cadata=secret_value(self.spec["ca_cert_secret"]))
        fd, path = tempfile.mkstemp(prefix="bench-mtls-")
        try:
            with os.fdopen(fd, "w") as f:
                f.write(secret_value(self.spec["client_cert_secret"]).strip() + "\n" + secret_value(self.spec["client_key_secret"]).strip() + "\n")
            ctx.load_cert_chain(path)
        except (ssl.SSLError, OSError) as e:
            fail(2, "client certificate: %s (want PEM certificate and key secrets)" % e)
        finally:
            os.unlink(path)
        return ctx

    def fetch(self, timeout):
        url = self.spec["token_url"]
        if self.type == "gcp_id_token":
            try:
                key = json.loads(secret_value(self.spec["service_account_secret"]))
                email, pem_key = key["client_email"], key["private_key"]
            except (ValueError, KeyError, TypeError):
                fail(2, "the service account secret is not a Google service account JSON key")
            head = {"alg": "RS256", "typ": "JWT"}
            if key.get("private_key_id"):
                head["kid"] = key["private_key_id"]
            iat = int(time.time())
            claims = {"iss": email, "sub": email, "aud": url, "iat": iat, "exp": iat + 3600, "target_audience": self.spec.get("audience", "")}
            signing = b64url(json.dumps(head).encode()) + "." + b64url(json.dumps(claims).encode())
            jwt = signing + "." + b64url(rsa_sign_sha256(pem_key, signing.encode("ascii")))
            form = {"grant_type": "urn:ietf:params:oauth:grant-type:jwt-bearer", "assertion": jwt}
            field = "id_token"
        else:
            form = {"grant_type": "client_credentials", "client_id": secret_value(self.spec["client_id_secret"]),
                    "client_secret": secret_value(self.spec["client_secret_secret"])}
            for k in ("scope", "audience"):
                if self.spec.get(k):
                    form[k] = self.spec[k]
            field = "access_token"
        ep = self.endpoints.get(url)
        body = urllib.parse.urlencode(form).encode("ascii")
        resp, raw = ep.do("POST", ep.path_of(url), body, {"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"}, timeout, plain=True)
        doc = parse_json(raw) or {}
        host = urllib.parse.urlsplit(url).netloc
        if resp.status // 100 != 2:
            why = doc.get("error_description") or doc.get("error") or "" if isinstance(doc, dict) else ""
            fail(1, "token endpoint %s answered %d %s" % (host, resp.status, clean(str(why), 200)))
        tok = doc.get(field) if isinstance(doc, dict) else None
        if not isinstance(tok, str) or not tok:
            fail(1, "token endpoint %s answered without %s" % (host, field))
        try:
            ttl = float(doc.get("expires_in", 3600))
        except (TypeError, ValueError):
            ttl = 3600.0
        self.fetches += 1
        emit({"type": "auth", "token_fetches": self.fetches, "host": host})
        return tok, time.time() + ttl

    def apply(self, method, host, path_query, headers, body, timeout):
        if self.type == "aws_sigv4":
            sigv4_sign(method, host, path_query, headers, body, {
                "access_key": secret_value(self.spec["access_key_secret"]), "secret_key": secret_value(self.spec["secret_key_secret"]),
                "session_token": secret_value(self.spec["session_token_secret"]) if self.spec.get("session_token_secret") else "",
                "region": self.spec["region"], "service": self.spec["service"]})
        elif self.type in ("oauth2_client_credentials", "azure_entra", "gcp_id_token"):
            if not self.token or time.time() + TOKEN_EARLY >= self.expires:
                self.token, self.expires = self.fetch(timeout)
            if self.header.lower() == "authorization":
                headers["Authorization"] = "Bearer " + self.token
            else:
                headers[self.header] = self.token


class Endpoint(Remote):
    """A host the run may reach, through its forwarder port."""

    def __init__(self, url, connect_map, ctx=None):
        Remote.__init__(self, url)
        key = "%s:%d" % (self.url.hostname, self.port)
        if ":" in self.url.hostname:
            key = "[%s]:%d" % (self.url.hostname, self.port)
        if key in connect_map:
            host, _, p = connect_map[key].rpartition(":")
            self.addr = (host, int(p))
        elif connect_map and not os.environ.get("BENCH_REMOTE_CONNECT"):
            self.addr = (self.url.hostname, self.port)
        self.ctx = ctx

    def path_of(self, url):
        u = urllib.parse.urlsplit(url)
        return (u.path or "/") + ("?" + u.query if u.query else "")

    def open(self, method, path, body, headers, timeout, plain=False):
        if self.url.scheme == "https":
            conn = TLSConn(self.url.hostname, self.port, self.addr, timeout)
            if self.ctx is not None and not plain:
                conn._context = self.ctx
        else:
            conn = Conn(self.url.hostname, self.port, self.addr, timeout)
        h = dict(headers)
        if plain:
            h = {k: v for k, v in h.items()}
        else:
            h = dict(self.headers, **headers)
        try:
            conn.request(method, path, body=body, headers=h)
            return conn, conn.getresponse()
        except (OSError, http.client.HTTPException) as e:
            conn.close()
            fail(1, "request to %s failed: %s" % (self.url.hostname, e))

    def do(self, method, path, body, headers, timeout, plain=False):
        conn, resp = self.open(method, path, body, headers, timeout, plain)
        try:
            raw = resp.read(MAX_ANSWER_BYTES + 1)
        except (OSError, http.client.HTTPException) as e:
            fail(1, "read the answer of %s: %s" % (self.url.hostname, e))
        finally:
            conn.close()
        if len(raw) > MAX_ANSWER_BYTES:
            fail(1, "the agent's answer is larger than %d bytes" % MAX_ANSWER_BYTES)
        return resp, raw


def stream_piece(payload, segs, decode):
    payload = payload.strip()
    if not payload:
        return ""
    try:
        doc = json.loads(payload)
        is_json = True
    except ValueError:
        doc, is_json = None, False
    if not is_json:
        text = "" if segs is not None else (payload.decode("utf-8", "replace") if isinstance(payload, bytes) else payload)
    elif segs is None:
        text = doc if isinstance(doc, str) else ""
    else:
        text = matches_text(path_eval(segs, doc), "")
    if decode == "base64" and text:
        try:
            text = base64.b64decode(text, validate=True).decode("utf-8", "replace")
        except ValueError:
            fail(1, "a stream piece is not base64")
    return text


def read_sse(resp, segs, decode):
    out, data, events = [], [], 0
    done = False

    def flush():
        nonlocal events
        if not data:
            return False
        payload = "\n".join(data)
        del data[:]
        events += 1
        if events > MAX_STREAM_EVENTS:
            fail(1, "more than %d stream events" % MAX_STREAM_EVENTS)
        if payload.strip() == "[DONE]":
            return True
        out.append(stream_piece(payload, segs, decode))
        return False

    while not done:
        line = resp.readline(4 << 20)
        if not line:
            break
        line = line.decode("utf-8", "replace").rstrip("\r\n")
        if line == "":
            done = flush()
        elif line.startswith("data:"):
            v = line[5:]
            data.append(v[1:] if v.startswith(" ") else v)
    if not done:
        flush()
    return "".join(out), events


def read_exact(resp, n):
    b = b""
    while len(b) < n:
        chunk = resp.read(n - len(b))
        if not chunk:
            return b
        b += chunk
    return b


def read_eventstream(resp, segs, decode):
    import zlib
    out, events = [], 0
    while True:
        prelude = read_exact(resp, 12)
        if not prelude:
            break
        if len(prelude) < 12:
            fail(1, "the event stream ended inside a message")
        total, hlen = int.from_bytes(prelude[0:4], "big"), int.from_bytes(prelude[4:8], "big")
        if zlib.crc32(prelude[:8]) != int.from_bytes(prelude[8:12], "big"):
            fail(1, "event stream prelude checksum mismatch")
        if total < 16 or total > 16 << 20 or hlen > total - 16:
            fail(1, "event stream message is malformed")
        rest = read_exact(resp, total - 12)
        if len(rest) < total - 12:
            fail(1, "the event stream ended inside a message")
        if zlib.crc32(prelude + rest[:-4]) != int.from_bytes(rest[-4:], "big"):
            fail(1, "event stream message checksum mismatch")
        headers, hb = {}, rest[:hlen]
        i = 0
        sizes = {0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8, 9: 16}
        while i < len(hb):
            n = hb[i]
            name = hb[i + 1:i + 1 + n].decode("utf-8", "replace")
            typ = hb[i + 1 + n]
            i += 2 + n
            if typ in (6, 7):
                ln = int.from_bytes(hb[i:i + 2], "big")
                if typ == 7:
                    headers[name] = hb[i + 2:i + 2 + ln].decode("utf-8", "replace")
                i += 2 + ln
            elif typ in sizes:
                i += sizes[typ]
            else:
                fail(1, "event stream headers are malformed")
        payload = rest[hlen:-4]
        events += 1
        if events > MAX_STREAM_EVENTS:
            fail(1, "more than %d stream events" % MAX_STREAM_EVENTS)
        if headers.get(":message-type") in ("exception", "error"):
            msg = headers.get(":exception-type", "") + headers.get(":error-code", "")
            doc = parse_json(payload)
            if isinstance(doc, dict) and doc.get("message"):
                msg += ": " + str(doc["message"])
            fail(1, "the agent's stream reported %s" % clean(msg, 500))
        out.append(stream_piece(payload, segs, decode))
    return "".join(out), events


def main_mapped():
    mapping = json.loads(os.environ.get("BENCH_REMOTE_MAPPING") or "{}")
    auth_spec = json.loads(os.environ.get("BENCH_REMOTE_AUTH") or "null")
    connect_map = json.loads(os.environ.get("BENCH_REMOTE_CONNECT_MAP") or "{}")
    url_tmpl = os.environ["BENCH_REMOTE_URL"]
    timeout = float(os.environ.get("BENCH_REMOTE_TIMEOUT_S", "600"))
    deadline = time.time() + timeout
    with open(os.environ["BENCH_REMOTE_INSTRUCTION"], encoding="utf-8") as f:
        instruction = f.read()
    answer_path = os.environ.get("BENCH_ANSWER_PATH", "answer.txt")
    output = os.environ.get("BENCH_REMOTE_OUTPUT", "answer")
    files_root = os.environ.get("BENCH_REMOTE_FILES_DIR", "")
    v = Vars(instruction, output)
    base = Endpoint(url_tmpl, connect_map)
    endpoints = {}
    auth = None
    if auth_spec:
        auth = Auth(auth_spec, endpoints, os.environ.get("BENCH_REMOTE_AUTH_HEADER", ""))
        base.ctx = auth.tls_context()
        if auth_spec.get("token_url"):
            endpoints[auth_spec["token_url"]] = Endpoint(auth_spec["token_url"], connect_map)
    if auth and auth.type in ("oauth2_client_credentials", "azure_entra", "gcp_id_token"):
        base.headers.pop("Authorization", None)  # a fetched token replaces the static one
    common = mapping.get("headers") or {}

    def remaining():
        left = deadline - time.time()
        if left <= 0:
            fail(1, "the agent did not answer in time")
        return left

    def build(step, method, url, headers, query, body_tmpl, default_body):
        target = render_text(url, v, False, path_escape)
        full = urllib.parse.urljoin(urllib.parse.urlunsplit(base.url), target)
        path = base.same_origin(full)
        if path is None:
            fail(2, "the %s url is not on the agent URL's host" % step)
        if query:
            u = urllib.parse.urlsplit(path)
            q = urllib.parse.parse_qsl(u.query, keep_blank_values=True)
            q = [p for p in q if p[0] not in query] + [(k, render_text(query[k], v, True)) for k in sorted(query)]
            path = u.path + "?" + urllib.parse.urlencode(q)
        hs = {k: render_text(val, v, True) for k, val in sorted((headers or {}).items())}
        body = None
        if body_tmpl:
            body = render_body(body_tmpl, v)
        elif default_body is not None and method in ("POST", "PUT", "PATCH"):
            body = default_body
        if body is not None:
            hs.setdefault("Content-Type", "application/json")
        hs.setdefault("Accept", "application/json, text/event-stream, text/plain")
        return method, path, hs, body

    def send(step, req, sign=False, stream=False):
        method, path, hs, body = req
        if sign and body is not None:
            hs.update(mapped_sign_headers(body))
        if auth:
            host = base.url.netloc
            auth.apply(method, host, path, hs, body, max(1.0, min(30.0, remaining())))
        emit({"type": "request", "step": step, "method": method, "bytes": len(body or b"")})
        if stream:
            return base.open(method, path, body, hs, remaining())
        return base.do(method, path, body, hs, remaining())

    report = {"mapped": mapping.get("preset") or "custom"}
    started = time.time()
    st = mapping.get("start")
    if st:
        req = build("start", (st.get("method") or "POST").upper(), st["url"], dict(common, **(st.get("headers") or {})), None, st.get("body"), None)
        resp, raw = send("start", req)
        emit({"type": "response", "step": "start", "status": resp.status, "bytes": len(raw)})
        if resp.status == 409:
            emit({"type": "note", "message": "the start step answered 409: the session already exists, so it is reused"})
        elif resp.status // 100 != 2:
            fail(1, "the start step answered HTTP %d: %s" % (resp.status, clean(raw[:500].decode("utf-8", "replace"), 500)))
        elif st.get("id_path"):
            doc = parse_json(raw)
            v.start_id = matches_text(path_eval(parse_path(st["id_path"]), doc), "") if doc is not None else ""
            if not v.start_id:
                fail(1, "the start step's answer has nothing at %s" % st["id_path"])
    try:
        default = None if mapping.get("body") else json.dumps(heimdall_body(instruction, output, answer_path, files_root)).encode("utf-8")
        req = build("request", (mapping.get("method") or "POST").upper(), url_tmpl, common, mapping.get("query"), mapping.get("body"), default)
        answer = None
        if mapping.get("stream"):
            conn, resp = send("request", req, sign=True, stream=True)
            try:
                if resp.status // 100 != 2:
                    raw = resp.read(4096)
                    fail(1, "the agent answered HTTP %d: %s" % (resp.status, clean(raw.decode("utf-8", "replace"), 500)))
                segs = parse_path(mapping["stream_path"]) if mapping.get("stream_path") else None
                if mapping["stream"] == "aws_eventstream":
                    answer, events = read_eventstream(resp, segs, mapping.get("stream_decode", ""))
                else:
                    answer, events = read_sse(resp, segs, mapping.get("stream_decode", ""))
            except (OSError, http.client.HTTPException) as e:
                fail(1, "read the agent's stream: %s" % e)
            finally:
                conn.close()
            emit({"type": "response", "step": "request", "status": resp.status, "events": events,
                  "latency_ms": int((time.time() - started) * 1000)})
            if not answer:
                fail(1, "the stream carried no text at %s (%d events)" % (mapping.get("stream_path") or "its data lines", events))
            report["events_streamed"] = events
        else:
            resp, raw = send("request", req, sign=True)
            emit({"type": "response", "step": "request", "status": resp.status, "bytes": len(raw),
                  "latency_ms": int((time.time() - started) * 1000)})
            if resp.status // 100 != 2:
                fail(1, "the agent answered HTTP %d: %s" % (resp.status, clean(raw[:500].decode("utf-8", "replace"), 500)))
            pl = mapping.get("poll")
            if pl:
                answer = poll_mapped(pl, raw, v, build, send, common, remaining, report)
            elif mapping.get("answer_path"):
                doc = parse_json(raw)
                if doc is None:
                    fail(1, "the agent's answer is not JSON, so answer_path %s cannot read it" % mapping["answer_path"])
                ms = path_eval(parse_path(mapping["answer_path"]), doc)
                if not ms:
                    fail(1, "the agent's answer has nothing at %s: %s" % (mapping["answer_path"], clean(raw[:300].decode("utf-8", "replace"), 300)))
                answer = matches_text(ms, "\n")
                if isinstance(doc, dict):
                    usage = usage_of(doc.get("usage"))
                    if usage:
                        report["usage"] = usage
            else:
                answer, more = read_answer(resp, raw, output, files_root)
                report.update(more)
    finally:
        end = mapping.get("end")
        if end:
            try:
                req = build("end", (end.get("method") or "DELETE").upper(), end["url"], dict(common, **(end.get("headers") or {})), None, end.get("body"), None)
                method, path, hs, body = req
                if auth:
                    auth.apply(method, base.url.netloc, path, hs, body, 10)
                resp, _ = base.do(method, path, body, hs, 10)
                emit({"type": "response", "step": "end", "status": resp.status})
            except SystemExit:
                pass
    if auth and auth.fetches:
        report["token_fetches"] = auth.fetches
    write_answer(answer, answer_path, report)


def poll_mapped(pl, first, v, build, send, common, remaining, report):
    if pl.get("reply_id_path"):
        doc = parse_json(first)
        v.reply_id = matches_text(path_eval(parse_path(pl["reply_id_path"]), doc), "") if doc is not None else ""
        if not v.reply_id:
            fail(1, "the agent's answer has nothing at %s" % pl["reply_id_path"])
    segs = parse_path(render_text(pl["answer_path"], v))
    interval = max(int(pl.get("interval_s") or 1), 1)
    settled, polls = "", 0
    for _ in range(MAX_POLLS):
        if remaining() <= interval:
            break
        time.sleep(interval)
        resp, raw = send("poll", build("poll", "GET", pl["url"], common, None, "", None))
        polls += 1
        if resp.status // 100 != 2:
            fail(1, "the poll answered HTTP %d: %s" % (resp.status, clean(raw[:500].decode("utf-8", "replace"), 500)))
        doc = parse_json(raw)
        if doc is None:
            continue
        text = matches_text(path_eval(segs, doc), "\n")
        if not text:
            continue
        if text == settled:
            break
        settled = text
    report["polls"] = polls
    emit({"type": "poll", "polls": polls})
    if not settled:
        fail(1, "no answer at %s after %d polls" % (pl["answer_path"], polls))
    return settled


if __name__ == "__main__":
    main()
