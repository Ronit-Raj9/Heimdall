"""Tests of the real-time voice harness: caller personas, rtcbridge.py and rtcgateway.py's checks.

Run from this folder: python3 -B -m unittest -v rtc_test (the Go test
TestRTCPython runs it). Everything is local: the caller runs as a subprocess
on 127.0.0.1 (voice_test.Caller), the gateway side is a fake heimdall-rtc/1
server speaking tone code, and rtcgateway.py is exercised without the
LiveKit SDK (its checks and refusals). The LiveKit path itself was run
against a real LiveKit server, recorded in testdata/call-livekit-webrtc.jsonl
(docs/voice-realtime.md).
"""

import array
import asyncio
import json
import math
import os
import socket
import sys
import threading
import time
import unittest
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import rtcbridge  # noqa: E402
import rtcgateway  # noqa: E402
import tonecode  # noqa: E402
import voicecaller  # noqa: E402
import voiceclient  # noqa: E402
import wsproto  # noqa: E402
from voice_test import LONG, Caller, free_port, kinds  # noqa: E402

SECRET = "0123456789abcdef0123456789abcdef"


def sine(freq, ms, amp=8000):
    n = tonecode.SAMPLE_RATE * ms // 1000
    return tonecode.to_bytes(array.array("h", (int(amp * math.sin(2 * math.pi * freq * i / tonecode.SAMPLE_RATE)) for i in range(n))))


