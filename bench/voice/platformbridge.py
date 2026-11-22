#!/usr/bin/env python3
"""Bridge between the heimdall-voice/1 caller and a voice agent hosted elsewhere.

Part of Heimdall (bench/voice), docs/voice-platforms.md. Standard library only.
The voice_platform adapter runs it as the agent of a voice task: it dials the
bench's voice caller (the simulated customer, which speaks with local text to
speech, plays the agent's audio on a real-time clock, transcribes it and
writes the call timeline), then opens the agent's own call WebSocket and
translates between the two. Latency to first audio, turn latency, barge-in
handling, dead air and talk-over are measured by the caller on what it played,
exactly as for an in-container agent; this bridge only carries audio.

Framings (BENCH_VOICE_FRAMING):

  twilio      Twilio Media Streams: the bridge plays Twilio. It sends
              connected, start (audio/x-mulaw, 8000 Hz, mono), a media event
              every 20 ms (the line carries silence while the caller is
              quiet, as a phone line does) and stop. The agent answers with
              media events, clear (barge-in: the caller drops the queued
              audio) and mark (echoed back once the audio before it played).
  vapi        Vapi's websocket transport: binary pcm_s16le 16 kHz both ways,
              {"type": "hangup"} to end. With BENCH_VOICE_PLATFORM=vapi the
              call is created first (POST /call, transport vapi.websocket)
              and the bridge dials transport.websocketCallUrl.
  elevenlabs  ElevenLabs Agents conversation events: user_audio_chunk
              (base64, the format conversation_initiation_metadata asks for)
              up, audio events down, interruption as barge-in, ping answered
              with pong, agent_response and user_transcript kept as
              agent-reported transcript lines.

Environment:
  BENCH_VOICE_PLATFORM      vapi | elevenlabs | websocket
  BENCH_VOICE_FRAMING       twilio | vapi | elevenlabs (websocket only; the
                            other platforms use their own)
  BENCH_VOICE_PLATFORM_URL  API base (vapi, elevenlabs) or the wss:// endpoint
  BENCH_VOICE_AGENT_ID      the platform's assistant or agent id
  BENCH_VOICE_PLATFORM_KEY  the API key or endpoint token (from Settings > Secrets)
  BENCH_REMOTE_CONNECT      host:port actually dialled (the run's egress forwarder)
  BENCH_VOICE_SESSION       the run's session id, sent as call metadata
  BENCH_VOICE_SETUP_S       call setup timeout, default 30
  BENCH_VOICE_URL           the caller (default ws://customer:8000/call)

Exit status: 0 when the call ran (whoever ended it), 3 when the agent's
platform could not be reached or refused the call (the message says which).
"""

import array
import base64
import http.client
import json
import os
import socket
import ssl
import sys
import threading
import time
import urllib.parse
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tonecode  # noqa: E402
import voiceclient  # noqa: E402
import wsproto  # noqa: E402

FRAME_MS = 20
RATE = tonecode.SAMPLE_RATE
MAX_TO_CALLER = 32000  # heimdall-voice/1: at most one second per message
EXIT_PLATFORM = 3
LINE_GAP_MS = 200  # caller silence after which the line carries silence

# ---- G.711 mu-law ----

_EXP = [0] * 256
for _i in range(1, 256):
    _EXP[_i] = min(7, _i.bit_length() - 1)


def _lin2ulaw(s):
    sign = 0x80 if s < 0 else 0
    if s < 0:
        s = -s
    s = min(s, 32635) + 0x84
    exp = _EXP[(s >> 7) & 0xFF]
    mant = (s >> (exp + 3)) & 0x0F
    return ~(sign | (exp << 4) | mant) & 0xFF


def _ulaw2lin(u):
    u = ~u & 0xFF
    t = (((u & 0x0F) << 3) + 0x84) << ((u & 0x70) >> 4)
    return 0x84 - t if u & 0x80 else t - 0x84


