"""Shared launcher helpers (adapters/conversation.go): the bench-driven
conversation and repo in, patch out. Bench code, Python standard library
only; the launchers import it from /tmp/bench.

Conversation (BENCH_CONV_USERS set): the bench, not the agent, talks to the
simulated user. The driver sends the agent's greeting (BENCH_CONV_GREETING)
to the user's respond_to_user tool, gives the user's message to the agent as
a chat turn (agent_turn), sends the agent's reply back to the user, and
repeats until the user's reply carries ###STOP### or the agent has replied
BENCH_CONV_MAX_TURNS times. The simulated user is a task service: it writes
the transcript (transcript.json) itself, in its own container, and the
verifier grades that, so the transcript never passes through the agent. Each
tools/call carries _meta.heimdall {driver: bench}, which the simulator
records. Every turn is also printed as a conversation_turn event.

Repo in, patch out: workspace_tarball packs the task's workspace (the base
tree, never the hidden tests, which reach only the grader) as tar.gz;
apply_patch checks a unified diff (size, paths) and applies it to the
workspace with git apply, so the bench exports and grades the change like
a local agent's.
"""

import io
import json
import os
import subprocess
import tarfile
import time
import urllib.request

STOP = "###STOP###"
RESPOND_TOOL = "respond_to_user"
MAX_BODY = 16 << 20
MAX_REPO_BYTES = 48 << 20
MAX_PATCH_BYTES = 8 << 20
MAX_PATCH_FILES = 2000
CONV_EVENT = "conversation_turn"


class ConversationError(Exception):
    """The conversation could not go on (the simulated user failed)."""


class MCP:
    """One streamable HTTP MCP session on the run's tool proxy."""

    def __init__(self, url, headers=None, client="heimdall-conversation"):
        self.url, self.headers, self.session, self.next_id, self.client = url, dict(headers or {}), None, 0, client

    def rpc(self, method, params=None, notify=False, timeout=180):
        msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
        if not notify:
            self.next_id += 1
            msg["id"] = self.next_id
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        headers.update(self.headers)
        if self.session:
            headers["Mcp-Session-Id"] = self.session
        req = urllib.request.Request(self.url, data=json.dumps(msg).encode(), headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            if resp.headers.get("Mcp-Session-Id"):
                self.session = resp.headers["Mcp-Session-Id"]
            body = resp.read(MAX_BODY).decode("utf-8", errors="replace")
        if notify:
            return None
        if "data:" in body:
            body = [line[5:] for line in body.splitlines() if line.startswith("data:")][-1]
        answer = json.loads(body)
        if "error" in answer:
            raise RuntimeError("%s: %s" % (method, json.dumps(answer["error"])[:300]))
        return answer.get("result") or {}

    def open(self):
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                "clientInfo": {"name": self.client, "version": "1"}})
        self.rpc("notifications/initialized", notify=True)
        return self.rpc("tools/list").get("tools", [])


def result_text(res):
    parts = []
    for c in res.get("content") or []:
        if isinstance(c, dict) and c.get("type") == "text":
            parts.append(str(c.get("text", "")))
        elif isinstance(c, dict):
            parts.append(json.dumps(c))
    if not parts and res.get("structuredContent") is not None:
        parts.append(json.dumps(res["structuredContent"]))
    return "\n".join(parts)


def servers_of(raw):
    """(id, url, headers) of a BENCH_MCP_SERVERS-shaped JSON object."""
    try:
        doc = json.loads(raw or "{}")
    except ValueError:
        return []
    out = []
    for sid in sorted(doc if isinstance(doc, dict) else {}):
        spec = doc[sid] if isinstance(doc[sid], dict) else {"url": doc[sid]}
        if spec.get("url"):
            out.append((sid, spec["url"], spec.get("headers")))
    return out


