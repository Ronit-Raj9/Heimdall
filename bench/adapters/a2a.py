"""Launcher of an a2a agent (adapters/a2a.go, docs/a2a-agents.md).

The agent is a deployed A2A server registered in this bench's Bifrost. This
program runs in the run's container on the evaluation network, where it can
reach only Bifrost. It sends the task as one A2A v1 message to the agent's
JSON-RPC endpoint on the gateway, follows the task to a terminal state and
writes the result where the task's output kind expects it. It is bench code:
nothing of the agent runs here.

Request: POST <BENCH_A2A_ENDPOINT> with x-bf-vk (the run key),
A2A-Version: 1.0 and a JSON-RPC 2.0 body, method SendMessage (or
SendStreamingMessage with BENCH_A2A_STREAMING=1). The message has the
instruction as a text part, small workspace files as one data part, and
metadata heimdall {output, team?, tools?}.

Following the task: a non-terminal task from SendMessage is polled with
GetTask. A stream that ends before a terminal state is recovered with
GetTask, then SubscribeToTask, as Bifrost's transport guide says (Bifrost
never replays a stream). input-required and auth-required end the run: the
bench has no user to answer. A task that ends without completing (failed,
rejected, canceled, or stopped for input) is the agent's result: the
launcher writes an empty answer and exits 0. Only a failure to talk to the
gateway, a protocol error or the timeout exits 1.

Output: answer tasks get the text parts of the task's artifacts (else the
status message, else a direct message) at BENCH_ANSWER_PATH. With
BENCH_A2A_ARTIFACTS=inline, file parts are written into the workspace at
their filename (relative, no "..", no hidden segments). One a2a_report line
on stdout sums what was sent and received; the bench joins it with
Bifrost's agent log.

BENCH_A2A_FAULT (set by the a2a probe layer) perturbs the client side of the
stream: {"stream": "drop", "after_events": n} closes the stream after n
events, {"stream": "delay", "delay_ms": n} stops reading for n ms after the
first event. Recovery is then the agent's and the gateway's to show.
"""

import base64
import hashlib
import http.client
import json
import os
import socket
import sys
import time
import urllib.parse
import uuid

TERMINAL = ("TASK_STATE_COMPLETED", "TASK_STATE_FAILED", "TASK_STATE_CANCELED", "TASK_STATE_REJECTED")
STOPPED = ("TASK_STATE_INPUT_REQUIRED", "TASK_STATE_AUTH_REQUIRED")
MAX_BODY = 16 << 20
MAX_FILES = 100
MAX_FILES_BYTES = 256 << 10
MAX_ARTIFACTS = 50
MAX_ARTIFACT_BYTES = 16 << 20
MAX_STR = 300

REPORT = {"type": "a2a_report", "operations": []}
FINAL = {}


def emit(event):
    print(json.dumps(event, sort_keys=True), flush=True)


def clean(value, limit=MAX_STR):
    if not isinstance(value, str):
        return ""
    return "".join(c for c in value if c.isprintable())[:limit]


def finish(code, error=""):
    if error:
        REPORT["error"] = clean(error, 500)
        print("a2a: " + error, file=sys.stderr, flush=True)
    emit(REPORT)
    # The last stdout line is the agent's own final text, so a reader of
    # the stream's tail (the bench's final message) gets the agent's words,
    # not the report.
    final = FINAL.get("text", "")
    if final:
        print(" ".join(final.split())[:2000], flush=True)
    sys.exit(code)


