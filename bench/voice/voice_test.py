"""Tests of the voice track's Python half: protocol, caller service, probes, fakes.

Run from this folder: python3 -B -m unittest -v voice_test (the Go test
TestVoicePython runs it). Everything is local: the caller runs as a
subprocess on 127.0.0.1, the agent side is voiceclient.py speaking tone
code, nothing reaches the network. With VOICE_WRITE_FIXTURES=<dir> the call
timelines are copied there (the Go analysis tests read them from testdata).
"""

import asyncio
import base64
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tonecode  # noqa: E402
import voiceclient  # noqa: E402
import wsproto  # noqa: E402

USER = {
    "id": "voice-test",
    "script": {
        "opening": "Hi, my grinder from order B-3001 arrived cracked. I would like a refund.",
        "replies": [
            {"when": ["email"], "say": "It is mia at example dot com."},
            {"when": ["refunded"], "say": "Great, thank you!", "stop": True},
        ],
        "done": "No, that's all.",
        "default": "Okay.",
    },
}
FAST = {"BENCH_VOICE_END_SILENCE_MS": "600", "BENCH_VOICE_REPLY_DELAY_MS": "100", "BENCH_VOICE_FIRST_WAIT_MS": "1500",
        "BENCH_VOICE_REPROMPT_MS": "2500", "BENCH_VOICE_MAX_REPROMPTS": "1", "BENCH_VOICE_STT": "tone",
        "BENCH_VOICE_TTS": "tone", "BENCH_VOICE_HANGUP_GRACE_MS": "200"}
LONG = ("I have looked at your order and I can see the grinder was delivered eight days ago, which is inside our thirty "
        "day window for damaged items, so I am going to issue the refund now.")


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class Caller:
    """voicecaller.py as a subprocess with its own state folder."""

    def __init__(self, env=None, user=None):
        self.dir = tempfile.mkdtemp(prefix="voicecaller-")
        self.state = os.path.join(self.dir, "state")
        cfg = os.path.join(self.dir, "user.json")
        with open(cfg, "w") as f:
            json.dump(user or USER, f)
        self.port = free_port()
        e = dict(os.environ)
        e.update(FAST)
        e.update(env or {})
        self.proc = subprocess.Popen([sys.executable, "-B", os.path.join(HERE, "voicecaller.py"), "serve", "--config", cfg,
                                      "--state", self.state, "--port", str(self.port)], env=e,
                                     stderr=subprocess.DEVNULL, stdout=subprocess.DEVNULL)
        for _ in range(100):
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{self.port}/healthz", timeout=1) as r:
                    if r.status == 200:
                        break
            except OSError:
                time.sleep(0.05)
        self.url = f"ws://127.0.0.1:{self.port}/call"

    def wait_done(self, timeout=30):
        """Wait until the call ended and the timeline has its call_end."""
        deadline = time.time() + timeout
        while time.time() < deadline:
            ev = self.events()
            if ev and ev[-1].get("ev") == "call_end":
                return ev
            time.sleep(0.1)
        raise AssertionError("call did not end: " + json.dumps(self.events()[-3:]))

    def events(self):
        try:
            with open(os.path.join(self.state, "timeline.jsonl")) as f:
                return [json.loads(line) for line in f if line.strip()]
        except (OSError, ValueError):
            return []

    def transcript(self):
        with open(os.path.join(self.state, "transcript.json")) as f:
            return json.load(f)

    def close(self, keep=None):
        self.proc.kill()
        self.proc.wait()
        out = os.environ.get("VOICE_WRITE_FIXTURES")
        if out and keep:
            shutil.copy(os.path.join(self.state, "timeline.jsonl"), os.path.join(out, keep))
        shutil.rmtree(self.dir, ignore_errors=True)


def kinds(events, ev):
    return [e for e in events if e.get("ev") == ev]


