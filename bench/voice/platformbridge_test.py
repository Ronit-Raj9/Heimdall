"""Tests of platformbridge.py against fake voice platforms, all local.

Run from this folder: python3 -B -m unittest -v platformbridge_test (the Go
test TestPlatformBridgePython runs it). Each test starts the real voice
caller (voicecaller.py, tone code speech) and a fake platform on 127.0.0.1
that speaks one framing (Twilio Media Streams, Vapi's websocket transport,
ElevenLabs conversation events) with a scripted tone-code agent behind it,
then runs the bridge as a subprocess between them and reads the caller's
timeline: the bench's own measure of the call.
"""

import base64
import http.server
import json
import os
import subprocess
import sys
import threading
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import platformbridge as pb  # noqa: E402
import tonecode  # noqa: E402
import wsproto  # noqa: E402
from voice_test import LONG, Caller, kinds  # noqa: E402

GREETING = "Hello, Acme Brew Club, how can I help?"
REFUNDED = "I have refunded the full 45.00."
SORRY = "Sorry. I have refunded the full 45.00."
VOICED = 250.0


class ToneAgent:
    """A scripted agent that hears and speaks tone code over any framing.

    send(pcm16k) ships agent audio (a burst; the caller plays it in real
    time), clear() is the framing's barge-in signal. With barge it answers
    the refund request with a long line and stops when the caller talks
    over it.
    """

    def __init__(self, send, clear, barge=False):
        self.send, self.clear, self.barge = send, clear, barge
        self.speech = bytearray()
        self.speaking = False
        self.quiet_ms = 0.0
        self.play_end = 0.0
        self.heard = []
        self.cleared = 0
        self.lock = threading.Lock()

    def say(self, text):
        pcm = tonecode.encode(text, lead_ms=40, tail_ms=60)
        self.play_end = max(self.play_end, time.monotonic()) + len(pcm) / 32000.0
        for f in range(0, len(pcm), 640 * 5):
            self.send(pcm[f:f + 640 * 5])

    def feed(self, pcm):
        with self.lock:
            ms = len(pcm) / 32.0
            voiced = tonecode.rms(pcm) > VOICED
            if voiced and time.monotonic() < self.play_end - 0.05:
                # The caller talks over the agent: stop at once.
                self.play_end = time.monotonic()
                self.cleared += 1
                self.clear()
            if voiced:
                self.speaking, self.quiet_ms = True, 0.0
            elif self.speaking:
                self.quiet_ms += ms
            if self.speaking:
                self.speech += pcm
            if self.speaking and self.quiet_ms >= 600:
                text = tonecode.decode(bytes(self.speech)) or ""
                self.speech.clear()
                self.speaking = False
                self.heard.append(text)
                self.answer(text)

    def answer(self, text):
        if "say that again" in text:
            self.say(SORRY)
        elif "refund" in text or "still there" in text:
            self.say(LONG if self.barge and not self.cleared else REFUNDED)


