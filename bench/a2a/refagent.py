#!/usr/bin/env python3
"""Reference A2A agents for $0 proofs (B1, docs/a2a-agents.md).

Part of Heimdall (bench/a2a). Standard library only. One process serves
several scripted agents ("personas"), each under its own path prefix, and
speaks the A2A v1 JSON-RPC binding the way Bifrost's agent gateway (a2a-go
v2) calls an upstream agent:

  GET  /<persona>/.well-known/agent-card.json   the persona's agent card
  POST /<persona>/json-rpc                      SendMessage, SendStreamingMessage,
                                                GetTask, SubscribeToTask,
                                                CancelTask, ListTasks
  GET  /_log                                    what this process received (tests)
  POST /_reset                                  clears the log and the task store

Nothing here is a model. A persona answers what its script says. The script
is a JSON file:

  {"model": "claude-sonnet-4-5",
   "personas": {
     "oracle": {"mode": "scripted", "model_call": true,
                "answers": [{"match": "refund window", "answer": "30 days",
                             "artifacts": [{"name": "report.csv", "text": "a,b\\n"}]}]},
     "noop":   {"mode": "noop"},
     "broken": {"mode": "broken", "broken": "failed_task"},
     "lead":   {"mode": "delegate", "workers": ["researcher", "writer"]}}}

Modes:
  scripted  the first answer whose "match" occurs in the message text (case
            folded) completes the task with that answer as a text artifact
            and the status message; no match fails the task. An answer's
            "tool_calls" ([{tool, method, path, body}]) are made first,
            through the bench's published tool gate (heimdall.tools in the
            message metadata, with its bearer token); a call that fails
            fails the task. "delay_s" keeps
            the task working that long first (the caller polls GetTask).
            "drop_stream": true ends a SendStreamingMessage stream after the
            working update, without a terminal event, while the task still
            completes (the caller must recover with GetTask or
            SubscribeToTask). "dead_first": true advertises a first interface
            that answers 404, so the gateway must fail over to the second.
  noop      completes every task with a polite non-answer and no artifact.
  broken    "broken": "failed_task" (default) fails every task;
            "jsonrpc_error" answers a JSON-RPC internal error;
            "http_500" answers HTTP 500; "garbage" answers a non-JSON body.
  delegate  sends the message text to each worker named in "workers" through
            the gateway (the endpoints come from the message metadata
            heimdall.team, which the bench's launcher sets), then completes
            with the workers' answers joined by "join" (default "; ").

With "model_call": true a scripted or delegate persona makes one chat call
through Bifrost before it answers, with the credential the gateway forwarded
(x-bf-vk) and the correlation headers Bifrost documents (x-bf-session-id: the
A2A context id, x-bf-agent-correlation-id: the A2A task id), so Bifrost's LLM
log rows link to the task. The model's reply is ignored. The gateway URL is
REF_GATEWAY_URL (default http://bifrost:8080).

Calls a persona makes to other agents or to the model carry the credential it
received, never one of its own. Received credentials are never logged; the
log keeps the sha256 prefix of each, so a test can tell which key came in.
"""

import argparse
import hashlib
import http.client
import json
import os
import sys
import threading
import time
import urllib.parse
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TERMINAL = ("TASK_STATE_COMPLETED", "TASK_STATE_FAILED", "TASK_STATE_CANCELED", "TASK_STATE_REJECTED")
NON_ANSWER = "I am not able to help with that request."
MAX_BODY = 4 << 20
CRED_HEADERS = ("x-bf-vk", "authorization")

STATE = {"script": {}, "public": "", "tasks": {}, "log": [], "lock": threading.Lock()}


def now():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def fingerprint(value):
    return hashlib.sha256(value.encode("utf-8")).hexdigest()[:12] if value else ""


def text_of(message):
    out = []
    for p in (message or {}).get("parts") or []:
        if isinstance(p, dict) and isinstance(p.get("text"), str):
            out.append(p["text"])
    return "\n".join(out)


def agent_message(text, task_id, context_id):
    return {"messageId": str(uuid.uuid4()), "role": "ROLE_AGENT", "parts": [{"text": text}],
            "taskId": task_id, "contextId": context_id}


