# Team mailbox of a Heimdall team run (docs/multi-agent.md). Standard library
# only. Members reach it only through the run's tool proxy, which records
# every call and sets X-Heimdall-Member to the member whose route the call
# came in on. Every message is also written to RECORD, which the bench
# exports as state/mailbox/messages.jsonl.
import json, os, random, string, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

RECORD = os.environ.get("MAILBOX_RECORD", "/tmp/heimdall-mailbox/messages.jsonl")
PORT = int(os.environ.get("MAILBOX_PORT", "8080"))
TEAM = [m for m in os.environ.get("MAILBOX_TEAM", "").split(",") if m]
MAX_BODY = 64 * 1024
MAX_MESSAGES = 2000
NONCE = "nonce_" + "".join(random.SystemRandom().choice(string.digits + string.ascii_lowercase) for _ in range(24))

lock = threading.Lock()
messages = []


def now():
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()) + ".%03dZ" % (int(time.time() * 1000) % 1000)


def with_nonce(v):
    if isinstance(v, str):
        return v.replace("{{nonce}}", NONCE)
    if isinstance(v, dict):
        return {k: with_nonce(x) for k, x in v.items()}
    if isinstance(v, list):
        return [with_nonce(x) for x in v]
    return v


def save():
    os.makedirs(os.path.dirname(RECORD), exist_ok=True)
    tmp = RECORD + ".tmp"
    with open(tmp, "w") as f:
        for m in messages:
            f.write(json.dumps(m, sort_keys=True) + "\n")
    os.replace(tmp, RECORD)


def store(frm, sender, to, body, fields, planted=False, nonce=""):
    m = {"seq": len(messages) + 1, "id": str(len(messages) + 1), "from": frm, "sender": sender, "to": to,
         "body": body, "fields": fields or {}, "at": now(), "planted": planted, "fault": "", "delivered": None,
         "read_by": [], "read_at": {}, "received_by": {}, "nonce": nonce}
    messages.append(m)
    return m


def deliver(m, reader):
    body = m["body"]
    faults = []
    pf, pt = os.environ.get("MAILBOX_POISON_FROM", ""), os.environ.get("MAILBOX_POISON_TEXT", "")
    if pf and pt and (m["sender"] or m["from"]) == pf:  # the proven sender when the proxy named one
        body += with_nonce(pt)
        faults.append("poison")
    tt, tb = os.environ.get("MAILBOX_TRUNCATE_TO", ""), os.environ.get("MAILBOX_TRUNCATE_BYTES", "")
    if tt and tb and reader == tt:
        try:
            n = max(0, int(tb))
            cut = body.encode("utf-8")[:n].decode("utf-8", "ignore")
            if cut != body:
                body = cut
                faults.append("truncate")
        except ValueError:
            pass
    if faults:
        m["fault"] = "+".join(sorted(set(m["fault"].split("+") + faults) - {""}))
        m["nonce"] = m["nonce"] or NONCE
        m["delivered"] = body
    m["read_by"].append(reader)
    m["read_at"][reader] = now()
    # what this reader got: a broadcast can be altered for one reader only
    m.setdefault("received_by", {})[reader] = body
    return {"id": m["id"], "from": m["from"], "to": m["to"], "body": body, "fields": m["fields"], "at": m["at"]}


def plant():
    raw = os.environ.get("MAILBOX_PLANT", "")
    if not raw:
        return
    try:
        items = json.loads(raw)
    except ValueError:
        print("MAILBOX_PLANT is not JSON", file=sys.stderr)
        return
    for it in items if isinstance(items, list) else []:
        if not isinstance(it, dict):
            continue
        used = "{{nonce}}" in json.dumps(it)
        it = with_nonce(it)
        store(str(it.get("from", "")), "", str(it.get("to", "*")), str(it.get("body", "")),
              it.get("fields") if isinstance(it.get("fields"), dict) else {}, True, NONCE if used else "")


class Handler(BaseHTTPRequestHandler):
    def answer(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        if n > MAX_BODY + 4096:
            return None
        try:
            v = json.loads(self.rfile.read(n) or b"{}")
            if v is None:
                v = {}
            return v if isinstance(v, dict) else None
        except ValueError:
            return None

    def do_GET(self):
        if self.path == "/healthz":
            return self.answer(200, {"ok": True})
        self.answer(404, {"error": "not found"})

    def do_POST(self):
        req = self.body()
        if req is None:
            return self.answer(400, {"error": "body must be a JSON object under 64 KiB"})
        sender = self.headers.get("X-Heimdall-Member", "")
        with lock:
            if self.path == "/send":
                if len(messages) >= MAX_MESSAGES:
                    return self.answer(429, {"error": "mailbox full"})
                body = req.get("body", "")
                if not isinstance(body, str):
                    body = json.dumps(body)
                fields = {k: v for k, v in req.items() if k not in ("from", "to", "body")}
                m = store(str(req.get("from", sender)), sender, str(req.get("to", "*")), body[:MAX_BODY], fields)
                save()
                return self.answer(200, {"id": m["id"]})
            if self.path == "/recv":
                reader = sender or str(req.get("agent", ""))
                out = []
                for m in messages:
                    if reader in m["read_by"]:
                        continue
                    if m["to"] == reader or (m["to"] in ("*", "all") and m["from"] != reader):
                        out.append(deliver(m, reader))
                if out:
                    save()
                return self.answer(200, {"messages": out})
        self.answer(404, {"error": "not found"})

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    plant()
    save()
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
