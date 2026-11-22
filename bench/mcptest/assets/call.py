#!/usr/bin/env python3
"""Control helper of a drafted MCP server task: opens an MCP session with the
server through the tool proxy and makes one tools/call.

    call.py <server id> <tool> <arguments json>

The reference control calls the tool with arguments the server accepted when
it was inspected; the mutant control calls it with arguments of the wrong
type. Both then write the start of the answer to answer.txt.
"""

import json
import os
import sys
import urllib.request

server, tool, args = sys.argv[1], sys.argv[2], json.loads(sys.argv[3])
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
url = (servers.get(server) or {}).get("url") or "http://toolproxy:9200/x/" + server
session = None


def post(msg):
    global session
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    if session:
        headers["Mcp-Session-Id"] = session
    req = urllib.request.Request(url, data=json.dumps(msg).encode(), headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            session = resp.headers.get("Mcp-Session-Id") or session
            body = resp.read().decode("utf-8", "replace")
    except Exception as e:  # the verifier decides from the ledger, not from here
        return str(e)
    return body


post({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}}})
post({"jsonrpc": "2.0", "method": "notifications/initialized"})
out = post({"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {"name": tool, "arguments": args}})
with open(os.environ.get("BENCH_ANSWER_PATH") or "answer.txt", "w") as f:
    f.write(out[:2000])