_ENC = bytes(_lin2ulaw(s - 32768) for s in range(65536))
_DEC = [_ulaw2lin(u) for u in range(256)]


def ulaw_encode(pcm):
    """16-bit little-endian PCM to mu-law bytes (same sample rate)."""
    return bytes(_ENC[s + 32768] for s in tonecode.to_samples(pcm))


def ulaw_decode(data):
    """Mu-law bytes to 16-bit little-endian PCM (same sample rate)."""
    return tonecode.to_bytes(array.array("h", (_DEC[b] for b in data)))


def from_format(data, fmt):
    """Agent audio in fmt (pcm_<rate> or ulaw_8000) to the caller's 16 kHz PCM."""
    if fmt == "ulaw_8000":
        return tonecode.resample(ulaw_decode(data), 8000, RATE)
    rate = int(fmt.split("_", 1)[1]) if fmt.startswith("pcm_") else RATE
    if len(data) % 2:
        data = data[:-1]
    return tonecode.resample(data, rate, RATE)


def to_format(pcm, fmt):
    """Caller audio (16 kHz PCM) to fmt."""
    if fmt == "ulaw_8000":
        return ulaw_encode(tonecode.resample(pcm, RATE, 8000))
    rate = int(fmt.split("_", 1)[1]) if fmt.startswith("pcm_") else RATE
    return tonecode.resample(pcm, RATE, rate)


def log(*a):
    print("platformbridge:", *a, file=sys.stderr, flush=True)


class PlatformError(Exception):
    """The agent's platform could not be reached or refused the call."""


def redact(text, secret):
    text = str(text)
    if secret:
        text = text.replace(secret, "[redacted]")
    return text[:400]


# ---- the platform side ----

class Target:
    """Where the platform is: URLs keep their host, sockets go to the egress forwarder when set."""

    def __init__(self, env):
        self.env = env
        self.key = env.get("BENCH_VOICE_PLATFORM_KEY", "")
        self.setup_s = float(env.get("BENCH_VOICE_SETUP_S") or 30)
        self.addr = None
        if env.get("BENCH_REMOTE_CONNECT"):
            host, _, port = env["BENCH_REMOTE_CONNECT"].rpartition(":")
            self.addr = (host, int(port))

    def _dial_addr(self, u):
        if self.addr:
            return self.addr
        return (u.hostname, u.port or (443 if u.scheme in ("https", "wss") else 80))

    def http_json(self, method, url, headers, body=None):
        u = urllib.parse.urlsplit(url)
        addr = self._dial_addr(u)
        sock = None
        for _ in range(20):  # the forwarder may still be starting
            try:
                sock = socket.create_connection(addr, self.setup_s)
                break
            except (ConnectionRefusedError, socket.gaierror):
                time.sleep(0.25)
        if sock is None:
            raise PlatformError(f"cannot reach {u.hostname}")
        if u.scheme == "https":
            sock = ssl.create_default_context().wrap_socket(sock, server_hostname=u.hostname)
        conn = http.client.HTTPConnection(u.hostname, u.port or (443 if u.scheme == "https" else 80), timeout=self.setup_s)
        conn.sock = sock
        raw = json.dumps(body).encode() if body is not None else None
        h = {"Accept": "application/json"}
        if raw is not None:
            h["Content-Type"] = "application/json"
        h.update(headers)
        path = (u.path or "/") + ("?" + u.query if u.query else "")
        try:
            conn.request(method, path, body=raw, headers=h)
            resp = conn.getresponse()
            data = resp.read(1 << 20)
        except (OSError, http.client.HTTPException) as e:
            raise PlatformError(f"{method} {u.hostname}{u.path}: {e}") from None
        finally:
            conn.close()
        if resp.status < 200 or resp.status > 299:
            raise PlatformError(f"{method} {u.hostname}{u.path} answered HTTP {resp.status}: {redact(data.decode('utf-8', 'replace'), self.key)}")
        try:
            return json.loads(data or b"{}")
        except ValueError:
            raise PlatformError(f"{method} {u.hostname}{u.path} answered something that is not JSON") from None

    def ws(self, url, headers=None):
        u = urllib.parse.urlsplit(url)
        last = None
        for _ in range(20):
            try:
                return ws_connect(url, headers or {}, self.setup_s, self._dial_addr(u))
            except (ConnectionRefusedError, socket.gaierror) as e:
                last = e
                time.sleep(0.25)
            except (OSError, ConnectionError) as e:
                raise PlatformError(f"websocket to {u.hostname}: {redact(e, self.key)}") from None
        raise PlatformError(f"websocket to {u.hostname}: {last}")