class Gateway:
    """The agent's JSON-RPC endpoint on Bifrost."""

    def __init__(self, endpoint, key, timeout):
        self.url = urllib.parse.urlsplit(endpoint)
        if self.url.scheme != "http" or not self.url.hostname:
            finish(2, "BENCH_A2A_ENDPOINT is not an http URL on the evaluation network")
        self.port = self.url.port or 80
        self.headers = {"Content-Type": "application/json", "A2A-Version": "1.0", "Accept": "application/json, text/event-stream"}
        if key:
            self.headers["x-bf-vk"] = key
        self.timeout = timeout
        self.seq = 0
        self.legacy = False

    def open(self, method, params, timeout):
        self.seq += 1
        rpc_id = "heimdall-%d" % self.seq
        body = json.dumps({"jsonrpc": "2.0", "id": rpc_id, "method": method, "params": params}).encode("utf-8")
        conn = http.client.HTTPConnection(self.url.hostname, self.port, timeout=timeout)
        op = {"method": method, "rpc_id": rpc_id, "status": 0}
        REPORT["operations"].append(op)
        for attempt in range(20):
            try:
                conn.request("POST", self.url.path, body=body, headers=self.headers)
                resp = conn.getresponse()
                op["status"] = resp.status
                return conn, resp, op
            except (ConnectionRefusedError, socket.gaierror) as e:
                if attempt == 19:
                    op["error"] = clean(str(e))
                    finish(1, "the gateway is not reachable: %s" % e)
                time.sleep(0.25)
            except (OSError, http.client.HTTPException) as e:
                op["error"] = clean(str(e))
                finish(1, "%s through the gateway failed: %s" % (method, e))

    def call(self, method, params, timeout=None, soft=()):
        """One JSON-RPC call. A JSON-RPC error whose code is in soft
        returns (op, None) instead of ending the run."""
        name = method
        if self.legacy:
            method = LEGACY_METHODS.get(method, method)
        conn, resp, op = self.open(method, params, timeout or self.timeout)
        try:
            raw = resp.read(MAX_BODY + 1)
        except (OSError, http.client.HTTPException) as e:
            op["error"] = clean(str(e))
            finish(1, "%s through the gateway failed while reading: %s" % (method, e))
        finally:
            conn.close()
        if len(raw) > MAX_BODY:
            finish(1, "the %s answer is larger than %d bytes" % (method, MAX_BODY))
        if resp.status != 200:
            op["error"] = clean(raw.decode("utf-8", "replace"))
            finish(1, "the gateway answered %s with HTTP %d: %s" % (method, resp.status, clean(raw.decode("utf-8", "replace"), 500)))
        try:
            doc = json.loads(raw)
        except ValueError:
            op["error"] = "not JSON"
            finish(1, "the gateway's %s answer is not JSON" % method)
        if isinstance(doc, dict) and doc.get("error"):
            err = doc["error"] if isinstance(doc["error"], dict) else {"message": str(doc["error"])}
            op["error"] = clean("%s %s" % (err.get("code", ""), err.get("message", "")))
            if soft and err.get("code") in soft:
                return op, None
            finish(1, "%s returned a JSON-RPC error: %s" % (method, op["error"]))
        result = (doc or {}).get("result") or {}
        if self.legacy:
            # GetTask answers a task; SendMessage a task or a message.
            result = legacy_task(result) if name == "GetTask" else legacy_result(result)
        return op, result


def sse_events(resp):
    """Yields each JSON-RPC payload of a server-sent event stream."""
    data = []
    while True:
        line = resp.readline(MAX_BODY)
        if not line:
            if data:
                yield "\n".join(data)
            return
        line = line.decode("utf-8", "replace").rstrip("\r\n")
        if line == "":
            if data:
                yield "\n".join(data)
                data = []
            continue
        if line.startswith("data:"):
            data.append(line[5:].lstrip(" "))