class User:
    """The simulated user, reached through the run's tool proxy."""

    def __init__(self, raw):
        self.client, self.server = None, ""
        problems = []
        for sid, url, headers in servers_of(raw):
            client = MCP(url, headers)
            try:
                names = [t.get("name") for t in client.open() if isinstance(t, dict)]
            except Exception as e:  # noqa: BLE001 - reported below
                problems.append("%s: %s" % (sid, str(e)[:200]))
                continue
            if RESPOND_TOOL in names:
                self.client, self.server = client, sid
                break
        if self.client is None:
            raise ConversationError("no simulated user with a respond_to_user tool answered (%s)" % ("; ".join(problems) or "none listed it"))

    def say(self, message, greeting=False):
        """Sends the agent's message; returns the user's reply."""
        meta = {"heimdall": {"driver": "bench"}}
        if greeting:
            meta["heimdall"]["greeting"] = True
        try:
            res = self.client.rpc("tools/call", {"name": RESPOND_TOOL, "arguments": {"message": message}, "_meta": meta})
        except Exception as e:  # noqa: BLE001
            raise ConversationError("the simulated user did not answer: %s" % str(e)[:300]) from None
        text = result_text(res)
        if res.get("isError"):
            # One retry, as an agent would: the simulator's model call can fail once.
            try:
                res = self.client.rpc("tools/call", {"name": RESPOND_TOOL, "arguments": {"message": message}, "_meta": meta})
            except Exception as e:  # noqa: BLE001
                raise ConversationError("the simulated user did not answer: %s" % str(e)[:300]) from None
            text = result_text(res)
            if res.get("isError"):
                raise ConversationError("the simulated user failed: %s" % text[:300])
        return text


def active():
    return bool(os.environ.get("BENCH_CONV_USERS"))


def settings():
    return {
        "max_turns": max(1, int(os.environ.get("BENCH_CONV_MAX_TURNS", "12") or "12")),
        "history": os.environ.get("BENCH_CONV_HISTORY", "client") or "client",
        "greeting": os.environ.get("BENCH_CONV_GREETING", "") or "Hi! How can I help you today?",
    }


def drive(agent_turn, emit, deadline):
    """Runs the conversation. agent_turn(user_text, messages, turn) returns the
    agent's reply text; messages is the chat so far, OpenAI-style roles
    (assistant is the agent, user is the simulated user), ending with the new
    user message. Returns a summary dict."""
    cfg = settings()
    user = User(os.environ["BENCH_CONV_USERS"])
    messages = [{"role": "assistant", "content": cfg["greeting"]}]
    started = time.time()
    reply = user.say(cfg["greeting"], greeting=True)
    messages.append({"role": "user", "content": reply})
    emit({"type": CONV_EVENT, "turn": 0, "role": "agent", "origin": "bench_greeting", "chars": len(cfg["greeting"])})
    emit({"type": CONV_EVENT, "turn": 0, "role": "user", "chars": len(reply), "stop": STOP in reply})
    turns, ended = 0, "max_turns"
    last = ""
    while True:
        if STOP in reply:
            ended = "user_stop"
            break
        if turns >= cfg["max_turns"]:
            break
        if time.time() >= deadline:
            ended = "timeout"
            break
        turns += 1
        t0 = time.time()
        last = agent_turn(reply.replace(STOP, "").strip(), list(messages), turns)
        if not isinstance(last, str):
            last = "" if last is None else str(last)
        messages.append({"role": "assistant", "content": last})
        emit({"type": CONV_EVENT, "turn": turns, "role": "agent", "chars": len(last), "latency_ms": int((time.time() - t0) * 1000)})
        if not last.strip():
            ended = "agent_silent"
            break
        reply = user.say(last)
        messages.append({"role": "user", "content": reply})
        emit({"type": CONV_EVENT, "turn": turns, "role": "user", "chars": len(reply), "stop": STOP in reply})
    summary = {"type": "conversation", "driver": "bench", "user_server": user.server, "agent_turns": turns,
               "ended_by": ended, "elapsed_ms": int((time.time() - started) * 1000), "history": cfg["history"]}
    emit(summary)
    return summary, last, messages


