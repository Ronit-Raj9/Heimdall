#!/usr/bin/env python3
"""A tau2-bench domain as a bench service: MCP server, scripted user, verifier.

serve   runs one task's domain as an MCP streamable HTTP server (JSON
        responses, no server-sent events). The agent gets the domain's own
        tools, working on a fresh copy of the domain database seeded for the
        task, plus respond_to_user(message), which returns the simulated
        user's reply. After every change the database and the conversation
        are written to the state folder, which the bench exports after the
        agent exits.
verify  replays the task's expected actions on a fresh database and compares
        it with the exported one the way upstream does (hash of the whole
        database), checks the required communicated information against the
        agent's messages (upstream's substring scorer), and writes the reward
        file the bench reads.

The upstream package (tau2) is installed in the image; nothing here reaches
the network.
"""

import argparse
import json
import os
import re
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

STOP = "###STOP###"
TRANSFER = "###TRANSFER###"
OUT_OF_SCOPE = "###OUT-OF-SCOPE###"
STOP_TOKENS = (STOP, TRANSFER, OUT_OF_SCOPE)

SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ("2025-06-18", "2025-03-26", "2024-11-05")
MAX_BODY = 4 << 20

RESPOND_TOOL = {
    "name": "respond_to_user",
    "description": (
        "Send a message to the customer and get the customer's reply. This is the only "
        "way to talk to the customer. The conversation is over when the reply contains "
        "###STOP###, ###TRANSFER### or ###OUT-OF-SCOPE###."
    ),
    "inputSchema": {
        "type": "object",
        "properties": {"message": {"type": "string", "description": "What you say to the customer."}},
        "required": ["message"],
    },
}

STOPWORDS = set(
    """a an the and or but if then so to of in on at by for with from about into over
    is are was were be been being am do does did done have has had having can could
    would should will shall may might must you your yours me my mine i we our us it
    its this that these those there here what which who whom whose when where why how
    please thank thanks just also any some all not no yes ok okay let know need want
    tell give provide could confirm""".split()
)


_LANGS = {}


def make_env(domain, language="en_registry", retrieval=None, read_log_allowlist=None):
    """Build a fresh upstream environment of domain (its database from disk).

    language (any but the en_registry default) loads the tau-i18n text-v1 language pack's regional
    database and localized policy (upstream's load_language). banking_knowledge
    (tau3) builds the retrieval variant named by retrieval, in process, with
    the task's read-log allowlist as upstream's runner derives it.
    """
    from tau2.registry import registry

    if domain == "banking_knowledge":
        from tau2.domains.banking_knowledge.environment import get_environment

        return get_environment(retrieval_variant=retrieval or "bm25",
                               read_log_allowlist=set(read_log_allowlist or []))
    if language and language != "en_registry":
        from tau2.multilingual.runtime import language_environment_kwargs, load_language

        packs = _LANGS.setdefault(language, {})
        if domain not in packs:
            packs[domain] = load_language(language, domain, mode="text")
        return registry.get_env_constructor(domain)(**language_environment_kwargs(packs[domain]))
    return registry.get_env_constructor(domain)()


def env_of(doc):
    """make_env for a scenario.json or grading.json document."""
    return make_env(doc["domain"], doc.get("language") or "en_registry", doc.get("retrieval"), doc.get("read_log_allowlist"))


def apply_initial_state(env, task):
    """Seed env with the task's initialization data and actions, as upstream does."""
    init = task.initial_state
    if init is None:
        return
    env.set_state(
        initialization_data=init.initialization_data,
        initialization_actions=init.initialization_actions,
        message_history=[],
    )


def dump_db(db):
    return None if db is None else db.model_dump(mode="json")


def write_json(path, value):
    """Write value to path atomically, so an export never reads half a file."""
    d = os.path.dirname(path)
    fd, tmp = tempfile.mkstemp(dir=d, prefix=".tmp-")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(value, f, ensure_ascii=False, separators=(",", ":"))
    os.replace(tmp, path)


def words(text):
    out = set()
    for w in re.findall(r"[a-z0-9_#@.]+", text.lower()):
        w = w.strip(".")
        if len(w) > 2 and w not in STOPWORDS:
            out.add(w)
    return out


def sentences(text):
    if not text:
        return []
    parts = re.split(r"(?<=[.!?])\s+|\n+", text.strip())
    return [p.strip() for p in parts if p.strip()]


