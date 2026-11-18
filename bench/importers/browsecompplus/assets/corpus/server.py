#!/usr/bin/env python3
"""corpus: a fixed-corpus search service for an imported BrowseComp-Plus suite.

It answers POST /search and POST /extract in the shape of Tavily's API (the
shape bench/research/standin serves and the research track's websearch tool
reads), over the documents of /srv/corpus/corpus.jsonl and nothing else: it
opens no connection. The suite's websearch MCP tool calls it through the
per-run tool proxy (http://toolproxy:9200/s/corpus), so every search and its
results land in the run's tool ledger.

Ranking is the stand-in's BM25, so the importer can predict it in Go
(research/standin): tokens are runs of letters and decimal digits, lower
case, at least two characters, stop words dropped, thousands separators kept
inside numbers; k1 1.2, b 0.75; the title counts twice; ties go to the
smaller URL; the score shown is bm25 / (bm25 + 5) rounded half away from
zero to 4 places. A search result's content is the document text after its
front matter, cut at the last sentence end within SNIPPET_CHARS characters
(default 2000, roughly the 512 tokens BrowseComp-Plus's own search tool
returns). /extract returns the document's full text as upstream has it.

Each corpus.jsonl line: {"docid", "url", "title", "date", "body_offset",
"text"}; body_offset is where the text after the front matter starts, in
characters.

Commands:
  python3 server.py           serve (PORT, default 8000)
  python3 server.py ready     exit 0 when the local server answers /healthz
"""

import hashlib
import json
import math
import os
import re
import sys
import unicodedata
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit, urlunsplit

PORT = int(os.environ.get("PORT", "8000"))
CORPUS = os.environ.get("CORPUS", "/srv/corpus/corpus.jsonl")
SNIPPET_CHARS = int(os.environ.get("SNIPPET_CHARS", "2000"))
MAX_BODY = 1 << 20
DEFAULT_RESULTS = 10
MAX_RESULTS = 20
MAX_URLS = 20
K1, B = 1.2, 0.75
STOPWORDS = set(
    "a an and are as at be by did do does for from had has have how in is it its of on or that the their this to was "
    "were what when where which who whom why with after before since today current currently now many much long".split()
)


def is_letter(c):
    return unicodedata.category(c)[0] == "L"


def is_digit(c):
    return unicodedata.category(c) == "Nd"


# A candidate token: a run of alphanumeric characters, with a thousands
# separator kept inside a number. Python's alphanumeric set is a little wider
# than the stand-in's letters and decimal digits, so a candidate with any
# non-ASCII character is re-split by slow_tokens, which follows the stand-in
# character for character.
CANDIDATE = re.compile(r"(?:[^\W_]|(?<=\d),(?=\d{3}(?!\d)))+")


def tokens(text):
    """research/standin.Tokens: lower case, runs of letters and decimal digits."""
    out = []
    for m in CANDIDATE.findall(text.lower()):
        if m.isascii():
            w = m.replace(",", "")
            if len(w) > 1 and w not in STOPWORDS:
                out.append(w)
        else:
            out.extend(slow_tokens(m))
    return out


def slow_tokens(rs):
    out, cur = [], []

    def flush():
        if len(cur) > 1:
            w = "".join(cur)
            if w not in STOPWORDS:
                out.append(w)
        cur.clear()

    n = len(rs)
    for i, c in enumerate(rs):
        if is_letter(c) or is_digit(c):
            cur.append(c)
        elif (c == "," and cur and is_digit(cur[-1]) and i + 3 < n and all(is_digit(x) for x in rs[i + 1:i + 4])
              and (i + 4 == n or not is_digit(rs[i + 4]))):
            pass  # a thousands separator: keep the number whole
        else:
            flush()
    flush()
    return out


def norm_url(raw):
    """research.NormalizeURL."""
    s = raw.strip().strip("<>\"'`").rstrip(".,;:)]}>\"'")
    try:
        p = urlsplit(s)
    except ValueError:
        return s
    if p.scheme.lower() not in ("http", "https") or not p.netloc:
        return s
    path = p.path.rstrip("/") if len(p.path) > 1 else p.path
    return urlunsplit((p.scheme.lower(), p.netloc.lower(), path, p.query, ""))


def round4(x):
    v = x * 1e4
    f = math.floor(v)
    return (f + 1 if v - f >= 0.5 else f) / 1e4


def snippet(body):
    if len(body) <= SNIPPET_CHARS:
        return body
    cut = body[:SNIPPET_CHARS]
    i = cut.rfind(". ")
    return cut[:i + 1] if i > 0 else cut


