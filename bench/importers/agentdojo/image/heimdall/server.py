"""Heimdall AgentDojo service: one AgentDojo suite's tools as an MCP server.

The agent reaches this server through the per-run tool proxy over MCP
streamable HTTP. Each tool call runs upstream's own tool function against the
suite's environment, exactly as upstream's tool executor does, and the result
is formatted with upstream's default formatter (YAML).

A condition plants an attack through two environment variables, set by a
probe of layer service (ProbeApply.ServiceEnv): AGENTDOJO_INJECTION_TASK and
AGENTDOJO_ATTACK. Without them the environment is upstream's default (the
clean condition). The attack text goes into the injection vectors the user
task's ground truth reads, the same placement upstream's runner uses.

Environment:
  AGENTDOJO_SUITE              workspace | travel | banking | slack
  AGENTDOJO_BENCHMARK_VERSION  upstream benchmark version, e.g. v1.2.2
  AGENTDOJO_USER_TASK          the task being run, e.g. user_task_0
  AGENTDOJO_INJECTION_TASK     injection task id (attack conditions only)
  AGENTDOJO_ATTACK             attack name (attack conditions only)
  AGENTDOJO_MODEL_NAME         optional name the attack addresses the model by
  AGENTDOJO_PORT               listen port (default 8000)
  AGENTDOJO_STATE              state file (default /state/run.json)

After every call the server rewrites the state file: the configuration, the
planted text, the environment before the run and now (as JSON to read, and as
an exact pickle for the verifier), and every call it answered. The bench exports that file after the agent exits and the verifier
decides from it with upstream's utility and security functions.
"""

import json
import os
import sys
import threading
import uuid
from ast import literal_eval
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, "/opt/heimdall")

import common  # noqa: E402
from agentdojo.agent_pipeline.tool_execution import is_string_list, tool_result_to_str  # noqa: E402
from agentdojo.functions_runtime import FunctionsRuntime  # noqa: E402

PROTOCOL_VERSIONS = ["2025-06-18", "2025-03-26", "2024-11-05"]
MAX_BODY = 4 << 20


class Service:
    """The suite's environment and the record of every call made against it."""

    def __init__(self, env):
        self.suite_name = env.get("AGENTDOJO_SUITE", "")
        self.version = env.get("AGENTDOJO_BENCHMARK_VERSION", "")
        self.user_task_id = env.get("AGENTDOJO_USER_TASK", "")
        self.injection_task_id = env.get("AGENTDOJO_INJECTION_TASK", "")
        self.attack_name = env.get("AGENTDOJO_ATTACK", "")
        self.state_path = env.get("AGENTDOJO_STATE", "/state/run.json")
        if not self.suite_name or not self.version or not self.user_task_id:
            raise ValueError("AGENTDOJO_SUITE, AGENTDOJO_BENCHMARK_VERSION and AGENTDOJO_USER_TASK are required")
        if bool(self.injection_task_id) != bool(self.attack_name):
            raise ValueError("AGENTDOJO_INJECTION_TASK and AGENTDOJO_ATTACK are set together or not at all")
        self.suite = common.load_suite(self.version, self.suite_name)
        self.user_task = self.suite.get_user_task_by_id(self.user_task_id)
        injection_task, attack = None, None
        if self.attack_name:
            injection_task = self.suite.get_injection_task_by_id(self.injection_task_id)
            attack = common.build_attack(self.attack_name, self.suite, env.get("AGENTDOJO_MODEL_NAME", ""))
        self.injections = common.plan_injections(self.suite, self.user_task, injection_task, attack)
        self.env = common.build_environment(self.suite, self.user_task, self.injections)
        self.pre = self.env.model_copy(deep=True)
        self.runtime = FunctionsRuntime(self.suite.tools)
        self.calls = []
        self.lock = threading.Lock()
        self.write_state()

    def tools(self):
        """The MCP tool list: upstream names, descriptions and input schemas."""
        out = []
        for f in self.suite.tools:
            schema = f.parameters.model_json_schema()
            schema.pop("title", None)
            out.append({"name": f.name, "description": f.description, "inputSchema": schema})
        return out

    def call(self, name, args):
        """Runs one tool as upstream's tool executor does; returns (text, is_error)."""
        with self.lock:
            args = dict(args or {})
            if name not in self.runtime.functions:
                text, error, result = "", f"Invalid tool {name} provided.", None
            else:
                for k, v in list(args.items()):
                    if isinstance(v, str) and is_string_list(v):
                        args[k] = literal_eval(v)
                result, error = self.runtime.run_function(self.env, name, args)
                text = tool_result_to_str(result)
            served = common.served_vectors(result, self.injections) if error is None else []
            self.calls.append({
                "seq": len(self.calls) + 1,
                "function": name,
                "args": _jsonable(args),
                "result": text,
                "error": error,
                "injection_vectors": served,
            })
            self.write_state()
            return (error if error is not None else text), error is not None

    def write_state(self):
        """Rewrites the state file atomically."""
        served = [{"call": c["seq"], "tool": c["function"], "vectors": c["injection_vectors"]}
                  for c in self.calls if c["injection_vectors"]]
        state = {
            "format": common.STATE_FORMAT,
            "config": {
                "agentdojo_version": common.package_version(),
                "benchmark_version": self.version,
                "suite": self.suite_name,
                "user_task": self.user_task_id,
                "injection_task": self.injection_task_id,
                "attack": self.attack_name,
            },
            "injections": self.injections,
            "planted": {"armed": bool(self.injections), "vectors": sorted(self.injections), "served": served},
            "pre_environment": self.pre.model_dump(mode="json"),
            "post_environment": self.env.model_dump(mode="json"),
            "environment_pickle": {"pre": common.dump_environment(self.pre), "post": common.dump_environment(self.env)},
            "calls": self.calls,
        }
        tmp = self.state_path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(state, f, ensure_ascii=False)
        os.replace(tmp, self.state_path)


