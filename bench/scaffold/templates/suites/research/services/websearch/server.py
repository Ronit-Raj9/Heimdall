#!/usr/bin/env python3
"""websearch: the web search tool of a Heimdall research suite.

An MCP server (streamable HTTP, Python standard library only) with two
tools, web_search(query, max_results) and fetch_page(url). Each tool call
becomes one call to Tavily's API through the per-run tool proxy's external
route (TAVILY_URL, default http://toolproxy:9200/x/tavily): POST /search or
POST /extract. The proxy adds the API key (the agent and this server never
see it), enforces the allowlist and caps, records the call in the run's tool
ledger, and in replay mode answers it from the suite's cassette. So every
search and every result lands in tools.jsonl twice: once as the agent's MCP
call to this server, once as the external Tavily call.

Replay matches a call by the sha256 of its body, so the bodies are
canonical: the query is normalized (control characters to spaces, white
space collapsed, lower case, " ?!.,;:" trimmed, at most 400 characters), the
tool always asks for 8 results and cuts the list to max_results itself, and
the JSON is written with sorted keys and no spaces. bench/research/research.go
(NormalizeQuery, SearchBody, ExtractBody) writes the same bytes; a Go test
runs this file against it.

Probe hooks (the suite's probes set them through service_env):
  WEBSEARCH_ORDER=oldest_first  return results oldest page first (stale results)

Commands:
  python3 server.py           serve (PORT, default 8000)
  python3 server.py ready     exit 0 when the local server answers /healthz
"""

import json
import os
import sys
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TAVILY_URL = os.environ.get("TAVILY_URL", "http://toolproxy:9200/x/tavily").rstrip("/")
PORT = int(os.environ.get("PORT", "8000"))
MCP_PATH = os.environ.get("MCP_PATH", "/mcp")
ORDER = os.environ.get("WEBSEARCH_ORDER", "")
SERVER_NAME = "websearch"
SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ["2025-06-18", "2025-03-26", "2024-11-05"]
MAX_BODY = 1 << 20
SEARCH_RESULTS = 8
MAX_QUERY = 400
QUERY_TRIM = " ?!.,;:"
STATUS_UNMATCHED = 599


class ToolError(Exception):
    """An error the agent sees as a tool result with isError true."""


# ------------------------------------------------------- Canonical bodies ----


def normalize_query(q):
    q = "".join(" " if (ord(c) < 0x20 or ord(c) == 0x7F or c in "  ") else c for c in q)
    q = " ".join(q.split()).lower().strip(QUERY_TRIM)
    if len(q) > MAX_QUERY:
        q = q[:MAX_QUERY].rstrip(QUERY_TRIM)
    return q


