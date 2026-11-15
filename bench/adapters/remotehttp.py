"""Launcher of a remote_http agent (adapters/remotehttp.go).

The agent is a deployed service. This program runs in the agent container,
posts the session to the service and writes the answer it returns to the
answer path. It is bench code: the container holds nothing of the agent's.

Request: POST <BENCH_REMOTE_URL> with a JSON body
  {"instruction", "session_id", "session_index", "files"?, "gateway"?}
and "Authorization: Bearer <token>" when the agent config names auth_env.
Answer: a JSON object with a string "answer", or a text/plain body.

The connection goes to BENCH_REMOTE_CONNECT (the run's egress forwarder) when
it is set; the Host header and the TLS server name stay the URL's, so TLS is
verified against the real host end to end.
"""

import http.client
import json
import os
import socket
import sys
import time
import urllib.parse

MAX_ANSWER_BYTES = 16 << 20
MAX_FILES = 100
MAX_FILES_BYTES = 256 << 10


def emit(event):
    print(json.dumps(event, sort_keys=True), flush=True)


def fail(code, message):
    emit({"type": "error", "message": message})
    print("remote_http: " + message, file=sys.stderr, flush=True)
    sys.exit(code)


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


def answer_of(raw, content_type):
    if content_type.startswith("text/plain"):
        return raw.decode("utf-8", "replace")
    try:
        doc = json.loads(raw)
    except ValueError:
        fail(1, "the agent's answer is neither JSON nor text/plain")
    if not isinstance(doc, dict) or "answer" not in doc:
        fail(1, 'the agent\'s JSON answer has no "answer" field')
    value = doc["answer"]
    return value if isinstance(value, str) else json.dumps(value)


def main():
    url = urllib.parse.urlsplit(os.environ["BENCH_REMOTE_URL"])
    if url.scheme not in ("http", "https") or not url.hostname:
        fail(2, "BENCH_REMOTE_URL is not an http(s) URL")
    port = url.port or (443 if url.scheme == "https" else 80)
    addr = (url.hostname, port)
    if os.environ.get("BENCH_REMOTE_CONNECT"):
        host, _, p = os.environ["BENCH_REMOTE_CONNECT"].rpartition(":")
        addr = (host, int(p))
    timeout = float(os.environ.get("BENCH_REMOTE_TIMEOUT_S", "600"))
    with open(os.environ["BENCH_REMOTE_INSTRUCTION"], encoding="utf-8") as f:
        instruction = f.read()
    answer_path = os.environ.get("BENCH_ANSWER_PATH", "answer.txt")
    body = {
        "instruction": instruction,
        "session_id": os.environ.get("BENCH_REMOTE_SESSION_ID", ""),
        "session_index": int(os.environ.get("BENCH_REMOTE_SESSION_INDEX", "1")),
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
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/plain"}
    token = os.environ.get("BENCH_REMOTE_TOKEN", "")
    if token:
        headers["Authorization"] = "Bearer " + token
    path = (url.path or "/") + ("?" + url.query if url.query else "")
    cls = TLSConn if url.scheme == "https" else Conn
    conn = cls(url.hostname, port, addr, timeout)
    started = time.time()
    emit({"type": "request", "session_id": body["session_id"], "session_index": body["session_index"],
          "files": len(body.get("files", {}))})
    try:
        conn.request("POST", path, body=json.dumps(body).encode("utf-8"), headers=headers)
        resp = conn.getresponse()
        raw = resp.read(MAX_ANSWER_BYTES + 1)
    except (OSError, http.client.HTTPException) as e:
        fail(1, "request to the agent failed: %s" % e)
    finally:
        conn.close()
    emit({"type": "response", "status": resp.status, "bytes": len(raw),
          "latency_ms": int((time.time() - started) * 1000)})
    if len(raw) > MAX_ANSWER_BYTES:
        fail(1, "the agent's answer is larger than %d bytes" % MAX_ANSWER_BYTES)
    if resp.status // 100 != 2:
        fail(1, "the agent answered HTTP %d: %s" % (resp.status, raw[:500].decode("utf-8", "replace")))
    answer = answer_of(raw, resp.getheader("Content-Type", "application/json").lower())
    parent = os.path.dirname(answer_path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    with open(answer_path, "w", encoding="utf-8") as f:
        f.write(answer)
    emit({"type": "result", "answer_bytes": len(answer.encode("utf-8"))})


if __name__ == "__main__":
    main()
