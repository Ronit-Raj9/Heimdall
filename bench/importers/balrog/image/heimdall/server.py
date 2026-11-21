"""Heimdall BALROG service: one BALROG episode as an MCP server.

The agent reaches this server through the per-run tool proxy over MCP
streamable HTTP and plays the game with two tools: `observe` (the current
observation, the step count and the valid actions) and `act` (take one
action, get the next observation). The environment, its text observations,
its action space, its step limit and its progression metric are upstream's.

Environment:
  BALROG_ENV    babyai | babaisai | textworld | crafter | nle | minihack
  BALROG_TASK   upstream task name, e.g. BabyAI-MixedTrainLocal-v0/goto
  BALROG_SEED   episode seed (integer)
  BALROG_PORT   listen port (default 8000)
  BALROG_STATE  state file (default /state/run.json)

After every call the server rewrites the state file: the configuration,
every action (as sent and as applied), the step count, whether the episode
is over, the episode return and upstream's progression. The bench exports it
after the agent exits; the verifier grades from it and replays the actions.
"""

import json
import os
import sys
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, "/opt/heimdall")

import common  # noqa: E402
from game import Episode  # noqa: E402

PROTOCOL_VERSIONS = ["2025-06-18", "2025-03-26", "2024-11-05"]
MAX_BODY = 1 << 20
MAX_LIST = 120  # actions listed in full up to this many (NetHack has more)


class Service:
    def __init__(self, env):
        self.env_name = env.get("BALROG_ENV", "")
        self.task = env.get("BALROG_TASK", "")
        seed = env.get("BALROG_SEED", "")
        self.state_path = env.get("BALROG_STATE", "/state/run.json")
        if self.env_name not in common.ENVS or not self.task or not seed.lstrip("-").isdigit():
            raise ValueError("BALROG_ENV (one of %s), BALROG_TASK and an integer BALROG_SEED are required" % common.ENVS)
        self.seed = int(seed)
        self.episode = Episode(common.load_config(), self.env_name, self.task, self.seed)
        self.actions = common.action_list(self.episode.env)
        self.log = []
        self.lock = threading.Lock()
        self.write_state()

    def tools(self):
        valid = self.actions
        if valid:
            listed = ", ".join(valid[:MAX_LIST]) + (" ..." if len(valid) > MAX_LIST else "")
            act_desc = f"Take one action in the game and get the next observation. Valid actions: {listed}."
        else:
            act_desc = "Take one action (a text command, for example 'go north' or 'take key') and get the next observation."
        return [
            {"name": "observe", "description": "Return the current observation, the step count and limit, and the valid actions. Does not advance the game.",
             "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False}},
            {"name": "act", "description": act_desc,
             "inputSchema": {"type": "object", "properties": {"action": {"type": "string", "description": "exactly one action"}},
                             "required": ["action"], "additionalProperties": False}},
        ]

    def status(self):
        ep = self.episode
        lines = [f"Step {ep.steps} of {ep.max_steps}." + (" The episode is over." if ep.done else "")]
        lines.append(f"Progress: {ep.progression():.3f}")
        return "\n".join(lines)

    def observe(self):
        out = self.status() + "\n\nObservation:\n" + self.episode.observation
        if self.actions:
            out += "\n\nValid actions: " + ", ".join(self.actions)
        return out

    def call(self, name, args):
        with self.lock:
            if name == "observe":
                return self.observe(), False
            if name != "act":
                return f"unknown tool {name!r}", True
            action = args.get("action")
            if not isinstance(action, str):
                return "act needs a string argument 'action'", True
            if self.episode.done:
                return "The episode is over; no more actions are taken.\n\n" + self.status(), True
            used, text, reward = self.episode.step(action.strip())
            self.log.append({"seq": len(self.log) + 1, "sent": action, "used": used, "reward": reward})
            self.write_state()
            return self.status() + "\n\nObservation:\n" + text, False

    def write_state(self):
        ep = self.episode
        state = {
            "format": common.STATE_FORMAT,
            "config": {"env": self.env_name, "task": self.task, "seed": self.seed},
            "steps": ep.steps,
            "max_steps": ep.max_steps,
            "done": ep.done,
            "episode_return": ep.episode_return,
            "progression": ep.progression(),
            "stats": _jsonable(ep.env.get_stats()),
            "actions": self.log,
        }
        tmp = self.state_path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(state, f, ensure_ascii=False)
        os.replace(tmp, self.state_path)


def _jsonable(value):
    return json.loads(json.dumps(value, default=str))


def handle_message(service, msg):
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0":
        return _error(None, -32600, "invalid request")
    if "method" not in msg or "id" not in msg:
        return None
    mid, method, params = msg.get("id"), msg["method"], msg.get("params") or {}
    if method == "initialize":
        asked = params.get("protocolVersion")
        return _result(mid, {
            "protocolVersion": asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0],
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": "balrog-" + service.env_name, "version": "1"},
        })
    if method == "ping":
        return _result(mid, {})
    if method == "tools/list":
        return _result(mid, {"tools": service.tools()})
    if method == "tools/call":
        name, args = params.get("name"), params.get("arguments") or {}
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
        self._send(200, json.dumps(replies if batch else replies[0], ensure_ascii=False).encode(),
                   "application/json", {"Mcp-Session-Id": self.session})

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
        sys.stderr.write("balrog-mcp: " + (fmt % args) + "\n")


def main():
    Handler.service = Service(os.environ)
    port = int(os.environ.get("BALROG_PORT", "8000"))
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    s = Handler.service
    sys.stderr.write(f"balrog-mcp: {s.env_name} {s.task} seed {s.seed} on :{port}\n")
    server.serve_forever()


if __name__ == "__main__":
    main()