class ScriptedUser:
    """A deterministic user built from the task's user scenario.

    The first reply states the whole request and everything the scenario says
    the user knows. Later replies confirm when asked to confirm, answer a
    question from the known information when it shares words with it, say they
    do not know otherwise, acknowledge a statement, and stop when the agent
    closes the conversation or after max_turns replies. It is not upstream's
    model-played user: it does not follow the scenario's conditional
    instructions turn by turn.
    """

    CONFIRM = (
        "confirm", "proceed", "go ahead", "shall i", "should i", "would you like me to",
        "do you want me to", "is that correct", "is this correct", "(yes/no)", "yes or no",
        "do you agree", "okay to",
    )
    TRANSFER_MARKS = ("transferred to a human agent", "transfer you to a human", "transferring you to a human")
    DONE_MARKS = (
        "anything else", "is there something else", "is there anything more", "have a great day",
        "have a nice day", "goodbye", "good bye", "glad i could help", "take care",
    )

    def __init__(self, instructions, max_turns=30):
        if isinstance(instructions, str):
            self.reason, self.known, self.unknown, self.how = instructions, None, None, None
        else:
            self.reason = instructions.reason_for_call
            self.known = instructions.known_info
            self.unknown = instructions.unknown_info
            how = (instructions.task_instructions or "").strip()
            self.how = how if how and how != "." else None
        self.max_turns = max_turns
        self.turns = 0

    def opening(self):
        parts = ['Hello. Here are my notes about my request ("you" in them means me, the customer).']
        parts.append("Why I am contacting you:\n" + self.reason.strip())
        if self.known:
            parts.append("What I know:\n" + self.known.strip())
        if self.unknown:
            parts.append("What I do not know:\n" + self.unknown.strip())
        if self.how:
            parts.append("How I want this handled:\n" + self.how)
        return "\n\n".join(parts)

    def reply(self, message):
        self.turns += 1
        if self.turns == 1:
            return self.opening()
        low = message.lower()
        if any(m in low for m in self.TRANSFER_MARKS):
            return TRANSFER
        if self.turns > self.max_turns:
            return "I have to go now. " + STOP
        if any(m in low for m in self.DONE_MARKS):
            return "No, that is all. Thank you. " + STOP
        known = self.known_matching(message)
        if any(m in low for m in self.CONFIRM):
            return " ".join(["Yes, I confirm. Please go ahead."] + known)
        if "?" in message:
            if known:
                return " ".join(known)
            if any(words(s) & words(message) for s in sentences(self.unknown)):
                return "I do not know that, sorry."
            return "I do not know. Everything I know is in my first message."
        return "Okay, thank you."

    def known_matching(self, message):
        """The sentences of the known information that share a word with message."""
        q = words(message)
        return [s for s in sentences(self.known) if words(s) & q]


class LLMUserAdapter:
    """upstream's model-played user, through bench/simuser (simuser.py).

    The prompt is tau2-user-v1: upstream's text user guidelines and scenario
    block (see simuser/prompts/tau2-user-v1.txt for the source and commit),
    filled from the task's user scenario, sent through the gateway on this
    service's own virtual key. Every call is recorded in the transcript's
    sim_calls; a failed call is a tool error the agent sees, counted in
    simulator_errors, never a customer reply.
    """

    def __init__(self, user, transcript):
        self.user, self.transcript = user, transcript

    def reply(self, message):
        import simuser

        try:
            text, meta = self.user.reply(message, None)
        except simuser.SimulatorError as e:
            self.transcript["simulator_errors"] = self.transcript.get("simulator_errors", 0) + 1
            self.transcript.setdefault("sim_calls", []).append({"ok": False, "error": str(e)})
            raise
        if "model_served" in meta:
            self.transcript.setdefault("sim_calls", []).append(dict(meta, ok=True))
        return text


