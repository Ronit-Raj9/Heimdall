#!/usr/bin/env python3
"""A ToolBench-X task as a bench service: MCP server and exact-match verifier.

serve   loads the task's upstream tool module (tools/<type>/<topic>/<subtopic>/
        task_N.py, or its tools_exception twin in the hazard variant) and serves
        the functions the task lists in tools_used as MCP tools over streamable
        HTTP (JSON responses, no server-sent events), plus submit_answer(answer),
        which records the agent's final answer. Every call and the answer are
        written to the state folder, which the bench exports after the agent
        exits.
verify  compares the exported answer with the task's final_answer the way
        upstream does (str(x).strip() equality, agent/evaluation.py) and writes
        the reward file the bench reads.

The tools' live-provider calls are switched off (an unreachable proxy is set
before the module loads), so every tool takes its deterministic fallback path,
the same on every run. In the hazard variant the module's own fault injection
runs with upstream's strict_no_hint_profile (seeded, deterministic).
"""

import argparse
import importlib.util
import inspect
import json
import os
import sys
import tempfile
import threading
import typing
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ("2025-06-18", "2025-03-26", "2024-11-05")
MAX_BODY = 4 << 20
MAX_RESULT = 64 << 10

SUBMIT_TOOL = {
    "name": "submit_answer",
    "description": (
        "Submit your final answer to the user's request. Give only the exact value "
        "(a number, a name, a short phrase), no explanation, units or currency symbols "
        "unless the request asks for them. The last submission counts."
    ),
    "inputSchema": {
        "type": "object",
        "properties": {"answer": {"type": "string", "description": "The final answer, exactly."}},
        "required": ["answer"],
    },
}


def write_json(path, value):
    d = os.path.dirname(path)
    fd, tmp = tempfile.mkstemp(dir=d, prefix=".tmp-")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(value, f, indent=1, ensure_ascii=False, default=str)
    os.replace(tmp, path)


def json_type(ann):
    """JSON schema of a Python annotation (best effort, permissive)."""
    if ann is inspect.Parameter.empty or ann is typing.Any:
        return {}
    origin = typing.get_origin(ann)
    args = [a for a in typing.get_args(ann) if a is not type(None)]
    if origin is typing.Union:
        if len(args) == 1:
            return json_type(args[0])
        return {"anyOf": [json_type(a) or {} for a in args]}
    if ann in (str,):
        return {"type": "string"}
    if ann is bool:
        return {"type": "boolean"}
    if ann is int:
        return {"type": "integer"}
    if ann is float:
        return {"type": "number"}
    if ann in (list, tuple) or origin in (list, tuple):
        item = json_type(args[0]) if args else {}
        return {"type": "array", "items": item} if item else {"type": "array"}
    if ann is dict or origin is dict:
        return {"type": "object"}
    return {}


def tool_schema(name, fn):
    sig = inspect.signature(fn)
    props, required = {}, []
    try:
        hints = typing.get_type_hints(fn)
    except Exception:
        hints = {}
    for pname, p in sig.parameters.items():
        if p.kind in (p.VAR_POSITIONAL, p.VAR_KEYWORD):
            continue
        props[pname] = json_type(hints.get(pname, p.annotation))
        if p.default is inspect.Parameter.empty:
            required.append(pname)
    schema = {"type": "object", "properties": props}
    if required:
        schema["required"] = required
    doc = inspect.getdoc(fn) or f"Run {name}."
    return {"name": name, "description": doc[:4000], "inputSchema": schema}