class PersonaTest(unittest.TestCase):
    def test_load(self):
        p = voicecaller.load_persona("indian-english")
        self.assertEqual((p["voice"], p["speaker"], p["accent"]), ("en_GB-vctk-medium", "p248", "Indian"))
        p = voicecaller.load_persona('{"id": "mine", "speed": 1.3, "channel": "phone"}')
        self.assertEqual((p["speed"], p["channel"], p["interruption_rate"]), (1.3, "phone", 0.0))
        self.assertIsNone(voicecaller.load_persona(""))
        for bad in ("pirate", '{"id": "x", "speed": 9}', '{"id": "x", "accent_strength": 1}', '{"id": "x", "channel": "radio"}', "{oops"):
            with self.assertRaises(SystemExit, msg=bad):
                voicecaller.load_persona(bad)

    def test_persona_timing_unless_the_task_sets_it(self):
        s = voicecaller.env_settings({"BENCH_VOICE_PERSONA": "elderly-slow"})
        self.assertEqual((s["reply_delay_ms"], s["max_reprompts"]), (1200, 3))
        s = voicecaller.env_settings({"BENCH_VOICE_PERSONA": "elderly-slow", "BENCH_VOICE_REPLY_DELAY_MS": "50"})
        self.assertEqual(s["reply_delay_ms"], 50)
        with self.assertRaises(SystemExit):
            voicecaller.env_settings({"BENCH_VOICE_VAD": "magic"})

    def test_phone_line_band(self):
        low = voicecaller.phone_line(sine(1000, 400))
        high = voicecaller.phone_line(sine(6500, 400))
        self.assertAlmostEqual(len(low), len(sine(1000, 400)), delta=8)
        mid = slice(len(low) // 4 * 2 // 2 * 2, len(low) // 4 * 3 // 2 * 2)
        self.assertGreater(tonecode.rms(low[mid]), 3500)
        self.assertLess(tonecode.rms(high[mid]), 400)
        # Tone code (800 to 2900 Hz) survives a phone line.
        self.assertEqual(tonecode.decode(voicecaller.phone_line(tonecode.encode("refund please", 40, 60))), "refund please")

    def test_silero_needs_its_model(self):
        old = os.environ.get("SILERO_VAD_MODEL")
        os.environ["SILERO_VAD_MODEL"] = "/nonexistent/silero.onnx"
        try:
            with self.assertRaises(SystemExit):
                voicecaller.make_vad("silero")
        finally:
            if old is None:
                os.environ.pop("SILERO_VAD_MODEL")
            else:
                os.environ["SILERO_VAD_MODEL"] = old
        self.assertEqual(voicecaller.make_vad("energy").name, "energy")


class PersonaCallTest(unittest.TestCase):
    def test_interrupter_talks_over_a_long_answer(self):
        c = Caller({"BENCH_VOICE_PERSONA": "interrupter"})
        try:
            phone = voiceclient.VoiceCall(c.url)
            phone.say_tone("Hello, how can I help?")
            phone.listen(end_silence_ms=700, max_wait_s=10)
            self.assertTrue(phone.say_tone(LONG, realtime=True, barge_in=True, barge_in_ms=150))
            heard = phone.listen(end_silence_ms=700, max_wait_s=15)
            self.assertEqual(tonecode.decode(heard), voicecaller.PERSONA_INTERRUPT_LINE)
            phone.hangup()
            ev = c.wait_done()
            start = ev[0]
            self.assertEqual(start["persona"]["id"], "interrupter")
            self.assertEqual(start["persona"]["voice_applied"], "not_applied")
            self.assertEqual(start["vad"], "energy")
            self.assertEqual(start["settings"]["reply_delay_ms"], 100)  # the task's own setting wins
            b = kinds(ev, "barge_in_start")
            self.assertEqual([x["by"] for x in b], ["persona"])
            self.assertEqual(c.transcript()["voice"]["persona"], "interrupter")
        finally:
            c.close()

    def test_phone_line_noise_and_hesitation(self):
        c = Caller({"BENCH_VOICE_PERSONA": '{"id": "um-phone", "channel": "phone", "noise_snr_db": 25, "filler_rate": 1}'})
        try:
            phone = voiceclient.VoiceCall(c.url)
            phone.say_tone("Hello, how can I help?")
            heard = phone.listen(end_silence_ms=700, max_wait_s=10)
            self.assertTrue((tonecode.decode(heard) or "").startswith("Um, hi"))
            phone.hangup()
            ev = c.wait_done()
            first = kinds(ev, "caller_speech_start")[0]
            self.assertTrue(first["text"].startswith("Um, "))
            self.assertEqual(first["probe"], {"persona_noise_snr_db": 25.0, "channel": "phone"})
        finally:
            c.close()

    def test_unknown_persona_stops_the_caller(self):
        c = Caller({"BENCH_VOICE_PERSONA": "pirate"})
        try:
            c.proc.wait(10)
            self.assertNotEqual(c.proc.returncode, 0)
        finally:
            c.close()


# ---- a fake gateway: heimdall-rtc/1 with a tone-code agent behind it ----

class FakeGateway:
    def __init__(self, refuse=None):
        self.port = free_port()
        self.refuse = refuse
        self.dial = None
        self.headers = None
        self.heard = []
        self.hangup = None
        self.loop = asyncio.new_event_loop()
        self.ready = threading.Event()
        threading.Thread(target=self._run, daemon=True).start()
        self.ready.wait(5)

    def _run(self):
        asyncio.set_event_loop(self.loop)

        async def main():
            srv = await asyncio.start_server(self.handle, "127.0.0.1", self.port)
            self.ready.set()
            async with srv:
                await srv.serve_forever()
        self.loop.run_until_complete(main())

    async def say(self, conn, text):
        for f in tonecode.frames(tonecode.encode(text, 40, 60)):
            await conn.send_binary(f)
            await asyncio.sleep(tonecode.FRAME_MS / 1000.0)

    async def handle(self, reader, writer):
        _, _, headers = await wsproto.read_http_head(reader)
        self.headers = headers
        await wsproto.server_handshake(reader, writer, headers)
        conn = wsproto.AsyncConn(reader, writer)
        _, data = await conn.recv()
        self.dial = json.loads(data)
        if self.refuse:
            await conn.send_text(json.dumps({"type": "error", "code": self.refuse, "message": "no such agent"}))
            await conn.close()
            return
        await conn.send_text(json.dumps({"type": "ready", "room": "heimdall-run1-abc", "agent": "agent-1", "setup_ms": 420}))
        await self.say(conn, "Hello, Acme Brew Club, how can I help?")
        buf, last_voiced, speaking = bytearray(), 0.0, False
        while True:
            try:
                kind, data = await asyncio.wait_for(conn.recv(), 0.1)
            except asyncio.TimeoutError:
                kind, data = None, None
            except (wsproto.Closed, asyncio.IncompleteReadError, ConnectionError):
                return
            now = time.monotonic()
            if kind == "text":
                m = json.loads(data)
                if m.get("type") == "hangup":
                    self.hangup = m
                    await conn.close()
                    return
                continue
            if kind == "binary":
                if tonecode.rms(data) > 250:
                    speaking, last_voiced = True, now
                if speaking:
                    buf += data
            if speaking and now - last_voiced > 0.6:
                speaking = False
                text = tonecode.decode(bytes(buf)) or ""
                buf = bytearray()
                self.heard.append(text)
                await conn.send_text(json.dumps({"type": "transcript", "role": "user", "text": text, "final": True}))
                if "refund" in text:
                    await self.say(conn, "I have refunded the full 45.00.")


def bridge_env(gw, caller_url, **extra):
    env = {"BENCH_RTC_GATEWAY_URL": f"ws://127.0.0.1:{gw.port}/dial", "BENCH_RTC_GATEWAY_TOKEN": "hrtc1.run1.9999999999.x",
           "BENCH_RTC_DIAL": json.dumps({"mode": "livekit", "run": "run1", "session": "s1", "livekit": {"url": "wss://acme.livekit.cloud", "dispatch": "agent", "agent_name": "support"}}),
           "BENCH_RTC_LK_API_KEY": "APIkey123", "BENCH_RTC_LK_API_SECRET": "secretvalue456", "BENCH_VOICE_URL": caller_url, "BENCH_VOICE_SETUP_S": "10"}
    env.update(extra)
    return env


class BridgeTest(unittest.TestCase):
    def test_call_through_the_gateway(self):
        gw = FakeGateway()
        c = Caller()
        try:
            code = rtcbridge.main(bridge_env(gw, c.url))
            self.assertEqual(code, 0)
            self.assertEqual(gw.headers.get("authorization"), "Bearer hrtc1.run1.9999999999.x")
            self.assertEqual(gw.headers.get("sec-websocket-protocol"), rtcbridge.PROTOCOL)
            self.assertEqual((gw.dial["type"], gw.dial["protocol"]), ("dial", "heimdall-rtc/1"))
            self.assertEqual(gw.dial["livekit"]["api_key"], "APIkey123")
            self.assertEqual(gw.dial["livekit"]["api_secret"], "secretvalue456")
            self.assertIn("refund", gw.heard[0])
            self.assertEqual(gw.hangup["type"], "hangup")
            ev = c.wait_done()
            self.assertEqual(ev[-1]["ended_by"], "caller_done")
            ready = [e for e in kinds(ev, "agent_reported") if e.get("name") == "rtc_ready"]
            self.assertEqual(len(ready), 1)
            self.assertIn("setup 420 ms", ready[0]["text"])
            said = [e["text"] for e in kinds(ev, "agent_transcript")]
            self.assertEqual(said, ["Hello, Acme Brew Club, how can I help?", "I have refunded the full 45.00."])
            reported = [e for e in kinds(ev, "agent_reported") if e.get("kind") == "transcript"]
            self.assertTrue(reported and reported[0]["role"] == "user" and "refund" in reported[0]["text"])
        finally:
            c.close()

    def test_refused_call_is_a_platform_failure(self):
        gw = FakeGateway(refuse="agent_never_joined")
        c = Caller()
        try:
            self.assertEqual(rtcbridge.main(bridge_env(gw, c.url)), rtcbridge.EXIT_PLATFORM)
        finally:
            c.close()

    def test_unreachable_gateway(self):
        env = bridge_env(type("G", (), {"port": free_port()})(), "ws://127.0.0.1:1/call")
        self.assertEqual(rtcbridge.main(env), rtcbridge.EXIT_PLATFORM)
        env["BENCH_RTC_GATEWAY_URL"] = ""
        self.assertEqual(rtcbridge.main(env), rtcbridge.EXIT_PLATFORM)

    def test_redaction(self):
        dial = {"livekit": {"api_key": "APIkey123", "api_secret": "secretvalue456"}, "sip": {"trunk_password": "pw-9999"}}
        self.assertEqual(rtcbridge.redact("bad key APIkey123 / pw-9999", rtcbridge.dial_secrets(dial)), "bad key [redacted] / [redacted]")


class GatewayChecksTest(unittest.TestCase):
    def test_token_matches_go(self):
        # bench/voicertc TestRunToken pins the same vector.
        tok = "hrtc1.run1.1800000000.f10876dd23f72811f4343e84b23109f912a37120838caa6503b86f01155a690d"
        self.assertEqual(rtcgateway.verify_token(SECRET, tok, now=1799999999), "run1")
        for bad, now in ((tok, 1800000001), (tok.replace("run1", "run2"), 1), ("x.y", 1)):
            with self.assertRaises(rtcgateway.DialError):
                rtcgateway.verify_token(SECRET, bad, now=now)

    def dial(self, **kw):
        d = {"type": "dial", "protocol": "heimdall-rtc/1", "run": "run1", "mode": "livekit", "livekit": {"dispatch": "agent", "agent_name": "support"}}
        d.update(kw)
        return d

    def code(self, msg, env):
        with self.assertRaises(rtcgateway.DialError) as cm:
            rtcgateway.check_dial(msg, env, "run1")
        return cm.exception.code

    def test_check_dial(self):
        own = {"LIVEKIT_URL": "ws://livekit:7880", "LIVEKIT_API_KEY": "devkey", "LIVEKIT_API_SECRET": "devsecret-devsecret"}
        d = rtcgateway.check_dial(self.dial(), own, "run1")
        self.assertTrue(d["livekit"]["own"])
        self.assertTrue(d["room"].startswith("heimdall-run1-"))
        self.assertEqual(d["dispatch"], {"kind": "agent", "agent_name": "support"})
        self.assertEqual(self.code(self.dial(), {}), "not_configured")
        self.assertEqual(self.code(self.dial(run="run2"), own), "unauthorized")
        self.assertEqual(self.code(self.dial(livekit={"url": "ws://x.example", "api_key": "k", "api_secret": "s", "agent_name": "a"}), own), "bad_dial")
        self.assertEqual(self.code(self.dial(livekit={"url": "wss://x.example", "agent_name": "a"}), own), "missing_credentials")
        ext = rtcgateway.check_dial(self.dial(livekit={"url": "ws://x.example", "api_key": "k", "api_secret": "s", "dispatch": "webhook", "start_url": "http://bot:8080/start"}),
                                    {"HEIMDALL_VOICE_RTC_ALLOW_INSECURE": "1"}, "run1")
        self.assertFalse(ext["livekit"]["own"])
        self.assertEqual(ext["dispatch"]["kind"], "webhook")
        self.assertEqual(self.code(self.dial(livekit={"dispatch": "webhook", "start_url": "http://bot/start"}), own), "bad_dial")
        self.assertEqual(self.code(self.dial(protocol="heimdall-rtc/9"), own), "bad_dial")

    def test_check_dial_phone(self):
        own = {"LIVEKIT_URL": "ws://livekit:7880", "LIVEKIT_API_KEY": "devkey", "LIVEKIT_API_SECRET": "devsecret-devsecret"}
        sip = {"number": "+14155550100", "trunk_address": "acme.pstn.twilio.com", "from_number": "+14155550199", "trunk_username": "u", "trunk_password": "p"}
        msg = self.dial(mode="sip", livekit={}, sip=sip)
        self.assertEqual(self.code(msg, own), "number_not_allowed")
        d = rtcgateway.check_dial(msg, dict(own, HEIMDALL_VOICE_SIP_ALLOW="+1415"), "run1")
        self.assertEqual((d["dispatch"]["kind"], d["dispatch"]["transport"], d["dispatch"]["trunk_address"]), ("sip", "udp", "acme.pstn.twilio.com"))
        self.assertEqual(self.code(self.dial(mode="sip", livekit={}, sip=dict(sip, from_number="")), dict(own, HEIMDALL_VOICE_SIP_ALLOW="+1")), "bad_dial")
        self.assertFalse(rtcgateway.sip_allowed("+14155550100", ""))
        self.assertTrue(rtcgateway.sip_allowed("+14155550100", "+44, +1415"))

    def test_server_refusals_without_livekit(self):
        port = free_port()
        env = {"HEIMDALL_VOICE_RTC_SECRET": SECRET}
        gw = rtcgateway.Gateway(env)
        loop = asyncio.new_event_loop()
        threading.Thread(target=lambda: loop.run_until_complete(gw.main(port)), daemon=True).start()
        for _ in range(100):
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=1) as r:
                    health = json.load(r)
                break
            except OSError:
                time.sleep(0.05)
        self.assertEqual((health["ok"], health["own_livekit"], health["phone"]), (True, False, False))
        with self.assertRaises(urllib.error.HTTPError) as cm:
            urllib.request.urlopen(urllib.request.Request(f"http://127.0.0.1:{port}/dial", headers={"Authorization": "Bearer nope"}), timeout=2)
        self.assertEqual(cm.exception.code, 401)
        exp = int(time.time()) + 60
        body = f"hrtc1.run1.{exp}"
        token = body + "." + rtcgateway.sign(SECRET, body)
        conn = rtcbridge.ws_connect(f"ws://127.0.0.1:{port}/dial", {"Authorization": "Bearer " + token}, 5)
        conn.send_text(json.dumps(self.dial()))
        kind, data = conn.recv(timeout=5)
        m = json.loads(data)
        self.assertEqual((m["type"], m["code"]), ("error", "not_configured"))
        with self.assertRaises(SystemExit):
            rtcgateway.Gateway({"HEIMDALL_VOICE_RTC_SECRET": "short"})


if __name__ == "__main__":
    unittest.main()