class ProtocolTest(unittest.TestCase):
    def test_frames_round_trip(self):
        got = {}

        async def server():
            async def handle(reader, writer):
                _, _, headers = await wsproto.read_http_head(reader)
                await wsproto.server_handshake(reader, writer, headers)
                conn = wsproto.AsyncConn(reader, writer)
                for _ in range(3):
                    kind, data = await conn.recv()
                    got.setdefault("msgs", []).append((kind, len(data)))
                    if kind == "binary":
                        await conn.send_binary(data)
                    else:
                        await conn.send_text(data)
                await conn.close()

            srv = await asyncio.start_server(handle, "127.0.0.1", 0)
            got["port"] = srv.sockets[0].getsockname()[1]
            got["ready"].set()
            async with srv:
                await asyncio.sleep(5)

        got["ready"] = threading.Event()
        threading.Thread(target=lambda: asyncio.run(server()), daemon=True).start()
        got["ready"].wait(5)
        c = wsproto.connect(f"ws://127.0.0.1:{got['port']}/x")
        big = os.urandom(200000)
        c.send_binary(big)
        c.send_text("héllo")
        c.send_binary(b"")
        self.assertEqual(c.recv(5), ("binary", big))
        self.assertEqual(c.recv(5), ("text", "héllo"))
        self.assertEqual(c.recv(5), ("binary", b""))
        with self.assertRaises(wsproto.Closed):
            c.recv(5)
        self.assertEqual(got["msgs"], [("binary", 200000), ("text", 5), ("binary", 0)])

    def test_listen_returns_one_queued_utterance(self):
        # Audit 8-E-01: two utterances already queued, 1.5 s of silence
        # apart, are two listens, not one.
        phone = voiceclient.VoiceCall.__new__(voiceclient.VoiceCall)
        phone.lock = threading.Condition()
        phone.ended, phone.end_reason = False, ""
        pcm = tonecode.encode("opening line", tail_ms=60) + tonecode.silence(1500) + tonecode.encode("reprompt line", tail_ms=60)
        t = time.monotonic()
        phone.frames = [(t, f) for f in tonecode.frames(pcm)]
        self.assertEqual(tonecode.decode(phone.listen(end_silence_ms=700, max_wait_s=1)), "opening line")
        phone.ended, phone.end_reason = True, "caller_done"
        self.assertEqual(tonecode.decode(phone.listen(end_silence_ms=700, max_wait_s=1)), "reprompt line")
        with self.assertRaises(voiceclient.Hangup):
            phone.listen(end_silence_ms=700, max_wait_s=1)

    def test_tone_code(self):
        pcm = tonecode.encode("Order B-3001: 45.00", lead_ms=77, tail_ms=200)
        self.assertEqual(tonecode.decode(pcm), "Order B-3001: 45.00")
        self.assertEqual(tonecode.decode(tonecode.add_noise(pcm, 10, 1)), "Order B-3001: 45.00")
        self.assertIsNone(tonecode.decode(tonecode.change_speed(pcm, 1.2)))
        self.assertIsNone(tonecode.decode(tonecode.silence(500)))
        wav = tonecode.wav_bytes(pcm)
        self.assertEqual(tonecode.to_16k(wav), pcm)


