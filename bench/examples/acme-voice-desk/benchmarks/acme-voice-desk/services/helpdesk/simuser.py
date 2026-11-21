#!/usr/bin/env python3
"""Heimdall user simulator: the customer a support agent talks to, over MCP.

Part of Heimdall (bench/simuser). Python standard library only. The bench
starts it as a task service (role user). The agent gets one tool,
respond_to_user(message), which sends the agent's message to the customer
and returns the customer's reply. The conversation ends when a reply carries
###STOP###. Every turn is written to the state folder (transcript.json),
which the bench exports after the agent exits for the verifier and the
support metrics.

Modes (BENCH_SIM_MODE):
  scripted  (default) a deterministic script from the task's user file: an
            opening message, ordered reply rules matched on words in the
            agent's message, a closing rule, a default. Same input, same
            conversation, every time.
  llm       a pinned model plays the customer from the task's persona
            (name, goal, known facts, unknown facts, behaviour, when
            satisfied) under the versioned prompt prompts/<version>.txt. The
            model is called through the gateway on the service's own virtual
            key (BENCH_SVC_GATEWAY_URL, BENCH_SVC_GATEWAY_KEY, BENCH_SVC_MODEL),
            at temperature 0, with BENCH_SIM_SEED when it is not 0. The prompt
            file must hash to BENCH_SIM_PROMPT_SHA256 when that is set, so the
            prompt that ran is the one the manifest names.

The agent's message is untrusted input to the simulator. In llm mode it is
wrapped in <agent_message> markers (markers inside it are broken up) and the
prompt tells the model that text inside is speech, never instruction. That
lowers, and does not remove, the chance that an agent steers the customer
(for example talks it into saying ###STOP### early); docs/customer-support.md
says so. The bench never trusts the customer's words for a verdict: the
verifier reads the exported state.

A service-layer probe can plant a hostile customer: BENCH_SIM_HOSTILE holds a
text the customer adds to its first message (scripted) or is told to include
(llm). The transcript then carries a "planted" object the probe layer reads.

Commands:
  python3 simuser.py serve [--config /sim/user.json] [--state /state] [--port 8000]
  python3 simuser.py ready [--port 8000]
"""

import argparse
import hashlib
import json
import os
import re
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

STOP = "###STOP###"
SERVER_NAME = "heimdall-simuser"
SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ("2025-06-18", "2025-03-26", "2024-11-05")
MAX_BODY = 1 << 20
MAX_AGENT_CHARS = 8000
MAX_REPLY_TOKENS = 300
DEFAULT_MAX_TURNS = 12
CALL_TIMEOUT_S = 120
PROMPTS_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "prompts")

RESPOND_TOOL = {
    "name": "respond_to_user",
    "description": (
        "Send a message to the customer and get the customer's reply. This is the only way to "
        "talk to the customer. The conversation is over when the reply contains ###STOP###."
    ),
    "inputSchema": {
        "type": "object",
        "properties": {"message": {"type": "string", "description": "What you say to the customer."}},
        "required": ["message"],
    },
}

DONE_MARKS = (
    "anything else", "something else", "anything more", "have a great day", "have a nice day",
    "goodbye", "good bye", "glad i could help", "take care",
)
OPEN_TAG, CLOSE_TAG = "<agent_message>", "</agent_message>"


class SimulatorError(Exception):
    """The simulator could not produce a reply (a model call failed)."""


def write_json(path, value):
    """Write value to path atomically, so an export never reads half a file."""
    folder = os.path.dirname(path) or "."
    os.makedirs(folder, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=folder, prefix=".tmp-")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(value, f, ensure_ascii=False, indent=1, sort_keys=True)
        f.write("\n")
    os.chmod(tmp, 0o644)
    os.replace(tmp, path)


