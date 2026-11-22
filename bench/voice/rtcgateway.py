#!/usr/bin/env python3
"""Heimdall's real-time voice gateway: the simulated caller's seat in a LiveKit room.

Part of Heimdall (bench/voice), docs/voice-realtime.md. It runs as the compose
service voice-rtc (profile voice) next to a LiveKit server and LiveKit SIP,
outside every evaluation network. A run's bridge (rtcbridge.py) reaches it
through the run's one egress forwarder and speaks heimdall-rtc/1 (see
rtcbridge.py). For each call the gateway:

  - checks the per-run token (HMAC of the run id and an expiry with
    HEIMDALL_VOICE_RTC_SECRET, the same rule as bench/voicertc.VerifyToken)
    and takes each run id once at a time;
  - creates a fresh LiveKit room on Heimdall's LiveKit server (LIVEKIT_URL,
    LIVEKIT_API_KEY, LIVEKIT_API_SECRET) or on the team's own server (the
    dial message's livekit.url and keys from the team's secrets);
  - joins it as the caller (identity heimdall-caller), publishes the
    caller's audio as a microphone track and subscribes to the agent's;
  - brings the agent in: LiveKit explicit agent dispatch (agent_name), a
    POST of the room and a join token to the agent's start URL (Pipecat and
    other bots), a worker that joins every room by itself, or a phone call
    through LiveKit SIP to a number on HEIMDALL_VOICE_SIP_ALLOW;
  - carries audio both ways as 16 kHz mono pcm_s16le. The caller's audio is
    paced at 20 ms with silence between lines, as a phone line carries it,
    so the agent's own voice activity detection sees the caller stop;
  - forwards the agent's lk.transcription streams (its own speech and what
    it heard) as agent-reported transcript lines;
  - deletes the room when the call ends (bridge hangup, the agent leaving,
    max_call_s).

  python3 rtcgateway.py serve [--port 8790]

Environment: HEIMDALL_VOICE_RTC_SECRET (required), LIVEKIT_URL,
LIVEKIT_API_KEY, LIVEKIT_API_SECRET (Heimdall's own server), LIVEKIT_PUBLIC_URL
(the URL a webhook-started bot is told, default LIVEKIT_URL),
HEIMDALL_VOICE_SIP_ALLOW (E.164 prefixes, comma separated; empty: no phone
calls), HEIMDALL_VOICE_MAX_CALLS (default 20), HEIMDALL_VOICE_RTC_ALLOW_INSECURE=1
(allow ws:// and http:// team URLs, for tests on a private network).

Only this file's serve path imports the LiveKit SDK (livekit, livekit-api);
the checks are standard library, so they are tested without it.
"""

import argparse
import asyncio
import datetime
import hashlib
import hmac
import json
import os
import re
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import wsproto  # noqa: E402

PROTOCOL = "heimdall-rtc/1"
TOKEN_PREFIX = "hrtc1"
RATE = 16000
FRAME_MS = 20
FRAME_BYTES = RATE * 2 * FRAME_MS // 1000
MAX_UPLINK_BUFFER = RATE * 2  # one second of caller audio waiting to be sent
CALLER_IDENTITY = "heimdall-caller"
E164 = re.compile(r"^\+[1-9][0-9]{6,14}$")
AGENT_NAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$")
ROOM_PREFIX = re.compile(r"^[a-z0-9][a-z0-9-]{0,31}$")
HOST = re.compile(r"^[A-Za-z0-9.-]{1,253}(:[0-9]{1,5})?$")
TRUNK_ID = re.compile(r"^[A-Za-z0-9_-]{1,64}$")