def load_module(path, prompt, variant):
    # No live provider: every tool takes its deterministic fallback.
    for k in ("http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"):
        os.environ[k] = "http://127.0.0.1:9"
    os.environ.pop("no_proxy", None)
    os.environ.pop("NO_PROXY", None)
    os.environ["USER_PROMPT"] = prompt
    os.environ["INJECTION_USER_PROMPT"] = prompt
    if variant == "hazard":
        os.environ["INJECTION_PROFILE"] = "strict_no_hint_profile"
    spec = importlib.util.spec_from_file_location("tbx_task_tools", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    if hasattr(mod, "_USER_PROMPT"):
        try:
            mod._USER_PROMPT = prompt
        except Exception:
            pass
    if callable(getattr(mod, "_parse_hint_context", None)) and hasattr(mod, "_HINT_CONTEXT"):
        try:
            mod._HINT_CONTEXT = mod._parse_hint_context(prompt)
        except Exception:
            pass
    if variant == "hazard":
        # Upstream's runner (agent/model_driven_core.py) applies the strict
        # profile the same way when no hint is given.
        strict = getattr(mod, "strict_no_hint_profile", None)
        guided = getattr(mod, "guided_with_hint_profile", None)
        if callable(getattr(mod, "set_injection_config", None)) and isinstance(strict, dict):
            cfg = {
                "profile": "strict_no_hint_profile",
                "profile_name": "strict_no_hint_profile",
                "profiles": {"strict_no_hint_profile": strict, "guided_with_hint_profile": guided or {}},
            }
            mod.set_injection_config(cfg)
            os.environ["INJECTION_CONFIG_JSON"] = json.dumps(cfg, ensure_ascii=False)
    else:
        os.environ["DISABLE_DEFAULT_EXCEPTION_INJECTION"] = "1"
    return mod


class Session:
    def __init__(self, scenario, tools_path, state_dir):
        self.scenario = scenario
        self.state_dir = state_dir
        self.lock = threading.Lock()
        self.mod = load_module(tools_path, scenario["user_prompt"], scenario.get("variant", "clean"))
        self.fns = {}
        self.tools = []
        for name in scenario["tools_used"]:
            fn = getattr(self.mod, name, None)
            if callable(fn) and name not in self.fns:
                self.fns[name] = fn
                self.tools.append(tool_schema(name, fn))
        self.tools.append(SUBMIT_TOOL)
        self.transcript = {"task": scenario["task"], "variant": scenario.get("variant", "clean"), "events": []}
        self.answer = {"task": scenario["task"], "answer": None, "submissions": 0}
        os.makedirs(state_dir, exist_ok=True)
        self._persist()

    def call(self, name, args):
        with self.lock:
            seq = len(self.transcript["events"]) + 1
            if name == "submit_answer":
                ans = args.get("answer")
                if ans is None or isinstance(ans, (dict, list)):
                    return "submit_answer needs a string argument answer", True
                self.answer["answer"] = str(ans)
                self.answer["submissions"] += 1
                self.transcript["events"].append({"seq": seq, "name": name, "arguments": args})
                self._persist()
                return "Answer recorded.", False
            fn = self.fns.get(name)
            if fn is None:
                return f"Error: unknown tool {name}", True
            try:
                out = fn(**args)
                text, err = json.dumps(out, ensure_ascii=False, default=str), False
            except TypeError as e:
                text, err = f"Error: TypeError: {e}", True
            except Exception as e:  # upstream faults surface as tool errors
                text, err = f"Error: {type(e).__name__}: {e}", True
            if len(text) > MAX_RESULT:
                text = text[:MAX_RESULT] + "...(truncated)"
            self.transcript["events"].append({"seq": seq, "name": name, "arguments": args, "error": err, "content": text[:4000]})
            self._persist()
            return text, err

    def _persist(self):
        write_json(os.path.join(self.state_dir, "answer.json"), self.answer)
        write_json(os.path.join(self.state_dir, "transcript.json"), self.transcript)


def rpc_result(rid, result):
    return {"jsonrpc": "2.0", "id": rid, "result": result}


def rpc_error(rid, code, message):
    return {"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}}


def handle_rpc(session, msg):
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0" or not isinstance(msg.get("method"), str):
        return rpc_error(msg.get("id") if isinstance(msg, dict) else None, -32600, "invalid request")
    method, rid = msg["method"], msg.get("id")
    if "id" not in msg:
        return None
    params = msg.get("params") or {}
    if method == "initialize":
        asked = params.get("protocolVersion")
        return rpc_result(rid, {
            "protocolVersion": asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0],
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": "toolbench-x", "version": SERVER_VERSION},
            "instructions": "Tools for one ToolBench-X request. Tools can fail or return drifted output; "
            "check results, retry or work around failures, then call submit_answer with the exact answer.",
        })
    if method == "ping":
        return rpc_result(rid, {})
    if method == "tools/list":
        return rpc_result(rid, {"tools": session.tools})
    if method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}
        if not isinstance(name, str) or not isinstance(args, dict):
            return rpc_error(rid, -32602, "tools/call needs a name and an arguments object")
        text, is_error = session.call(name, args)
        return rpc_result(rid, {"content": [{"type": "text", "text": text}], "isError": is_error})
    return rpc_error(rid, -32601, f"method {method} not found")