class Task:
    """What the launcher knows about the task, from every response."""

    def __init__(self):
        self.id = ""
        self.context_id = ""
        self.state = ""
        self.status_text = ""
        self.message_text = ""
        self.artifacts = {}  # artifactId -> artifact (parts appended per the append flag)
        self.order = []

    def take_task(self, t):
        if not isinstance(t, dict):
            return
        self.id = str(t.get("id") or self.id)
        self.context_id = str(t.get("contextId") or self.context_id)
        self.take_status(t.get("status"))
        for a in t.get("artifacts") or []:
            self.take_artifact(a, append=False)

    def take_status(self, s):
        if not isinstance(s, dict):
            return
        self.state = str(s.get("state") or self.state)
        text = text_of(s.get("message"))
        if text:
            self.status_text = text

    def take_artifact(self, a, append):
        if not isinstance(a, dict):
            return
        aid = str(a.get("artifactId") or len(self.order))
        if aid not in self.artifacts:
            self.order.append(aid)
            self.artifacts[aid] = {"name": a.get("name"), "parts": []}
        if not append:
            self.artifacts[aid]["parts"] = []
        parts = self.artifacts[aid]["parts"]
        for p in a.get("parts") or []:
            if not isinstance(p, dict):
                continue
            # An appended text chunk continues the artifact's last text part.
            if append and parts and is_text(parts[-1]) and is_text(p):
                parts[-1] = dict(parts[-1], text=parts[-1]["text"] + p["text"])
            else:
                parts.append(p)

    def take_event(self, result):
        kinds = REPORT.setdefault("response_kinds", [])
        for key, kind in (("task", "task"), ("message", "message"), ("statusUpdate", "status_update"), ("artifactUpdate", "artifact_update")):
            if key in result:
                if kind not in kinds:
                    kinds.append(kind)
                if kind == "task":
                    self.take_task(result["task"])
                elif kind == "message":
                    self.message_text = text_of(result["message"]) or self.message_text
                    m = result["message"]
                    self.id = str(m.get("taskId") or self.id)
                    self.context_id = str(m.get("contextId") or self.context_id)
                elif kind == "status_update":
                    u = result["statusUpdate"]
                    self.id = str(u.get("taskId") or self.id)
                    self.context_id = str(u.get("contextId") or self.context_id)
                    self.take_status(u.get("status"))
                else:
                    u = result["artifactUpdate"]
                    self.id = str(u.get("taskId") or self.id)
                    self.take_artifact(u.get("artifact"), bool(u.get("append")))

    def answer(self):
        texts = []
        for aid in self.order:
            for p in self.artifacts[aid]["parts"]:
                if isinstance(p.get("text"), str) and not p.get("filename"):
                    texts.append(p["text"])
        if texts:
            return "\n".join(texts)
        return self.status_text or self.message_text

    def files(self):
        out = []
        for aid in self.order:
            for p in self.artifacts[aid]["parts"]:
                name = p.get("filename")
                if not name:
                    continue
                if isinstance(p.get("raw"), str):
                    try:
                        data = base64.b64decode(p["raw"], validate=True)
                    except (ValueError, TypeError):
                        continue
                elif isinstance(p.get("text"), str):
                    data = p["text"].encode("utf-8")
                else:
                    continue
                out.append((name, data))
        return out


# Y03 (G1 P0-7): agents that still speak A2A before 1.0 (protocolVersion
# 0.x: methods message/send and tasks/get, lowercase states, "kind" on every
# object). When SendMessage is refused as an unknown method, or the card
# says 0.x (BENCH_A2A_PROTOCOL), the launcher sends the legacy shape and
# reads the answer back into the 1.0 shape the rest of this file uses.
JSONRPC_METHOD_NOT_FOUND = -32601
LEGACY_METHODS = {"SendMessage": "message/send", "GetTask": "tasks/get", "CancelTask": "tasks/cancel"}
LEGACY_STATES = {"submitted": "TASK_STATE_SUBMITTED", "working": "TASK_STATE_WORKING", "completed": "TASK_STATE_COMPLETED",
                 "failed": "TASK_STATE_FAILED", "canceled": "TASK_STATE_CANCELED", "rejected": "TASK_STATE_REJECTED",
                 "input-required": "TASK_STATE_INPUT_REQUIRED", "auth-required": "TASK_STATE_AUTH_REQUIRED"}