class DialError(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code
        self.message = message


def log(**kw):
    kw["t"] = round(time.time(), 3)
    sys.stderr.write(json.dumps(kw, sort_keys=True) + "\n")
    sys.stderr.flush()


def redact(text, values):
    text = str(text)
    for v in values:
        if v and len(v) >= 4:
            text = text.replace(v, "[redacted]")
    return text


# ---- checks (standard library) ----

def sign(secret, body):
    return hmac.new(secret.encode(), body.encode(), hashlib.sha256).hexdigest()


def verify_token(secret, token, now=None):
    """The run id of a valid run token (bench/voicertc.VerifyToken), else DialError."""
    now = time.time() if now is None else now
    parts = (token or "").split(".")
    if len(parts) != 4 or parts[0] != TOKEN_PREFIX or not parts[1]:
        raise DialError("unauthorized", "not a run token")
    body = ".".join(parts[:3])
    if not hmac.compare_digest(sign(secret, body), parts[3]):
        raise DialError("unauthorized", "bad run token")
    try:
        exp = int(parts[2])
    except ValueError:
        raise DialError("unauthorized", "bad run token") from None
    if now > exp:
        raise DialError("unauthorized", "the run token has expired")
    return parts[1]


def sip_allowed(number, allow):
    """number is E.164 and starts with one of allow's comma separated + prefixes (empty allows nothing)."""
    if not E164.match(number or ""):
        return False
    for p in (allow or "").split(","):
        p = p.strip()
        if p.startswith("+") and number.startswith(p):
            return True
    return False


def http_url(ws_url):
    u = urllib.parse.urlsplit(ws_url)
    scheme = {"wss": "https", "ws": "http"}.get(u.scheme, u.scheme)
    return urllib.parse.urlunsplit((scheme, u.netloc, u.path, "", ""))


def check_dial(msg, env, run_id):
    """The dial message made safe to act on, or DialError with the reason."""
    if not isinstance(msg, dict) or msg.get("type") != "dial":
        raise DialError("bad_dial", "the first message must be a dial message")
    if msg.get("protocol") != PROTOCOL:
        raise DialError("bad_dial", f"protocol {msg.get('protocol')!r}: want {PROTOCOL}")
    if msg.get("run") != run_id:
        raise DialError("unauthorized", "the dial message is for another run than its token")
    insecure = env.get("HEIMDALL_VOICE_RTC_ALLOW_INSECURE") == "1"
    mode = msg.get("mode")
    if mode not in ("livekit", "sip"):
        raise DialError("bad_dial", f"mode {mode!r}: want livekit or sip")
    lk = msg.get("livekit") or {}
    out = {"mode": mode, "run": run_id, "session": str(msg.get("session") or run_id)[:128], "task": str(msg.get("task") or "")[:128]}
    out["setup_s"] = max(5, min(300, int(msg.get("setup_s") or 30)))
    out["max_call_s"] = max(10, min(3600, int(msg.get("max_call_s") or 600)))
    prefix = lk.get("room_prefix") or "heimdall"
    if not ROOM_PREFIX.match(prefix):
        raise DialError("bad_dial", f"room_prefix {prefix!r}")
    safe_run = re.sub(r"[^a-z0-9-]", "-", run_id.lower())[:48]
    out["room"] = f"{prefix}-{safe_run}-{secrets.token_hex(3)}"
    url = lk.get("url") or ""
    if url:
        u = urllib.parse.urlsplit(url)
        if u.scheme != "wss" and not (insecure and u.scheme == "ws"):
            raise DialError("bad_dial", f"livekit.url {url!r}: want wss://")
        if not lk.get("api_key") or not lk.get("api_secret"):
            raise DialError("missing_credentials", "your LiveKit server needs its API key and secret (check the secrets named in the agent's voice_rtc block)")
        out["livekit"] = {"url": url, "public_url": url, "api_key": lk["api_key"], "api_secret": lk["api_secret"], "own": False}
    else:
        if not (env.get("LIVEKIT_URL") and env.get("LIVEKIT_API_KEY") and env.get("LIVEKIT_API_SECRET")):
            raise DialError("not_configured", "this Heimdall has no LiveKit server of its own (LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET on the voice-rtc service)")
        out["livekit"] = {"url": env["LIVEKIT_URL"], "public_url": env.get("LIVEKIT_PUBLIC_URL") or env["LIVEKIT_URL"],
                          "api_key": env["LIVEKIT_API_KEY"], "api_secret": env["LIVEKIT_API_SECRET"], "own": True}
    if mode == "livekit":
        dispatch = lk.get("dispatch") or "agent"
        if dispatch == "agent":
            if not AGENT_NAME.match(lk.get("agent_name") or ""):
                raise DialError("bad_dial", "dispatch agent needs agent_name")
            out["dispatch"] = {"kind": "agent", "agent_name": lk["agent_name"]}
        elif dispatch == "webhook":
            su = urllib.parse.urlsplit(lk.get("start_url") or "")
            if su.scheme != "https" and not (insecure and su.scheme == "http"):
                raise DialError("bad_dial", "dispatch webhook needs an https start_url")
            out["dispatch"] = {"kind": "webhook", "start_url": lk["start_url"], "start_token": lk.get("start_token") or ""}
        elif dispatch == "auto":
            out["dispatch"] = {"kind": "auto"}
        else:
            raise DialError("bad_dial", f"dispatch {dispatch!r}: want agent, auto or webhook")
        return out
    sip = msg.get("sip") or {}
    number = sip.get("number") or ""
    if not E164.match(number):
        raise DialError("bad_dial", f"sip.number {number!r}: want E.164")
    if not sip_allowed(number, env.get("HEIMDALL_VOICE_SIP_ALLOW", "")):
        raise DialError("number_not_allowed", f"{number} is not on this Heimdall's phone allowlist (HEIMDALL_VOICE_SIP_ALLOW)")
    transport = sip.get("transport") or "udp"
    if transport not in ("udp", "tcp", "tls"):
        raise DialError("bad_dial", f"sip.transport {transport!r}")
    d = {"kind": "sip", "number": number, "transport": transport, "from_number": sip.get("from_number") or ""}
    if sip.get("trunk_id"):
        if not TRUNK_ID.match(sip["trunk_id"]):
            raise DialError("bad_dial", "sip.trunk_id")
        d["trunk_id"] = sip["trunk_id"]
    else:
        if not HOST.match(sip.get("trunk_address") or ""):
            raise DialError("bad_dial", "sip.trunk_address: want your SIP provider's host")
        if not E164.match(d["from_number"]):
            raise DialError("bad_dial", "sip.from_number: want the E.164 caller id of your trunk")
        d["trunk_address"] = sip["trunk_address"]
        d["trunk_username"] = sip.get("trunk_username") or ""
        d["trunk_password"] = sip.get("trunk_password") or ""
    out["dispatch"] = d
    return out


def call_secrets(dial):
    lk = dial.get("livekit") or {}
    d = dial.get("dispatch") or {}
    return [lk.get("api_key", ""), lk.get("api_secret", ""), d.get("start_token", ""), d.get("trunk_username", ""), d.get("trunk_password", "")]


def post_start(url, token, body, timeout):
    """POST the room and join token to a webhook-started agent (blocking; run in an executor)."""
    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method="POST", headers={"Content-Type": "application/json"})
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status
    except urllib.error.HTTPError as e:
        raise DialError("agent_refused", f"the agent's start URL answered HTTP {e.code}") from None
    except (OSError, ValueError) as e:
        raise DialError("agent_unreachable", f"the agent's start URL: {e}") from None


