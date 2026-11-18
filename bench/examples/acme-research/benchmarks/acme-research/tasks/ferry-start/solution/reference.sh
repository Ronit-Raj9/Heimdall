#!/bin/sh
# Reference control: researches the question the way a careful agent would,
# through the websearch MCP tool (BENCH_MCP_SERVERS names its URL), which
# calls Tavily through the tool proxy (replayed from the suite's cassette).
# It checks that every search returns the page it expects and every fetched
# page says what it expects, then writes answer.txt and sources.json.
# Admission runs it in the agent's place and requires the verifier to pass it
# three times in a row; the oracle arm of the smoke experiment runs it too.
exec python3 -I - <<'PY'
import json, os, sys, urllib.request

PLAN = json.loads(r'''{"steps": [{"search": "Marlow Skerry ferry regular service began", "expect": "https://calder-transit.example/history/marlow-skerry-ferry"}, {"fetch": "https://calder-transit.example/history/marlow-skerry-ferry", "contains": "began in 1962"}], "answer": "1962", "sources": ["https://calder-transit.example/history/marlow-skerry-ferry"]}''')

SERVICE = "websearch"
servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
URL = (servers.get(SERVICE) or {}).get("url") or f"http://toolproxy:9200/s/{SERVICE}/mcp"
session = {}


def rpc(method, params=None, notify=False):
    msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
    if not notify:
        msg["id"] = rpc.next_id = getattr(rpc, "next_id", 0) + 1
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    headers.update(session)
    req = urllib.request.Request(URL, data=json.dumps(msg).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=60) as resp:
        if resp.headers.get("Mcp-Session-Id"):
            session["Mcp-Session-Id"] = resp.headers["Mcp-Session-Id"]
        body = resp.read().decode()
    if notify:
        return None
    if body.lstrip().startswith("data:") or "\ndata:" in body:
        body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
    answer = json.loads(body)
    if "error" in answer:
        sys.exit(f"{method}: {answer['error']}")
    return answer["result"]


def call(tool, args):
    result = rpc("tools/call", {"name": tool, "arguments": args})
    text = "".join(c.get("text", "") for c in result.get("content", []))
    if result.get("isError"):
        sys.exit(f"{tool}: {text}")
    return json.loads(text)


rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}})
rpc("notifications/initialized", notify=True)
for step in PLAN["steps"]:
    if "search" in step:
        found = [r["url"] for r in call("web_search", {"query": step["search"], "max_results": 8})["results"]]
        if step["expect"] not in found:
            sys.exit(f"search {step['search']!r} did not return {step['expect']}: {found}")
    else:
        page = call("fetch_page", {"url": step["fetch"]})
        if step["contains"] not in page["content"]:
            sys.exit(f"{step['fetch']} does not say {step['contains']!r}")
with open("answer.txt", "w") as f:
    f.write(PLAN["answer"] + "\n")
with open("sources.json", "w") as f:
    json.dump(PLAN["sources"], f, indent=2)
print(PLAN["answer"], PLAN["sources"])
PY
