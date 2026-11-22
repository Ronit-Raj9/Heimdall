#!/usr/bin/env python3
"""Knowledge-base search service of a Heimdall knowledge-base suite.

Written by `bifrost-bench suite init -template knowledge-base`. Python
standard library only, so the image needs nothing but python3 and never
reaches the network.

It serves the documents in DOCS_DIR (Markdown, text or HTML files, baked into
the image from services/kb/docs/) as two MCP tools over streamable HTTP
(JSON-RPC 2.0 POSTed to /mcp):

  search_docs  {query, k?}   BM25 ranking over passages: doc id, title, score
                             and the best passage of each matching document
  read_doc     {doc_id}      the whole document as text

Every search and read is recorded in STATE_PATH (exported by the task as
kb/reads.json), so a review or a check can see which documents the agent
used. The corpus sha256 (over every file name and its bytes) is on /healthz
and in the server info, so a run records which version of the documents it
searched.

Commands:
  python3 server.py           serve (PORT, default 8000)
  python3 server.py ready     exit 0 when the local server answers /healthz
  python3 server.py index     print the corpus: id, title, bytes, and its sha256
"""

import hashlib
import html
import json
import math
import os
import re
import sys
import tempfile
import threading
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DOCS_DIR = os.environ.get("DOCS_DIR", "/srv/docs")
STATE_PATH = os.environ.get("STATE_PATH", "/state/reads.json")
PORT = int(os.environ.get("PORT", "8000"))
MCP_PATH = os.environ.get("MCP_PATH", "/mcp")
SERVER_NAME = "kb"
SERVER_VERSION = "1.0.0"
PROTOCOL_VERSIONS = ["2025-06-18", "2025-03-26", "2024-11-05"]
MAX_BODY = 1 << 20
MAX_DOC_CHARS = 40000
EXTENSIONS = (".md", ".markdown", ".txt", ".html", ".htm")
K1, B = 1.5, 0.75
WORD = re.compile(r"[a-z0-9]+")


class ToolError(Exception):
    """An error the agent sees as a tool result with isError true."""


def html_text(raw):
    """Readable text of an HTML page: scripts and styles dropped, tags removed."""
    raw = re.sub(r"(?is)<(script|style|noscript)\b.*?</\1>", " ", raw)
    raw = re.sub(r"(?i)<br\s*/?>|</(p|div|li|h[1-6]|tr|section|article)>", "\n", raw)
    raw = re.sub(r"(?s)<[^>]+>", " ", raw)
    text = html.unescape(raw)
    return "\n".join(re.sub(r"[ \t]+", " ", ln).strip() for ln in text.splitlines()).strip()


def title_of(doc_id, text):
    for ln in text.splitlines():
        ln = ln.strip().lstrip("#").strip()
        if ln:
            return ln[:200]
    return doc_id


def tokens(text):
    return WORD.findall(text.lower())


class Corpus:
    """The documents, split into passages (blank-line separated) for BM25."""

    def __init__(self, root):
        self.docs = {}
        self.passages = []  # (doc_id, text, tokens)
        digest = hashlib.sha256()
        for dirpath, dirnames, names in os.walk(root):
            dirnames.sort()
            for name in sorted(names):
                if not name.lower().endswith(EXTENSIONS):
                    continue
                path = os.path.join(dirpath, name)
                rel = os.path.relpath(path, root).replace(os.sep, "/")
                with open(path, "rb") as f:
                    raw = f.read()
                digest.update(rel.encode() + b"\0" + raw + b"\0")
                text = raw.decode("utf-8", "replace")
                if name.lower().endswith((".html", ".htm")):
                    text = html_text(text)
                doc_id = re.sub(r"\.[a-z]+$", "", rel, flags=re.I)
                self.docs[doc_id] = {"id": doc_id, "file": rel, "title": title_of(doc_id, text), "text": text}
                for part in re.split(r"\n\s*\n", text):
                    toks = tokens(part)
                    if toks:
                        self.passages.append((doc_id, part.strip(), toks))
        self.sha256 = digest.hexdigest()
        self.df = {}
        for _, _, toks in self.passages:
            for w in set(toks):
                self.df[w] = self.df.get(w, 0) + 1
        n = len(self.passages)
        self.avg = (sum(len(t) for _, _, t in self.passages) / n) if n else 0.0

    def search(self, query, k):
        q = tokens(query)
        if not q:
            raise ToolError("query has no words to search for")
        n = len(self.passages)
        best = {}
        for doc_id, text, toks in self.passages:
            tf = {}
            for w in toks:
                tf[w] = tf.get(w, 0) + 1
            score = 0.0
            for w in set(q):
                f = tf.get(w)
                if not f:
                    continue
                idf = math.log(1 + (n - self.df[w] + 0.5) / (self.df[w] + 0.5))
                score += idf * f * (K1 + 1) / (f + K1 * (1 - B + B * len(toks) / (self.avg or 1)))
            if score > 0 and score > best.get(doc_id, (0, ""))[0]:
                best[doc_id] = (score, text)
        ranked = sorted(best.items(), key=lambda kv: (-kv[1][0], kv[0]))[:k]
        return [{"doc_id": d, "title": self.docs[d]["title"], "score": round(s, 4), "passage": t[:800]} for d, (s, t) in ranked]