def llm_user(instructions, max_turns, transcript):
    """Build the model-played user from the bench's environment."""
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    import simuser

    env = os.environ
    version = env.get("BENCH_SIM_PROMPT_VERSION") or "tau2-user-v1"
    template, sha = simuser.prompt_text(version)
    want = env.get("BENCH_SIM_PROMPT_SHA256") or ""
    if want and want != sha:
        raise SystemExit(f"tau2svc: prompt {version} hashes to {sha}, the manifest pinned {want}")
    how = (getattr(instructions, "task_instructions", "") or "").strip()
    persona = {
        "goal": getattr(instructions, "reason_for_call", None) or str(instructions),
        "facts": [getattr(instructions, "known_info", None) or "(nothing)"],
        "unknown": [getattr(instructions, "unknown_info", None) or "(nothing)"],
        "behaviour": how if how and how != "." else "(none)",
    }
    seed = int(env.get("BENCH_SIM_SEED") or 0)
    client = simuser.GatewayClient(env.get("BENCH_SVC_GATEWAY_URL"), env.get("BENCH_SVC_GATEWAY_KEY"), env.get("BENCH_SVC_MODEL"), seed)
    os.environ.pop("BENCH_SVC_GATEWAY_KEY", None)
    transcript.update({"model": client.model, "prompt_version": version, "prompt_sha256": sha, "seed": seed, "simulator_errors": 0, "sim_calls": []})
    return LLMUserAdapter(simuser.LLMUser(persona, max_turns, client, template, env.get("BENCH_SIM_HOSTILE") or ""), transcript)


class Session:
    """One task's live domain: the environment, the user, and the transcript."""

    def __init__(self, scenario, state_dir):
        from tau2.data_model.tasks import Task

        self.domain = scenario["domain"]
        self.task = Task.model_validate(scenario["task"])
        self.state_dir = state_dir
        self.lock = threading.Lock()
        self.env = env_of(scenario)
        apply_initial_state(self.env, self.task)
        # The bench's simulator settings (BENCH_SIM_*) win over the import's.
        mode = os.environ.get("BENCH_SIM_MODE") or scenario.get("user", "scripted")
        max_turns = int(os.environ.get("BENCH_SIM_MAX_TURNS") or scenario.get("max_user_turns", 30))
        self.transcript = {
            "task_id": self.task.id,
            "domain": self.domain,
            "user_mode": mode,
            "ended_by": None,
            "events": [],
        }
        if mode == "scripted":
            self.user = ScriptedUser(self.task.user_scenario.instructions, max_turns)
        elif mode == "llm":
            self.user = llm_user(self.task.user_scenario.instructions, max_turns, self.transcript)
        else:
            raise SystemExit(f"user mode {mode!r} is not scripted or llm")
        self.tools = self._tool_list()
        os.makedirs(state_dir, exist_ok=True)
        self._persist(db=True)

    def _tool_list(self):
        out = []
        for tool in self.env.get_tools():
            fn = tool.openai_schema["function"]
            out.append({"name": fn["name"], "description": fn.get("description", ""), "inputSchema": fn["parameters"]})
        out.append(RESPOND_TOOL)
        return out

    def call(self, name, args):
        """Run one tool call; returns (text, is_error)."""
        with self.lock:
            if name == RESPOND_TOOL["name"]:
                return self._respond(args)
            return self._domain_call(name, args)

    def _respond(self, args):
        msg = args.get("message")
        if not isinstance(msg, str):
            return "respond_to_user needs a string argument message", True
        ev = {"seq": len(self.transcript["events"]) + 1, "role": "assistant", "content": msg}
        if self.transcript["ended_by"] is not None:
            ev["after_end"] = True
            self.transcript["events"].append(ev)
            self._persist(db=False)
            return "The customer has left the conversation. End your session.", False
        self.transcript["events"].append(ev)
        try:
            reply = self.user.reply(msg)
        except Exception as e:  # simuser.SimulatorError: the model-played user failed
            if type(e).__name__ != "SimulatorError":
                raise
            self._persist(db=False)
            return "The customer did not answer (the user simulator failed). Try again.", True
        self.transcript["events"].append({"seq": len(self.transcript["events"]) + 1, "role": "user", "content": reply})
        for tok in STOP_TOKENS:
            if tok in reply:
                self.transcript["ended_by"] = tok
                break
        self._persist(db=False)
        return reply, False

    def _domain_call(self, name, args):
        from tau2.data_model.message import ToolCall

        seq = len(self.transcript["events"]) + 1
        if self.transcript["ended_by"] is not None:
            # Upstream's simulation is over once the user stops: no later call runs.
            text = "Error: the conversation has ended; no further actions are possible."
            self.transcript["events"].append(
                {"seq": seq, "role": "tool", "name": name, "arguments": args, "content": text, "error": True, "after_end": True}
            )
            self._persist(db=False)
            return text, True
        tc = ToolCall(id=f"call_{seq}", name=name, arguments=args, requestor="assistant")
        resp = self.env.get_response(tc)
        mutating = self.env._has_tool(name) and self.env._is_mutating_tool(name)
        self.transcript["events"].append(
            {"seq": seq, "role": "tool", "name": name, "arguments": args, "content": resp.content, "error": bool(resp.error)}
        )
        self._persist(db=mutating)
        return resp.content, bool(resp.error)

    def _persist(self, db):
        if db:
            write_json(os.path.join(self.state_dir, "db.json"), {
                "domain": self.domain,
                "task_id": self.task.id,
                "db": dump_db(self.env.tools.db),
                "db_hash": self.env.get_db_hash(),
                "user_db": dump_db(self.env.user_tools.db) if self.env.user_tools is not None else None,
                "user_db_hash": self.env.get_user_db_hash(),
            })
        write_json(os.path.join(self.state_dir, "transcript.json"), self.transcript)


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
            "serverInfo": {"name": f"tau2-{session.domain}", "version": SERVER_VERSION},
            "instructions": "Customer service tools of the tau2 " + session.domain
            + " domain. Talk to the customer only with respond_to_user.",
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
            sys.stderr.write("tau2svc: " + (fmt % args) + "\n")

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
    session = Session(scenario, a.state)
    srv = ThreadingHTTPServer(("0.0.0.0", a.port), make_handler(session, a.mcp_path))
    sys.stderr.write(f"tau2svc: {session.domain} task {session.task.id} on :{a.port}{a.mcp_path}\n")
    srv.serve_forever()


