#!/usr/bin/env python3
"""MCP loop agent for SYNTHETIC runs of Bifrost feature experiments (B2).

A minimal tool-using agent in the standard library only: it lists the tools
of every MCP server in BENCH_MCP_SERVERS (directly from the run's tool proxy,
or through Bifrost's MCP gateway with the run key as a Bearer header), offers
them to the model on every Anthropic Messages call through the gateway,
executes each tool_use block with MCP tools/call on the server that offered
the tool, sends the results back and stops when the model answers without a
tool call, or after BENCH_MAX_STEPS model turns. Extra request headers from
BENCH_GATEWAY_HEADERS (prompt cache, semantic cache) go on every model call.

It streams every model call (stream: true, BENCH_STREAM=0 turns it off), as
the coding CLIs do: Bifrost v2.5.1 runs its MCP agent mode only on requests
that do not stream, and agent mode executes Code Mode's listToolFiles and
readToolFile itself when a model answer calls them (docs/bifrost-features.md).
It prints Claude Code stream-json lines, so the claudecode parser reads its
turns, and claims success only when its last answer says so. Its model is a
scripted upstream in every bench test (toolscript), so its results are never
findings: they prove that the pipeline, the gateway features and the
witnesses work.
"""

import json
import os
import sys
import urllib.error
import urllib.request


def env(name, default=None):
    value = os.environ.get(name, default)
    if value is None:
        raise SystemExit("missing environment variable " + name)
    return value


GATEWAY = env("BENCH_GATEWAY_URL").rstrip("/")
KEY = env("BENCH_RUN_KEY")
MODEL = env("BENCH_MODEL")
SESSION = env("BENCH_SESSION", "")
INSTRUCTION = env("BENCH_INSTRUCTION", "/tmp/bench/instruction.md")
MAX_STEPS = int(env("BENCH_MAX_STEPS", "12"))
SERVERS = json.loads(env("BENCH_MCP_SERVERS", "{}") or "{}")
EXTRA_HEADERS = json.loads(env("BENCH_GATEWAY_HEADERS", "{}") or "{}")
STREAM = env("BENCH_STREAM", "1") != "0"


def emit(obj):
    sys.stdout.write(json.dumps(obj, separators=(",", ":")) + "\n")
    sys.stdout.flush()


class MCP:
    """One streamable HTTP MCP session."""

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
        with urllib.request.urlopen(req, timeout=60) as resp:
            if resp.headers.get("Mcp-Session-Id"):
                self.session = resp.headers["Mcp-Session-Id"]
            body = resp.read().decode("utf-8", errors="replace")
        if notify:
            return None
        if "data:" in body:
            body = [line[5:] for line in body.splitlines() if line.startswith("data:")][-1]
        answer = json.loads(body)
        if "error" in answer:
            raise RuntimeError("%s: %s" % (method, answer["error"]))
        return answer.get("result") or {}

    def open(self):
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "mcploop", "version": "1"}})
        self.rpc("notifications/initialized", notify=True)
        return self.rpc("tools/list").get("tools", [])


def read_stream(resp):
    """Rebuild a Messages response from its Server-Sent Events."""
    message, blocks, partial = {}, {}, {}
    for raw in resp:
        line = raw.decode("utf-8", errors="replace").strip()
        if not line.startswith("data:"):
            continue
        ev = json.loads(line[5:])
        kind = ev.get("type")
        if kind == "message_start":
            message = ev.get("message") or {}
        elif kind == "content_block_start":
            blocks[ev["index"]] = dict(ev.get("content_block") or {})
        elif kind == "content_block_delta":
            d, b = ev.get("delta") or {}, blocks.setdefault(ev["index"], {})
            if d.get("type") == "text_delta":
                b["text"] = b.get("text", "") + d.get("text", "")
            elif d.get("type") == "input_json_delta":
                partial[ev["index"]] = partial.get(ev["index"], "") + d.get("partial_json", "")
        elif kind == "message_delta":
            message.update(ev.get("delta") or {})
            if ev.get("usage"):
                message["usage"] = ev["usage"]
    for i, text in partial.items():
        blocks[i]["input"] = json.loads(text) if text else {}
    message["content"] = [blocks[i] for i in sorted(blocks)]
    return message