def ws_connect(url, headers, timeout, addr):
    """A blocking ws:// or wss:// client connection (wsproto.Conn) dialled at addr.

    TLS and the Host header keep the URL's host while the socket goes to
    addr, the run's egress forwarder. wsproto.connect stays ws:// only: the
    caller side never leaves the run network.
    """
    u = urllib.parse.urlsplit(url)
    if u.scheme not in ("ws", "wss"):
        raise PlatformError(f"want a ws:// or wss:// URL, got {u.scheme}://")
    sock = socket.create_connection(addr, timeout=timeout)
    if u.scheme == "wss":
        sock = ssl.create_default_context().wrap_socket(sock, server_hostname=u.hostname)
    sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    default = 443 if u.scheme == "wss" else 80
    host = u.hostname if (u.port or default) == default else f"{u.hostname}:{u.port}"
    key = base64.b64encode(os.urandom(16)).decode()
    path = (u.path or "/") + ("?" + u.query if u.query else "")
    lines = [f"GET {path} HTTP/1.1", f"Host: {host}", "Upgrade: websocket", "Connection: Upgrade",
             "Sec-WebSocket-Key: " + key, "Sec-WebSocket-Version: 13"]
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
        raise ConnectionError("websocket upgrade refused: " + status)
    if wsproto.accept_key(key).encode() not in head:
        raise ConnectionError("websocket upgrade: bad Sec-WebSocket-Accept")
    return wsproto.Conn(sock, rest)


class Link:
    """One framing. on_audio(pcm16k), on_clear(), on_report(role, text) and on_end(reason) are set by the bridge."""

    send_ms = FRAME_MS

    def __init__(self, conn):
        self.conn = conn
        self.on_audio = self.on_clear = self.on_report = self.on_end = None
        self.play_end = 0.0  # monotonic time the agent audio sent so far finishes playing

    def start(self):
        pass

    def send_caller(self, pcm):
        raise NotImplementedError

    def handle(self, kind, data):
        raise NotImplementedError

    def tick(self):
        pass

    def stop(self, reason):
        pass