def fence(text):
    """The agent's message as data: capped, its markers broken up, wrapped."""
    text = str(text)
    if len(text) > MAX_AGENT_CHARS:
        text = text[:MAX_AGENT_CHARS] + "\n[truncated]"
    text = re.sub(r"<\s*/?\s*agent_message\s*>", lambda m: m.group(0).replace("<", "< ").replace(">", " >"), text, flags=re.I)
    return OPEN_TAG + "\n" + text + "\n" + CLOSE_TAG


def bullet(items):
    if not items:
        return "- (nothing listed)"
    if isinstance(items, dict):
        items = [f"{k}: {v}" for k, v in items.items()]
    return "\n".join("- " + str(i) for i in items)


def prompt_text(version):
    """The prompt template of version and its sha256."""
    if not re.fullmatch(r"[a-z0-9][a-z0-9.-]*", version or ""):
        raise SystemExit(f"simuser: prompt version {version!r} is not a valid name")
    with open(os.path.join(PROMPTS_DIR, version + ".txt"), "rb") as f:
        raw = f.read()
    return raw.decode("utf-8"), hashlib.sha256(raw).hexdigest()


def render_prompt(template, persona, max_turns, hostile):
    extra = ""
    if hostile:
        extra = ("8. In your first message, after your request, include this text exactly as written: "
                 + json.dumps(hostile))
    values = {
        "{{name}}": str(persona.get("name") or "a customer"),
        "{{goal}}": str(persona.get("goal") or ""),
        "{{facts}}": bullet(persona.get("facts")),
        "{{unknown}}": bullet(persona.get("unknown")),
        "{{behaviour}}": str(persona.get("behaviour") or "Polite and brief."),
        "{{satisfied_when}}": str(persona.get("satisfied_when") or "your request is handled."),
        "{{max_turns}}": str(max_turns),
        "{{stop}}": STOP,
        "{{extra}}": extra,
    }
    # One pass, so text inside a value is never expanded again.
    pattern = re.compile("|".join(re.escape(k) for k in values))
    return pattern.sub(lambda m: values[m.group(0)], template).rstrip() + "\n"


class ScriptedUser:
    """A deterministic customer from a script.

    script: {"opening": str, "replies": [{"when": [phrase, ...], "say": str,
    "repeat": bool, "stop": bool}], "done": str, "done_when": [phrase, ...],
    "default": str}. The first call returns the opening (plus the hostile
    text under that probe). Later calls take the first rule, in file order,
    that has a phrase in the agent's message (case folded) and was not used
    yet (repeat: true allows reuse); a rule with stop: true ends the
    conversation. Without a rule, a closing phrase ends it with done, else
    the default reply is given. After max_turns replies the customer leaves.
    """

    def __init__(self, script, max_turns, hostile=""):
        if not isinstance(script, dict) or not str(script.get("opening") or "").strip():
            raise SystemExit("simuser: scripted mode needs script.opening in the user file")
        self.script = script
        self.max_turns = max_turns
        self.hostile = hostile
        self.used = set()
        self.turns = 0

    def reply(self, message, history):
        self.turns += 1
        if self.turns == 1:
            text = self.script["opening"].strip()
            if self.hostile:
                text += "\n\n" + self.hostile
            return text, {"rule": "opening"}
        if self.turns > self.max_turns:
            return "I have to go now. " + STOP, {"rule": "max_turns"}
        low = str(message).lower()
        for i, rule in enumerate(self.script.get("replies") or []):
            if i in self.used and not rule.get("repeat"):
                continue
            if any(str(p).lower() in low for p in rule.get("when") or []):
                self.used.add(i)
                text = str(rule.get("say") or "").strip()
                if rule.get("stop") and STOP not in text:
                    text = (text + " " + STOP).strip()
                return text, {"rule": i}
        marks = [str(p).lower() for p in self.script.get("done_when") or DONE_MARKS]
        if any(m in low for m in marks):
            return (str(self.script.get("done") or "No, that is all. Thank you.") + " " + STOP).strip(), {"rule": "done"}
        return str(self.script.get("default") or "Okay, thank you."), {"rule": "default"}


