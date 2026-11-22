#!/usr/bin/env python3
"""AutomationBench as a bench service: MCP server, state export, verifier.

serve   runs one task's simulated business world (Gmail, Sheets, Slack,
        Salesforce, Zendesk, Jira, HubSpot and the other upstream apps) as an
        MCP streamable HTTP server (JSON responses, no server-sent events).
        The world starts from the task's initial state; the agent gets
        upstream's tools for the task's toolset. After every call the world
        is written to the state folder, which the bench exports after the
        agent exits.
verify  runs the task's assertions on the exported world the way upstream's
        rubric does (partial_credit, free assertions excluded) and writes the
        reward file the bench reads: the reward is partial_credit, and a task
        passes only when every scored assertion holds (upstream
        task_completed_correctly).
export  writes every upstream task (all domains, simple included) as JSON
        lines, for `bifrost-bench import automationbench`.

The upstream package (zapier/AutomationBench, MIT) is in the image at a
pinned commit; nothing here reaches the network. Two upstream imports are
replaced by offline stand-ins (stubs/): Hugging Face `datasets` (the domain
loaders only call Dataset.from_list) and `openai` (upstream calls a model for
Salesforce SOSL search and some SOQL filters when OPENAI_API_KEY is set; here
those calls fail as they do upstream without a key).
"""

import argparse
import copy
import inspect
import json
import os
import re
import sys
import tempfile
import threading
import typing
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import personas  # noqa: E402

SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ("2025-06-18", "2025-03-26", "2024-11-05")
MAX_BODY = 4 << 20
TOOLSETS = ("api", "limited_zapier")


def write_json(path, value):
    """Write value to path atomically, so an export never reads half a file."""
    d = os.path.dirname(path) or "."
    fd, tmp = tempfile.mkstemp(dir=d, prefix=".tmp-")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(value, f, ensure_ascii=False, separators=(",", ":"), default=str)
    os.replace(tmp, path)


def strip_none(obj):
    """upstream runner.strip_none_values: drop None values recursively."""
    if isinstance(obj, dict):
        return {k: strip_none(v) for k, v in obj.items() if v is not None}
    if isinstance(obj, list):
        return [strip_none(v) for v in obj if v is not None]
    return obj


def allowed_services(initial_state, assertions, tools):
    """upstream runner.compute_allowed_services (runner.py imports verifiers,
    which this image does not install): a service is in scope when the task
    seeds it, asserts on it, or grants one of its tools."""
    from automationbench.schema.world import WorldState

    fields = sorted((str(f) for f in WorldState.model_fields if f != "meta"), key=len, reverse=True)

    def service(name):
        for f in fields:
            if name == f or name.startswith(f + "_"):
                return f
        return None

    out = set()
    for k in initial_state:
        if k != "meta" and k in WorldState.model_fields:
            out.add(k)
    for a in assertions or []:
        s = service(str(a.get("type", "")))
        if s:
            out.add(s)
    for t in tools or []:
        s = service(t)
        if s:
            out.add(s)
    return sorted(out)


# ---------------------------------------------------------------- tool schemas

_ARGS_RE = re.compile(r"^\s*(\w+)\s*(?:\([^)]*\))?\s*:\s*(.*)$")


def parse_doc(doc):
    """Split a Google-style docstring into the summary and per-argument text."""
    doc = inspect.cleandoc(doc or "")
    summary, args, cur, section = [], {}, None, None
    for line in doc.splitlines():
        s = line.strip()
        if s in ("Args:", "Arguments:", "Parameters:"):
            section, cur = "args", None
            continue
        if s in ("Returns:", "Raises:", "Yields:", "Examples:", "Example:", "Note:", "Notes:") or (
            s.endswith(":") and not line.startswith(" ") and section == "args"
        ):
            section, cur = "other", None
            if s.startswith("Returns"):
                summary.append("")
                summary.append(s)
                section = "returns"
            continue
        if section == "args":
            m = _ARGS_RE.match(line)
            indent = len(line) - len(line.lstrip())
            if m and indent <= 4:
                cur = m.group(1)
                args[cur] = m.group(2).strip()
            elif cur and s:
                args[cur] = (args[cur] + " " + s).strip()
        elif section == "returns":
            if s:
                summary.append(s)
        elif section is None:
            summary.append(line)
    return "\n".join(summary).strip(), args