class Store:
    """The corpus and the record of what the agent searched and read."""

    def __init__(self, corpus, state_path):
        self.corpus = corpus
        self.state_path = state_path
        self.lock = threading.Lock()
        self.log = {"corpus_sha256": corpus.sha256, "searches": [], "reads": []}
        self.save()

    def save(self):
        d = os.path.dirname(self.state_path) or "."
        os.makedirs(d, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=d, prefix=".reads-")
        with os.fdopen(fd, "w") as f:
            json.dump(self.log, f, indent=2, sort_keys=True)
        os.replace(tmp, self.state_path)

    def call(self, tool, args):
        with self.lock:
            out = tool["fn"](self, args)
            self.save()
            return out


def search_docs(store, args):
    query = args["query"]
    if not isinstance(query, str):
        raise ToolError("query must be text")
    k = args.get("k", 5)
    if not isinstance(k, int) or k < 1 or k > 20:
        raise ToolError("k must be a whole number from 1 to 20")
    results = store.corpus.search(query, k)
    store.log["searches"].append({"query": query[:500], "results": [r["doc_id"] for r in results]})
    return {"results": results}


def read_doc(store, args):
    doc_id = args["doc_id"]
    doc = store.corpus.docs.get(doc_id) if isinstance(doc_id, str) else None
    if doc is None:
        raise ToolError(f"no document {doc_id!r}; search_docs lists the ids")
    if doc_id not in store.log["reads"]:
        store.log["reads"].append(doc_id)
    text = doc["text"]
    return {"doc_id": doc_id, "title": doc["title"], "text": text[:MAX_DOC_CHARS], "truncated": len(text) > MAX_DOC_CHARS}


TOOLS = [
    {
        "name": "search_docs",
        "description": "Search the help-center documents. Returns the best matching documents with their best passage.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "What to look for, in words"},
                "k": {"type": "integer", "minimum": 1, "maximum": 20, "description": "How many documents to return (default 5)"},
            },
            "required": ["query"],
        },
        "fn": search_docs,
    },
    {
        "name": "read_doc",
        "description": "Read one help-center document in full, by the doc_id search_docs returned.",
        "inputSchema": {
            "type": "object",
            "properties": {"doc_id": {"type": "string"}},
            "required": ["doc_id"],
        },
        "fn": read_doc,
    },
]


def missing_args(tool, args):
    return [k for k in tool["inputSchema"].get("required", []) if k not in args]


def rpc_error(msg_id, code, message):
    return {"jsonrpc": "2.0", "id": msg_id, "error": {"code": code, "message": message}}


def rpc_result(msg_id, result):
    return {"jsonrpc": "2.0", "id": msg_id, "result": result}


def tool_result(payload, is_error=False):
    text = payload if isinstance(payload, str) else json.dumps(payload, sort_keys=True)
    return {"content": [{"type": "text", "text": text}], "isError": is_error}


def handle_message(store, msg):
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
            "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION, "corpus_sha256": store.corpus.sha256},
        })
    if method == "ping":
        return rpc_result(msg_id, {})
    if method == "tools/list":
        listed = [{k: t[k] for k in ("name", "description", "inputSchema")} for t in TOOLS]
        return rpc_result(msg_id, {"tools": listed})
    if method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}
        tool = next((t for t in TOOLS if t["name"] == name), None)
        if tool is None:
            return rpc_error(msg_id, -32602, f"unknown tool: {name}")
        if not isinstance(args, dict):
            return rpc_result(msg_id, tool_result("arguments must be an object", True))
        missing = missing_args(tool, args)
        if missing:
            return rpc_result(msg_id, tool_result("missing arguments: " + ", ".join(missing), True))
        try:
            return rpc_result(msg_id, tool_result(store.call(tool, args)))
        except ToolError as e:
            return rpc_result(msg_id, tool_result(str(e), True))
        except (KeyError, TypeError, ValueError) as e:
            return rpc_result(msg_id, tool_result(f"bad arguments: {e}", True))
    return rpc_error(msg_id, -32601, f"method not found: {method}")


class Handler(BaseHTTPRequestHandler):
    server_version = "kb-mcp/1.0"
    protocol_version = "HTTP/1.1"

    def send_json(self, status, body, headers=None):
        data = json.dumps(body).encode()
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
            c = self.server.store.corpus
            self.send_json(200, {"ok": True, "server": SERVER_NAME, "documents": len(c.docs), "corpus_sha256": c.sha256})
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
        batch = isinstance(msg, list)
        msgs = msg if batch else [msg]
        answers = [a for a in (handle_message(self.server.store, m) for m in msgs) if a is not None]
        headers = {}
        if any(isinstance(m, dict) and m.get("method") == "initialize" for m in msgs):
            headers["Mcp-Session-Id"] = uuid.uuid4().hex
        if not answers:
            self.send_empty(202, headers)
        else:
            self.send_json(200, answers if batch else answers[0], headers)

    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))


def serve():
    corpus = Corpus(DOCS_DIR)
    httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    httpd.store = Store(corpus, STATE_PATH)
    sys.stderr.write(f"{SERVER_NAME} MCP server on :{PORT}{MCP_PATH}, {len(corpus.docs)} documents, corpus {corpus.sha256}\n")
    httpd.serve_forever()


def ready():
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/healthz", timeout=2) as r:
            return 0 if r.status == 200 else 1
    except OSError:
        return 1


def index():
    c = Corpus(DOCS_DIR)
    for d in sorted(c.docs.values(), key=lambda d: d["id"]):
        print(f"{d['id']}\t{d['title']}\t{len(d['text'])}")
    print(f"corpus sha256 {c.sha256}, {len(c.docs)} documents")
    return 0


if __name__ == "__main__":
    if sys.argv[1:] == ["ready"]:
        sys.exit(ready())
    if sys.argv[1:] == ["index"]:
        sys.exit(index())
    serve()