class FakePlatform:
    """A ThreadingHTTPServer playing one platform; framing in twilio | vapi | elevenlabs."""

    def __init__(self, framing, barge=False, key="", rest=None):
        self.framing, self.barge, self.key = framing, barge, key
        self.rest = rest or {}
        self.agents = []
        self.events = []
        self.requests = []
        fake = self

        class H(http.server.BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_GET(self):
                fake.requests.append(("GET", self.path, dict(self.headers)))
                if self.headers.get("upgrade", "").lower() == "websocket":
                    conn = wsproto.server_accept(self.connection, self.headers)
                    if conn:
                        fake.serve(conn, self.path, self.headers)
                    self.close_connection = True
                    return
                self.reply()

            def do_POST(self):
                n = int(self.headers.get("content-length") or 0)
                body = json.loads(self.rfile.read(n) or b"{}")
                fake.requests.append(("POST", self.path, dict(self.headers), body))
                self.reply()

            def reply(self):
                path = self.path.split("?")[0]
                fn = fake.rest.get(path)
                if fn is None:
                    self.send_response(404)
                    self.end_headers()
                    return
                status, out = fn(self)
                raw = json.dumps(out).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        self.srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.port = self.srv.server_address[1]
        self.base = f"http://127.0.0.1:{self.port}"
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def close(self):
        self.srv.shutdown()

    def serve(self, conn, path, headers):
        getattr(self, "serve_" + self.framing)(conn, path, headers)

    def serve_twilio(self, conn, path, headers):
        stream = {}

        def send(pcm):
            payload = base64.b64encode(pb.ulaw_encode(tonecode.resample(pcm, 16000, 8000))).decode()
            conn.send_text(json.dumps({"event": "media", "streamSid": stream.get("sid"), "media": {"payload": payload}}))

        def clear():
            conn.send_text(json.dumps({"event": "clear", "streamSid": stream.get("sid")}))

        agent = ToneAgent(send, clear, self.barge)
        self.agents.append(agent)
        try:
            while True:
                msg = conn.recv(timeout=30)
                if msg is None:
                    return
                ev = json.loads(msg[1])
                self.events.append(ev.get("event"))
                if ev.get("event") == "start":
                    stream["sid"] = ev["streamSid"]
                    stream["start"] = ev["start"]
                    self.start = ev["start"]
                    agent.say(GREETING)
                    conn.send_text(json.dumps({"event": "mark", "streamSid": stream["sid"], "mark": {"name": "greeting"}}))
                elif ev.get("event") == "media":
                    agent.feed(tonecode.resample(pb.ulaw_decode(base64.b64decode(ev["media"]["payload"])), 8000, 16000))
                elif ev.get("event") == "mark":
                    self.events.append("mark:" + ev["mark"]["name"])
                elif ev.get("event") == "stop":
                    return
        except wsproto.Closed:
            return

    def serve_vapi(self, conn, path, headers):
        agent = ToneAgent(conn.send_binary, lambda: conn.send_text(json.dumps({"type": "user-interrupted"})), self.barge)
        self.agents.append(agent)
        agent.say(GREETING)
        try:
            while True:
                msg = conn.recv(timeout=30)
                if msg is None:
                    return
                kind, data = msg
                if kind == "binary":
                    agent.feed(data)
                else:
                    self.events.append(json.loads(data).get("type"))
                    if json.loads(data).get("type") == "hangup":
                        return
        except wsproto.Closed:
            return

    def serve_elevenlabs(self, conn, path, headers):
        def send(pcm):
            conn.send_text(json.dumps({"type": "audio", "audio_event": {"audio_base_64": base64.b64encode(pb.ulaw_encode(tonecode.resample(pcm, 16000, 8000))).decode(), "event_id": 1}}))

        agent = ToneAgent(send, lambda: conn.send_text(json.dumps({"type": "interruption", "interruption_event": {"event_id": 2}})), self.barge)
        self.agents.append(agent)
        try:
            while True:
                msg = conn.recv(timeout=30)
                if msg is None:
                    return
                ev = json.loads(msg[1])
                if ev.get("type") == "conversation_initiation_client_data":
                    self.events.append("init")
                    conn.send_text(json.dumps({"type": "conversation_initiation_metadata", "conversation_initiation_metadata_event": {
                        "conversation_id": "conv_1", "user_input_audio_format": "pcm_16000", "agent_output_audio_format": "ulaw_8000"}}))
                    conn.send_text(json.dumps({"type": "ping", "ping_event": {"event_id": 9, "ping_ms": 0}}))
                    conn.send_text(json.dumps({"type": "agent_response", "agent_response_event": {"agent_response": GREETING}}))
                    agent.say(GREETING)
                elif ev.get("type") == "pong":
                    self.events.append("pong:%s" % ev.get("event_id"))
                elif "user_audio_chunk" in ev:
                    agent.feed(base64.b64decode(ev["user_audio_chunk"]))
        except wsproto.Closed:
            return


def run_bridge(env, timeout=90):
    e = dict(os.environ)
    e.update(env)
    p = subprocess.run([sys.executable, "-B", os.path.join(HERE, "platformbridge.py")], env=e, capture_output=True, text=True, timeout=timeout)
    return p.returncode, p.stderr


class CodecTest(unittest.TestCase):
    def test_ulaw_round_trip_keeps_tone_code(self):
        pcm = tonecode.encode("Refund order B-3001, 45.00.", lead_ms=40, tail_ms=60)
        back = tonecode.resample(pb.ulaw_decode(pb.ulaw_encode(tonecode.resample(pcm, 16000, 8000))), 8000, 16000)
        self.assertEqual(tonecode.decode(back), "Refund order B-3001, 45.00.")
        self.assertEqual(pb.ulaw_encode(tonecode.to_bytes([0]))[0], 0xFF)
        self.assertEqual(len(pb.ulaw_encode(bytes(320))), 160)

    def test_formats(self):
        pcm = tonecode.encode("hi", tail_ms=20)
        self.assertEqual(len(pb.to_format(pcm, "pcm_24000")), len(pcm) * 3 // 2)
        self.assertEqual(len(pb.to_format(pcm, "ulaw_8000")), len(pcm) // 4)
        self.assertEqual(tonecode.decode(pb.from_format(pb.to_format(pcm, "pcm_24000"), "pcm_24000")), "hi")


class BridgeTest(unittest.TestCase):
    def call(self, framing, platform, barge=False, key="", rest=None, env=None, wait=True):
        fake = FakePlatform(framing, barge=barge, key=key, rest=rest)
        cenv = {"BENCH_VOICE_PROBE": "interrupt", "BENCH_VOICE_PROBE_TURN": "2", "BENCH_VOICE_INTERRUPT_AFTER_MS": "500"} if barge else None
        c = Caller(cenv)
        try:
            url = fake.base.replace("http://", "ws://") + "/media" if platform == "websocket" else fake.base
            benv = {"BENCH_VOICE_URL": c.url, "BENCH_VOICE_PLATFORM": platform, "BENCH_VOICE_FRAMING": framing if platform == "websocket" else "",
                    "BENCH_VOICE_PLATFORM_URL": url, "BENCH_VOICE_AGENT_ID": "agent_1", "BENCH_VOICE_PLATFORM_KEY": key,
                    "BENCH_VOICE_SESSION": "run-1", "BENCH_VOICE_SETUP_S": "10"}
            benv.update(env or {})
            code, err = run_bridge(benv)
            ev = c.wait_done(30) if wait else c.events()
            return fake, ev, code, err
        finally:
            c.close()
            fake.close()

    def check_good(self, ev, code, err):
        self.assertEqual(code, 0, err)
        self.assertEqual(ev[-1]["ended_by"], "caller_done", err)
        heard = [h["text"] for h in kinds(ev, "agent_transcript")]
        self.assertEqual(heard[:2], [GREETING, REFUNDED], err)
        self.assertTrue(kinds(ev, "first_agent_audio"))

    def check_barge(self, fake, ev, code, err):
        self.assertEqual(code, 0, err)
        b = kinds(ev, "barge_in")
        self.assertEqual(len(b), 1, err)
        self.assertEqual(b[0]["by"], "clear", b)
        self.assertLess(b[0]["stopped_after_ms"], 1000)
        self.assertEqual(fake.agents[0].cleared, 1)
        self.assertIn(SORRY, [h["text"] for h in kinds(ev, "agent_transcript")])

    def test_twilio_good_call(self):
        fake, ev, code, err = self.call("twilio", "websocket", key="tok-1")
        self.check_good(ev, code, err)
        self.assertEqual(fake.start["mediaFormat"], {"encoding": "audio/x-mulaw", "sampleRate": 8000, "channels": 1})
        self.assertEqual(fake.start["customParameters"], {"heimdall_session": "run-1"})
        self.assertEqual(fake.events[:2], ["connected", "start"])
        self.assertIn("mark:greeting", fake.events)
        self.assertEqual(fake.events[-1], "stop")
        self.assertEqual(fake.requests[0][2].get("Authorization"), "Bearer tok-1")
        self.assertNotIn("tok-1", err)

    def test_twilio_barge_in(self):
        fake, ev, code, err = self.call("twilio", "websocket", barge=True)
        self.check_barge(fake, ev, code, err)

    def test_vapi_call(self):
        def create(h):
            if h.headers.get("Authorization") != "Bearer vk":
                return 401, {"message": "Invalid Key. Hot tip, you may be using the private key instead of the public key, or vice versa."}
            return 201, {"id": "call_1", "transport": {"provider": "vapi.websocket", "websocketCallUrl": f"ws://127.0.0.1:{h.server.server_address[1]}/call_1/transport"}}

        fake, ev, code, err = self.call("vapi", "vapi", key="vk", rest={"/call": create})
        self.check_good(ev, code, err)
        post = [r for r in fake.requests if r[0] == "POST"][0]
        self.assertEqual(post[3]["assistantId"], "agent_1")
        self.assertEqual(post[3]["transport"], {"provider": "vapi.websocket", "audioFormat": {"format": "pcm_s16le", "container": "raw", "sampleRate": 16000}})
        self.assertIn("hangup", fake.events)

    def test_vapi_refused(self):
        def create(h):
            return 401, {"message": "Invalid Key"}

        fake, ev, code, err = self.call("vapi", "vapi", key="vk-secret", rest={"/call": create}, wait=False)
        self.assertEqual(code, pb.EXIT_PLATFORM)
        self.assertEqual(ev[0]["ev"], "no_call")
        self.assertIn("HTTP 401: {\"message\": \"Invalid Key\"}", err)
        self.assertNotIn("vk-secret", err)

    def test_elevenlabs_signed_url_and_barge_in(self):
        def signed(h):
            if h.headers.get("xi-api-key") != "xi":
                return 401, {"detail": "invalid_api_key"}
            return 200, {"signed_url": f"ws://127.0.0.1:{h.server.server_address[1]}/v1/convai/conversation?agent_id=agent_1&conversation_signature=s"}

        fake, ev, code, err = self.call("elevenlabs", "elevenlabs", barge=True, key="xi",
                                        rest={"/v1/convai/conversation/get-signed-url": signed})
        self.check_barge(fake, ev, code, err)
        self.assertIn("init", fake.events)
        self.assertIn("pong:9", fake.events)
        self.assertTrue([e for e in kinds(ev, "agent_reported") if e.get("text") == GREETING])

    def test_elevenlabs_public_agent(self):
        fake, ev, code, err = self.call("elevenlabs", "elevenlabs")
        self.check_good(ev, code, err)
        self.assertTrue(any(r[1].startswith("/v1/convai/conversation?agent_id=agent_1") for r in fake.requests))


if __name__ == "__main__":
    unittest.main()
