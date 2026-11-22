#!/usr/bin/env python3
"""Bridge between the heimdall-voice/1 caller and a real-time call over WebRTC or SIP.

Part of Heimdall (bench/voice), docs/voice-realtime.md. Standard library only.
The voice_rtc adapter runs it as the agent of a voice task. It dials the
bench's voice caller (the simulated customer: it speaks, plays the agent's
audio on a real-time clock, transcribes it and writes the call timeline) and
Heimdall's real-time voice gateway (bench/voice/rtcgateway.py, the compose
service voice-rtc beside a LiveKit server). The gateway joins a LiveKit room
as the caller, brings the agent in (a LiveKit Agents worker by explicit
dispatch, a Pipecat or other bot told the room through its start webhook, a
worker that joins every room, or a phone number dialled through LiveKit SIP)
and carries audio both ways. This bridge only carries audio and control
messages between the two sockets, so latency to first audio, turn latency,
barge-in, dead air and talk-over are measured by the caller on what it
played, exactly as for an in-container agent; the gateway's setup time and
the agent's own transcripts are kept as agent-reported lines.

The gateway protocol, heimdall-rtc/1 (one WebSocket, GET /dial with
Authorization: Bearer <gateway token>):

  bridge -> gateway  {"type": "dial", "protocol": "heimdall-rtc/1", ...}
                     binary pcm_s16le 16 kHz mono (the caller's audio)
                     {"type": "hangup", "reason": ...}
  gateway -> bridge  {"type": "ready", "room": ..., "setup_ms": ...}
                     binary pcm_s16le 16 kHz mono (the agent's audio)
                     {"type": "transcript", "role": "agent"|"user", "text": ..., "final": ...}
                     {"type": "hangup", "reason": ...}
                     {"type": "error", "code": ..., "message": ...}

Environment:
  BENCH_RTC_GATEWAY_URL   ws(s)://host[:port]/dial of the gateway
  BENCH_RTC_GATEWAY_TOKEN the gateway's bearer token
  BENCH_RTC_DIAL          the dial message (JSON) without credentials
  BENCH_RTC_LK_API_KEY, BENCH_RTC_LK_API_SECRET, BENCH_RTC_START_TOKEN,
  BENCH_RTC_TRUNK_USER, BENCH_RTC_TRUNK_PASSWORD
                          the org's credentials (from Settings > Secrets,
                          expanded by the engine), added to the dial message
  BENCH_REMOTE_CONNECT    host:port actually dialled (the run's egress forwarder)
  BENCH_VOICE_SETUP_S     call setup timeout, default 30
  BENCH_VOICE_URL         the caller (default ws://customer:8000/call)

Exit status: 0 when the call ran (whoever ended it), 3 when the gateway or
the agent could not be reached or refused the call (the message says which).
"""

import base64
import json
import os
import socket
import ssl
import sys
import threading
import time
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import wsproto  # noqa: E402

PROTOCOL = "heimdall-rtc/1"
EXIT_PLATFORM = 3
MAX_TO_CALLER = 32000  # heimdall-voice/1: at most one second of audio per message
DEFAULT_CALLER = "ws://customer:8000/call"
CREDENTIALS = [  # env -> (dial block, field)
    ("BENCH_RTC_LK_API_KEY", "livekit", "api_key"),
    ("BENCH_RTC_LK_API_SECRET", "livekit", "api_secret"),
    ("BENCH_RTC_START_TOKEN", "livekit", "start_token"),
    ("BENCH_RTC_TRUNK_USER", "sip", "trunk_username"),
    ("BENCH_RTC_TRUNK_PASSWORD", "sip", "trunk_password"),
]


class GatewayError(Exception):
    """The gateway or the agent behind it failed or refused the call."""


def log(*a):
    sys.stderr.write("rtcbridge: " + " ".join(str(x) for x in a) + "\n")
    sys.stderr.flush()


def redact(text, secrets):
    text = str(text)
    for s in secrets:
        if s and len(s) >= 4:
            text = text.replace(s, "[redacted]")
    return text


def dial_secrets(dial):
    """Every secret value in a dial message, for redaction."""
    out = []
    for block in (dial.get("livekit") or {}, dial.get("sip") or {}):
        for k, v in block.items():
            if isinstance(v, str) and k in ("api_key", "api_secret", "start_token", "trunk_username", "trunk_password"):
                out.append(v)
    return out


