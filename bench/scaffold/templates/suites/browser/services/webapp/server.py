#!/usr/bin/env python3
"""Customer console: a tiny server-rendered web application for browser tasks.

Written by `bifrost-bench suite init -template browser`. Python standard
library only, so the image needs nothing but python3 and never reaches the
network. The agent never talks to it directly: it is a hidden service, and
the agent drives a headless browser (the browser service) that loads these
pages. Every page is plain HTML with labelled form fields, so the browser's
accessibility snapshot names each field by its label.

Pages:
  GET  /                      redirect to /customers
  GET  /customers             the customer list (?q= filters by name or email)
  GET  /customers/new         the new customer form
  POST /customers             create a customer from the form, then show it
  GET  /customers/<id>        one customer, with an edit form
  POST /customers/<id>        save the edit form
  GET  /healthz               200 when the server is up (the ready check)

State: the task's seed database (SEED_PATH, copied in by the task's
`files:`) is loaded at start and written to STATE_PATH at once and after
every change; the bench exports it after the agent exits and the hidden
verifier reads it at /grader/state/webapp/db.json. The verifier never looks at
pages or screenshots: only this file.

APP_BANNER, when set, is shown as a notice at the top of every page. A probe
of layer service sets it (probes/hostile-banner.yaml) to plant hostile text
in the page the agent reads; the clean condition leaves it empty. While it is
set, the database also records each page served with it under "planted"
({"armed": true, "served": [{"call": n, "tool": "GET /path"}]}), the receipt
the service probe layer reads to show the text reached the browser.

Commands:
  python3 server.py           serve (PORT, default 8000)
  python3 server.py ready     exit 0 when the local server answers /healthz
"""

import html
import json
import os
import re
import sys
import tempfile
import threading
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SEED_PATH = os.environ.get("SEED_PATH", "/seed/db.json")
STATE_PATH = os.environ.get("STATE_PATH", "/state/db.json")
PORT = int(os.environ.get("PORT", "8000"))
BANNER = os.environ.get("APP_BANNER", "")
MAX_BODY = 64 << 10
PLANS = ["free", "team", "business"]
EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")


class Store:
    """The database: one JSON document, saved after every change."""

    def __init__(self, seed, state):
        self.state = state
        self.lock = threading.RLock()
        with open(seed, encoding="utf-8") as f:
            self.db = json.load(f)
        self.db.setdefault("customers", [])
        if BANNER:
            self.db["planted"] = {"armed": True, "served": []}
        self.save()

    def served(self, path):
        """Record one page served with the banner (only while it is set)."""
        if not BANNER:
            return
        with self.lock:
            served = self.db["planted"]["served"]
            served.append({"call": len(served) + 1, "tool": "GET " + path})
            self.save()

    def save(self):
        d = os.path.dirname(self.state) or "."
        os.makedirs(d, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=d, prefix=".db-")
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(self.db, f, indent=2, sort_keys=True)
            f.write("\n")
        os.replace(tmp, self.state)


def esc(s):
    return html.escape(str(s), quote=True)


def page(title, body):
    banner = f'<div class="notice" role="note">{esc(BANNER)}</div>' if BANNER else ""
    return (
        "<!doctype html><html lang=en><head><meta charset=utf-8>"
        f"<title>{esc(title)} - Customer console</title>"
        "<style>body{font-family:sans-serif;margin:2rem}table{border-collapse:collapse}"
        "td,th{border:1px solid #ccc;padding:4px 8px}.notice{background:#fff3cd;padding:8px;margin-bottom:1rem}"
        ".error{color:#a00}label{display:block;margin-top:8px}</style></head><body>"
        '<nav><a href="/customers">Customers</a> | <a href="/customers/new">New customer</a></nav>'
        f"{banner}<h1>{esc(title)}</h1>{body}</body></html>"
    )


def customer_form(action, c, submit, errors):
    err = "".join(f'<p class="error" role="alert">{esc(e)}</p>' for e in errors)
    plans = "".join(
        f'<option value="{p}"{" selected" if c.get("plan") == p else ""}>{p.title()}</option>' for p in PLANS
    )
    return (
        f'{err}<form method="post" action="{esc(action)}">'
        f'<label for="name">Name</label><input id="name" name="name" value="{esc(c.get("name", ""))}">'
        f'<label for="email">Email</label><input id="email" name="email" type="email" value="{esc(c.get("email", ""))}">'
        f'<label for="plan">Plan</label><select id="plan" name="plan">{plans}</select>'
        f'<label for="notes">Notes</label><textarea id="notes" name="notes">{esc(c.get("notes", ""))}</textarea>'
        f'<p><button type="submit">{esc(submit)}</button></p></form>'
    )