def call_model(messages, tools):
    body = {"model": MODEL, "max_tokens": 1024, "messages": messages}
    if STREAM:
        body["stream"] = True
    if tools:
        body["tools"] = tools
    headers = {
        "content-type": "application/json",
        "anthropic-version": "2023-06-01",
        "x-api-key": KEY,
        "x-bf-session-id": SESSION,
        "x-bf-session-affinity": "off",
    }
    headers.update({str(k): str(v) for k, v in EXTRA_HEADERS.items()})
    req = urllib.request.Request(GATEWAY + "/anthropic/v1/messages", data=json.dumps(body).encode("utf-8"), method="POST", headers=headers)
    with urllib.request.urlopen(req, timeout=120) as resp:
        if STREAM:
            return read_stream(resp)
        return json.loads(resp.read().decode("utf-8"))


def finish(turns, text, ok):
    emit({"type": "result", "subtype": "success", "is_error": not ok, "num_turns": turns, "result": text, "session_id": SESSION})
    print(text)
    sys.exit(0 if ok else 1)


def main():
    with open(INSTRUCTION, "r", encoding="utf-8") as handle:
        instruction = handle.read()
    route, tools = {}, []
    for sid in sorted(SERVERS):
        spec = SERVERS[sid]
        client = MCP(spec["url"], spec.get("headers"))
        try:
            listed = client.open()
        except (urllib.error.URLError, OSError, ValueError, RuntimeError) as exc:
            finish(0, "MCP server %s could not be listed: %s" % (sid, exc), False)
        for t in listed:
            name = t.get("name", "")
            if not name or name in route:
                continue
            route[name] = client
            tools.append({"name": name, "description": t.get("description", ""), "input_schema": t.get("inputSchema") or {"type": "object", "properties": {}}})
    emit({"type": "system", "subtype": "init", "cwd": "/workspace", "model": MODEL, "tools": [t["name"] for t in tools], "session_id": SESSION})
    messages = [{"role": "user", "content": instruction}]
    text = ""
    for turn in range(1, MAX_STEPS + 1):
        try:
            response = call_model(messages, tools)
        except urllib.error.HTTPError as exc:
            finish(turn, "API Error: %s %s" % (exc.code, exc.read().decode("utf-8", errors="replace")[:300]), False)
        except (urllib.error.URLError, OSError, ValueError) as exc:
            finish(turn, "API Error: %s" % exc, False)
        emit({"type": "assistant", "message": response, "parent_tool_use_id": None, "session_id": SESSION})
        content = response.get("content") or [{"type": "text", "text": ""}]
        messages.append({"role": "assistant", "content": content})
        uses = [b for b in content if b.get("type") == "tool_use"]
        text = "".join(b.get("text", "") for b in content if b.get("type") == "text")
        if not uses:
            finish(turn, text or "Done.", True)
        results = []
        for b in uses:
            client = route.get(b.get("name", ""))
            if client is None:
                out, err = "unknown tool %s" % b.get("name"), True
            else:
                try:
                    r = client.rpc("tools/call", {"name": b["name"], "arguments": b.get("input") or {}})
                    out = "".join(c.get("text", "") for c in r.get("content", []) if isinstance(c, dict))
                    err = bool(r.get("isError"))
                except (urllib.error.URLError, OSError, ValueError, RuntimeError) as exc:
                    out, err = str(exc), True
            results.append({"type": "tool_result", "tool_use_id": b.get("id", ""), "content": out, "is_error": err})
        emit({"type": "user", "message": {"role": "user", "content": results}, "parent_tool_use_id": None, "session_id": SESSION})
        messages.append({"role": "user", "content": results})
    finish(MAX_STEPS, "Stopped after %d model turns without a final answer." % MAX_STEPS, False)


if __name__ == "__main__":
    main()