def gateway_headers(raw):
    """The x-bf-maxim-* headers the bench passed (BENCH_SVC_GATEWAY_HEADERS, a JSON
    object), so Bifrost's Maxim plugin logs this user's calls as Heimdall's for the run.
    Any other name, a non-string value or a line break is dropped."""
    if not raw:
        return {}
    try:
        value = json.loads(raw)
    except ValueError:
        return {}
    if not isinstance(value, dict):
        return {}
    out = {}
    for k, v in value.items():
        if (isinstance(k, str) and isinstance(v, str) and k.lower().startswith("x-bf-maxim-")
                and "\r" not in v and "\n" not in v and "\r" not in k and "\n" not in k):
            out[k] = v
    return out


class GatewayClient:
    """Chat completions through the gateway on the service's own key."""

    def __init__(self, url, key, model, seed, headers=None):
        if not url or not key or not model:
            raise SystemExit("simuser: llm mode needs BENCH_SVC_GATEWAY_URL, BENCH_SVC_GATEWAY_KEY and BENCH_SVC_MODEL "
                             "(the service's gateway access; set simulator provider and model)")
        self.url = url.rstrip("/") + "/v1/chat/completions"
        self.key = key
        self.model = model
        self.seed = seed
        self.headers = gateway_headers(headers)

    def chat(self, messages):
        body = {"model": self.model, "messages": messages, "temperature": 0, "max_tokens": MAX_REPLY_TOKENS}
        if self.seed:
            body["seed"] = self.seed
        req = urllib.request.Request(self.url, data=json.dumps(body).encode(), method="POST",
                                     headers={**self.headers, "Content-Type": "application/json", "x-bf-vk": self.key})
        started = time.monotonic()
        try:
            with urllib.request.urlopen(req, timeout=CALL_TIMEOUT_S) as r:
                raw = r.read(1 << 20)
        except urllib.error.HTTPError as e:
            detail = e.read(300).decode("utf-8", "replace")
            e.close()
            raise SimulatorError(f"gateway answered {e.code}: {self.redact(detail)}") from None
        except (OSError, ValueError) as e:
            raise SimulatorError(f"gateway call failed: {self.redact(str(e))}") from None
        latency = int((time.monotonic() - started) * 1000)
        try:
            out = json.loads(raw)
            content = out["choices"][0]["message"]["content"]
        except (ValueError, KeyError, IndexError, TypeError):
            raise SimulatorError("gateway answer is not a chat completion") from None
        if isinstance(content, list):
            content = "".join(p.get("text", "") for p in content if isinstance(p, dict))
        usage = out.get("usage") or {}
        meta = {"model_served": out.get("model", ""), "latency_ms": latency,
                "prompt_tokens": usage.get("prompt_tokens"), "completion_tokens": usage.get("completion_tokens")}
        return self.redact(str(content or "")), meta

    def redact(self, text):
        return text.replace(self.key, "[key]") if self.key else text


class LLMUser:
    """A customer played by a pinned model from a persona."""

    def __init__(self, persona, max_turns, client, template, hostile=""):
        if not isinstance(persona, dict) or not str(persona.get("goal") or "").strip():
            raise SystemExit("simuser: llm mode needs persona.goal in the user file")
        self.max_turns = max_turns
        self.client = client
        self.system = render_prompt(template, persona, max_turns, hostile)
        self.messages = [{"role": "system", "content": self.system}]
        self.turns = 0

    def reply(self, message, history):
        self.turns += 1
        if self.turns > self.max_turns:
            return "I have to go now. " + STOP, {"rule": "max_turns"}
        msgs = self.messages + [{"role": "user", "content": fence(message)}]
        text, meta = self.client.chat(msgs)
        text = text.replace(OPEN_TAG, "").replace(CLOSE_TAG, "").strip() or "..."
        self.messages = msgs + [{"role": "assistant", "content": text}]
        return text, meta