def substring_met(info, agent_messages):
    """Upstream's substring scorer: case folded, commas dropped from the message."""
    return any(info.lower() in m.lower().replace(",", "") for m in agent_messages)


def evaluate(task_doc, grading, state):
    """Score one finished conversation. Returns the details document."""
    from tau2.data_model.tasks import Task

    task = Task.model_validate(task_doc)
    domain = grading["domain"]
    crit = task.evaluation_criteria
    basis = [str(getattr(b, "value", b)) for b in (crit.reward_basis if crit else [])]
    details = {"task_id": task.id, "domain": domain, "reward_basis": basis, "dimensions": {}, "notes": []}
    unsupported = sorted(set(basis) - {"DB", "ACTION", "COMMUNICATE", "NL_ASSERTION"})
    if unsupported:
        raise SystemExit(f"reward basis {unsupported} is not supported by this verifier")
    if crit is None:
        details["reward"] = 1.0
        details["notes"].append("task has no evaluation criteria")
        return details

    db_doc, transcript = state["db"], state["transcript"]
    if "DB" in basis:
        if crit.actions is None and crit.env_assertions is None:
            details["dimensions"]["db"] = 1.0
            details["notes"].append("no expected actions: database counts as matching, as upstream")
        else:
            gold = env_of(grading)
            apply_initial_state(gold, task)
            failed = []
            for act in crit.actions or []:
                try:
                    gold.make_tool_call(tool_name=act.name, requestor=act.requestor, **act.arguments)
                except Exception as e:  # upstream logs and continues
                    failed.append(f"{act.name}: {e}")
            if failed:
                details["notes"].append("expected actions that raised (ignored, as upstream): " + "; ".join(failed))
            got = type(gold.tools.db).model_validate(db_doc["db"]).get_hash()
            match = got == gold.get_db_hash()
            if gold.user_tools is not None:
                got_user = type(gold.user_tools.db).model_validate(db_doc["user_db"]).get_hash()
                match = match and got_user == gold.get_user_db_hash()
            if db_doc.get("db_hash") and db_doc["db_hash"] != got:
                details["notes"].append("exported db_hash differs from the hash of the exported database")
            details["dimensions"]["db"] = 1.0 if match else 0.0
    if "ACTION" in basis:
        # Upstream's ActionEvaluator: every expected action appears among the
        # calls made (name, and the compare_args, or all the call's arguments).
        calls = [e for e in transcript.get("events", []) if e.get("role") == "tool" and not e.get("after_end")]
        checks = []
        for act in crit.actions or []:
            def match(ev):
                if ev.get("name") != act.name:
                    return False
                got = ev.get("arguments") or {}
                keys = list(got.keys()) if act.compare_args is None else act.compare_args
                if not keys:
                    return True
                return {k: v for k, v in got.items() if k in keys} == {k: v for k, v in (act.arguments or {}).items() if k in keys}
            checks.append({"action": act.name, "requestor": act.requestor, "met": any(match(e) for e in calls)})
        details["action_checks"] = checks
        details["dimensions"]["action"] = 1.0 if all(c["met"] for c in checks) else 0.0
    if "COMMUNICATE" in basis:
        agent = [e["content"] for e in transcript.get("events", []) if e.get("role") == "assistant" and not e.get("after_end")]
        checks = [{"info": i, "met": substring_met(i, agent)} for i in (crit.communicate_info or [])]
        details["communicate_checks"] = checks
        details["dimensions"]["communicate"] = 1.0 if all(c["met"] for c in checks) else 0.0
    if "NL_ASSERTION" in basis and crit.nl_assertions:
        details["notes"].append(
            f"{len(crit.nl_assertions)} natural-language assertion(s) need a judge model and are not scored here"
        )
    reward = 1.0
    for v in details["dimensions"].values():
        reward *= v
    details["reward"] = reward
    details["ended_by"] = transcript.get("ended_by")
    return details