# ---- repo in, patch out ----------------------------------------------------

def workspace_tarball(root, skip=()):
    """tar.gz of root without .git and the paths in skip; (bytes, files)."""
    buf = io.BytesIO()
    skip = {os.path.abspath(p) for p in skip if p}
    files = 0
    with tarfile.open(fileobj=buf, mode="w:gz", compresslevel=6) as tar:
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = sorted(d for d in dirnames if d != ".git")
            for name in sorted(filenames):
                path = os.path.join(dirpath, name)
                if os.path.abspath(path) in skip:
                    continue
                rel = os.path.relpath(path, root)
                try:
                    tar.add(path, arcname=rel, recursive=False)
                except OSError:
                    continue
                files += 1
                if buf.tell() > MAX_REPO_BYTES:
                    raise ValueError("the workspace is larger than %d bytes compressed" % MAX_REPO_BYTES)
    data = buf.getvalue()
    if len(data) > MAX_REPO_BYTES:
        raise ValueError("the workspace is larger than %d bytes compressed" % MAX_REPO_BYTES)
    return data, files


def _patch_path(raw):
    raw = raw.strip()
    if "\t" in raw:
        raw = raw.split("\t", 1)[0]
    if raw.startswith('"') and raw.endswith('"'):
        raw = raw[1:-1]
    if raw == "/dev/null":
        return None
    for prefix in ("a/", "b/"):
        if raw.startswith(prefix):
            raw = raw[len(prefix):]
            break
    return raw


def _unsafe(path):
    if not path or path.startswith("/") or "\\" in path or "\x00" in path or path.startswith("~"):
        return True
    parts = path.split("/")
    return any(p in ("", ".", "..") for p in parts) or parts[0] == ".git" or ".git" in parts


def check_patch(text):
    """The files a unified diff touches; raises ValueError on an unsafe or
    oversized patch (absolute paths, '..', .git, symlinks)."""
    if not isinstance(text, str):
        raise ValueError("the patch is not a string")
    if len(text.encode("utf-8")) > MAX_PATCH_BYTES:
        raise ValueError("the patch is larger than %d bytes" % MAX_PATCH_BYTES)
    touched = []
    for line in text.splitlines():
        path = None
        if line.startswith("diff --git "):
            continue
        if line.startswith("--- ") or line.startswith("+++ "):
            path = _patch_path(line[4:])
        elif line.startswith(("rename from ", "rename to ", "copy from ", "copy to ")):
            path = line.split(" ", 2)[2]
        elif line.startswith(("new file mode 120000", "new mode 120000", "old mode 120000", "deleted file mode 120000")):
            raise ValueError("the patch creates or changes a symbolic link; refused")
        elif line.startswith(("new file mode 160000", "new mode 160000")):
            raise ValueError("the patch adds a submodule; refused")
        if path is None:
            continue
        if _unsafe(path):
            raise ValueError("the patch touches an unsafe path %r; refused" % path[:200])
        if path not in touched:
            touched.append(path)
        if len(touched) > MAX_PATCH_FILES:
            raise ValueError("the patch touches more than %d files" % MAX_PATCH_FILES)
    if text.strip() and not touched:
        raise ValueError("the patch is not a unified diff (no ---/+++ file lines)")
    return touched


