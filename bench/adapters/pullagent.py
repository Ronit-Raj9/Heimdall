"""Launcher of a pull agent (adapters/pull.go, docs/pull-agents.md).

The agent runs in the customer's own process (heimdall.serve in the Python
or TypeScript SDK), which connects out to Heimdall. This program runs in the
run's container and is bench code: nothing of the agent runs here. Its only
route out is Heimdall's pull endpoint (BENCH_PULL_URL, through the run's
forwarder at BENCH_PULL_CONNECT), where it authenticates with the run's
signed pull token (BENCH_PULL_TOKEN).

Each turn is queued for the agent (POST <pull>/turns) and its answer
awaited (GET <pull>/turns/<id>, a long poll). A turn's payload:

  {"kind": "task" | "conversation" | "tool_results", "task_id",
   "session_id", "session_index", "output", "instruction",
   "tools": [{"name", "description", "input_schema"}],
   "messages": [... the turn's chat so far, OpenAI roles ...],
   "turn"?, "message"? (conversation), "repo"? (coding tasks)}

The answer: {"text"?, "tool_calls"?: [{"id", "name", "arguments"}],
"patch"? (a unified diff), "usage"?, "version"?}. Tool calls are run with
MCP tools/call on the run's tool proxy, so the proxy records them like any
agent's, and their results go back as the next turn (kind tool_results),
until the agent answers without one or BENCH_PULL_TOOL_ROUNDS is reached.

With BENCH_CONV_USERS the bench drives a chat task (heimdall_conv.drive):
each simulated customer message is a conversation turn and the agent's text
is its reply; respond_to_user is never offered. With BENCH_REPO=tarball the
first turn carries a signed link to the workspace tarball, and the "patch"
of the final answer is applied to the workspace.

stdout carries the agent event protocol (model_request, model_response,
tool_call, tool_result, claim) and the shared helpers' conversation events.
"""

import json
import os
import re
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import heimdall_conv as hc  # noqa: E402

SCHEMA = "bifrost.agent.event"
MAX_TOOL_RESULT = 64 << 10
MAX_SUMMARY = 300
SECRET_RE = re.compile(r"(?i)(sk-[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._~+/=-]{8,}|hpr1\.[A-Za-z0-9._-]{8,})")
NAME_RE = re.compile(r"[^A-Za-z0-9_-]")
SESSION_INDEX = int(os.environ.get("BENCH_PULL_SESSION_INDEX", "1") or "1")
SEQ = [0]
REPORT = {"type": "pull_report", "turns": 0, "tool_calls": 0}


def log(msg):
    print("pull: " + msg, file=sys.stderr, flush=True)


def clean(value, limit=MAX_SUMMARY):
    if not isinstance(value, str):
        value = json.dumps(value) if value is not None else ""
    value = "".join(c for c in value if c.isprintable())
    return SECRET_RE.sub("[redacted]", value)[:limit]


def now():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def event(kind, **fields):
    SEQ[0] += 1
    eid = "s%d-e%d" % (SESSION_INDEX, SEQ[0])
    ev = {"schema": SCHEMA, "version": "1", "type": kind, "timestamp": now(), "id": eid}
    ev.update({k: v for k, v in fields.items() if v not in (None, "")})
    print(json.dumps(ev, sort_keys=True), flush=True)
    return eid


def emit(doc):
    print(json.dumps(doc, sort_keys=True), flush=True)


def fail(code, message):
    REPORT["error"] = clean(message, 500)
    emit(REPORT)
    event("error", summary=clean(message), status="error", is_error=True)
    log(message)
    sys.exit(code)


# ---- tools ------------------------------------------------------------------

def load_tools(conversation):
    """name -> (MCP client, tool name, description, schema). respond_to_user
    is left out when the bench drives the conversation."""
    listed = []
    for sid, url, headers in hc.servers_of(os.environ.get("BENCH_MCP_SERVERS", "")):
        client = hc.MCP(url, headers, client="heimdall-pull")
        try:
            for t in client.open():
                if isinstance(t, dict) and t.get("name") and not (conversation and t["name"] == hc.RESPOND_TOOL):
                    listed.append((sid, client, t))
        except Exception as e:  # noqa: BLE001 - a server that will not list is skipped
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
            name = base[:60] + "_%d" % n
            n += 1
        schema = t.get("inputSchema") if isinstance(t.get("inputSchema"), dict) else {"type": "object", "properties": {}}
        out[name] = (client, t["name"], str(t.get("description") or "")[:1024], schema)
    return out