class TwilioLink(Link):
    def __init__(self, conn, session):
        super().__init__(conn)
        self.stream = "MZ" + uuid.uuid4().hex
        self.call = "CA" + uuid.uuid4().hex
        self.account = "AC" + uuid.uuid4().hex
        self.session = session
        self.seq = 0
        self.chunk = 0
        self.t0 = time.monotonic()
        self.marks = []  # (play time, name)
        self.lock = threading.Lock()

    def _send(self, ev):
        with self.lock:
            self.seq += 1
            ev["sequenceNumber"] = str(self.seq)
            self.conn.send_text(json.dumps(ev))

    def start(self):
        self.conn.send_text(json.dumps({"event": "connected", "protocol": "Call", "version": "1.0.0"}))
        self._send({"event": "start", "streamSid": self.stream, "start": {
            "streamSid": self.stream, "accountSid": self.account, "callSid": self.call, "tracks": ["inbound"],
            "customParameters": {"heimdall_session": self.session},
            "mediaFormat": {"encoding": "audio/x-mulaw", "sampleRate": 8000, "channels": 1}}})

    def send_caller(self, pcm):
        self.chunk += 1
        self._send({"event": "media", "streamSid": self.stream, "media": {
            "track": "inbound", "chunk": str(self.chunk), "timestamp": str(int((time.monotonic() - self.t0) * 1000)),
            "payload": base64.b64encode(ulaw_encode(tonecode.resample(pcm, RATE, 8000))).decode()}})

    def handle(self, kind, data):
        if kind != "text":
            return
        try:
            ev = json.loads(data)
        except ValueError:
            return
        e = ev.get("event")
        if e == "media":
            payload = (ev.get("media") or {}).get("payload") or ""
            self.on_audio(tonecode.resample(ulaw_decode(base64.b64decode(payload)), 8000, RATE))
        elif e == "clear":
            self.on_clear()
            with self.lock:
                marks, self.marks = self.marks, []
            for _, name in marks:
                self._send({"event": "mark", "streamSid": self.stream, "mark": {"name": name}})
        elif e == "mark":
            name = (ev.get("mark") or {}).get("name") or ""
            with self.lock:
                self.marks.append((max(self.play_end, time.monotonic()), name))

    def tick(self):
        now = time.monotonic()
        with self.lock:
            due = [m for m in self.marks if m[0] <= now]
            self.marks = [m for m in self.marks if m[0] > now]
        for _, name in due:
            self._send({"event": "mark", "streamSid": self.stream, "mark": {"name": name}})

    def stop(self, reason):
        try:
            self._send({"event": "stop", "streamSid": self.stream, "stop": {"accountSid": self.account, "callSid": self.call}})
        except (OSError, wsproto.Closed):
            pass