def apply_patch(root, text):
    """Checks and applies a unified diff to root. Returns the touched files.
    Raises ValueError when it is unsafe or does not apply."""
    touched = check_patch(text)
    if not text.strip():
        return []
    if not text.endswith("\n"):
        text += "\n"
    data = text.encode("utf-8")
    for argv in (["git", "apply", "--whitespace=nowarn", "-p1", "-"],
                 ["git", "apply", "--whitespace=nowarn", "-p0", "-"]):
        try:
            check = subprocess.run(argv[:2] + ["--check"] + argv[2:], input=data, cwd=root, capture_output=True, timeout=120)
        except FileNotFoundError:
            break
        if check.returncode == 0:
            done = subprocess.run(argv, input=data, cwd=root, capture_output=True, timeout=120)
            if done.returncode == 0:
                return touched
            raise ValueError("git apply failed: %s" % done.stderr.decode("utf-8", "replace")[:500])
        last = check.stderr.decode("utf-8", "replace")[:500]
    else:
        raise ValueError("the patch does not apply to the task's repository: %s" % last)
    # No git in the image: patch(1), refusing anything outside root.
    try:
        done = subprocess.run(["patch", "-p1", "--forward", "--batch", "--no-backup-if-mismatch", "-d", root],
                              input=data, capture_output=True, timeout=120)
    except FileNotFoundError:
        raise ValueError("neither git nor patch is installed in the task image; the patch cannot be applied") from None
    if done.returncode != 0:
        raise ValueError("the patch does not apply to the task's repository: %s" % done.stdout.decode("utf-8", "replace")[:500])
    return touched


# ---- the Heimdall server (pull agents, repo tarballs) -----------------------

class Server:
    """Heimdall's pull endpoint as the run reaches it: BENCH_PULL_URL, through
    the forwarder at connect (host:port) when set, with the run's pull
    token. The Host header and TLS server name stay the URL's."""

    def __init__(self, url, token, connect=""):
        import http.client
        import socket
        import urllib.parse
        self._http, self._socket = http.client, socket
        self.url = urllib.parse.urlsplit(url.rstrip("/"))
        if self.url.scheme not in ("http", "https") or not self.url.hostname:
            raise ValueError("BENCH_PULL_URL is not an http(s) URL")
        self.port = self.url.port or (443 if self.url.scheme == "https" else 80)
        self.addr = (self.url.hostname, self.port)
        if connect:
            host, _, p = connect.rpartition(":")
            self.addr = (host, int(p))
        self.token = token

    def _conn(self, timeout):
        http, socket, addr = self._http, self._socket, self.addr

        def dial():
            last = None
            for _ in range(20):
                try:
                    return socket.create_connection(addr, timeout)
                except (ConnectionRefusedError, socket.gaierror) as e:
                    last = e
                    time.sleep(0.25)
            raise last

        if self.url.scheme == "https":
            class C(http.HTTPSConnection):
                def connect(self):
                    self.sock = self._context.wrap_socket(dial(), server_hostname=self.host)
        else:
            class C(http.HTTPConnection):
                def connect(self):
                    self.sock = dial()
        return C(self.url.hostname, self.port, timeout=timeout)

    def request(self, method, path, body=None, ctype="application/json", timeout=60):
        """(status, parsed JSON or {}) of one request; raises OSError on a
        transport failure."""
        conn = self._conn(timeout)
        headers = {"Authorization": "Bearer " + self.token, "Accept": "application/json"}
        if body is not None:
            if not isinstance(body, (bytes, bytearray)):
                body = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = ctype
        try:
            conn.request(method, (self.url.path or "") + path, body=body, headers=headers)
            resp = conn.getresponse()
            raw = resp.read(MAX_BODY + 1)
        except self._http.HTTPException as e:
            raise OSError(str(e)) from None
        finally:
            conn.close()
        try:
            doc = json.loads(raw) if raw.strip() else {}
        except ValueError:
            doc = {"error": raw[:300].decode("utf-8", "replace")}
        return resp.status, doc if isinstance(doc, dict) else {}

    def publish_repo(self, data):
        """Uploads a workspace tarball; returns {url, expires_at, sha256, bytes}."""
        status, doc = self.request("POST", "/repos", body=data, ctype="application/gzip", timeout=300)
        if status // 100 != 2 or not doc.get("url"):
            raise ValueError("the server refused the repository tarball (HTTP %d): %s" % (status, error_of(doc)))
        return doc


def error_of(doc):
    err = doc.get("error") if isinstance(doc, dict) else None
    if isinstance(err, dict):
        err = err.get("message") or json.dumps(err)
    return str(err or doc)[:500]
