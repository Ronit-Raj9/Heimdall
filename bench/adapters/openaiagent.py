"""Launcher of an openai_agent (adapters/openaiagent.go, docs/openai-agents.md).

The agent is a user's service that speaks the OpenAI chat/completions or
Responses API. This program runs in the run's container and is bench code:
nothing of the agent runs here.

It sends the task instruction as a user message (BENCH_OPENAI_SYSTEM first
when set; with BENCH_OPENAI_HISTORY, the earlier sessions of a multi-session
task as earlier user and assistant turns). With BENCH_MCP_SERVERS it lists
the tools of every MCP server (the run's tool proxy) and offers them as
function tools. Every tool call the endpoint makes is run with MCP
tools/call on the server that offered the tool, so the tool proxy records it
like any agent's call, and its result is sent back. The loop ends when the
endpoint answers without a tool call, or after BENCH_OPENAI_MAX_TURNS
requests. The final assistant text is written to BENCH_ANSWER_PATH.

stdout carries the agent event protocol (schema bifrost.agent.event): one
model_request and model_response per request (with the endpoint's reported
usage as tokens), one tool_call and tool_result per tool call and a final
claim. Diagnostics go to stderr. The Authorization header carries
BENCH_OPENAI_API_KEY when set; it is never printed.

Conversation (lane Y11, BENCH_CONV_USERS set): the bench drives a chat task.
The instruction becomes the system message, each simulated customer message
a user message and each final assistant text the reply sent to the customer
(heimdall_conv.drive); respond_to_user is never offered.

The connection to the endpoint goes to BENCH_REMOTE_CONNECT (the run's egress
forwarder) when set; the Host header and the TLS server name stay the URL's.
"""

import http.client
import json
import os
import re
import socket
import sys
import time
import urllib.parse
import urllib.request

MAX_BODY = 16 << 20
MAX_TOOL_RESULT = 64 << 10
MAX_SUMMARY = 300
SCHEMA = "bifrost.agent.event"
NAME_RE = re.compile(r"[^A-Za-z0-9_-]")
SECRET_RE = re.compile(r"(?i)(sk-[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._~+/=-]{8,})")

SESSION_INDEX = int(os.environ.get("BENCH_OPENAI_SESSION_INDEX", "1") or "1")
SEQ = [0]


def log(msg):
    print("openai_agent: " + msg, file=sys.stderr, flush=True)


def clean(value, limit=MAX_SUMMARY):
    if not isinstance(value, str):
        value = json.dumps(value) if value is not None else ""
    value = "".join(c for c in value if c.isprintable())
    return SECRET_RE.sub("[redacted]", value)[:limit]


def now():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def event(kind, **fields):
    """Prints one agent event protocol line and returns its id."""
    SEQ[0] += 1
    eid = "s%d-e%d" % (SESSION_INDEX, SEQ[0])
    ev = {"schema": SCHEMA, "version": "1", "type": kind, "timestamp": now(), "id": eid}
    ev.update({k: v for k, v in fields.items() if v not in (None, "")})
    print(json.dumps(ev, sort_keys=True), flush=True)
    return eid


def fail(code, message):
    event("error", summary=clean(message), status="error", is_error=True)
    log(message)
    sys.exit(code)


# ---- the endpoint -------------------------------------------------------

def connect_to(addr, timeout):
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