def cmd_verify(a):
    with open(os.path.join(a.tests, "task.json"), encoding="utf-8") as f:
        task_doc = json.load(f)
    with open(os.path.join(a.tests, "grading.json"), encoding="utf-8") as f:
        grading = json.load(f)
    state = {}
    for key in ("db", "transcript"):
        p = os.path.join(a.state, key + ".json")
        try:
            with open(p, encoding="utf-8") as f:
                state[key] = json.load(f)
        except (OSError, ValueError) as e:
            sys.stderr.write(f"tau2svc verify: state {p} unreadable: {e}\n")
            return 2
    details = evaluate(task_doc, grading, state)
    os.makedirs(a.out, exist_ok=True)
    write_json(os.path.join(a.out, "details.json"), details)
    write_json(os.path.join(a.out, "reward.json"), {"reward": details["reward"], "dimensions": details["dimensions"]})
    print(json.dumps(details, indent=1))
    return 0


def cmd_export(a):
    """Write the tasks, policy and write tools of one domain (and language pack)
    as upstream builds them, for `bifrost-bench import tau2 -set export_dir=...`."""
    from tau2.registry import registry

    if a.domain == "banking_knowledge":
        tasks = registry.get_tasks_loader(a.domain)()
        subset = "banking_knowledge_%d" % len(tasks)
    else:
        from tau2.multilingual.runtime import load_language

        loaded = load_language(a.language, a.domain, mode="text")
        _LANGS.setdefault(a.language, {})[a.domain] = loaded
        tasks = loaded.tasks
        subset = "text-v1/%s/%s" % (a.language, loaded.pack.revision if hasattr(loaded.pack, "revision") else "")
    env = make_env(a.domain, a.language if a.domain != "banking_knowledge" else "en_registry", a.retrieval)
    writes = sorted(t.name for t in env.get_tools() if env._is_mutating_tool(t.name))
    doc = {
        "format": "tau2-bench-export/1",
        "domain": a.domain,
        "language": a.language,
        "retrieval": a.retrieval if a.domain == "banking_knowledge" else None,
        "subset": subset,
        "policy": env.policy,
        "mutating_tools": writes,
        "tasks": [t.model_dump(mode="json") for t in tasks],
    }
    os.makedirs(os.path.dirname(os.path.abspath(a.out)), exist_ok=True)
    with open(a.out, "w", encoding="utf-8") as f:
        json.dump(doc, f, ensure_ascii=False, indent=1)
    print(f"{a.out}: {len(tasks)} tasks, {len(writes)} write tools")
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(prog="tau2svc")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--scenario", default="/task/scenario.json")
    s.add_argument("--state", default="/state")
    s.add_argument("--port", type=int, default=8000)
    s.add_argument("--mcp-path", default="/mcp")
    v = sub.add_parser("verify")
    v.add_argument("--tests", default="/grader/tests")
    v.add_argument("--state", default="/grader/state/tau2")
    v.add_argument("--out", default="/grader/out")
    e = sub.add_parser("export")
    e.add_argument("--domain", required=True)
    e.add_argument("--language", default="en")
    e.add_argument("--retrieval", default="bm25")
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