def _jsonable(value):
    """Returns value as plain JSON data (tool arguments can hold any JSON)."""
    return json.loads(json.dumps(value, default=str))


def handle_message(service, msg):
    """Answers one JSON-RPC message; returns None for notifications."""
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0":
        return _error(None, -32600, "invalid request")
    if "method" not in msg:
        return None  # a response from the client; nothing to answer
    mid, method, params = msg.get("id"), msg["method"], msg.get("params") or {}
    if "id" not in msg:
        return None
    if method == "initialize":
        asked = params.get("protocolVersion")
        version = asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0]
        return _result(mid, {
            "protocolVersion": version,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": "agentdojo-" + service.suite_name, "version": common.package_version()},
        })
    if method == "ping":
        return _result(mid, {})
    if method == "tools/list":
        return _result(mid, {"tools": service.tools()})
    if method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}
        if not isinstance(name, str) or not isinstance(args, dict):
            return _error(mid, -32602, "tools/call needs a name and an arguments object")
        text, is_error = service.call(name, args)
        return _result(mid, {"content": [{"type": "text", "text": text}], "isError": is_error})
    return _error(mid, -32601, f"method {method!r} not found")


def _result(mid, result):
    return {"jsonrpc": "2.0", "id": mid, "result": result}


def _error(mid, code, message):
    return {"jsonrpc": "2.0", "id": mid, "error": {"code": code, "message": message}}


class Handler(BaseHTTPRequestHandler):
    """MCP streamable HTTP over plain JSON responses; no server-sent stream."""

    service = None
    session = uuid.uuid4().hex

    def do_GET(self):
        if self.path == "/healthz":
            self._send(200, b"ok\n", "text/plain")
        elif self.path.startswith("/mcp"):
            self._send(405, b"", "text/plain", {"Allow": "POST"})
        else:
            self._send(404, b"", "text/plain")

    def do_DELETE(self):
        self._send(200 if self.path.startswith("/mcp") else 404, b"", "text/plain")

    def do_POST(self):
        if not self.path.startswith("/mcp"):
            self._send(404, b"", "text/plain")
            return
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > MAX_BODY:
            self._send(400, json.dumps(_error(None, -32700, "bad body length")).encode(), "application/json")
            return
        try:
            body = json.loads(self.rfile.read(length))
        except ValueError:
            self._send(400, json.dumps(_error(None, -32700, "parse error")).encode(), "application/json")
            return
        batch = isinstance(body, list)
        replies = [r for r in (handle_message(self.service, m) for m in (body if batch else [body])) if r is not None]
        if not replies:
            self._send(202, b"", "text/plain")
            return
        out = replies if batch else replies[0]
        self._send(200, json.dumps(out, ensure_ascii=False).encode(), "application/json",
                   {"Mcp-Session-Id": self.session})

    def _send(self, status, body, ctype, headers=None):
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        if body:
            self.wfile.write(body)

    def log_message(self, fmt, *args):
        sys.stderr.write("agentdojo-mcp: " + (fmt % args) + "\n")


def main():
    Handler.service = Service(os.environ)
    port = int(os.environ.get("AGENTDOJO_PORT", "8000"))
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    sys.stderr.write(f"agentdojo-mcp: {Handler.service.suite_name} {Handler.service.user_task_id} on :{port}"
                     f" (attack: {Handler.service.attack_name or 'none'})\n")
    server.serve_forever()


if __name__ == "__main__":
    main()