class Endpoint:
    def __init__(self, url, key):
        self.url = urllib.parse.urlsplit(url)
        if self.url.scheme not in ("http", "https") or not self.url.hostname:
            fail(2, "BENCH_OPENAI_URL is not an http(s) URL")
        self.port = self.url.port or (443 if self.url.scheme == "https" else 80)
        self.addr = (self.url.hostname, self.port)
        if os.environ.get("BENCH_REMOTE_CONNECT"):
            host, _, p = os.environ["BENCH_REMOTE_CONNECT"].rpartition(":")
            self.addr = (host, int(p))
        self.path = (self.url.path or "/") + ("?" + self.url.query if self.url.query else "")
        self.headers = {"Content-Type": "application/json", "Accept": "application/json"}
        if key:
            self.headers["Authorization"] = "Bearer " + key

    def post(self, body, timeout):
        cls = TLSConn if self.url.scheme == "https" else Conn
        conn = cls(self.url.hostname, self.port, self.addr, timeout)
        try:
            conn.request("POST", self.path, body=json.dumps(body).encode("utf-8"), headers=self.headers)
            resp = conn.getresponse()
            raw = resp.read(MAX_BODY + 1)
        except (OSError, http.client.HTTPException) as e:
            fail(1, "request to the endpoint failed: %s" % e)
        finally:
            conn.close()
        if len(raw) > MAX_BODY:
            fail(1, "the endpoint's answer is larger than %d bytes" % MAX_BODY)
        if resp.status // 100 != 2:
            fail(1, "the endpoint answered HTTP %d: %s" % (resp.status, clean(raw[:500].decode("utf-8", "replace"), 500)))
        try:
            doc = json.loads(raw)
        except ValueError:
            fail(1, "the endpoint's answer is not JSON")
        if not isinstance(doc, dict):
            fail(1, "the endpoint's answer is not a JSON object")
        if doc.get("error"):
            fail(1, "the endpoint returned an error: %s" % clean(doc["error"], 500))
        return doc


# ---- MCP tools ------------------------------------------------------------

class MCP:
    """One streamable HTTP MCP session on the run's tool proxy."""

    def __init__(self, url, headers):
        self.url, self.headers, self.session, self.next_id = url, dict(headers or {}), None, 0

    def rpc(self, method, params=None, notify=False):
        msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
        if not notify:
            self.next_id += 1
            msg["id"] = self.next_id
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        headers.update(self.headers)
        if self.session:
            headers["Mcp-Session-Id"] = self.session
        req = urllib.request.Request(self.url, data=json.dumps(msg).encode(), headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=120) as resp:
            if resp.headers.get("Mcp-Session-Id"):
                self.session = resp.headers["Mcp-Session-Id"]
            body = resp.read(MAX_BODY).decode("utf-8", errors="replace")
        if notify:
            return None
        if "data:" in body:
            body = [line[5:] for line in body.splitlines() if line.startswith("data:")][-1]
        answer = json.loads(body)
        if "error" in answer:
            raise RuntimeError("%s: %s" % (method, clean(answer["error"])))
        return answer.get("result") or {}

    def open(self):
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                "clientInfo": {"name": "heimdall-openai-agent", "version": "1"}})
        self.rpc("notifications/initialized", notify=True)
        return self.rpc("tools/list").get("tools", [])


def load_tools():
    """Function name -> (server id, MCP client, MCP tool name, description, schema)."""
    servers = json.loads(os.environ.get("BENCH_MCP_SERVERS", "{}") or "{}")
    listed = []
    for sid in sorted(servers):
        spec = servers[sid] if isinstance(servers[sid], dict) else {"url": servers[sid]}
        client = MCP(spec.get("url", ""), spec.get("headers"))
        try:
            for t in client.open():
                if isinstance(t, dict) and t.get("name"):
                    listed.append((sid, client, t))
        except Exception as e:  # a server that will not list is skipped, not fatal
            log("list tools of %s: %s" % (sid, clean(str(e))))
    counts = {}
    for _, _, t in listed:
        counts[t["name"]] = counts.get(t["name"], 0) + 1
    out = {}
    for sid, client, t in listed:
        name = t["name"] if counts[t["name"]] == 1 else sid + "__" + t["name"]
        name = NAME_RE.sub("_", name)[:64] or "tool"
        base, n = name, 2
        while name in out:
            name = (base[:60] + "_%d" % n)
            n += 1
        schema = t.get("inputSchema") if isinstance(t.get("inputSchema"), dict) else {"type": "object", "properties": {}}
        out[name] = (sid, client, t["name"], str(t.get("description") or "")[:1024], schema)
    return out