def ws_connect(url, headers, timeout, addr=None):
    """A blocking ws:// or wss:// client connection (wsproto.Conn) dialled at addr (default: the URL's host)."""
    u = urllib.parse.urlsplit(url)
    if u.scheme not in ("ws", "wss"):
        raise GatewayError(f"want a ws:// or wss:// gateway URL, got {u.scheme}://")
    default = 443 if u.scheme == "wss" else 80
    addr = addr or (u.hostname, u.port or default)
    sock = socket.create_connection(addr, timeout=timeout)
    if u.scheme == "wss":
        sock = ssl.create_default_context().wrap_socket(sock, server_hostname=u.hostname)
    sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    host = u.hostname if (u.port or default) == default else f"{u.hostname}:{u.port}"
    key = base64.b64encode(os.urandom(16)).decode()
    path = (u.path or "/") + ("?" + u.query if u.query else "")
    lines = [f"GET {path} HTTP/1.1", f"Host: {host}", "Upgrade: websocket", "Connection: Upgrade",
             "Sec-WebSocket-Key: " + key, "Sec-WebSocket-Version: 13", "Sec-WebSocket-Protocol: " + PROTOCOL]
    lines += [f"{k}: {v}" for k, v in headers.items()]
    sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode())
    raw = b""
    while b"\r\n\r\n" not in raw:
        chunk = sock.recv(4096)
        if not chunk:
            raise ConnectionError("websocket upgrade: connection closed")
        raw += chunk
        if len(raw) > 16384:
            raise ConnectionError("websocket upgrade: response head too large")
    head, rest = raw.split(b"\r\n\r\n", 1)
    status = head.split(b"\r\n", 1)[0].decode("latin-1")
    if " 101 " not in status + " ":
        raise GatewayError("the gateway refused the websocket: " + status)
    if wsproto.accept_key(key).encode() not in head:
        raise ConnectionError("websocket upgrade: bad Sec-WebSocket-Accept")
    return wsproto.Conn(sock, rest)


def forwarder_addr(env):
    """(host, port) of BENCH_REMOTE_CONNECT, or None to dial the URL's own host."""
    v = env.get("BENCH_REMOTE_CONNECT", "")
    if not v:
        return None
    host, _, port = v.rpartition(":")
    return host.strip("[]"), int(port)


def open_gateway(env):
    """Dial the gateway, send the dial message and wait for ready. Returns (conn, ready, dial)."""
    url = env.get("BENCH_RTC_GATEWAY_URL", "")
    if not url:
        raise GatewayError("BENCH_RTC_GATEWAY_URL is not set: the operator has not configured the real-time voice gateway")
    try:
        dial = json.loads(env.get("BENCH_RTC_DIAL") or "")
    except ValueError:
        raise GatewayError("BENCH_RTC_DIAL is not a JSON object") from None
    if not isinstance(dial, dict):
        raise GatewayError("BENCH_RTC_DIAL is not a JSON object")
    dial["type"] = "dial"
    dial["protocol"] = PROTOCOL
    for env_name, block, key in CREDENTIALS:
        if env.get(env_name):
            dial.setdefault(block, {})[key] = env[env_name]
    setup_s = float(env.get("BENCH_VOICE_SETUP_S") or 30)
    dial.setdefault("setup_s", setup_s)
    secrets = dial_secrets(dial) + [env.get("BENCH_RTC_GATEWAY_TOKEN", "")]
    headers = {}
    if env.get("BENCH_RTC_GATEWAY_TOKEN"):
        headers["Authorization"] = "Bearer " + env["BENCH_RTC_GATEWAY_TOKEN"]
    conn, last = None, None
    for _ in range(20):  # the forwarder may still be starting
        try:
            conn = ws_connect(url, headers, setup_s, forwarder_addr(env))
            break
        except (ConnectionRefusedError, socket.gaierror) as e:
            last = e
            time.sleep(0.25)
        except (OSError, ConnectionError) as e:
            raise GatewayError(f"cannot reach the voice gateway: {redact(e, secrets)}") from None
    if conn is None:
        raise GatewayError(f"cannot reach the voice gateway: {last}")
    try:
        return wait_ready(conn, dial, setup_s, secrets)
    except GatewayError:
        conn.close()
        raise


def wait_ready(conn, dial, setup_s, secrets):
    conn.send_text(json.dumps(dial))
    deadline = time.monotonic() + setup_s + 15  # the gateway enforces setup_s itself; this is the backstop
    while time.monotonic() < deadline:
        try:
            msg = conn.recv(timeout=0.5)
        except wsproto.Closed as e:
            raise GatewayError(f"the gateway closed the call during setup ({e.code} {redact(e.reason, secrets)})") from None
        if msg is None:
            continue
        kind, data = msg
        if kind != "text":
            continue  # audio before ready: the agent is early; ready follows
        try:
            m = json.loads(data)
        except ValueError:
            continue
        t = m.get("type")
        if t == "ready":
            return conn, m, dial
        if t == "error":
            raise GatewayError(f"{m.get('code') or 'error'}: {redact(m.get('message') or '', secrets)}")
        if t == "hangup":
            raise GatewayError(f"the call ended during setup: {m.get('reason') or ''}")
    raise GatewayError(f"the agent did not join the call within {int(setup_s)} s")