def call_tool(tools, name, args):
    if name not in tools:
        return "unknown tool %s" % name, True
    client, tool, _, _ = tools[name]
    if isinstance(args, str):
        try:
            args = json.loads(args) if args.strip() else {}
        except ValueError:
            return "tool arguments are not JSON", True
    if args is None:
        args = {}
    if not isinstance(args, dict):
        return "tool arguments are not a JSON object", True
    try:
        res = client.rpc("tools/call", {"name": tool, "arguments": args})
    except Exception as e:  # noqa: BLE001
        return "tool call failed: %s" % clean(str(e), 1000), True
    return hc.result_text(res)[:MAX_TOOL_RESULT], bool(res.get("isError"))


# ---- turns -------------------------------------------------------------------

class Agent:
    def __init__(self, server, base, tools, deadline):
        self.server, self.base, self.tools, self.deadline = server, base, tools, deadline
        self.turn_s = max(10, int(os.environ.get("BENCH_PULL_TURN_S", "300") or "300"))
        self.rounds = max(1, int(os.environ.get("BENCH_PULL_TOOL_ROUNDS", "20") or "20"))
        self.last = {}

    def ask(self, payload):
        """Queues one turn and waits for its answer object."""
        left = self.deadline - time.time()
        if left <= 1:
            fail(1, "the run's time ran out before the agent answered")
        body = {"timeout_s": int(min(self.turn_s, left)), "payload": payload}
        req = event("model_request", summary="%s turn" % payload.get("kind", "task"))
        try:
            status, doc = self.server.request("POST", "/turns", body=body, timeout=60)
        except OSError as e:
            fail(1, "Heimdall's pull endpoint is not reachable: %s" % e)
        if status == 409:
            fail(1, hc.error_of(doc))
        if status // 100 != 2 or not doc.get("id"):
            fail(1, "Heimdall refused the turn (HTTP %d): %s" % (status, hc.error_of(doc)))
        tid = doc["id"]
        REPORT["turns"] += 1
        started = time.time()
        while True:
            try:
                status, res = self.server.request("GET", "/turns/%s?wait=25" % tid, timeout=60)
            except OSError as e:
                if time.time() > self.deadline:
                    fail(1, "Heimdall's pull endpoint is not reachable: %s" % e)
                time.sleep(1)
                continue
            if status == 202:
                if time.time() > self.deadline:
                    fail(1, "the agent did not answer before the run's timeout")
                continue
            if status != 200:
                fail(1, "reading the answer failed (HTTP %d): %s" % (status, hc.error_of(res)))
            if res.get("status") == "failed":
                fail(1, "the agent did not answer: %s" % clean(res.get("error", ""), 500))
            answer = res.get("answer")
            if not isinstance(answer, dict):
                fail(1, "the agent's answer is not a JSON object")
            usage = answer.get("usage") if isinstance(answer.get("usage"), dict) else None
            tokens = None
            if usage:
                inp, out = usage.get("input_tokens", usage.get("prompt_tokens")), usage.get("output_tokens", usage.get("completion_tokens"))
                if isinstance(inp, int) and isinstance(out, int) and inp >= 0 and out >= 0:
                    tokens = {"fresh": inp, "out": out}
            event("model_response", parent_event_id=req, summary=clean(answer.get("text", "")), tokens=tokens,
                  latency_ms=int((time.time() - started) * 1000))
            self.last = answer
            for k in ("version", "build"):
                if clean(answer.get(k)):
                    REPORT[k] = clean(answer.get(k))
            return answer

    def run(self, messages, extra):
        """One agent turn: asks, runs tool calls, asks again, until text.
        messages is extended in place. Returns the answer object."""
        kind = extra.pop("kind", "task")
        for rnd in range(1, self.rounds + 1):
            payload = dict(self.base, kind=kind, messages=messages, **extra)
            answer = self.ask(payload)
            calls = [c for c in (answer.get("tool_calls") or []) if isinstance(c, dict)]
            text = answer.get("text") if isinstance(answer.get("text"), str) else ""
            if not calls:
                messages.append({"role": "assistant", "content": text})
                return answer
            if not self.tools:
                fail(1, "the agent asked for tool calls, and this run offers it no tools")
            norm = []
            for i, c in enumerate(calls):
                cid = str(c.get("id") or "call_%d_%d" % (rnd, i))
                norm.append({"id": cid, "type": "function", "function": {"name": str(c.get("name", "")),
                             "arguments": c.get("arguments") if isinstance(c.get("arguments"), str) else json.dumps(c.get("arguments") or {})}})
            messages.append({"role": "assistant", "content": text or None, "tool_calls": norm})
            for c in norm:
                name, args = c["function"]["name"], c["function"]["arguments"]
                ev = event("tool_call", tool_call_id=c["id"], tool=clean(name, 128), target=clean(args, 200), status="started")
                out, is_err = call_tool(self.tools, name, args)
                event("tool_result", parent_event_id=ev, tool_call_id=c["id"], tool=clean(name, 128),
                      status="error" if is_err else "success", is_error=is_err, summary=clean(out))
                messages.append({"role": "tool", "tool_call_id": c["id"], "content": out})
                REPORT["tool_calls"] += 1
            kind = "tool_results"
            extra = {k: v for k, v in extra.items() if k != "repo"}
            if time.time() >= self.deadline:
                fail(1, "timed out after %d tool rounds" % rnd)
        fail(1, "no final answer after %d tool rounds (pull.max_tool_rounds)" % self.rounds)