def call_tool(tools, name, raw_args):
    """Runs one tool call; returns (text, is_error)."""
    if name not in tools:
        return "unknown tool %s" % name, True
    sid, client, tool, _, _ = tools[name]
    try:
        args = json.loads(raw_args) if isinstance(raw_args, str) and raw_args.strip() else (raw_args or {})
    except ValueError:
        return "tool arguments are not JSON", True
    if not isinstance(args, dict):
        return "tool arguments are not a JSON object", True
    try:
        res = client.rpc("tools/call", {"name": tool, "arguments": args})
    except Exception as e:
        return "tool call failed: %s" % clean(str(e), 1000), True
    parts = []
    for c in res.get("content") or []:
        if isinstance(c, dict) and c.get("type") == "text":
            parts.append(str(c.get("text", "")))
        elif isinstance(c, dict):
            parts.append(json.dumps(c))
    if not parts and res.get("structuredContent") is not None:
        parts.append(json.dumps(res["structuredContent"]))
    return "\n".join(parts)[:MAX_TOOL_RESULT], bool(res.get("isError"))


# ---- usage -----------------------------------------------------------------

def count(v):
    if isinstance(v, bool) or not isinstance(v, (int, float)) or v < 0 or v > 1e12:
        return None
    return int(v)


def tokens_of(u):
    """Agent event tokens from a chat or Responses usage object."""
    if not isinstance(u, dict):
        return None
    inp = count(u.get("prompt_tokens", u.get("input_tokens")))
    out = count(u.get("completion_tokens", u.get("output_tokens")))
    details = u.get("prompt_tokens_details") or u.get("input_tokens_details") or {}
    cached = count(details.get("cached_tokens")) if isinstance(details, dict) else None
    if inp is None and out is None:
        return None
    t = {"fresh": max((inp or 0) - (cached or 0), 0), "out": out or 0}
    if cached:
        t["cache_read"] = cached
    return t


# ---- the loops -------------------------------------------------------------

def text_of_content(c):
    if isinstance(c, str):
        return c
    if isinstance(c, list):
        return "".join(str(p.get("text", "")) for p in c if isinstance(p, dict) and p.get("type") in ("text", "output_text"))
    return ""


def run_tool_calls(tools, calls, parent):
    """calls: [(call id, name, raw args)] -> [(call id, result text)]."""
    out = []
    for cid, name, raw in calls:
        target = clean(raw if isinstance(raw, str) else json.dumps(raw), 200)
        ev = event("tool_call", parent_event_id=parent, tool_call_id=cid, tool=clean(name, 128), target=target, status="started")
        text, is_err = call_tool(tools, name, raw)
        event("tool_result", parent_event_id=ev, tool_call_id=cid, tool=clean(name, 128),
              status="error" if is_err else "success", is_error=is_err, summary=clean(text))
        out.append((cid, text))
    return out


def chat_loop(ep, model, history, system, instruction, tools, max_turns, deadline):
    messages = ([{"role": "system", "content": system}] if system else []) + history + [{"role": "user", "content": instruction}]
    return chat_until_text(ep, model, messages, tools, max_turns, deadline)


def chat_until_text(ep, model, messages, tools, max_turns, deadline):
    """Runs requests and tool calls on messages (extended in place) until the
    endpoint answers without a tool call; returns (text, requests)."""
    defs = [{"type": "function", "function": {"name": n, "description": d, "parameters": s}}
            for n, (_, _, _, d, s) in sorted(tools.items())]
    for turn in range(1, max_turns + 1):
        body = {"model": model, "messages": messages}
        if defs:
            body["tools"] = defs
        req = event("model_request", summary="turn %d" % turn)
        doc = ep.post(body, max(1.0, deadline - time.time()))
        choices = doc.get("choices") or []
        msg = (choices[0] or {}).get("message") if choices and isinstance(choices[0], dict) else None
        if not isinstance(msg, dict):
            fail(1, "the endpoint's chat answer has no choices[0].message")
        text = text_of_content(msg.get("content"))
        calls = [c for c in (msg.get("tool_calls") or []) if isinstance(c, dict)]
        resp = event("model_response", parent_event_id=req, summary=clean(text), tokens=tokens_of(doc.get("usage")))
        if not calls:
            messages.append({"role": "assistant", "content": text})
            return text, turn
        messages.append({"role": "assistant", "content": msg.get("content"), "tool_calls": calls})
        parsed = []
        for i, c in enumerate(calls):
            fn = c.get("function") or {}
            parsed.append((str(c.get("id") or "call_%d_%d" % (turn, i)), str(fn.get("name", "")), fn.get("arguments", "{}")))
        for cid, result in run_tool_calls(tools, parsed, resp):
            messages.append({"role": "tool", "tool_call_id": cid, "content": result})
        if time.time() >= deadline:
            fail(1, "timed out after %d turns" % turn)
    fail(1, "no final answer after %d turns (openai.max_turns)" % max_turns)