class Bridge:
    def __init__(self, gw, caller, secrets):
        self.gw = gw
        self.caller = caller
        self.secrets = secrets
        self.done = threading.Event()
        self.reason = None
        self.caller_ended = False
        self.agent_bytes = 0
        self.caller_bytes = 0

    def end(self, reason):
        if self.reason is None:
            self.reason = reason
        self.done.set()

    def report(self, role, text, final=True):
        try:
            self.caller.send_text(json.dumps({"type": "transcript", "role": role, "text": str(text)[:2000], "final": bool(final)}))
        except (wsproto.Closed, OSError):
            pass

    def uplink(self):
        """Caller -> gateway: the caller's audio, and its hangup."""
        try:
            while not self.done.is_set():
                msg = self.caller.recv(timeout=0.2)
                if msg is None:
                    continue
                kind, data = msg
                if kind == "binary":
                    self.caller_bytes += len(data)
                    self.gw.send_binary(data)
                    continue
                try:
                    m = json.loads(data)
                except ValueError:
                    continue
                if m.get("type") == "hangup":
                    self.caller_ended = True
                    self.end("caller_hangup")
        except wsproto.Closed:
            self.caller_ended = True
            self.end("caller_hangup")
        except (OSError, ConnectionError):
            self.caller_ended = True
            self.end("caller_disconnected")

    def downlink(self):
        """Gateway -> caller: the agent's audio, its transcripts and the end of the call."""
        try:
            while not self.done.is_set():
                msg = self.gw.recv(timeout=0.2)
                if msg is None:
                    continue
                kind, data = msg
                if kind == "binary":
                    if len(data) % 2:
                        data = data[:-1]
                    self.agent_bytes += len(data)
                    for i in range(0, len(data), MAX_TO_CALLER):
                        self.caller.send_binary(data[i:i + MAX_TO_CALLER])
                    continue
                try:
                    m = json.loads(data)
                except ValueError:
                    continue
                t = m.get("type")
                if t == "transcript" and isinstance(m.get("text"), str):
                    role = "agent" if m.get("role") == "agent" else "user"
                    self.report(role, m["text"], m.get("final", True))
                elif t == "hangup":
                    self.end("agent_hangup:" + str(m.get("reason") or ""))
                elif t == "error":
                    log("gateway error during the call:", m.get("code"), redact(m.get("message") or "", self.secrets))
                    self.end("gateway_error")
        except wsproto.Closed as e:
            if not self.done.is_set():
                log("gateway closed:", e.code, redact(e.reason, self.secrets))
            self.end("gateway_closed")
        except (OSError, ConnectionError) as e:
            if not self.done.is_set():
                log("gateway socket failed:", redact(e, self.secrets))
            self.end("gateway_closed")

    def run(self, ready):
        try:
            self.caller.send_text(json.dumps({"type": "event", "name": "rtc_ready",
                                              "text": "room %s, agent %s, setup %s ms" % (ready.get("room", ""), ready.get("agent", ""), ready.get("setup_ms", ""))}))
        except (wsproto.Closed, OSError):
            pass
        up = threading.Thread(target=self.uplink, daemon=True)
        down = threading.Thread(target=self.downlink, daemon=True)
        up.start()
        down.start()
        self.done.wait()
        if self.caller_ended:
            try:
                self.gw.send_text(json.dumps({"type": "hangup", "reason": self.reason}))
            except (wsproto.Closed, OSError):
                pass
        try:
            self.gw.close()
        except OSError:
            pass
        if not self.caller_ended:
            try:
                self.caller.send_text(json.dumps({"type": "hangup", "reason": self.reason or "agent_hangup"}))
            except (wsproto.Closed, OSError):
                pass
        time.sleep(0.3)
        try:
            self.caller.close()
        except OSError:
            pass
        log("call ended:", self.reason, "caller bytes", self.caller_bytes, "agent bytes", self.agent_bytes)
        return 0


def connect_caller(env, timeout=60):
    url = env.get("BENCH_VOICE_URL") or DEFAULT_CALLER
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            conn = wsproto.connect(url, timeout=10)
            return conn
        except (OSError, ConnectionError) as e:
            last = e
            time.sleep(0.5)
    raise ConnectionError(f"cannot reach the caller at {url}: {last}")


def main(env=None):
    env = dict(os.environ) if env is None else env
    try:
        gw, ready, dial = open_gateway(env)
    except GatewayError as e:
        log("the call:", e)
        return EXIT_PLATFORM
    secrets = dial_secrets(dial) + [env.get("BENCH_RTC_GATEWAY_TOKEN", "")]
    try:
        caller = connect_caller(env)
    except ConnectionError as e:
        log("the caller:", e)
        try:
            gw.send_text(json.dumps({"type": "hangup", "reason": "no_caller"}))
            gw.close()
        except (wsproto.Closed, OSError):
            pass
        return 1
    return Bridge(gw, caller, secrets).run(ready)


if __name__ == "__main__":
    sys.exit(main())