def make_handler(session, mcp_path):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, fmt, *args):
            sys.stderr.write("tbxsvc: " + (fmt % args) + "\n")

        def _send(self, code, body=None, ctype="application/json"):
            data = b"" if body is None else (body if isinstance(body, bytes) else json.dumps(body).encode())
            self.send_response(code)
            if body is not None:
                self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            if self.path == "/healthz":
                return self._send(200, b"ok\n", "text/plain")
            if self.path.split("?")[0] == mcp_path:
                return self._send(405, {"error": "this server sends no event stream; POST JSON-RPC instead"})
            return self._send(404, {"error": "not found"})

        def do_DELETE(self):
            return self._send(405 if self.path.split("?")[0] == mcp_path else 404, {"error": "not supported"})

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
            if isinstance(msg, list):
                out = [r for r in (handle_rpc(session, m) for m in msg) if r is not None]
                return self._send(200, out) if out else self._send(202)
            out = handle_rpc(session, msg)
            return self._send(202) if out is None else self._send(200, out)

    return Handler


def cmd_serve(a):
    with open(a.scenario, encoding="utf-8") as f:
        scenario = json.load(f)
    session = Session(scenario, a.tools, a.state)
    srv = ThreadingHTTPServer(("0.0.0.0", a.port), make_handler(session, a.mcp_path))
    sys.stderr.write(f"tbxsvc: {scenario['task']} on :{a.port}{a.mcp_path}\n")
    srv.serve_forever()


def cmd_verify(a):
    with open(os.path.join(a.tests, "expected.json"), encoding="utf-8") as f:
        expected = json.load(f)
    p = os.path.join(a.state, "answer.json")
    try:
        with open(p, encoding="utf-8") as f:
            got = json.load(f)
    except (OSError, ValueError) as e:
        sys.stderr.write(f"tbxsvc verify: state {p} unreadable: {e}\n")
        return 2
    want = str(expected["final_answer"]).strip()
    ans = got.get("answer")
    actual = "" if ans is None else str(ans).strip()
    match = 1 if ans is not None and actual == want else 0
    details = {"reward": match, "dimensions": {"exact_match": match}, "expected": want,
               "answer": actual, "submitted": ans is not None, "submissions": got.get("submissions", 0),
               "rule": "str(answer).strip() == str(final_answer).strip() (upstream agent/evaluation.py)"}
    os.makedirs(a.out, exist_ok=True)
    write_json(os.path.join(a.out, "details.json"), details)
    write_json(os.path.join(a.out, "reward.json"), {"reward": match, "dimensions": details["dimensions"]})
    print(json.dumps(details, indent=1))
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(prog="tbxsvc")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--scenario", default="/task/scenario.json")
    s.add_argument("--tools", default="/task/tools.py")
    s.add_argument("--state", default="/state")
    s.add_argument("--port", type=int, default=8000)
    s.add_argument("--mcp-path", default="/mcp")
    v = sub.add_parser("verify")
    v.add_argument("--tests", default="/grader/tests")
    v.add_argument("--state", default="/grader/state/toolbench")
    v.add_argument("--out", default="/grader/out")
    a = p.parse_args(argv)
    if a.cmd == "serve":
        cmd_serve(a)
        return 0
    return cmd_verify(a)


if __name__ == "__main__":
    sys.exit(main())