# ---- one call (LiveKit SDK) ----

class Call:
    def __init__(self, conn, dial):
        self.conn = conn
        self.dial = dial
        self.ended = asyncio.Event()
        self.reason = None
        self.uplink = bytearray()
        self.dropped_ms = 0
        self.agent_identity = None
        self.caller_sid = None
        self.tasks = []
        self.secrets = call_secrets(dial)

    def end(self, reason):
        if self.reason is None:
            self.reason = reason
        self.ended.set()

    async def send(self, obj):
        try:
            await self.conn.send_text(json.dumps(obj))
        except (wsproto.Closed, OSError, ConnectionError):
            self.end("bridge_gone")

    async def run(self):
        from livekit import api, rtc  # noqa: PLC0415 - only the serve path needs the SDK

        d, lkcfg = self.dial, self.dial["livekit"]
        t0 = time.monotonic()
        lk = api.LiveKitAPI(http_url(lkcfg["url"]), lkcfg["api_key"], lkcfg["api_secret"])
        room_name = d["room"]
        room = rtc.Room()
        created = False
        try:
            meta = json.dumps({"heimdall_run": d["run"], "session": d["session"], "task": d["task"]})
            await lk.room.create_room(api.CreateRoomRequest(name=room_name, empty_timeout=30, departure_timeout=10,
                                                            max_participants=6, metadata=meta))
            created = True
            ttl = datetime.timedelta(seconds=d["max_call_s"] + d["setup_s"] + 120)
            token = (api.AccessToken(lkcfg["api_key"], lkcfg["api_secret"]).with_identity(CALLER_IDENTITY)
                     .with_name("Heimdall caller").with_ttl(ttl)
                     .with_grants(api.VideoGrants(room_join=True, room=room_name, can_publish=True, can_subscribe=True,
                                                  can_publish_data=True)).to_jwt())
            tracks = asyncio.Queue()

            @room.on("track_subscribed")
            def _on_track(track, _pub, participant):
                if track.kind == rtc.TrackKind.KIND_AUDIO and participant.identity != CALLER_IDENTITY:
                    tracks.put_nowait((track, participant.identity))

            @room.on("participant_disconnected")
            def _on_left(participant):
                if participant.identity == self.agent_identity:
                    self.end("agent_left")

            @room.on("disconnected")
            def _on_disconnected(*_a):
                self.end("room_disconnected")

            def _on_transcript(reader, identity):
                self.tasks.append(asyncio.ensure_future(self.transcript(reader, identity)))

            room.register_text_stream_handler("lk.transcription", _on_transcript)
            await asyncio.wait_for(room.connect(lkcfg["url"], token, rtc.RoomOptions(auto_subscribe=True)), d["setup_s"])
            source = rtc.AudioSource(RATE, 1, queue_size_ms=200)
            mic = rtc.LocalAudioTrack.create_audio_track("caller", source)
            pub = await room.local_participant.publish_track(mic, rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_MICROPHONE))
            self.caller_sid = pub.sid
            await self.bring_agent(api, lk, lkcfg, room_name, d, ttl, t0)
            left = d["setup_s"] - (time.monotonic() - t0)
            try:
                track, identity = await asyncio.wait_for(tracks.get(), max(left, 1))
            except asyncio.TimeoutError:
                raise DialError("agent_never_joined", f"the agent published no audio within {d['setup_s']} s of the call starting") from None
            self.agent_identity = identity
            setup_ms = round((time.monotonic() - t0) * 1000)
            await self.send({"type": "ready", "room": room_name, "agent": identity, "setup_ms": setup_ms})
            log(ev="call_ready", run=d["run"], room=room_name, mode=d["mode"], dispatch=d["dispatch"]["kind"], agent=identity, setup_ms=setup_ms)
            self.tasks += [asyncio.ensure_future(self.downlink(rtc.AudioStream(track, sample_rate=RATE, num_channels=1))),
                           asyncio.ensure_future(self.read_bridge()),
                           asyncio.ensure_future(self.pace(source, rtc))]
            try:
                await asyncio.wait_for(self.ended.wait(), d["max_call_s"])
            except asyncio.TimeoutError:
                self.end("max_call")
            if self.reason not in ("bridge_hangup", "bridge_gone"):
                await self.send({"type": "hangup", "reason": self.reason})
        finally:
            for t in self.tasks:
                t.cancel()
            try:
                await room.disconnect()
            except Exception:  # noqa: BLE001 - cleanup is best effort
                pass
            if created:
                try:
                    await lk.room.delete_room(api.DeleteRoomRequest(room=room_name))
                except Exception as e:  # noqa: BLE001
                    log(ev="room_delete_failed", room=room_name, error=redact(e, self.secrets)[:300])
            await lk.aclose()
            log(ev="call_end", run=d["run"], room=room_name, reason=self.reason, dropped_ms=self.dropped_ms,
                call_ms=round((time.monotonic() - t0) * 1000))

    async def bring_agent(self, api, lk, lkcfg, room_name, d, ttl, t0):
        disp = d["dispatch"]
        if disp["kind"] == "agent":
            await lk.agent_dispatch.create_dispatch(api.CreateAgentDispatchRequest(
                agent_name=disp["agent_name"], room=room_name, metadata=json.dumps({"heimdall_run": d["run"], "session": d["session"]})))
        elif disp["kind"] == "webhook":
            agent_token = (api.AccessToken(lkcfg["api_key"], lkcfg["api_secret"]).with_identity("agent-" + secrets.token_hex(3))
                           .with_name("Agent").with_ttl(ttl)
                           .with_grants(api.VideoGrants(room_join=True, room=room_name, can_publish=True, can_subscribe=True,
                                                        can_publish_data=True)).to_jwt())
            body = {"livekit_url": lkcfg["public_url"], "room": room_name, "token": agent_token, "session": d["session"]}
            left = max(1.0, d["setup_s"] - (time.monotonic() - t0))
            await asyncio.get_running_loop().run_in_executor(None, post_start, disp["start_url"], disp["start_token"], body, left)
        elif disp["kind"] == "sip":
            req = api.CreateSIPParticipantRequest(sip_call_to=disp["number"], room_name=room_name, participant_identity="phone-agent",
                                                  participant_name="Agent phone", wait_until_answered=True, play_dialtone=False)
            kw = {"timeout": max(5.0, d["setup_s"] - (time.monotonic() - t0))}
            if disp.get("trunk_id"):
                kw["trunk_id"] = disp["trunk_id"]
            else:
                req.sip_number = disp["from_number"]
                transport = {"udp": api.SIPTransport.SIP_TRANSPORT_UDP, "tcp": api.SIPTransport.SIP_TRANSPORT_TCP,
                             "tls": api.SIPTransport.SIP_TRANSPORT_TLS}[disp["transport"]]
                kw["outbound_trunk_config"] = api.SIPOutboundConfig(hostname=disp["trunk_address"], transport=transport,
                                                                    auth_username=disp["trunk_username"], auth_password=disp["trunk_password"])
            try:
                await lk.sip.create_sip_participant(req, **kw)
            except Exception as e:  # noqa: BLE001 - the SDK's SIPError carries the SIP status
                raise DialError("phone_call_failed", f"the call to {disp['number']} did not connect: {redact(e, self.secrets)[:300]}") from None

    async def downlink(self, stream):
        """The agent's audio to the bridge, as it arrives."""
        try:
            async for ev in stream:
                data = bytes(ev.frame.data)
                if data:
                    await self.conn.send_binary(data)
        except (wsproto.Closed, OSError, ConnectionError):
            self.end("bridge_gone")
        finally:
            await stream.aclose()

    async def read_bridge(self):
        try:
            while not self.ended.is_set():
                kind, data = await self.conn.recv()
                if kind == "binary":
                    self.uplink += data[: len(data) - len(data) % 2]
                    over = len(self.uplink) - MAX_UPLINK_BUFFER
                    if over > 0:
                        del self.uplink[:over]
                        self.dropped_ms += over * 1000 // (RATE * 2)
                    continue
                try:
                    m = json.loads(data)
                except ValueError:
                    continue
                if m.get("type") == "hangup":
                    self.end("bridge_hangup")
        except (wsproto.Closed, asyncio.IncompleteReadError, OSError, ConnectionError):
            self.end("bridge_gone")

    async def pace(self, source, rtc):
        """The caller's line: 20 ms frames on a real-time clock, silence when the caller is quiet."""
        silence = bytes(FRAME_BYTES)
        start = time.monotonic()
        n = 0
        while not self.ended.is_set():
            if len(self.uplink) >= FRAME_BYTES:
                chunk = bytes(self.uplink[:FRAME_BYTES])
                del self.uplink[:FRAME_BYTES]
            else:
                chunk = silence
            await source.capture_frame(rtc.AudioFrame(chunk, RATE, 1, FRAME_BYTES // 2))
            n += 1
            delay = start + n * FRAME_MS / 1000.0 - time.monotonic()
            if delay > 0:
                await asyncio.sleep(delay)

    async def transcript(self, reader, identity):
        try:
            text = await reader.read_all()
        except Exception:  # noqa: BLE001 - a broken stream is skipped
            return
        attrs = getattr(reader.info, "attributes", None) or {}
        track = attrs.get("lk.transcribed_track_id", "")
        role = "user" if (identity == CALLER_IDENTITY or (track and track == self.caller_sid)) else "agent"
        final = attrs.get("lk.transcription_final", "true") != "false"
        if text and final:
            await self.send({"type": "transcript", "role": role, "text": text[:2000], "final": True})


# ---- server ----

class Gateway:
    def __init__(self, env):
        self.env = env
        self.secret = env.get("HEIMDALL_VOICE_RTC_SECRET", "")
        if len(self.secret) < 16:
            raise SystemExit("rtcgateway: HEIMDALL_VOICE_RTC_SECRET must be set (16 characters or more), the same value as the server's")
        self.max_calls = int(env.get("HEIMDALL_VOICE_MAX_CALLS") or 20)
        self.active = set()

    async def respond(self, writer, status, body):
        raw = json.dumps(body).encode()
        writer.write(b"HTTP/1.1 " + status.encode() + b"\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n" % len(raw) + raw)
        await writer.drain()
        writer.close()

    async def handle(self, reader, writer):
        try:
            _method, path, headers = await wsproto.read_http_head(reader)
        except (asyncio.IncompleteReadError, ValueError, asyncio.LimitOverrunError):
            writer.close()
            return
        route = path.split("?", 1)[0]
        if route == "/healthz":
            own = bool(self.env.get("LIVEKIT_URL") and self.env.get("LIVEKIT_API_KEY") and self.env.get("LIVEKIT_API_SECRET"))
            await self.respond(writer, "200 OK", {"ok": True, "server": "heimdall-voice-rtc", "protocol": PROTOCOL,
                                                  "own_livekit": own, "phone": bool(self.env.get("HEIMDALL_VOICE_SIP_ALLOW")),
                                                  "active_calls": len(self.active), "max_calls": self.max_calls})
            return
        if route != "/dial":
            await self.respond(writer, "404 Not Found", {"error": "not found"})
            return
        auth = headers.get("authorization", "")
        try:
            run_id = verify_token(self.secret, auth[7:] if auth.lower().startswith("bearer ") else "")
        except DialError as e:
            await self.respond(writer, "401 Unauthorized", {"error": e.message})
            return
        if run_id in self.active:
            await self.respond(writer, "409 Conflict", {"error": "this run already has a call"})
            return
        if len(self.active) >= self.max_calls:
            await self.respond(writer, "503 Service Unavailable", {"error": "the voice gateway is at its call limit"})
            return
        if not await wsproto.server_handshake(reader, writer, headers):
            writer.close()
            return
        conn = wsproto.AsyncConn(reader, writer)
        self.active.add(run_id)
        dial = None
        try:
            kind, data = await asyncio.wait_for(conn.recv(), 15)
            try:
                msg = json.loads(data) if kind == "text" else None
            except ValueError:
                msg = None
            dial = check_dial(msg, self.env, run_id)
            await Call(conn, dial).run()
        except DialError as e:
            log(ev="call_refused", run=run_id, code=e.code, message=redact(e.message, call_secrets(dial or {}))[:300])
            try:
                await conn.send_text(json.dumps({"type": "error", "code": e.code, "message": e.message}))
            except (wsproto.Closed, OSError, ConnectionError):
                pass
        except asyncio.TimeoutError:
            try:
                await conn.send_text(json.dumps({"type": "error", "code": "setup_timeout", "message": "the call did not start in time"}))
            except (wsproto.Closed, OSError, ConnectionError):
                pass
        except (wsproto.Closed, asyncio.IncompleteReadError, OSError, ConnectionError):
            pass
        except Exception as e:  # noqa: BLE001 - LiveKit API errors: reported to the run, never a crash of the gateway
            msg = redact(e, call_secrets(dial or {}))[:300]
            log(ev="call_failed", run=run_id, error=msg)
            try:
                await conn.send_text(json.dumps({"type": "error", "code": "livekit_error", "message": msg}))
            except (wsproto.Closed, OSError, ConnectionError):
                pass
        finally:
            self.active.discard(run_id)
            await conn.close()

    async def main(self, port):
        srv = await asyncio.start_server(self.handle, "0.0.0.0", port)
        log(ev="listening", port=port, protocol=PROTOCOL)
        async with srv:
            await srv.serve_forever()


def main(argv=None):
    p = argparse.ArgumentParser(prog="rtcgateway")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--port", type=int, default=int(os.environ.get("PORT", "8790")))
    a = p.parse_args(argv)
    gw = Gateway(dict(os.environ))
    try:
        asyncio.run(gw.main(a.port))
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