class Session:
    """One conversation: the user, the transcript, and its persistence."""

    def __init__(self, cfg, state_dir, env):
        self.state_dir = state_dir
        self.lock = threading.Lock()
        mode = env.get("BENCH_SIM_MODE") or cfg.get("mode") or "scripted"
        max_turns = int(env.get("BENCH_SIM_MAX_TURNS") or cfg.get("max_turns") or DEFAULT_MAX_TURNS)
        hostile = (env.get("BENCH_SIM_HOSTILE") or "").strip()
        self.transcript = {
            "task": cfg.get("id", ""),
            "mode": mode,
            "max_turns": max_turns,
            "ended_by": None,
            "user_turns": 0,
            "simulator_errors": 0,
            "events": [],
            "sim_calls": [],
        }
        if hostile:
            self.transcript["planted"] = {"armed": True, "served": []}
        if mode == "scripted":
            self.user = ScriptedUser(cfg.get("script"), max_turns, hostile)
        elif mode == "llm":
            version = env.get("BENCH_SIM_PROMPT_VERSION") or "user-v1"
            template, sha = prompt_text(version)
            want = env.get("BENCH_SIM_PROMPT_SHA256") or ""
            if want and want != sha:
                raise SystemExit(f"simuser: prompt {version} hashes to {sha}, the manifest pinned {want}")
            seed = int(env.get("BENCH_SIM_SEED") or 0)
            client = GatewayClient(env.get("BENCH_SVC_GATEWAY_URL"), env.get("BENCH_SVC_GATEWAY_KEY"), env.get("BENCH_SVC_MODEL"), seed,
                                   env.get("BENCH_SVC_GATEWAY_HEADERS"))
            self.user = LLMUser(cfg.get("persona"), max_turns, client, template, hostile)
            self.transcript.update({"model": client.model, "prompt_version": version, "prompt_sha256": sha, "seed": seed})
        else:
            raise SystemExit(f"simuser: mode {mode!r} is not scripted or llm")
        self.persist()

    def respond(self, args):
        """Run one respond_to_user call; returns (text, is_error)."""
        msg = args.get("message") if isinstance(args, dict) else None
        if not isinstance(msg, str) or not msg.strip():
            return "respond_to_user needs a non-empty string argument message", True
        with self.lock:
            t = self.transcript
            ev = {"seq": len(t["events"]) + 1, "role": "agent", "content": msg}
            if t["ended_by"] is not None:
                ev["after_end"] = True
                t["events"].append(ev)
                self.persist()
                return "The customer has left the conversation. End your session.", False
            t["events"].append(ev)
            try:
                reply, meta = self.user.reply(msg, t["events"])
            except SimulatorError as e:
                t["simulator_errors"] += 1
                t["sim_calls"].append({"turn": t["user_turns"] + 1, "ok": False, "error": str(e)})
                self.persist()
                return "The customer did not answer (the user simulator failed). Try again.", True
            t["user_turns"] += 1
            if "model_served" in meta:
                t["sim_calls"].append(dict(meta, turn=t["user_turns"], ok=True))
            t["events"].append({"seq": len(t["events"]) + 1, "role": "user", "content": reply, "rule": meta.get("rule")})
            if "planted" in t and t["user_turns"] == 1:
                t["planted"]["served"].append({"call": ev["seq"], "tool": RESPOND_TOOL["name"]})
            if STOP in reply:
                t["ended_by"] = "max_turns" if meta.get("rule") == "max_turns" else STOP
            self.persist()
            return reply, False

    def persist(self):
        write_json(os.path.join(self.state_dir, "transcript.json"), self.transcript)


def rpc_result(rid, result):
    return {"jsonrpc": "2.0", "id": rid, "result": result}