class VapiLink(Link):
    def send_caller(self, pcm):
        self.conn.send_binary(pcm)

    def handle(self, kind, data):
        if kind == "binary":
            self.on_audio(bytes(data[: len(data) // 2 * 2]))
            return
        try:
            ev = json.loads(data)
        except ValueError:
            return
        t = ev.get("type")
        if t in ("clear", "user-interrupted", "interruption"):
            self.on_clear()
        elif t in ("hangup", "end-call"):
            self.on_end("agent_hangup")
        elif t == "transcript" and ev.get("transcriptType") == "final":
            self.on_report("caller" if ev.get("role") == "user" else "agent", ev.get("transcript") or "")

    def stop(self, reason):
        try:
            self.conn.send_text(json.dumps({"type": "hangup"}))
        except (OSError, wsproto.Closed):
            pass


class ElevenLink(Link):
    send_ms = 100  # ElevenLabs takes user audio in chunks; 100 ms keeps latency low

    def __init__(self, conn, session, setup_s):
        super().__init__(conn)
        self.session = session
        self.setup_s = setup_s
        self.in_fmt = "pcm_16000"
        self.out_fmt = "pcm_16000"
        self.buf = bytearray()

    def start(self):
        self.conn.send_text(json.dumps({"type": "conversation_initiation_client_data", "user_id": self.session,
                                        "conversation_config_override": {}, "dynamic_variables": {}}))
        deadline = time.monotonic() + self.setup_s
        while time.monotonic() < deadline:
            msg = self.conn.recv(timeout=0.5)
            if msg is None or msg[0] != "text":
                continue
            try:
                ev = json.loads(msg[1])
            except ValueError:
                continue
            if ev.get("type") == "conversation_initiation_metadata":
                md = ev.get("conversation_initiation_metadata_event") or {}
                self.in_fmt = md.get("user_input_audio_format") or self.in_fmt
                self.out_fmt = md.get("agent_output_audio_format") or self.out_fmt
                for f in (self.in_fmt, self.out_fmt):
                    if f != "ulaw_8000" and not f.startswith("pcm_"):
                        raise PlatformError(f"ElevenLabs audio format {f!r} is not supported")
                log("conversation", md.get("conversation_id", ""), "in", self.in_fmt, "out", self.out_fmt)
                return
            if ev.get("type") == "ping":
                self._pong(ev)
        raise PlatformError("ElevenLabs sent no conversation_initiation_metadata")

    def _pong(self, ev):
        self.conn.send_text(json.dumps({"type": "pong", "event_id": (ev.get("ping_event") or {}).get("event_id", 0)}))

    def send_caller(self, pcm):
        self.conn.send_text(json.dumps({"user_audio_chunk": base64.b64encode(to_format(pcm, self.in_fmt)).decode()}))

    def handle(self, kind, data):
        if kind != "text":
            return
        try:
            ev = json.loads(data)
        except ValueError:
            return
        t = ev.get("type")
        if t == "audio":
            b64 = (ev.get("audio_event") or {}).get("audio_base_64") or ""
            self.on_audio(from_format(base64.b64decode(b64), self.out_fmt))
        elif t == "interruption":
            self.on_clear()
        elif t == "ping":
            self._pong(ev)
        elif t == "agent_response":
            self.on_report("agent", (ev.get("agent_response_event") or {}).get("agent_response") or "")
        elif t == "user_transcript":
            self.on_report("caller", (ev.get("user_transcription_event") or {}).get("user_transcript") or "")


def open_link(env):
    """Create the call on the agent's platform and return its Link (not started)."""
    target = Target(env)
    platform = env.get("BENCH_VOICE_PLATFORM", "")
    base = (env.get("BENCH_VOICE_PLATFORM_URL") or "").rstrip("/")
    agent = env.get("BENCH_VOICE_AGENT_ID", "")
    session = env.get("BENCH_VOICE_SESSION", "")
    key = target.key
    if platform == "vapi":
        base = base or "https://api.vapi.ai"
        call = target.http_json("POST", base + "/call", {"Authorization": "Bearer " + key}, {
            "assistantId": agent, "metadata": {"heimdall_session": session},
            "transport": {"provider": "vapi.websocket",
                          "audioFormat": {"format": "pcm_s16le", "container": "raw", "sampleRate": RATE}}})
        url = (call.get("transport") or {}).get("websocketCallUrl") or ""
        if not url.startswith(("wss://", "ws://")):
            raise PlatformError("Vapi created the call without transport.websocketCallUrl")
        if urllib.parse.urlsplit(url).hostname != urllib.parse.urlsplit(base).hostname:
            raise PlatformError(f"Vapi's call socket is on {urllib.parse.urlsplit(url).hostname}, not the API host the run may reach")
        log("vapi call", call.get("id", ""))
        return VapiLink(target.ws(url))
    if platform == "elevenlabs":
        base = base or "https://api.elevenlabs.io"
        if key:
            got = target.http_json("GET", base + "/v1/convai/conversation/get-signed-url?agent_id=" + urllib.parse.quote(agent),
                                   {"xi-api-key": key})
            url = got.get("signed_url") or ""
            if not url.startswith(("wss://", "ws://")):
                raise PlatformError("ElevenLabs answered get-signed-url without a signed_url")
        else:
            url = base.replace("https://", "wss://", 1).replace("http://", "ws://", 1) + \
                "/v1/convai/conversation?agent_id=" + urllib.parse.quote(agent)
        return ElevenLink(target.ws(url), session, target.setup_s)
    if platform == "websocket":
        headers = {"Authorization": "Bearer " + key} if key else {}
        conn = target.ws(base, headers)
        framing = env.get("BENCH_VOICE_FRAMING") or "twilio"
        if framing == "twilio":
            return TwilioLink(conn, session)
        if framing == "vapi":
            return VapiLink(conn)
        if framing == "elevenlabs":
            return ElevenLink(conn, session, target.setup_s)
        raise PlatformError(f"unknown framing {framing!r}")
    raise PlatformError(f"unknown platform {platform!r}")


# ---- the bridge ----

class Bridge:
    def __init__(self, link, phone, secret=""):
        self.link = link
        self.phone = phone
        self.secret = secret
        self.done = threading.Event()
        self.reason = ""
        link.on_audio = self.to_caller
        link.on_clear = self.clear
        link.on_report = self.report
        link.on_end = self.end

    def end(self, reason):
        if not self.done.is_set():
            self.reason = reason
            self.done.set()

    def to_caller(self, pcm):
        if not pcm:
            return
        now = time.monotonic()
        self.link.play_end = max(self.link.play_end, now) + len(pcm) / (RATE * 2.0)
        for i in range(0, len(pcm), MAX_TO_CALLER):
            self.phone.conn.send_binary(pcm[i:i + MAX_TO_CALLER])

    def clear(self):
        self.link.play_end = time.monotonic()
        self.phone.clear()

    def report(self, role, text):
        if text:
            try:
                self.phone.report(role, text)
            except (OSError, wsproto.Closed):
                pass

    def uplink(self):
        """The caller's line to the agent at real-time pace, link.send_ms per message.

        Caller audio goes on as it arrives, in whole messages. A phone line
        never goes quiet, so once the caller has sent nothing for LINE_GAP_MS
        the line carries silence; a frame that is only late (network jitter)
        is waited for, never replaced by silence inside an utterance.
        """
        idx = 0
        step = self.link.send_ms / 1000.0
        size = int(RATE * 2 * step)
        buf = bytearray()
        last = time.monotonic()
        nxt = time.monotonic()
        while not self.done.is_set():
            nxt += step
            d = nxt - time.monotonic()
            if d > 0:
                time.sleep(d)
            with self.phone.lock:
                new = self.phone.frames[idx:]
                idx = len(self.phone.frames)
                ended = self.phone.ended
            for t, f in new:
                buf += f
                last = t
            out = []
            while len(buf) >= size:
                out.append(bytes(buf[:size]))
                del buf[:size]
            if not out and time.monotonic() - last >= LINE_GAP_MS / 1000.0:
                out.append(bytes(buf) + bytes(size - len(buf)))
                buf.clear()
            try:
                for m in out:
                    self.link.send_caller(m)
            except (OSError, wsproto.Closed):
                self.end("agent_disconnected")
                return
            if ended:
                self.end("caller_hangup")
                return

    def run(self):
        self.link.start()
        threading.Thread(target=self.uplink, daemon=True).start()
        try:
            while not self.done.is_set():
                msg = self.link.conn.recv(timeout=0.05)
                self.link.tick()
                if msg is not None:
                    self.link.handle(*msg)
                if self.phone.ended:
                    self.end("caller_hangup")
        except wsproto.Closed as e:
            self.end("agent_disconnected")
            log("agent socket closed:", e.code, redact(e.reason, self.secret))
        except (OSError, ConnectionError) as e:
            self.end("agent_disconnected")
            log("agent socket failed:", redact(e, self.secret))
        if self.reason == "caller_hangup":
            self.link.stop(self.reason)
        try:
            self.link.conn.close()
        except OSError:
            pass
        if not self.phone.ended:
            # Let the queued agent audio play before the line goes down.
            wait = self.link.play_end - time.monotonic()
            if wait > 0:
                time.sleep(min(wait, 30))
            self.phone.hangup(self.reason or "agent_hangup")
        log("call ended:", self.reason)
        return 0


def main(env=None):
    env = dict(os.environ) if env is None else env
    secret = env.get("BENCH_VOICE_PLATFORM_KEY", "")
    try:
        link = open_link(env)
    except PlatformError as e:
        log("the agent's platform:", redact(e, secret))
        return EXIT_PLATFORM
    try:
        phone = voiceclient.VoiceCall(timeout=60)
    except ConnectionError as e:
        log("the caller:", e)
        link.stop("no_caller")
        link.conn.close()
        return 1
    try:
        return Bridge(link, phone, secret).run()
    except PlatformError as e:
        log("the agent's platform:", redact(e, secret))
        phone.hangup("platform_error")
        return EXIT_PLATFORM


if __name__ == "__main__":
    sys.exit(main())