def responses_loop(ep, model, history, system, instruction, tools, max_turns, deadline):
    items = history + [{"role": "user", "content": instruction}]
    return responses_until_text(ep, model, items, system, tools, max_turns, deadline)


def responses_until_text(ep, model, items, system, tools, max_turns, deadline):
    """The Responses API form of chat_until_text (items extended in place)."""
    defs = [{"type": "function", "name": n, "description": d, "parameters": s}
            for n, (_, _, _, d, s) in sorted(tools.items())]
    for turn in range(1, max_turns + 1):
        body = {"model": model, "input": items, "store": False}
        if system:
            body["instructions"] = system
        if defs:
            body["tools"] = defs
        req = event("model_request", summary="turn %d" % turn)
        doc = ep.post(body, max(1.0, deadline - time.time()))
        if str(doc.get("status", "completed")) in ("failed", "cancelled"):
            fail(1, "the endpoint's response %s: %s" % (doc.get("status"), clean(doc.get("error") or doc.get("incomplete_details"))))
        output = [o for o in (doc.get("output") or []) if isinstance(o, dict)]
        text = doc.get("output_text") if isinstance(doc.get("output_text"), str) else ""
        if not text:
            text = "".join(text_of_content(o.get("content")) for o in output if o.get("type") == "message")
        calls = [o for o in output if o.get("type") == "function_call"]
        resp = event("model_response", parent_event_id=req, summary=clean(text), tokens=tokens_of(doc.get("usage")))
        if not calls:
            items.append({"role": "assistant", "content": text})
            return text, turn
        items.extend(output)
        parsed = [(str(c.get("call_id") or c.get("id") or "call_%d_%d" % (turn, i)), str(c.get("name", "")), c.get("arguments", "{}"))
                  for i, c in enumerate(calls)]
        for cid, result in run_tool_calls(tools, parsed, resp):
            items.append({"type": "function_call_output", "call_id": cid, "output": result})
        if time.time() >= deadline:
            fail(1, "timed out after %d turns" % turn)
    fail(1, "no final answer after %d turns (openai.max_turns)" % max_turns)


CONV_NOTE = ("You are in a live chat with the customer: every reply you write is sent to the customer as it is, "
             "and the customer's answer comes back as the next user message. Talk to the customer directly; "
             "there is no respond_to_user tool.")


def converse(ep, model, system, instruction, tools, max_turns, deadline):
    """Y11: the bench drives the conversation (heimdall_conv.drive). The task
    instruction is the system message; each customer message is a user
    message; tool calls run between them as usual."""
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    import heimdall_conv  # noqa: E402
    full = "\n\n".join(p for p in (system, instruction, CONV_NOTE) if p)
    responses = os.environ.get("BENCH_OPENAI_API") == "responses"
    msgs = [] if responses else [{"role": "system", "content": full}]
    requests = [0]

    def agent_turn(user_text, _messages, turn):
        if turn == 1:
            msgs.append({"role": "assistant", "content": _messages[0]["content"]})
        msgs.append({"role": "user", "content": user_text})
        if responses:
            text, n = responses_until_text(ep, model, msgs, full, tools, max_turns, deadline)
        else:
            text, n = chat_until_text(ep, model, msgs, tools, max_turns, deadline)
        requests[0] += n
        return text

    try:
        summary, last, _ = heimdall_conv.drive(agent_turn, lambda ev: print(json.dumps(ev, sort_keys=True), flush=True), deadline)
    except heimdall_conv.ConversationError as e:
        fail(1, str(e))
    return last, requests[0], summary