class CallerTest(unittest.TestCase):
    def call(self, caller, steps):
        phone = voiceclient.VoiceCall(caller.url)
        self.assertEqual(phone.start["protocol"], "heimdall-voice/1")
        self.assertEqual(phone.start["format"]["sample_rate"], 16000)
        return phone

    def test_good_call(self):
        c = Caller()
        try:
            phone = self.call(c, None)
            phone.say_tone("Hello, Acme Brew Club, how can I help?")
            heard = phone.listen(end_silence_ms=700, max_wait_s=10)
            self.assertIn("refund", tonecode.decode(heard))
            phone.report("caller", tonecode.decode(heard), 1)
            phone.say_tone("I have refunded the full 45.00.")
            heard = phone.listen(end_silence_ms=700, max_wait_s=10)
            self.assertEqual(tonecode.decode(heard), "Great, thank you!")
            self.assertTrue(phone.wait_end(10))
            ev = c.wait_done()
            self.assertEqual(ev[0]["ev"], "call_start")
            end = ev[-1]
            self.assertEqual(end["ended_by"], "caller_done")
            self.assertEqual(end["caller_turns"], 2)
            self.assertEqual(end["agent_utterances"], 2)
            self.assertEqual(len(end["caller_audio"]["sha256"]), 64)
            heard = kinds(ev, "agent_transcript")
            self.assertEqual([h["text"] for h in heard], ["Hello, Acme Brew Club, how can I help?", "I have refunded the full 45.00."])
            self.assertTrue(all(h["stt"] == "tone_code" for h in heard))
            starts = kinds(ev, "caller_speech_start")
            self.assertEqual([s["kind"] for s in starts], ["reply", "reply"])
            self.assertTrue(starts[-1]["final"])
            self.assertEqual(len(kinds(ev, "agent_reported")), 1)
            frames = kinds(ev, "frame")
            self.assertTrue(any(f["dir"] == "caller" for f in frames) and any(f["dir"] == "agent" for f in frames))
            t = c.transcript()
            self.assertEqual(t["ended_by"], "###STOP###")
            self.assertEqual([e["role"] for e in t["events"]], ["agent", "user", "agent", "user"])
            self.assertTrue(os.path.getsize(os.path.join(c.state, "agent.wav")) > 44)
        finally:
            c.close(keep="call-good.jsonl")

    def test_no_call_exports_exist(self):
        c = Caller()
        try:
            self.assertEqual(c.events(), [{"ev": "no_call", "t_ms": 0, "note": "the agent has not called"}])
            self.assertEqual(c.transcript()["events"], [])
            self.assertEqual(os.path.getsize(os.path.join(c.state, "caller.wav")), 44)
            with urllib.request.urlopen(f"http://127.0.0.1:{c.port}/kit/voiceclient.py") as r:
                self.assertIn(b"class VoiceCall", r.read())
        finally:
            c.close()

    def test_second_call_refused(self):
        c = Caller()
        try:
            phone = voiceclient.VoiceCall(c.url)
            with self.assertRaises(ConnectionError):
                wsproto.connect(c.url)
            phone.hangup()
            c.wait_done()
        finally:
            c.close()

    def test_caller_speaks_first_and_reprompts(self):
        c = Caller()
        try:
            phone = voiceclient.VoiceCall(c.url)
            heard = phone.listen(end_silence_ms=700, max_wait_s=10)  # the agent says nothing; the caller opens
            self.assertIn("grinder", tonecode.decode(heard))
            heard = phone.listen(end_silence_ms=700, max_wait_s=10)  # still nothing: the caller asks if anyone is there
            self.assertEqual(tonecode.decode(heard), "Hello? Are you still there?")
            ev = c.wait_done(20)
            self.assertEqual(ev[-1]["ended_by"], "agent_silent")
            starts = [s["kind"] for s in kinds(ev, "caller_speech_start")]
            self.assertEqual(starts[:2], ["opening", "reprompt"])
        finally:
            c.close(keep="call-silent-agent.jsonl")

    def barge(self, handled):
        c = Caller({"BENCH_VOICE_PROBE": "interrupt", "BENCH_VOICE_PROBE_TURN": "2", "BENCH_VOICE_INTERRUPT_AFTER_MS": "500"})
        try:
            phone = voiceclient.VoiceCall(c.url)
            phone.say_tone("Hello, how can I help?")
            phone.listen(end_silence_ms=700, max_wait_s=10)
            interrupted = phone.say_tone(LONG, realtime=handled, barge_in=handled, barge_in_ms=150)
            self.assertEqual(interrupted, handled)
            heard = phone.listen(end_silence_ms=700, max_wait_s=15)
            self.assertEqual(tonecode.decode(heard), "Sorry, sorry, could you say that again?")
            phone.say_tone("Sorry. I have refunded the full 45.00.")
            for _ in range(3):
                try:
                    heard = phone.listen(end_silence_ms=700, max_wait_s=15)
                except voiceclient.Hangup:
                    break
                if tonecode.decode(heard or b"") == "Hello? Are you still there?":
                    # The answer was queued behind the agent's own talking, so the caller
                    # never heard it as a reply: say it again.
                    phone.say_tone("I have refunded the full 45.00.")
            phone.wait_end(10)
            ev = c.wait_done()
            b = kinds(ev, "barge_in")
            self.assertEqual(len(b), 1)
            if handled:
                self.assertEqual(b[0]["by"], "clear")
                self.assertLess(b[0]["stopped_after_ms"], 1000)
                self.assertTrue(kinds(ev, "agent_clear"))
            else:
                self.assertGreater(b[0]["stopped_after_ms"], 1000)
                self.assertTrue(kinds(ev, "overlap") or b[0]["stopped_after_ms"] > 1000)
            first = [t for t in kinds(ev, "agent_transcript") if t["interrupted"]]
            self.assertEqual(len(first), 1)
        finally:
            c.close(keep="call-barge-handled.jsonl" if handled else "call-barge-ignored.jsonl")

    def test_barge_in_handled(self):
        self.barge(True)

    def test_barge_in_ignored(self):
        self.barge(False)

    def test_answer_over_the_caller_waits(self):
        """An agent that answers while the caller is still talking: the caller finishes its line first."""
        c = Caller()
        try:
            phone = voiceclient.VoiceCall(c.url)
            phone.say_tone("Hello, how can I help?")
            # Wait until the caller's reply (about 3 s) is audibly under way, not
            # a fixed sleep: on a loaded machine the reply can start late.
            deadline = time.monotonic() + 10
            while not phone.caller_speaking() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue(phone.caller_speaking(), "the caller never started its reply")
            phone.say_tone("I have refunded the full 45.00.", realtime=False)
            phone.wait_end(20)
            ev = c.wait_done()
            spans = [(e["turn"], e["t_ms"]) for e in ev if e["ev"] == "caller_speech_start"]
            ends = {e["turn"]: e["t_ms"] for e in ev if e["ev"] == "caller_speech_end"}
            for (turn, start), (nxt, nstart) in zip(spans, spans[1:]):
                self.assertGreaterEqual(nstart, ends[turn], f"caller line {nxt} started before line {turn} ended")
            self.assertTrue(kinds(ev, "overlap"))
        finally:
            c.close()

    def test_silent_probe(self):
        c = Caller({"BENCH_VOICE_PROBE": "silent", "BENCH_VOICE_PROBE_TURN": "2", "BENCH_VOICE_SILENT_MS": "1500",
                    "BENCH_VOICE_REPROMPT_MS": "6000"})
        try:
            phone = voiceclient.VoiceCall(c.url)
            phone.say_tone("Hello, how can I help?")
            phone.listen(end_silence_ms=700, max_wait_s=10)
            t0 = time.time()
            phone.say_tone("Could you tell me your email?")
            heard = phone.listen(end_silence_ms=700, max_wait_s=10)
            self.assertGreater(time.time() - t0, 1.5)
            self.assertEqual(tonecode.decode(heard), "It is mia at example dot com.")
            phone.hangup()
            ev = c.wait_done()
            self.assertEqual(kinds(ev, "probe")[0]["silent_ms"], 1500)
            self.assertEqual(ev[-1]["ended_by"], "agent_hangup")
        finally:
            c.close()

    def test_noise_and_speed_probes(self):
        for probe, readable in (("noise", True), ("speed", False)):
            c = Caller({"BENCH_VOICE_PROBE": probe})
            try:
                phone = voiceclient.VoiceCall(c.url)
                phone.say_tone("Hello, how can I help?")
                heard = phone.listen(end_silence_ms=700, max_wait_s=10)
                text = tonecode.decode(heard)
                if readable:
                    self.assertIn("grinder", text or "")
                    self.assertGreater(tonecode.rms(heard[:640]), 0)
                else:
                    self.assertIsNone(text)
                phone.hangup()
                ev = c.wait_done()
                applied = kinds(ev, "caller_speech_start")[0]["probe"]
                self.assertIn("noise_snr_db" if probe == "noise" else "speed", applied)
            finally:
                c.close()

    def test_bad_settings_refused(self):
        p = subprocess.run([sys.executable, "-B", os.path.join(HERE, "voicecaller.py"), "serve", "--config", "/nonexistent",
                            "--state", tempfile.mkdtemp(), "--port", str(free_port())],
                           env=dict(os.environ, BENCH_VOICE_PROBE="loud"), capture_output=True, timeout=20)
        self.assertNotEqual(p.returncode, 0)


class FakeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.port = free_port()
        script = os.path.join(HERE, "testdata", "chat-script.json")
        cls.proc = subprocess.Popen([sys.executable, "-B", os.path.join(HERE, "fakeopenai.py"), "serve", "--port", str(cls.port),
                                     "--host", "127.0.0.1", "--chat-script", script], stderr=subprocess.DEVNULL)
        cls.base = f"http://127.0.0.1:{cls.port}"
        for _ in range(100):
            try:
                urllib.request.urlopen(cls.base + "/healthz", timeout=1)
                break
            except OSError:
                time.sleep(0.05)

    @classmethod
    def tearDownClass(cls):
        cls.proc.kill()
        cls.proc.wait()

    def post(self, path, body, ctype="application/json"):
        req = urllib.request.Request(self.base + path, data=body, method="POST", headers={"Content-Type": ctype, "Authorization": "Bearer x"})
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.read()

    def test_speech_and_transcription(self):
        wav = self.post("/v1/audio/speech", json.dumps({"model": "gpt-4o-mini-tts", "input": "Refunded 45.00", "voice": "alloy",
                                                        "response_format": "wav"}).encode())
        pcm, rate = tonecode.read_wav(wav)
        self.assertEqual(rate, 24000)
        self.assertEqual(tonecode.decode(tonecode.resample(pcm, 24000, 16000)), "Refunded 45.00")
        b = "bnd"
        form = (f'--{b}\r\nContent-Disposition: form-data; name="model"\r\n\r\nwhisper-1\r\n'
                f'--{b}\r\nContent-Disposition: form-data; name="file"; filename="a.wav"\r\n\r\n').encode() + \
            tonecode.wav_bytes(tonecode.encode("my order is B-3001")) + f"\r\n--{b}--\r\n".encode()
        out = json.loads(self.post("/v1/audio/transcriptions", form, "multipart/form-data; boundary=" + b))
        self.assertEqual(out["text"], "my order is B-3001")
        self.assertEqual(out["usage"]["type"], "duration")

    def test_chat_scripts_are_per_conversation(self):
        def chat(msgs):
            return json.loads(self.post("/v1/chat/completions", json.dumps({"model": "gpt-4o-mini", "messages": msgs}).encode()))["choices"][0]["message"]

        base = [{"role": "system", "content": "s"}, {"role": "assistant", "content": "Hello"}, {"role": "user", "content": "my grinder is cracked"}]
        self.assertEqual(chat(base)["tool_calls"][0]["function"]["name"], "get_policy")
        other = [{"role": "user", "content": "the kettle burned me"}]
        self.assertEqual(chat(other)["tool_calls"][0]["function"]["name"], "find_customer")
        two = base + [{"role": "assistant", "content": None, "tool_calls": []}, {"role": "tool", "content": "{}"}]
        self.assertEqual(chat(two)["tool_calls"][0]["function"]["name"], "find_customer")
        again = base + [{"role": "assistant", "content": "I have refunded 45.00."}, {"role": "user", "content": "Sorry, could you say that again?"}]
        self.assertEqual(chat(again)["content"], "I have refunded 45.00.")

    def test_realtime_function_call_and_audio(self):
        async def run():
            conn = await wsproto.async_connect(f"ws://127.0.0.1:{self.port}/v1/realtime?model=gpt-realtime")
            types = []

            async def until(t):
                while True:
                    kind, data = await asyncio.wait_for(conn.recv(), 10)
                    ev = json.loads(data)
                    types.append(ev["type"])
                    if ev["type"] == t:
                        return ev

            await until("session.created")
            pcm = tonecode.resample(tonecode.encode("the grinder arrived cracked", tail_ms=800), 16000, 24000)
            for i in range(0, len(pcm), 4800):
                await conn.send_text(json.dumps({"type": "input_audio_buffer.append", "audio": base64.b64encode(pcm[i:i + 4800]).decode()}))
            heard = await until("conversation.item.input_audio_transcription.completed")
            done = await until("response.done")
            await conn.close()
            return heard, done, types

        heard, done, types = asyncio.run(run())
        self.assertEqual(heard["transcript"], "the grinder arrived cracked")
        self.assertIn("input_audio_buffer.speech_started", types)
        self.assertIn("response.created", types)


if __name__ == "__main__":
    unittest.main()