def tool_schema(fn):
    """The MCP tool of one upstream tool function: its name, docstring and a
    JSON schema of its parameters (the world argument hidden), as upstream's
    verifiers convert_func_to_oai_tool builds them from the signature."""
    from pydantic import create_model

    sig = inspect.signature(fn)
    hints = typing.get_type_hints(fn)
    summary, arg_doc = parse_doc(fn.__doc__)
    fields = {}
    for name, p in sig.parameters.items():
        if name == "world" or p.kind in (p.VAR_POSITIONAL, p.VAR_KEYWORD):
            continue
        ann = hints.get(name, typing.Any)
        default = ... if p.default is inspect.Parameter.empty else p.default
        fields[name] = (ann, default)
    model = create_model(fn.__name__ + "_args", **fields)
    schema = model.model_json_schema()
    schema.pop("title", None)
    for name, prop in (schema.get("properties") or {}).items():
        prop.pop("title", None)
        if arg_doc.get(name):
            prop["description"] = arg_doc[name]
    schema.setdefault("properties", {})
    return {"name": fn.__name__, "description": summary, "inputSchema": schema}


def toolset_functions(toolset, zapier_tools):
    if toolset == "api":
        from automationbench.tools.api import API_TOOLS

        return list(API_TOOLS)
    if toolset == "limited_zapier":
        from automationbench.tools import ALL_TOOLS

        by = {t.__name__: t for t in ALL_TOOLS}
        unknown = sorted(set(zapier_tools) - set(by))
        if unknown:
            raise SystemExit(f"abench: unknown tools in task: {unknown}")
        return [by[n] for n in zapier_tools]
    raise SystemExit(f"abench: toolset {toolset!r} is not one of {TOOLSETS}")


# ---------------------------------------------------------------- the clock


def bench_now():
    """The run clock's start (clock: field of the task, BENCH_NOW), or None."""
    v = os.environ.get("BENCH_NOW") or ""
    if not v:
        return None
    try:
        return datetime.fromisoformat(v.replace("Z", "+00:00")).astimezone(timezone.utc)
    except ValueError:
        sys.stderr.write(f"abench: BENCH_NOW {v!r} is not RFC 3339; ignored\n")
        return None


# ---------------------------------------------------------------- serve


class Session:
    """One task's live world and the calls made on it."""

    def __init__(self, scenario, state_dir, personas_path=None):
        from automationbench.schema.world import WorldState

        self.task = scenario["task_name"]
        self.state_dir = state_dir
        self.lock = threading.Lock()
        init = strip_none(copy.deepcopy(scenario["initial_state"]))
        now = bench_now()
        if now is not None and not (init.get("meta") or {}).get("current_time"):
            init.setdefault("meta", {})["current_time"] = now.isoformat().replace("+00:00", "Z")
        self.world = WorldState(**init)
        self.world.meta.allowed_services = list(scenario.get("allowed_services") or [])
        toolset = os.environ.get("ABENCH_TOOLSET") or scenario.get("toolset") or "api"
        self.fns = {f.__name__: f for f in toolset_functions(toolset, scenario.get("zapier_tools") or [])}
        self.tools = [tool_schema(f) for f in self.fns.values()]
        self.toolset = toolset
        self.calls = []
        self.colleagues = None
        doc = personas.load(personas_path)
        if doc is not None:  # Z17 simulated colleagues who reply (personas.py)
            self.colleagues = personas.Colleagues(doc, self.world)
        os.makedirs(state_dir, exist_ok=True)
        self._persist()

    def call(self, name, args):
        """Run one tool call; returns (text, is_error)."""
        with self.lock:
            fn = self.fns.get(name)
            if fn is None:
                return f"unknown tool {name!r}", True
            # upstream update_tool_args: an empty object means "no value".
            args = {k: v for k, v in args.items() if not (isinstance(v, dict) and len(v) == 0)}
            if "world" in inspect.signature(fn).parameters:
                args["world"] = self.world
            entry = {"seq": len(self.calls) + 1, "name": name,
                     "arguments": {k: v for k, v in args.items() if k != "world"}}
            try:
                out = fn(**args)
                text, err = (out if isinstance(out, str) else json.dumps(out, default=str)), False
            except Exception as e:  # verifiers hands a tool's exception back to the model
                text, err = f"Error: {type(e).__name__}: {e}", True
            entry["error"] = err
            entry["result"] = text[:2000]
            self.calls.append(entry)
            if self.colleagues is not None:
                self.colleagues.after_call(self.world, entry["seq"])
            self._persist()
            return text, err

    def _persist(self):
        write_json(os.path.join(self.state_dir, "world.json"), self.world.model_dump(mode="json"))
        doc = {"task": self.task, "toolset": self.toolset, "calls": self.calls}
        if self.colleagues is not None:
            doc["personas"] = self.colleagues.report()
        write_json(os.path.join(self.state_dir, "calls.json"), doc)


def rpc_result(rid, result):
    return {"jsonrpc": "2.0", "id": rid, "result": result}