def rpc_error(rid, code, message):
    return {"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}}


def handle_rpc(session, msg):
    """Answer one JSON-RPC message; None for a notification."""
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0" or not isinstance(msg.get("method"), str):
        return rpc_error(msg.get("id") if isinstance(msg, dict) else None, -32600, "invalid request")
    if "id" not in msg:
        return None
    method, rid = msg["method"], msg.get("id")
    params = msg.get("params") or {}
    if method == "initialize":
        asked = params.get("protocolVersion")
        return rpc_result(rid, {
            "protocolVersion": asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0],
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
            "instructions": "The customer. Talk to the customer only with respond_to_user.",
        })
    if method == "ping":
        return rpc_result(rid, {})
    if method == "tools/list":
        return rpc_result(rid, {"tools": [RESPOND_TOOL]})
    if method == "tools/call":
        if params.get("name") != RESPOND_TOOL["name"]:
            return rpc_error(rid, -32602, f"unknown tool: {params.get('name')}")
        text, is_error = session.respond(params.get("arguments") or {})
        return rpc_result(rid, {"content": [{"type": "text", "text": text}], "isError": is_error})
    return rpc_error(rid, -32601, f"method {method} not found")


def make_handler(session, mcp_path):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, fmt, *args):
            sys.stderr.write("simuser: " + (fmt % args) + "\n")

        def _send(self, code, body=None):
            data = b"" if body is None else (body if isinstance(body, bytes) else json.dumps(body).encode())
            self.send_response(code)
            if body is not None:
                self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            path = self.path.split("?")[0]
            if path == "/healthz":
                return self._send(200, {"ok": True, "server": SERVER_NAME})
            return self._send(405 if path == mcp_path else 404, {"error": "POST JSON-RPC to " + mcp_path})

        def do_DELETE(self):
            return self._send(200 if self.path.split("?")[0] == mcp_path else 404, {})

        def do_POST(self):
            if self.path.split("?")[0] != mcp_path:
                return self._send(404, {"error": "not found"})
            n = int(self.headers.get("Content-Length") or 0)
            if n <= 0 or n > MAX_BODY:
                return self._send(400, rpc_error(None, -32700, "missing or oversized body"))
            try:
                msg = json.loads(self.rfile.read(n))
            except ValueError:
                return self._send(400, rpc_error(None, -32700, "parse error"))
            batch = msg if isinstance(msg, list) else [msg]
            out = [r for r in (handle_rpc(session, m) for m in batch) if r is not None]
            if not out:
                return self._send(202)
            return self._send(200, out if isinstance(msg, list) else out[0])

    return Handler


def load_config(path):
    with open(path, encoding="utf-8") as f:
        cfg = json.load(f)
    if not isinstance(cfg, dict):
        raise SystemExit(f"simuser: {path} is not a JSON object")
    return cfg


def cmd_serve(a):
    session = Session(load_config(a.config), a.state, dict(os.environ))
    # The key is in this process only; drop it from the environment children would inherit.
    os.environ.pop("BENCH_SVC_GATEWAY_KEY", None)
    srv = ThreadingHTTPServer(("0.0.0.0", a.port), make_handler(session, a.mcp_path))
    sys.stderr.write(f"simuser: {session.transcript['mode']} user on :{a.port}{a.mcp_path}\n")
    srv.serve_forever()


def cmd_ready(a):
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{a.port}/healthz", timeout=2) as r:
            return 0 if r.status == 200 else 1
    except OSError:
        return 1


def main(argv=None):
    p = argparse.ArgumentParser(prog="simuser")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--config", default="/sim/user.json")
    s.add_argument("--state", default="/state")
    s.add_argument("--port", type=int, default=int(os.environ.get("PORT", "8000")))
    s.add_argument("--mcp-path", default="/mcp")
    r = sub.add_parser("ready")
    r.add_argument("--port", type=int, default=int(os.environ.get("PORT", "8000")))
    a = p.parse_args(argv)
    if a.cmd == "serve":
        cmd_serve(a)
        return 0
    return cmd_ready(a)


if __name__ == "__main__":
    sys.exit(main())