def read_history(path):
    if not path:
        return []
    try:
        with open(path, encoding="utf-8") as f:
            doc = json.load(f)
    except (OSError, ValueError):
        return []
    return [m for m in doc if isinstance(m, dict) and m.get("role") in ("user", "assistant") and isinstance(m.get("content"), str)] if isinstance(doc, list) else []


def write_history(path, history):
    if not path:
        return
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        tmp = path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(history, f)
        os.replace(tmp, path)
    except OSError as e:
        log("could not keep the conversation for the next session: %s" % e)


def main():
    timeout = float(os.environ.get("BENCH_OPENAI_TIMEOUT_S", "600"))
    deadline = time.time() + timeout
    ep = Endpoint(os.environ["BENCH_OPENAI_URL"], os.environ.get("BENCH_OPENAI_API_KEY", ""))
    with open(os.environ["BENCH_OPENAI_INSTRUCTION"], encoding="utf-8") as f:
        instruction = f.read()
    system = ""
    if os.environ.get("BENCH_OPENAI_SYSTEM"):
        with open(os.environ["BENCH_OPENAI_SYSTEM"], encoding="utf-8") as f:
            system = f.read()
    history_path = os.environ.get("BENCH_OPENAI_HISTORY", "")
    history = read_history(history_path)
    tools = load_tools() if os.environ.get("BENCH_MCP_SERVERS") else {}
    conversation = bool(os.environ.get("BENCH_CONV_USERS"))
    if conversation:  # the bench speaks to the customer; the endpoint never gets the tool
        tools = {n: t for n, t in tools.items() if t[2] != "respond_to_user"}
    max_turns = max(1, int(os.environ.get("BENCH_OPENAI_MAX_TURNS", "20") or "20"))
    model = os.environ.get("BENCH_OPENAI_MODEL", "")
    log("api %s, %d tools, %d earlier turns, session %s" % (os.environ.get("BENCH_OPENAI_API", "chat"), len(tools), len(history),
                                                            clean(os.environ.get("BENCH_OPENAI_SESSION_ID", ""), 64)))
    if conversation:
        answer, turns, summary = converse(ep, model, system, instruction, tools, max_turns, deadline)
        answer = answer or ""
        event("claim", claim={"made": False, "text": clean(answer, 1000)},
              summary="conversation ended by %s after %d agent turns (%d requests)" % (summary["ended_by"], summary["agent_turns"], turns))
        if os.environ.get("BENCH_OPENAI_OUTPUT", "answer") == "answer":
            answer_path = os.environ.get("BENCH_ANSWER_PATH", "answer.txt")
            if os.path.dirname(answer_path):
                os.makedirs(os.path.dirname(answer_path), exist_ok=True)
            with open(answer_path, "w", encoding="utf-8") as f:
                f.write(answer)
        log("conversation done: %d agent turns, ended by %s" % (summary["agent_turns"], summary["ended_by"]))
        return
    loop = responses_loop if os.environ.get("BENCH_OPENAI_API") == "responses" else chat_loop
    answer, turns = loop(ep, model, history, system, instruction, tools, max_turns, deadline)
    answer = answer or ""
    write_history(history_path, history + [{"role": "user", "content": instruction}, {"role": "assistant", "content": answer}])
    answer_path = os.environ.get("BENCH_ANSWER_PATH", "answer.txt")
    if answer or os.environ.get("BENCH_OPENAI_OUTPUT", "answer") == "answer":
        parent = os.path.dirname(answer_path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(answer_path, "w", encoding="utf-8") as f:
            f.write(answer)
    event("claim", claim={"made": False, "text": clean(answer, 1000)}, summary="final answer after %d turns" % turns)
    log("done after %d turns, %d answer bytes" % (turns, len(answer.encode("utf-8"))))


if __name__ == "__main__":
    main()