def rpc_error(rid, code, message):
    return {"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}}


def handle_rpc(session, msg):
    """Answer one JSON-RPC message; None for a notification."""
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0" or not isinstance(msg.get("method"), str):
        return rpc_error(msg.get("id") if isinstance(msg, dict) else None, -32600, "invalid request")
    method, rid = msg["method"], msg.get("id")
    if "id" not in msg:
        return None
    params = msg.get("params") or {}
    if method == "initialize":
        asked = params.get("protocolVersion")
        version = asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0]
        return rpc_result(rid, {
            "protocolVersion": version,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": "automationbench", "version": SERVER_VERSION},
            "instructions": "Simulated business apps of AutomationBench. Only their final state is graded.",
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
            sys.stderr.write("abench: " + (fmt % args) + "\n")

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
    session = Session(scenario, a.state, a.personas)
    srv = ThreadingHTTPServer(("0.0.0.0", a.port), make_handler(session, a.mcp_path))
    sys.stderr.write(f"abench: task {session.task} ({session.toolset}, {len(session.tools)} tools) on :{a.port}{a.mcp_path}\n")
    srv.serve_forever()


# ---------------------------------------------------------------- verify


def score(grading, final_world):
    """upstream rubric.partial_credit on the exported world. Returns the
    details document (reward, dimensions, per-assertion results)."""
    from automationbench.rubric import partial_credit
    from automationbench.schema.world import WorldState

    world = WorldState(**final_world)
    state = {
        "info": {"assertions": [strip_none(x) for x in grading["assertions"]]},
        "world": world,
        "initial_state": strip_none(copy.deepcopy(grading["initial_state"])),
    }
    pc = float(partial_credit(state))
    done = 1.0 if pc == 1.0 else 0.0
    results = state.get("_assertion_results") or []
    return {
        "task": grading.get("task_name"),
        "reward": pc,
        "dimensions": {"partial_credit": pc, "task_completed_correctly": done},
        "scored": sum(1 for r in results if not r.get("excluded")),
        "passed": sum(1 for r in results if not r.get("excluded") and r.get("passed")),
        "assertions": results,
    }


def cmd_verify(a):
    with open(os.path.join(a.tests, "grading.json"), encoding="utf-8") as f:
        grading = json.load(f)
    p = os.path.join(a.state, "world.json")
    try:
        with open(p, encoding="utf-8") as f:
            final = json.load(f)
    except (OSError, ValueError) as e:
        sys.stderr.write(f"abench verify: state {p} unreadable: {e}\n")
        return 2
    details = score(grading, final)
    os.makedirs(a.out, exist_ok=True)
    write_json(os.path.join(a.out, "details.json"), details)
    write_json(os.path.join(a.out, "reward.json"), {"reward": details["reward"], "dimensions": details["dimensions"]})
    print(json.dumps({k: v for k, v in details.items() if k != "assertions"}))
    for r in details["assertions"]:
        status = "excluded" if r.get("excluded") else ("pass" if r["passed"] else "FAIL")
        print(f"  [{status}] {r['type']} {json.dumps(r.get('params'), default=str)[:300]}")
    return 0


# ---------------------------------------------------------------- export


def cmd_export(a):
    from automationbench.domains import DOMAINS
    from automationbench.task_contract import task_contract_sha256

    n = 0
    with open(a.out, "w", encoding="utf-8") as out:
        for domain, load in DOMAINS.items():
            for row in load():
                info = json.loads(row["info"]) if isinstance(row["info"], str) else row["info"]
                prompt = [{"role": m["role"], "content": m["content"]} for m in row["prompt"]]
                doc = {
                    "domain": domain,
                    "example_id": row["example_id"],
                    "task_name": info["task_name"],
                    "prompt": prompt,
                    "initial_state": info["initial_state"],
                    "assertions": info["assertions"],
                    "zapier_tools": info["zapier_tools"],
                    "allowed_services": allowed_services(strip_none(info["initial_state"]), info["assertions"], info["zapier_tools"]),
                    "contract_sha256": task_contract_sha256(example_id=row["example_id"], prompt=prompt, info=info),
                }
                out.write(json.dumps(doc, ensure_ascii=False, sort_keys=True) + "\n")
                n += 1
    print(f"{a.out}: {n} tasks")
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(prog="abench")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--scenario", default="/task/scenario.json")
    s.add_argument("--state", default="/state")
    s.add_argument("--port", type=int, default=8080)
    s.add_argument("--mcp-path", default="/mcp")
    s.add_argument("--personas", default="/task/personas.json", help="simulated colleagues (personas.py); absent means none")
    v = sub.add_parser("verify")
    v.add_argument("--tests", default="/grader/tests")
    v.add_argument("--state", default="/grader/state/apps")
    v.add_argument("--out", default="/grader/out")
    e = sub.add_parser("export")
    e.add_argument("--out", required=True)
    a = p.parse_args(argv)
    if a.cmd == "export":
        return cmd_export(a)
    if a.cmd == "serve":
        cmd_serve(a)
        return 0
    return cmd_verify(a)


if __name__ == "__main__":
    sys.exit(main())