class Index:
    def __init__(self, path):
        self.docs, self.by_url, self.terms, self.lens, self.df = [], {}, [], [], {}
        total = 0
        with open(path, encoding="utf-8") as f:
            for line in f:
                if not line.strip():
                    continue
                d = json.loads(line)
                d["body"] = d["text"][d.get("body_offset") or 0:]
                if not d["body"].strip():
                    d["body"] = d["title"]  # as the importer indexes it
                key = norm_url(d["url"])
                if key in self.by_url:
                    raise SystemExit(f"corpus: url {d['url']} appears twice")
                self.by_url[key] = len(self.docs)
                toks = tokens(d["title"]) + tokens(d["title"]) + tokens(d["body"])
                counts = {}
                for t in toks:
                    counts[t] = counts.get(t, 0) + 1
                for t in counts:
                    self.df[t] = self.df.get(t, 0) + 1
                self.terms.append(counts)
                self.lens.append(len(toks))
                total += len(toks)
                self.docs.append(d)
        if not self.docs:
            raise SystemExit("corpus: no documents")
        self.avg = total / len(self.docs)

    def search(self, query, n):
        q = set(tokens(query))
        N = float(len(self.docs))
        hits = []
        for i, d in enumerate(self.docs):
            s = 0.0
            for t in q:
                tf = self.terms[i].get(t, 0)
                if tf == 0:
                    continue
                df = self.df[t]
                idf = math.log(1 + (N - df + 0.5) / (df + 0.5))
                s += idf * tf * (K1 + 1) / (tf + K1 * (1 - B + B * self.lens[i] / self.avg))
            if s > 0:
                hits.append((round4(s / (s + 5)), d))
        hits.sort(key=lambda h: (-h[0], h[1]["url"].encode("utf-8")))
        return hits[:n]


INDEX = None


def truthy(v):
    if isinstance(v, bool):
        return v
    return isinstance(v, str) and v != "" and v != "false"


def request_id(path, raw):
    return "corpus-" + hashlib.sha256((path + "\n").encode() + raw).hexdigest()[:16]


class Handler(BaseHTTPRequestHandler):
    server_version = "bcp-corpus/1.0"
    protocol_version = "HTTP/1.1"

    def send_json(self, status, body):
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def error(self, status, msg):
        self.send_json(status, {"detail": {"error": msg}})

    def do_GET(self):
        if self.path.split("?", 1)[0] == "/healthz":
            self.send_json(200, {"ok": True, "docs": len(INDEX.docs)})
        else:
            self.error(404, "not found")

    def do_POST(self):
        path = self.path.split("?", 1)[0]
        if path not in ("/search", "/extract"):
            self.error(404, "not found: " + path)
            return
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > MAX_BODY:
            self.error(400, "request body missing or too large")
            return
        raw = self.rfile.read(length)
        try:
            req = json.loads(raw)
        except ValueError:
            self.error(400, "request body is not a JSON object")
            return
        if not isinstance(req, dict):
            self.error(400, "request body is not a JSON object")
            return
        rid = request_id(path, raw)
        if path == "/search":
            self.search(req, rid)
        else:
            self.extract(req, rid)

    def search(self, req, rid):
        query = req.get("query")
        if not isinstance(query, str) or not query.strip():
            self.error(400, "query is required and must be a non-empty string")
            return
        n = req.get("max_results", DEFAULT_RESULTS)
        if not isinstance(n, int) or isinstance(n, bool) or n < 0 or n > MAX_RESULTS:
            self.error(400, f"max_results must be an integer from 0 to {MAX_RESULTS}")
            return
        dates = truthy(req.get("include_published_date"))
        results = []
        for score, d in INDEX.search(query, n):
            r = {"url": d["url"], "title": d["title"], "content": snippet(d["body"]), "score": score}
            if dates and d.get("date"):
                r["published_date"] = d["date"]
            results.append(r)
        self.send_json(200, {"query": query, "answer": None, "images": [], "results": results,
                             "response_time": 0.01, "request_id": rid})

    def extract(self, req, rid):
        urls = req.get("urls")
        if isinstance(urls, str) and urls:
            urls = [urls]
        if not isinstance(urls, list) or not urls or len(urls) > MAX_URLS or not all(isinstance(u, str) for u in urls):
            self.error(400, f"urls must hold 1 to {MAX_URLS} URLs")
            return
        dates = truthy(req.get("include_published_date"))
        results, failed = [], []
        for u in urls:
            i = INDEX.by_url.get(norm_url(u))
            if i is None:
                failed.append({"url": u, "error": "page not in the BrowseComp-Plus corpus of this suite"})
                continue
            d = INDEX.docs[i]
            r = {"url": d["url"], "raw_content": d["text"]}
            if dates and d.get("date"):
                r["published_date"] = d["date"]
            results.append(r)
        self.send_json(200, {"results": results, "failed_results": failed, "response_time": 0.01, "request_id": rid})

    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))


def main():
    global INDEX
    if sys.argv[1:] == ["ready"]:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/healthz", timeout=2) as r:
                sys.exit(0 if r.status == 200 else 1)
        except OSError:
            sys.exit(1)
    INDEX = Index(CORPUS)
    sys.stderr.write(f"corpus: {len(INDEX.docs)} documents from {CORPUS} on :{PORT}\n")
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