def canonical(body):
    return json.dumps(body, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def search_body(query):
    return canonical({"include_published_date": True, "max_results": SEARCH_RESULTS, "query": normalize_query(query)})


def extract_body(url):
    return canonical({"include_published_date": True, "urls": [url.strip()]})


# ------------------------------------------------------------------ Tavily ----


def tavily(path, body):
    req = urllib.request.Request(TAVILY_URL + path, data=body, method="POST",
                                 headers={"Content-Type": "application/json", "Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=90) as resp:
            status, raw = resp.status, resp.read()
    except urllib.error.HTTPError as e:
        status, raw = e.code, e.read()
    except (urllib.error.URLError, OSError) as e:
        raise ToolError(f"search backend unreachable: {e}")
    if status == STATUS_UNMATCHED:
        raise ToolError("no recorded result for this request: the search API is replayed from a recording "
                        "and this exact query was never recorded; try a different query")
    try:
        data = json.loads(raw or b"{}")
    except ValueError:
        data = {"error": raw[:300].decode("utf-8", "replace")}
    if status >= 400:
        detail = data.get("detail") if isinstance(data, dict) else None
        msg = (detail.get("error") if isinstance(detail, dict) else detail) or (data.get("error") if isinstance(data, dict) else "") or ""
        raise ToolError(f"search API error {status}: {msg}".rstrip(": "))
    if not isinstance(data, dict):
        raise ToolError("search API answered something that is not a JSON object")
    return data


def web_search(args):
    query = args.get("query")
    if not isinstance(query, str) or not normalize_query(query):
        raise ToolError("query must be a non-empty string")
    n = args.get("max_results", 5)
    if not isinstance(n, int) or isinstance(n, bool) or n < 1 or n > SEARCH_RESULTS:
        raise ToolError(f"max_results must be an integer from 1 to {SEARCH_RESULTS}")
    data = tavily("/search", search_body(query))
    results = []
    for r in data.get("results") or []:
        if isinstance(r, dict):
            results.append({k: r.get(k) for k in ("title", "url", "published_date", "content", "score")})
    if ORDER == "oldest_first":
        results.sort(key=lambda r: (r.get("published_date") or "9999", r.get("url") or ""))
    out = {"query": normalize_query(query), "results": results[:n]}
    if data.get("notice"):
        out["notice"] = data["notice"]
    return out


def fetch_page(args):
    url = args.get("url")
    if not isinstance(url, str) or not url.strip().lower().startswith(("http://", "https://")):
        raise ToolError("url must be an http(s) URL")
    data = tavily("/extract", extract_body(url))
    results = [r for r in data.get("results") or [] if isinstance(r, dict)]
    if not results:
        failed = [f.get("error", "") for f in data.get("failed_results") or [] if isinstance(f, dict)]
        raise ToolError(f"could not fetch {url.strip()}: {'; '.join(failed) or 'no content returned'}")
    r = results[0]
    out = {"url": r.get("url"), "published_date": r.get("published_date"), "content": r.get("raw_content") or ""}
    if data.get("notice"):
        out["notice"] = data["notice"]
    return out


TOOLS = [
    {
        "name": "web_search",
        "description": "Search the web. Returns the best matching pages, each with title, url, published_date, "
                       "a content snippet and a relevance score. Cite the url of every page you rely on.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "What to search for"},
                "max_results": {"type": "integer", "minimum": 1, "maximum": SEARCH_RESULTS,
                                "description": f"How many results to return (1 to {SEARCH_RESULTS}, default 5)"},
            },
            "required": ["query"],
        },
        "handler": web_search,
    },
    {
        "name": "fetch_page",
        "description": "Fetch the full text of one web page by its URL (for example a search result).",
        "inputSchema": {
            "type": "object",
            "properties": {"url": {"type": "string", "description": "The page's http(s) URL"}},
            "required": ["url"],
        },
        "handler": fetch_page,
    },
]


# --------------------------------------------------------------- Protocol ----


def rpc_error(msg_id, code, message):
    return {"jsonrpc": "2.0", "id": msg_id, "error": {"code": code, "message": message}}


def rpc_result(msg_id, result):
    return {"jsonrpc": "2.0", "id": msg_id, "result": result}


def tool_result(payload, is_error=False):
    text = payload if isinstance(payload, str) else json.dumps(payload, ensure_ascii=False)
    return {"content": [{"type": "text", "text": text}], "isError": is_error}


def handle_message(msg):
    """Answers one JSON-RPC message; None for notifications and responses."""
    if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0":
        return rpc_error(None, -32600, "invalid request")
    method = msg.get("method")
    msg_id = msg.get("id")
    if method is None or "id" not in msg:
        return None
    params = msg.get("params") or {}
    if method == "initialize":
        asked = params.get("protocolVersion")
        version = asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0]
        return rpc_result(msg_id, {
            "protocolVersion": version,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
        })
    if method == "ping":
        return rpc_result(msg_id, {})
    if method == "tools/list":
        return rpc_result(msg_id, {"tools": [{k: t[k] for k in ("name", "description", "inputSchema")} for t in TOOLS]})
    if method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}
        tool = next((t for t in TOOLS if t["name"] == name), None)
        if tool is None:
            return rpc_error(msg_id, -32602, f"unknown tool: {name}")
        if not isinstance(args, dict):
            return rpc_result(msg_id, tool_result("arguments must be an object", True))
        try:
            return rpc_result(msg_id, tool_result(tool["handler"](args)))
        except ToolError as e:
            return rpc_result(msg_id, tool_result(str(e), True))
    return rpc_error(msg_id, -32601, f"method not found: {method}")


class Handler(BaseHTTPRequestHandler):
    server_version = "websearch-mcp/1.0"
    protocol_version = "HTTP/1.1"

    def send_json(self, status, body, headers=None):
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(data)

    def send_empty(self, status, headers=None):
        self.send_response(status)
        self.send_header("Content-Length", "0")
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/healthz":
            self.send_json(200, {"ok": True, "server": SERVER_NAME})
        elif path == MCP_PATH:
            self.send_empty(405, {"Allow": "POST, DELETE"})
        else:
            self.send_json(404, {"error": "not found"})

    def do_DELETE(self):
        if self.path.split("?", 1)[0] == MCP_PATH:
            self.send_empty(200)
        else:
            self.send_json(404, {"error": "not found"})

    def do_POST(self):
        if self.path.split("?", 1)[0] != MCP_PATH:
            self.send_json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > MAX_BODY:
            self.send_json(400, rpc_error(None, -32600, "missing or oversized body"))
            return
        try:
            msg = json.loads(self.rfile.read(length))
        except ValueError:
            self.send_json(400, rpc_error(None, -32700, "parse error"))
            return
        msgs = msg if isinstance(msg, list) else [msg]
        answers = [a for a in (handle_message(m) for m in msgs) if a is not None]
        headers = {}
        if any(isinstance(m, dict) and m.get("method") == "initialize" for m in msgs):
            headers["Mcp-Session-Id"] = uuid.uuid4().hex
        if not answers:
            self.send_empty(202, headers)
        else:
            self.send_json(200, answers if isinstance(msg, list) else answers[0], headers)

    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))


def serve():
    httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    sys.stderr.write(f"{SERVER_NAME} MCP server on :{PORT}{MCP_PATH}, Tavily at {TAVILY_URL}\n")
    httpd.serve_forever()


def ready():
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/healthz", timeout=2) as r:
            return 0 if r.status == 200 else 1
    except OSError:
        return 1


if __name__ == "__main__":
    if sys.argv[1:] == ["ready"]:
        sys.exit(ready())
    serve()