def card(persona, spec):
    base = STATE["public"].rstrip("/") + "/" + persona
    interfaces = [{"url": base + "/json-rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}]
    if spec.get("dead_first"):
        interfaces.insert(0, {"url": base + "/gone/json-rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"})
    return {
        "name": persona,
        "description": "Heimdall reference A2A agent, mode %s: scripted answers, no model decides anything" % spec.get("mode", "scripted"),
        "version": "1.0.0",
        "supportedInterfaces": interfaces,
        "capabilities": {"streaming": True, "pushNotifications": False},
        "defaultInputModes": ["text/plain"],
        "defaultOutputModes": ["text/plain"],
        "skills": [{"id": "answer", "name": "answer", "description": "Answers scripted questions.", "tags": ["heimdall", "reference"]}],
    }


class Task:
    def __init__(self, persona, message):
        self.id = str(uuid.uuid4())
        self.context_id = message.get("contextId") or str(uuid.uuid4())
        self.persona = persona
        self.state = "TASK_STATE_SUBMITTED"
        self.status_message = None
        self.artifacts = []
        self.history = [dict(message, taskId=self.id, contextId=self.context_id)]
        self.done = threading.Event()

    def doc(self):
        status = {"state": self.state, "timestamp": now()}
        if self.status_message:
            status["message"] = self.status_message
        d = {"id": self.id, "contextId": self.context_id, "status": status, "history": self.history}
        if self.artifacts:
            d["artifacts"] = self.artifacts
        return d

    def finish(self, state, text, artifacts=None):
        self.artifacts = artifacts or []
        self.status_message = agent_message(text, self.id, self.context_id) if text else None
        self.state = state
        self.done.set()


def post_json(url, body, headers, timeout=60):
    u = urllib.parse.urlsplit(url)
    conn = http.client.HTTPConnection(u.hostname, u.port or 80, timeout=timeout)
    try:
        conn.request("POST", u.path + ("?" + u.query if u.query else ""), body=json.dumps(body).encode("utf-8"),
                     headers=dict({"Content-Type": "application/json"}, **headers))
        resp = conn.getresponse()
        raw = resp.read(MAX_BODY)
        return resp.status, raw
    finally:
        conn.close()


def model_call(task, creds, text):
    """One chat call through the gateway with the forwarded credential and
    the correlation headers. Returns the HTTP status, or 0 when it failed."""
    gw = os.environ.get("REF_GATEWAY_URL", "http://bifrost:8080").rstrip("/")
    model = STATE["script"].get("model", "claude-sonnet-4-5")
    headers = {"x-bf-session-id": task.context_id, "x-bf-agent-correlation-id": task.id}
    headers.update(creds)
    body = {"model": model, "max_tokens": 64, "messages": [{"role": "user", "content": text[:2000]}]}
    if "/" not in model:
        body["model"] = STATE["script"].get("provider", "anthropic") + "/" + model
    try:
        status, _ = post_json(gw + "/v1/chat/completions", body, headers)
    except OSError:
        status = 0
    record({"kind": "model_call", "persona": task.persona, "task_id": task.id, "status": status})
    return status


def delegate(task, spec, creds, text, team):
    answers = []
    for worker in spec.get("workers") or []:
        endpoint = (team or {}).get(worker)
        if not endpoint:
            return None, "worker %s has no endpoint in heimdall.team" % worker
        headers = {"A2A-Version": "1.0", "x-bf-agent-correlation-id": task.id, "x-bf-session-id": task.context_id}
        headers.update(creds)
        body = {"jsonrpc": "2.0", "id": "d-" + worker, "method": "SendMessage",
                "params": {"message": {"messageId": str(uuid.uuid4()), "role": "ROLE_USER", "parts": [{"text": text}]}}}
        try:
            status, raw = post_json(endpoint, body, headers)
            doc = json.loads(raw)
        except (OSError, ValueError) as e:
            return None, "worker %s: %s" % (worker, e)
        record({"kind": "delegate", "persona": task.persona, "worker": worker, "status": status})
        sub = ((doc or {}).get("result") or {}).get("task") or {}
        if sub.get("status", {}).get("state") != "TASK_STATE_COMPLETED":
            return None, "worker %s did not complete: %s" % (worker, json.dumps(doc)[:300])
        parts = []
        for a in sub.get("artifacts") or []:
            parts.append(text_of(a))
        answers.append(" ".join(p for p in parts if p) or text_of(sub.get("status", {}).get("message")))
    return spec.get("join", "; ").join(answers), ""


def call_tools(task, calls, tools):
    """Makes the scripted tool calls through the bench's published tool gate
    (the URLs and bearer token in the message metadata heimdall.tools).
    Returns why it failed, or ""."""
    for c in calls:
        entry = (tools or {}).get(c.get("tool", ""))
        if not entry:
            return "tool %s is not in heimdall.tools" % c.get("tool")
        url = entry.get("url", "").rstrip("/") + c.get("path", "")
        u = urllib.parse.urlsplit(url)
        conn = http.client.HTTPConnection(u.hostname, u.port or 80, timeout=30)
        try:
            body = json.dumps(c["body"]).encode("utf-8") if "body" in c else None
            conn.request(c.get("method", "GET"), u.path + ("?" + u.query if u.query else ""), body=body,
                         headers={"Content-Type": "application/json", "Authorization": "Bearer " + entry.get("token", "")})
            resp = conn.getresponse()
            resp.read(MAX_BODY)
            status = resp.status
        except OSError as e:
            return "tool %s: %s" % (c.get("tool"), e)
        finally:
            conn.close()
        record({"kind": "tool_call", "persona": task.persona, "tool": c.get("tool"), "method": c.get("method", "GET"), "path": c.get("path", ""), "status": status})
        if status // 100 != 2:
            return "tool %s answered HTTP %d" % (c.get("tool"), status)
    return ""


def work(task, spec, creds, text, team, tools=None):
    """Runs a task to its terminal state."""
    mode = spec.get("mode", "scripted")
    task.state = "TASK_STATE_WORKING"
    if mode == "noop":
        task.finish("TASK_STATE_COMPLETED", NON_ANSWER)
        return
    if mode == "broken":
        task.finish("TASK_STATE_FAILED", "internal error: the reference agent is broken on purpose")
        return
    if spec.get("model_call"):
        model_call(task, creds, text)
    if mode == "delegate":
        answer, why = delegate(task, spec, creds, text, team)
        if answer is None:
            task.finish("TASK_STATE_FAILED", why)
            return
        task.finish("TASK_STATE_COMPLETED", answer, [{"artifactId": "answer", "name": "answer", "parts": [{"text": answer}]}])
        return
    folded = text.casefold()
    for entry in spec.get("answers") or []:
        if str(entry.get("match", "")).casefold() in folded:
            delay = float(entry.get("delay_s", spec.get("delay_s", 0)) or 0)
            if delay > 0:
                time.sleep(delay)
            why = call_tools(task, entry.get("tool_calls") or [], tools)
            if why:
                task.finish("TASK_STATE_FAILED", why)
                return
            arts = [{"artifactId": "answer", "name": "answer", "parts": [{"text": entry.get("answer", "")}]}]
            for i, a in enumerate(entry.get("artifacts") or []):
                arts.append({"artifactId": "file-%d" % (i + 1), "name": a.get("name", "file"),
                             "parts": [{"text": a.get("text", ""), "filename": a.get("name", "file"), "mediaType": a.get("media_type", "text/plain")}]})
            task.finish("TASK_STATE_COMPLETED", entry.get("answer", ""), arts)
            return
    task.finish("TASK_STATE_FAILED", "no scripted answer matches this message")


def record(entry):
    with STATE["lock"]:
        entry["at"] = now()
        STATE["log"].append(entry)
        del STATE["log"][:-500]


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "heimdall-refagent/1"

    def log_message(self, fmt, *args):
        sys.stderr.write("refagent: " + (fmt % args) + "\n")

    def send(self, status, doc, ctype="application/json"):
        body = doc if isinstance(doc, bytes) else json.dumps(doc).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def persona(self):
        parts = urllib.parse.urlsplit(self.path).path.strip("/").split("/")
        name = parts[0] if parts else ""
        spec = (STATE["script"].get("personas") or {}).get(name)
        return name, spec, "/" + "/".join(parts[1:])

    def do_GET(self):
        path = urllib.parse.urlsplit(self.path).path
        if path == "/_log":
            with STATE["lock"]:
                return self.send(200, {"log": list(STATE["log"])})
        if path == "/healthz":
            return self.send(200, {"ok": True})
        name, spec, rest = self.persona()
        if spec is None:
            return self.send(404, {"error": "no such persona"})
        if rest == "/.well-known/agent-card.json":
            record({"kind": "card", "persona": name})
            return self.send(200, card(name, spec))
        return self.send(404, {"error": "not found"})

    def do_POST(self):
        path = urllib.parse.urlsplit(self.path).path
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(min(length, MAX_BODY)) if length else b""
        if path == "/_reset":
            with STATE["lock"]:
                STATE["log"].clear()
                STATE["tasks"].clear()
            return self.send(200, {"ok": True})
        name, spec, rest = self.persona()
        if spec is None:
            return self.send(404, {"error": "no such persona"})
        if rest == "/gone/json-rpc":
            record({"kind": "gone", "persona": name})
            return self.send(404, {"error": "this interface is gone"})
        if rest != "/json-rpc":
            return self.send(404, {"error": "not found"})
        try:
            req = json.loads(raw)
        except ValueError:
            return self.send(200, {"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "parse error"}})
        creds = {h: self.headers.get(h) for h in CRED_HEADERS if self.headers.get(h)}
        method, rid, params = req.get("method"), req.get("id"), req.get("params") or {}
        record({"kind": "rpc", "persona": name, "method": method,
                "credential": {h: fingerprint(v) for h, v in creds.items()},
                "agent_correlation_id": self.headers.get("x-bf-agent-correlation-id", ""),
                "a2a_version": self.headers.get("A2A-Version", "")})
        creds.pop("authorization", None)
        if spec.get("mode") == "broken" and spec.get("broken") in ("jsonrpc_error", "http_500", "garbage") and method in ("SendMessage", "SendStreamingMessage"):
            kind = spec["broken"]
            if kind == "http_500":
                return self.send(500, {"error": "broken on purpose"})
            if kind == "garbage":
                return self.send(200, b"this is not json", "text/plain")
            return self.send(200, {"jsonrpc": "2.0", "id": rid, "error": {"code": -32603, "message": "internal error (broken on purpose)"}})
        if method in ("SendMessage", "SendStreamingMessage"):
            return self.send_message(name, spec, rid, params, creds, method == "SendStreamingMessage")
        if method in ("GetTask", "CancelTask", "SubscribeToTask"):
            with STATE["lock"]:
                task = STATE["tasks"].get(str(params.get("id", "")))
            if task is None:
                return self.send(200, {"jsonrpc": "2.0", "id": rid, "error": {"code": -32001, "message": "task not found"}})
            if method == "CancelTask" and task.state not in TERMINAL:
                task.finish("TASK_STATE_CANCELED", "canceled by the caller")
            if method == "SubscribeToTask":
                return self.stream(rid, task, drop=False, first=False)
            return self.send(200, {"jsonrpc": "2.0", "id": rid, "result": task.doc()})
        if method == "ListTasks":
            with STATE["lock"]:
                tasks = [t.doc() for t in STATE["tasks"].values() if t.persona == name]
            return self.send(200, {"jsonrpc": "2.0", "id": rid, "result": {"tasks": tasks, "totalSize": len(tasks), "pageSize": len(tasks), "nextPageToken": ""}})
        return self.send(200, {"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "method not found"}})

    def send_message(self, name, spec, rid, params, creds, streaming):
        message = params.get("message") or {}
        task = Task(name, message)
        with STATE["lock"]:
            STATE["tasks"][task.id] = task
        text = text_of(message)
        meta = (message.get("metadata") or {}).get("heimdall") or (params.get("metadata") or {}).get("heimdall") or {}
        worker = threading.Thread(target=work, args=(task, spec, creds, text, meta.get("team"), meta.get("tools")), daemon=True)
        worker.start()
        if streaming:
            return self.stream(rid, task, drop=bool(spec.get("drop_stream")), first=True)
        blocking = not ((params.get("configuration") or {}).get("returnImmediately"))
        if blocking:
            task.done.wait(min(float(spec.get("block_s", 2.0)), 30.0))
        return self.send(200, {"jsonrpc": "2.0", "id": rid, "result": {"task": task.doc()}})

    def stream(self, rid, task, drop, first):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        self.close_connection = True

        def event(result):
            self.wfile.write(("data: " + json.dumps({"jsonrpc": "2.0", "id": rid, "result": result}) + "\n\n").encode("utf-8"))
            self.wfile.flush()

        try:
            event({"task": task.doc()})
            if first:
                event({"statusUpdate": {"taskId": task.id, "contextId": task.context_id, "status": {"state": "TASK_STATE_WORKING", "timestamp": now()}}})
            if drop:
                record({"kind": "stream_dropped", "persona": task.persona, "task_id": task.id})
                return
            task.done.wait(60)
            for a in task.artifacts:
                event({"artifactUpdate": {"taskId": task.id, "contextId": task.context_id, "artifact": a, "lastChunk": True}})
            status = {"state": task.state, "timestamp": now()}
            if task.status_message:
                status["message"] = task.status_message
            event({"statusUpdate": {"taskId": task.id, "contextId": task.context_id, "status": status}})
        except (BrokenPipeError, ConnectionResetError):
            pass


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--listen", default="0.0.0.0")
    ap.add_argument("--port", type=int, default=9000)
    ap.add_argument("--script", required=True, help="JSON script: personas and their answers")
    ap.add_argument("--public-url", required=True, help="base URL the gateway reaches this process at")
    args = ap.parse_args()
    with open(args.script, encoding="utf-8") as f:
        STATE["script"] = json.load(f)
    STATE["public"] = args.public_url
    srv = ThreadingHTTPServer((args.listen, args.port), Handler)
    srv.daemon_threads = True
    print("refagent: serving %s on %s:%d" % (", ".join(sorted(STATE["script"].get("personas") or {})), args.listen, args.port), flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