def legacy_message(message):
    parts = []
    for p in message.get("parts") or []:
        if "text" in p:
            parts.append({"kind": "text", "text": p["text"]})
        elif "data" in p:
            parts.append({"kind": "data", "data": p["data"]})
    return {"kind": "message", "messageId": message.get("messageId"), "role": "user", "parts": parts,
            "metadata": message.get("metadata") or {}}


def legacy_part(p):
    if not isinstance(p, dict):
        return p
    if p.get("kind") == "file" and isinstance(p.get("file"), dict):
        f = p["file"]
        out = {"filename": f.get("name") or ""}
        if isinstance(f.get("bytes"), str):
            out["raw"] = f["bytes"]
        return out
    return {k: v for k, v in p.items() if k != "kind"}


def legacy_task(t):
    if not isinstance(t, dict):
        return t
    t = dict(t)
    st = t.get("status")
    if isinstance(st, dict):
        st = dict(st)
        st["state"] = LEGACY_STATES.get(str(st.get("state", "")).lower(), st.get("state"))
        if isinstance(st.get("message"), dict):
            st["message"] = dict(st["message"], parts=[legacy_part(p) for p in st["message"].get("parts") or []])
        t["status"] = st
    t["artifacts"] = [dict(a, parts=[legacy_part(p) for p in a.get("parts") or []]) for a in t.get("artifacts") or [] if isinstance(a, dict)]
    return t


def legacy_result(result):
    """A 0.x result (a Task or a Message with "kind") in the 1.0 shape."""
    if not isinstance(result, dict) or "kind" not in result:
        return result
    if result.get("kind") == "message":
        return {"message": dict(result, parts=[legacy_part(p) for p in result.get("parts") or []])}
    return {"task": legacy_task(result)}


def is_text(part):
    return isinstance(part.get("text"), str) and not part.get("filename")


def text_of(message):
    if not isinstance(message, dict):
        return ""
    return "\n".join(p["text"] for p in message.get("parts") or [] if isinstance(p, dict) and isinstance(p.get("text"), str))


def workspace_files(root, skip):
    out, total = {}, 0
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if not d.startswith("."))
        for name in sorted(filenames):
            path = os.path.join(dirpath, name)
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
            out[os.path.relpath(path, root)] = text
    return out


def safe_rel(path):
    if not isinstance(path, str) or not path or path.startswith("/") or "\\" in path or "\x00" in path:
        return None
    norm = os.path.normpath(path)
    if norm != path or norm in (".", "..") or norm.startswith("../") or any(p.startswith(".") for p in norm.split("/")):
        return None
    return norm


def write_files(files, root):
    written, total = [], 0
    for name, data in files[:MAX_ARTIFACTS]:
        rel = safe_rel(name)
        if rel is None:
            REPORT.setdefault("notes", []).append("artifact file with an unsafe name refused")
            continue
        total += len(data)
        if total > MAX_ARTIFACT_BYTES:
            REPORT.setdefault("notes", []).append("artifact files past %d bytes refused" % MAX_ARTIFACT_BYTES)
            break
        dest = os.path.join(root, rel)
        parent = os.path.dirname(dest)
        os.makedirs(parent, exist_ok=True)
        if os.path.realpath(parent) != os.path.join(os.path.realpath(root), os.path.dirname(rel)).rstrip("/") or os.path.islink(dest):
            REPORT.setdefault("notes", []).append("artifact file %s leaves the workspace; refused" % clean(rel))
            continue
        with open(dest, "wb") as f:
            f.write(data)
        written.append(rel)
    return written


def write_answer(path, text):
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