def write_answer(path, text):
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


def main():
    timeout = float(os.environ.get("BENCH_PULL_TIMEOUT_S", "600"))
    deadline = time.time() + timeout
    try:
        server = hc.Server(os.environ["BENCH_PULL_URL"].rstrip("/"), os.environ.get("BENCH_PULL_TOKEN", ""), os.environ.get("BENCH_PULL_CONNECT", ""))
    except (KeyError, ValueError) as e:
        fail(2, "no pull endpoint: %s" % e)
    with open(os.environ["BENCH_PULL_INSTRUCTION"], encoding="utf-8") as f:
        instruction = f.read()
    output = os.environ.get("BENCH_PULL_OUTPUT", "answer")
    answer_path = os.environ.get("BENCH_ANSWER_PATH", "answer.txt")
    conversation = hc.active()
    tools = load_tools(conversation) if os.environ.get("BENCH_MCP_SERVERS") else {}
    base = {
        "task_id": os.environ.get("BENCH_PULL_TASK_ID", ""),
        "session_id": os.environ.get("BENCH_PULL_SESSION_ID", ""),
        "session_index": SESSION_INDEX,
        "output": output,
        "instruction": instruction,
        "tools": [{"name": n, "description": d, "input_schema": s} for n, (_, _, d, s) in sorted(tools.items())],
    }
    agent = Agent(server, base, tools, deadline)
    extra = {}
    repo_mode = os.environ.get("BENCH_REPO") == "tarball"
    if repo_mode:
        root = os.environ.get("BENCH_REPO_DIR", "/workspace")
        try:
            data, files = hc.workspace_tarball(root, skip=[os.path.abspath(answer_path)])
            info = server.publish_repo(data)
        except (ValueError, OSError) as e:
            fail(1, "could not offer the repository: %s" % clean(str(e), 500))
        extra["repo"] = {"url": info["url"], "format": "tar.gz", "sha256": info.get("sha256", ""), "bytes": len(data),
                         "expires_at": info.get("expires_at", ""), "files": files}
        REPORT["repo"] = {"bytes": len(data), "files": files}
    log("agent %s, %d tools, conversation %s, repo %s" % (clean(os.environ.get("BENCH_PULL_AGENT", ""), 128), len(tools), conversation, repo_mode))
    if conversation:
        msgs = []

        def agent_turn(user_text, messages, turn):
            if turn == 1:
                msgs.append({"role": "assistant", "content": messages[0]["content"]})
            msgs.append({"role": "user", "content": user_text})
            ans = agent.run(msgs, {"kind": "conversation", "turn": turn, "message": user_text})
            return ans.get("text") if isinstance(ans.get("text"), str) else ""

        try:
            summary, text, _ = hc.drive(agent_turn, emit, deadline)
        except hc.ConversationError as e:
            fail(1, str(e))
        REPORT["conversation"] = {k: summary[k] for k in ("agent_turns", "ended_by", "user_server")}
        answer = agent.last
    else:
        messages = [{"role": "user", "content": instruction}]
        extra["kind"] = "task"
        answer = agent.run(messages, extra)
        text = answer.get("text") if isinstance(answer.get("text"), str) else ""
    if repo_mode:
        patch = answer.get("patch")
        if not patch:
            REPORT.setdefault("notes", []).append("the agent returned no patch")
        else:
            try:
                touched = hc.apply_patch(os.environ.get("BENCH_REPO_DIR", "/workspace"), patch)
                REPORT["patch"] = {"applied": True, "files": len(touched), "bytes": len(patch.encode("utf-8"))}
            except ValueError as e:
                REPORT["patch"] = {"applied": False, "error": clean(str(e), 500)}
    if output == "answer":
        write_answer(answer_path, text or "")
    emit(REPORT)
    event("claim", claim={"made": False, "text": clean(text or "", 1000)}, summary="final answer after %d turns" % REPORT["turns"])


if __name__ == "__main__":
    main()
