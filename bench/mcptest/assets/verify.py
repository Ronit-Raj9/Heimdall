#!/usr/bin/env python3
"""Verifier of a drafted MCP server task (bench/mcptest, docs/mcp-server-testing.md).

It runs after the agent exited, with no network. It reads the tool proxy
ledger at /grader/trajectory/tools.jsonl and this folder's expect.json (the
server id, the tool the task is about and its input schema), and writes
/grader/out/reward.json as {"reward": 0 or 1, "dimensions": {...}}:

  picked       the agent called the tool at least once
  valid_args   one call had arguments that fit the tool's input schema
  succeeded    one call with valid arguments got a result that is not an error

The reward is succeeded. The ledger is written by the proxy, outside the
agent's reach, so the agent cannot claim a call it never made.
"""

import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
LEDGER = os.environ.get("GRADER_TOOLS", "/grader/trajectory/tools.jsonl")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")

TYPES = {
    "string": lambda v: isinstance(v, str),
    "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
    "number": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
    "boolean": lambda v: isinstance(v, bool),
    "array": lambda v: isinstance(v, list),
    "object": lambda v: isinstance(v, dict),
    "null": lambda v: v is None,
}


def fits(schema, value):
    """A small JSON Schema check: type, enum, required and nested properties."""
    if not isinstance(schema, dict):
        return True
    if "enum" in schema and value not in schema["enum"]:
        return False
    t = schema.get("type")
    if isinstance(t, str) and t in TYPES and not TYPES[t](value):
        return False
    if isinstance(t, list) and t and not any(TYPES.get(x, lambda v: True)(value) for x in t if isinstance(x, str)):
        return False
    if isinstance(value, dict):
        props = schema.get("properties") or {}
        for r in schema.get("required") or []:
            if r not in value:
                return False
        for k, v in value.items():
            if k in props and not fits(props[k], v):
                return False
    if isinstance(value, list) and isinstance(schema.get("items"), dict):
        return all(fits(schema["items"], x) for x in value)
    return True


def call_arguments(row):
    """The arguments of the tools/call a ledger row records, or None."""
    raw = row.get("arguments") or ""
    try:
        msg = json.loads(raw)
    except ValueError:
        return None
    if isinstance(msg, list):
        msg = next((m for m in msg if isinstance(m, dict) and m.get("method") == "tools/call"), None)
    if not isinstance(msg, dict) or msg.get("method") != "tools/call":
        return None
    params = msg.get("params") or {}
    args = params.get("arguments")
    return {} if args is None else args


def main():
    with open(os.path.join(HERE, "expect.json")) as f:
        expect = json.load(f)
    server, tool, schema = expect["server"], expect["tool"], expect.get("schema") or {}
    rows = []
    try:
        with open(LEDGER) as f:
            for line in f:
                line = line.strip()
                if line:
                    try:
                        rows.append(json.loads(line))
                    except ValueError:
                        pass
    except OSError:
        pass
    picked = valid = ok = False
    for r in rows:
        if r.get("server") != server or r.get("tool") != tool or r.get("blocked") or r.get("fault"):
            continue
        picked = True
        args = call_arguments(r)
        if args is None or not fits(schema, args):
            continue
        valid = True
        if not r.get("is_error") and (r.get("status") or 200) < 400:
            ok = True
    checks = {"picked": picked, "valid_args": valid, "succeeded": ok}
    reward = 1 if ok else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w") as f:
        json.dump({"reward": reward, "dimensions": {k: int(v) for k, v in checks.items()}}, f)
    print(json.dumps({"reward": reward, "checks": checks}))


if __name__ == "__main__":
    main()