def follow_stream(gw, resp, conn, op, task, fault, deadline):
    """Reads one event stream into task. Returns terminal, early or error."""
    events = 0
    try:
        for data in sse_events(resp):
            try:
                doc = json.loads(data)
            except ValueError:
                op["error"] = "a stream event is not JSON"
                return "error"
            if isinstance(doc, dict) and doc.get("error"):
                op["error"] = clean(json.dumps(doc["error"]))
                return "error"
            task.take_event((doc or {}).get("result") or {})
            events += 1
            if not op.get("task_id") and task.id:
                op["task_id"] = task.id
            if task.state in TERMINAL or task.state in STOPPED:
                return "terminal"
            if fault.get("stream") == "drop" and events >= int(fault.get("after_events", 1) or 1):
                REPORT["fault"] = {"stream": "drop", "after_events": events, "fired": True}
                return "early"
            if fault.get("stream") == "delay" and events == 1 and not REPORT.get("fault"):
                ms = max(0, min(int(fault.get("delay_ms", 2000) or 0), 60000))
                REPORT["fault"] = {"stream": "delay", "delay_ms": ms, "fired": True}
                time.sleep(ms / 1000.0)
            if time.time() > deadline:
                return "early"
    except (OSError, http.client.HTTPException) as e:
        op["error"] = clean(str(e))
        return "error"
    finally:
        conn.close()
    return "early"


