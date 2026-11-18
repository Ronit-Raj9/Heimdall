#!/bin/sh
# Reference control: does the task the way a correct voice support agent would. Admission runs it in the agent's place and requires the verifier to pass it three times in a row.
# It answers the call (heimdall-voice/1 on the customer service, docs/voice-protocol.md),
# speaks in tone code (the bench's deterministic test codec, which the caller
# reads exactly), and works the helpdesk through the same MCP server the
# agent gets (BENCH_MCP_SERVERS names its URL).
exec python3 -I - <<'PY'
import json, os, sys, tempfile, time, urllib.request

# The caller serves the protocol helpers, so the control needs nothing installed.
CALLER = os.environ.get("BENCH_VOICE_URL") or "ws://customer:8000/call"
BASE = "http://" + CALLER.split("://", 1)[1].split("/", 1)[0]
KIT = tempfile.mkdtemp()
for name in ("voiceclient.py", "tonecode.py", "wsproto.py"):
    for _ in range(120):
        try:
            with urllib.request.urlopen(BASE + "/kit/" + name, timeout=5) as r:
                data = r.read()
            break
        except OSError:
            time.sleep(0.5)
    else:
        sys.exit("cannot fetch the voice kit from " + BASE)
    with open(os.path.join(KIT, name), "wb") as f:
        f.write(data)
sys.path.insert(0, KIT)
import voiceclient  # noqa: E402

servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")


def url(service):
    return (servers.get(service) or {}).get("url") or f"http://toolproxy:9200/s/{service}/mcp"


class Client:
    def __init__(self, service):
        self.url, self.session, self.next_id = url(service), {}, 0
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "control", "version": "1"}})
        self.rpc("notifications/initialized", notify=True)

    def rpc(self, method, params=None, notify=False):
        msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
        if not notify:
            self.next_id += 1
            msg["id"] = self.next_id
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        headers.update(self.session)
        req = urllib.request.Request(self.url, data=json.dumps(msg).encode(), headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=60) as resp:
            if resp.headers.get("Mcp-Session-Id"):
                self.session["Mcp-Session-Id"] = resp.headers["Mcp-Session-Id"]
            body = resp.read().decode()
        if notify:
            return None
        if body.lstrip().startswith("data:") or "\ndata:" in body:
            body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
        answer = json.loads(body)
        if "error" in answer:
            sys.exit(f"{method}: {answer['error']}")
        return answer["result"]

    def call(self, tool, args):
        result = self.rpc("tools/call", {"name": tool, "arguments": args})
        text = "".join(c.get("text", "") for c in result.get("content", []))
        if result.get("isError"):
            sys.exit(f"{tool}: {text}")
        return text


desk = Client("helpdesk")
phone = voiceclient.VoiceCall(CALLER, timeout=60)


def say(message):
    """Speak one line (tone code: the caller reads it exactly) and wait for the caller's answer."""
    phone.say_tone(message)
    try:
        heard = phone.listen(max_wait_s=40)
    except voiceclient.Hangup:
        return None
    print("caller answered:", "nothing" if heard is None else f"{len(heard) // 32} ms of audio")
    return heard


def call(tool, args):
    return json.loads(desk.call(tool, args))
say("Hello, thank you for contacting Acme Brew Club. How can I help?")
call("find_customer", {"email": "leo.park@example.com"})
call("get_order", {"order_id": "B-3002"})
call("get_policy", {})
say("I'm sorry, Leo. Order B-3002 was delivered on 20 September, and our policy only refunds damaged, faulty or wrong items within 30 days of delivery, so I can't refund it.")
if not phone.wait_end(20):
    phone.hangup("done")
PY