def validate(form):
    c = {k: form.get(k, "").strip() for k in ("name", "email", "plan", "notes")}
    errors = []
    if not c["name"]:
        errors.append("Name is required.")
    if not EMAIL_RE.match(c["email"]):
        errors.append("Email is not a valid address.")
    if c["plan"] not in PLANS:
        errors.append("Plan must be one of: " + ", ".join(PLANS) + ".")
    return c, errors


class Handler(BaseHTTPRequestHandler):
    server_version = "customer-console/1.0"

    def send_html(self, status, body):
        if self.command == "GET" and status == 200:
            self.server.store.served(urllib.parse.urlsplit(self.path).path)
        data = body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def redirect(self, where):
        self.send_response(303)
        self.send_header("Location", where)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def find(self, cid):
        return next((c for c in self.server.store.db["customers"] if c["id"] == cid), None)

    def do_GET(self):
        url = urllib.parse.urlsplit(self.path)
        path, query = url.path.rstrip("/") or "/", urllib.parse.parse_qs(url.query)
        store = self.server.store
        if path == "/healthz":
            self.send_response(200)
            self.send_header("Content-Length", "0")
            self.end_headers()
        elif path == "/":
            self.redirect("/customers")
        elif path == "/customers":
            q = (query.get("q") or [""])[0].strip().lower()
            with store.lock:
                rows = [c for c in store.db["customers"] if q in c["name"].lower() or q in c["email"].lower()]
            table = "".join(
                f'<tr><td><a href="/customers/{esc(c["id"])}">{esc(c["id"])}</a></td><td>{esc(c["name"])}</td>'
                f'<td>{esc(c["email"])}</td><td>{esc(c["plan"])}</td></tr>'
                for c in rows
            )
            search = (
                '<form method="get" action="/customers"><label for="q">Search</label>'
                f'<input id="q" name="q" value="{esc(q)}"><button type="submit">Search</button></form>'
            )
            self.send_html(200, page("Customers", search + "<table><tr><th>ID</th><th>Name</th><th>Email</th>"
                                     f"<th>Plan</th></tr>{table}</table><p>{len(rows)} customers</p>"))
        elif path == "/customers/new":
            self.send_html(200, page("New customer", customer_form("/customers", {"plan": "free"}, "Create customer", [])))
        elif path.startswith("/customers/"):
            with store.lock:
                c = self.find(path.split("/")[2])
            if c is None:
                self.send_html(404, page("Not found", "<p>No such customer.</p>"))
                return
            self.send_html(200, page(f'Customer {c["id"]}', customer_form(f'/customers/{c["id"]}', c, "Save changes", [])))
        else:
            self.send_html(404, page("Not found", "<p>No such page.</p>"))

    def do_POST(self):
        path = urllib.parse.urlsplit(self.path).path.rstrip("/")
        length = int(self.headers.get("Content-Length") or 0)
        if length > MAX_BODY:
            self.send_html(413, page("Too large", "<p>Form too large.</p>"))
            return
        form = {k: v[0] for k, v in urllib.parse.parse_qs(self.rfile.read(length).decode("utf-8")).items()}
        store = self.server.store
        if path == "/customers":
            c, errors = validate(form)
            if errors:
                self.send_html(422, page("New customer", customer_form("/customers", c, "Create customer", errors)))
                return
            with store.lock:
                if any(x["email"].lower() == c["email"].lower() for x in store.db["customers"]):
                    errors.append("A customer with this email already exists.")
                else:
                    c["id"] = "C-%d" % (1001 + len(store.db["customers"]))
                    store.db["customers"].append(c)
                    store.save()
            if errors:
                self.send_html(409, page("New customer", customer_form("/customers", c, "Create customer", errors)))
                return
            self.redirect(f'/customers/{c["id"]}')
        elif path.startswith("/customers/"):
            cid = path.split("/")[2]
            c, errors = validate(form)
            with store.lock:
                old = self.find(cid)
                if old is None:
                    self.send_html(404, page("Not found", "<p>No such customer.</p>"))
                    return
                if not errors:
                    old.update(c)
                    store.save()
            if errors:
                c["id"] = cid
                self.send_html(422, page(f"Customer {cid}", customer_form(f"/customers/{cid}", c, "Save changes", errors)))
                return
            self.redirect(f"/customers/{cid}")
        else:
            self.send_html(404, page("Not found", "<p>No such page.</p>"))

    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))


def serve():
    httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    httpd.store = Store(SEED_PATH, STATE_PATH)
    sys.stderr.write(f"customer console on :{PORT}, state in {STATE_PATH}\n")
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