def main():
    started = time.time()
    timeout = float(os.environ.get("BENCH_A2A_TIMEOUT_S", "600"))
    deadline = started + timeout
    gw = Gateway(os.environ["BENCH_A2A_ENDPOINT"], os.environ.get("BENCH_A2A_KEY", ""), max(5.0, min(timeout, 600.0)))
    with open(os.environ["BENCH_A2A_INSTRUCTION"], encoding="utf-8") as f:
        instruction = f.read()
    output = os.environ.get("BENCH_A2A_OUTPUT", "answer")
    answer_path = os.environ.get("BENCH_ANSWER_PATH", "answer.txt")
    files_root = os.environ.get("BENCH_A2A_FILES_DIR", "")
    try:
        fault = json.loads(os.environ.get("BENCH_A2A_FAULT") or "{}")
    except ValueError:
        fault = {}
    meta = {"output": output}
    if os.environ.get("BENCH_A2A_TEAM"):
        meta["team"] = json.loads(os.environ["BENCH_A2A_TEAM"])
    if os.environ.get("BENCH_REMOTE_TOOLS"):
        gate_auth = os.environ.get("BENCH_REMOTE_TOOLS_TOKEN", "")
        meta["tools"] = {}
        for name, entry in sorted(json.loads(os.environ["BENCH_REMOTE_TOOLS"]).items()):
            entry = dict(entry)
            entry["token"] = gate_auth
            meta["tools"][name] = entry
    parts = [{"text": instruction}]
    if files_root and os.path.isdir(files_root):
        files = workspace_files(files_root, os.path.abspath(answer_path))
        if files is None:
            REPORT.setdefault("notes", []).append("workspace files left out: more than %d files or %d bytes" % (MAX_FILES, MAX_FILES_BYTES))
        elif files:
            parts.append({"data": {"files": files}, "mediaType": "application/json"})
    message_id = str(uuid.uuid4())
    REPORT["message_id"] = message_id
    message = {"messageId": message_id, "role": "ROLE_USER", "parts": parts, "metadata": {"heimdall": meta}}
    task = Task()
    streaming = os.environ.get("BENCH_A2A_STREAMING") == "1"
    if streaming:
        conn, resp, op = gw.open("SendStreamingMessage", {"message": message}, gw.timeout)
        if resp.status != 200:
            op["error"] = clean(resp.read(4096).decode("utf-8", "replace"))
            conn.close()
            finish(1, "the gateway answered SendStreamingMessage with HTTP %d: %s" % (resp.status, op["error"]))
        ctype = (resp.getheader("Content-Type") or "").lower()
        if ctype.startswith("application/json"):
            raw = resp.read(MAX_BODY)
            conn.close()
            doc = json.loads(raw or b"{}")
            if doc.get("error"):
                op["error"] = clean(json.dumps(doc["error"]))
                finish(1, "SendStreamingMessage returned a JSON-RPC error: %s" % op["error"])
            task.take_event(doc.get("result") or {})
            ended = "terminal" if task.state in TERMINAL else "early"
        else:
            ended = follow_stream(gw, resp, conn, op, task, fault, deadline)
        REPORT["stream_ended"] = ended
        if ended == "error" and not task.id:
            finish(1, "the stream failed before the task had an id: %s" % op.get("error", ""))
        if ended != "terminal":
            if not task.id:
                finish(1, "the stream ended before the agent named its task; nothing to recover")
            sop, snap = gw.call("GetTask", {"id": task.id})
            sop["task_id"] = task.id
            task.take_task(snap)
            REPORT["recovered"] = "get_task"
            if task.state not in TERMINAL and task.state not in STOPPED:
                conn, resp, sub = gw.open("SubscribeToTask", {"id": task.id}, gw.timeout)
                sub["task_id"] = task.id
                if resp.status == 200 and not (resp.getheader("Content-Type") or "").lower().startswith("application/json"):
                    follow_stream(gw, resp, conn, sub, task, {}, deadline)
                    REPORT["recovered"] = "subscribe"
                else:
                    conn.close()
    else:
        result = None
        if not os.environ.get("BENCH_A2A_PROTOCOL", "").startswith("0."):
            op, result = gw.call("SendMessage", {"message": message}, soft=(JSONRPC_METHOD_NOT_FOUND,))
        if result is None:
            gw.legacy = True
            REPORT["protocol"] = "legacy"
            op, result = gw.call("SendMessage", {"message": legacy_message(message)})
        task.take_event(result)
        op["task_id"] = task.id
    polls = 0
    while task.id and task.state and task.state not in TERMINAL and task.state not in STOPPED:
        if time.time() + 1 > deadline:
            REPORT["polls"] = polls
            REPORT["task_id"], REPORT["final_state"] = task.id, task.state
            finish(1, "the task was still %s at the timeout (%d polls)" % (task.state, polls))
        time.sleep(1.0 if polls < 30 else 3.0)
        polls += 1
        pop, snap = gw.call("GetTask", {"id": task.id})
        pop["task_id"] = task.id
        task.take_task(snap)
    if polls:
        REPORT["polls"] = polls
    REPORT["task_id"], REPORT["context_id"] = task.id, task.context_id
    REPORT["final_state"] = task.state or ("message" if task.message_text else "")
    REPORT["elapsed_ms"] = int((time.time() - started) * 1000)
    answer = task.answer()
    FINAL["text"] = task.status_text or answer or task.message_text
    REPORT["answer_sha256"] = hashlib.sha256(answer.encode("utf-8")).hexdigest() if answer else ""
    # A task that ended without completing is the agent's result, not a
    # crash: the run finished, with no answer (an empty answer file, so the
    # grade fails on the answer and not on a missing file).
    if task.state in STOPPED or (task.state and task.state != "TASK_STATE_COMPLETED"):
        if output == "answer":
            write_answer(answer_path, "")
        if task.state in STOPPED:
            finish(0, "the agent stopped in %s; the bench has no user to answer it" % task.state)
        finish(0, "the agent's task ended %s: %s" % (task.state, clean(task.status_text, 300)))
    if os.environ.get("BENCH_A2A_ARTIFACTS") == "inline" and files_root:
        written = write_files(task.files(), files_root)
        if written:
            REPORT["artifacts"] = written
    if output == "answer" and not answer:
        write_answer(answer_path, "")
        finish(0, "the agent's task completed with no text to answer with")
    if answer:
        write_answer(answer_path, answer)
    emit({"type": "result", "answer_bytes": len(answer.encode("utf-8"))})
    finish(0)


if __name__ == "__main__":
    main()
