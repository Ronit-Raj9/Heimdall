#!/usr/bin/env python3
"""Heimdall voice caller: a simulated customer who phones a voice agent.

Part of Heimdall (bench/voice), docs/voice-agents.md and docs/voice-protocol.md.
Standard library only; Piper (text to speech) and faster-whisper (speech to
text) are used when the image has them. The bench starts it as a task
service (role user), like the text user simulator, and its exports are bench
evidence the agent cannot edit, as the mailbox and the tool proxy's ledger
are: the agent only ever sees audio.

The agent dials ws://<service>:8000/call and speaks the heimdall-voice/1
protocol (16 kHz mono pcm_s16le in binary frames, JSON control messages).
The caller:
  - decides what to say with the text simulator (simuser.py, from
    bench/simuser): the task's script (scripted mode, the default) or a
    pinned model playing the persona (llm mode, through its own gateway key);
  - speaks each line with local text to speech (BENCH_VOICE_TTS): tone (the
    tone code, tonecode.py: deterministic, for controls and $0 fakes) or
    piper (real synthesized speech);
  - plays the agent's audio on a real-time clock, as a telephone would, so
    audio that arrives faster than real time queues and a clear message
    drops what has not played yet;
  - finds where the agent's speech starts and ends (energy on the played
    audio), transcribes each agent utterance (BENCH_VOICE_STT: auto, tone or
    whisper) and answers it;
  - applies voice probes (BENCH_VOICE_PROBE): noise, speed, interrupt
    (barge in while the agent talks), silent (the caller goes quiet);
  - writes state/timeline.jsonl (every audio frame boundary, speech start and
    end for both sides, transcripts, interruptions, control messages),
    state/transcript.json (the conversation in the text simulator's shape,
    so support verifiers read it unchanged) and, unless
    BENCH_VOICE_KEEP_AUDIO=0, caller.wav and agent.wav on the call clock,
    capped at BENCH_VOICE_MAX_AUDIO_S seconds each, with their sha256 in
    the timeline.

  python3 voicecaller.py serve [--config /sim/user.json] [--state /state] [--port 8000]
  python3 voicecaller.py ready [--port 8000]
"""

import argparse
import asyncio
import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import threading
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tonecode  # noqa: E402
import wsproto  # noqa: E402

PROTOCOL = "heimdall-voice/1"
STOP = "###STOP###"
FRAME_MS = tonecode.FRAME_MS
FRAME_BYTES = tonecode.FRAME_BYTES
MAX_FRAME_BYTES = 32000  # one second of audio in one binary message
VOICED_RMS = 250.0
MAX_TIMELINE_FRAMES = 200000

DEFAULTS = {
    "tts": "tone",            # tone | piper
    "stt": "auto",            # auto | tone | whisper
    "end_silence_ms": 800,    # agent silence that ends an agent utterance
    "reply_delay_ms": 300,    # the caller's reaction time before answering
    "first_wait_ms": 3000,    # the caller speaks first when the agent is silent this long
    "reprompt_ms": 7000,      # "Hello, are you there?" after this much silence on the agent's floor
    "max_reprompts": 2,       # then the caller hangs up
    "max_call_s": 240,
    "max_audio_s": 240,
    "keep_audio": True,
    "hangup_grace_ms": 600,
}

KIT_FILES = ("voiceclient.py", "tonecode.py", "wsproto.py")
REPROMPT = "Hello? Are you still there?"
INTERRUPT_LINE = "Sorry, sorry, could you say that again?"


def env_settings(env):
    s = dict(DEFAULTS)
    for key in list(DEFAULTS):
        v = env.get("BENCH_VOICE_" + key.upper())
        if v is None or v == "":
            continue
        if isinstance(DEFAULTS[key], bool):
            s[key] = v not in ("0", "false", "no")
        elif isinstance(DEFAULTS[key], int):
            s[key] = int(v)
        else:
            s[key] = v
    if s["tts"] not in ("tone", "piper"):
        raise SystemExit(f"voicecaller: BENCH_VOICE_TTS {s['tts']!r}: want tone or piper")
    if s["stt"] not in ("auto", "tone", "whisper"):
        raise SystemExit(f"voicecaller: BENCH_VOICE_STT {s['stt']!r}: want auto, tone or whisper")
    probe = {"id": env.get("BENCH_VOICE_PROBE", "")}
    if probe["id"]:
        if probe["id"] not in ("noise", "speed", "interrupt", "silent"):
            raise SystemExit(f"voicecaller: BENCH_VOICE_PROBE {probe['id']!r}: want noise, speed, interrupt or silent")
        probe["snr_db"] = float(env.get("BENCH_VOICE_NOISE_SNR_DB") or 10)
        probe["speed"] = float(env.get("BENCH_VOICE_SPEED") or 1.15)
        probe["turn"] = int(env.get("BENCH_VOICE_PROBE_TURN") or 2)
        probe["after_ms"] = int(env.get("BENCH_VOICE_INTERRUPT_AFTER_MS") or 700)
        probe["silent_ms"] = int(env.get("BENCH_VOICE_SILENT_MS") or 9000)
        probe["seed"] = int(env.get("BENCH_VOICE_PROBE_SEED") or 20261118)
        probe["line"] = env.get("BENCH_VOICE_INTERRUPT_LINE") or INTERRUPT_LINE
    s["probe"] = probe
    return s


def load_simuser():
    """The text simulator module (simuser.py), next to this file or in bench/simuser."""
    for cand in (os.path.join(HERE, "simuser.py"), os.path.join(HERE, "..", "simuser", "simuser.py")):
        if os.path.exists(cand):
            spec = importlib.util.spec_from_file_location("simuser", cand)
            mod = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(mod)
            return mod
    raise SystemExit("voicecaller: simuser.py not found next to voicecaller.py")


# ---- speech engines ----

class Engines:
    """Text to speech and speech to text, loaded once per service."""

    def __init__(self, settings):
        self.settings = settings
        self.tts = settings["tts"]
        self.stt = settings["stt"]
        self.whisper = None
        self.whisper_error = ""
        self.ready = threading.Event()
        self.cache = {}
        self.cache_lock = threading.Lock()
        self.piper_bin = os.environ.get("PIPER_BIN", "/opt/voice/piper/piper")
        self.piper_voice = os.environ.get("PIPER_VOICE", "/opt/voice/voices/en_US-ljspeech-medium.onnx")
        # Recorded lines: the user file's "audio" map, line text -> WAV path.
        # A line in it is played from the recording (a benchmark's own audio,
        # e.g. VoiceBench) instead of being synthesized; its text stays the
        # known transcript of what the caller said.
        self.audio_lines = {}

    def load(self):
        try:
            if self.tts == "piper" and not (os.path.exists(self.piper_bin) and os.path.exists(self.piper_voice)):
                raise SystemExit(f"voicecaller: tts piper needs {self.piper_bin} and {self.piper_voice}")
            if self.stt in ("whisper", "auto"):
                try:
                    from faster_whisper import WhisperModel  # type: ignore
                except ImportError:
                    if self.stt == "whisper":
                        raise SystemExit("voicecaller: stt whisper needs faster-whisper in the image")
                    self.whisper_error = "faster-whisper is not installed: only tone-coded agent audio is transcribed"
                else:
                    path = os.environ.get("WHISPER_MODEL_DIR", "/opt/voice/whisper")
                    threads = int(os.environ.get("WHISPER_THREADS", "2"))
                    self.whisper = WhisperModel(path, device="cpu", compute_type="int8", cpu_threads=threads)
        finally:
            self.ready.set()

    def speak(self, text):
        """PCM for text and the engine that made it."""
        key = (self.tts, text)
        with self.cache_lock:
            if key in self.cache:
                return self.cache[key]
        if text in self.audio_lines:
            with open(self.audio_lines[text], "rb") as f:
                pcm = tonecode.to_16k(f.read())
            res = (tonecode.silence(60) + pcm + tonecode.silence(60), "recording")
        elif self.tts == "piper":
            out = subprocess.run([self.piper_bin, "--model", self.piper_voice, "--output-raw", "--noise_scale", "0",
                                  "--noise_w", "0", "--quiet"], input=text.encode(), capture_output=True, timeout=120, check=False)
            if out.returncode != 0:
                raise RuntimeError("piper failed: " + out.stderr.decode("utf-8", "replace")[-300:])
            with open(self.piper_voice + ".json", encoding="utf-8") as f:
                rate = json.load(f)["audio"]["sample_rate"]
            pcm = tonecode.resample(out.stdout, rate, tonecode.SAMPLE_RATE)
            res = (tonecode.silence(60) + pcm + tonecode.silence(60), "piper")
        else:
            res = (tonecode.encode(text, lead_ms=40, tail_ms=60), "tone_code")
        with self.cache_lock:
            self.cache[key] = res
        return res

    def transcribe(self, pcm):
        """(text, engine) for the agent's utterance; text is None when nothing could read it."""
        if self.stt in ("auto", "tone"):
            text = tonecode.decode(pcm)
            if text is not None:
                return text, "tone_code"
            if self.stt == "tone":
                return None, "tone_code"
        if self.whisper is None:
            return None, "none"
        try:
            import numpy  # type: ignore
        except ImportError:
            return None, "none"
        x = tonecode.to_samples(pcm)
        audio = numpy.frombuffer(x.tobytes(), dtype=numpy.int16).astype(numpy.float32) / 32768.0
        segs, _ = self.whisper.transcribe(audio, language="en", beam_size=1, vad_filter=False,
                                          condition_on_previous_text=False, temperature=0.0)
        return " ".join(s.text.strip() for s in segs).strip(), "whisper"


# ---- the call ----

class Timeline:
    def __init__(self, path):
        self.f = open(path, "w", encoding="utf-8")
        self.frames = 0
        self.dropped = 0
        self.lock = threading.Lock()

    def write(self, rec):
        if rec.get("ev") == "frame":
            self.frames += 1
            if self.frames > MAX_TIMELINE_FRAMES:
                self.dropped += 1
                return
        with self.lock:
            self.f.write(json.dumps(rec, sort_keys=True, ensure_ascii=False) + "\n")
            self.f.flush()

    def close(self):
        self.f.close()


class Track:
    """Audio placed on the call clock, for caller.wav and agent.wav."""

    def __init__(self, cap_s):
        self.buf = bytearray()
        self.cap = int(cap_s * tonecode.SAMPLE_RATE * 2)
        self.truncated = False

    def place(self, t_ms, pcm):
        off = (int(t_ms) * tonecode.SAMPLE_RATE // 1000) * 2
        end = off + len(pcm)
        if end > self.cap:
            self.truncated = True
            pcm = pcm[: max(0, self.cap - off)]
            end = off + len(pcm)
            if not pcm:
                return
        if len(self.buf) < end:
            self.buf += bytes(end - len(self.buf))
        self.buf[off:end] = pcm

    def wav(self):
        return tonecode.wav_bytes(bytes(self.buf))


class Call:
    def __init__(self, conn, cfg, settings, engines, state_dir, session):
        self.conn = conn
        self.cfg = cfg
        self.s = settings
        self.probe = settings["probe"]
        self.engines = engines
        self.state_dir = state_dir
        self.t0 = time.monotonic()
        self.wall0 = time.time()
        self.timeline = Timeline(os.path.join(state_dir, "timeline.jsonl"))
        self.caller_track = Track(settings["max_audio_s"])
        self.agent_track = Track(settings["max_audio_s"])
        self.session = session  # the text simulator, its transcript and its persistence (simuser.Session)
        self.transcript = self.session.transcript
        self.transcript["voice"] = {"protocol": PROTOCOL, "called": True, "tts": settings["tts"], "stt": settings["stt"], "probe": self.probe.get("id") or None}
        self.ended = asyncio.Event()
        self.ended_by = None
        # Agent playout.
        self.queue = []          # [play_start_ms, pcm, recv_ms, seq]
        self.tail_ms = 0.0
        self.agent_seq = 0
        self.agent_speaking = False
        self.agent_utt = 0
        self.utt_start = 0.0
        self.last_voiced_end = 0.0
        self.utt_pcm = bytearray()
        self.utt_interrupted = False
        self.agent_ever = False
        # Caller speech.
        self.caller_speaking = False
        self.caller_turn = 0
        self.caller_seq = 0
        self.speak_task = None
        self.floor = "agent"     # who is expected to speak next
        self.floor_since = 0.0
        self.reprompts = 0
        self.pending_reply = None
        # The caller's answer from the moment it is committed (text being
        # made, then synthesis) until its last frame: the one outgoing
        # speech owner, so an interrupt never starts a second line whose
        # frames would interleave with it.
        self.answering = False
        self.barge = None
        self.barges = 0
        self.stop_after_speaking = False
        self.agent_hung_up = False

    def now(self):
        return (time.monotonic() - self.t0) * 1000.0

    def log(self, ev, **kw):
        rec = {"t_ms": round(self.now(), 1), "ev": ev}
        rec.update(kw)
        self.timeline.write(rec)

    # -- agent audio in --

    def on_audio(self, data):
        if len(data) % 2:
            data = data[:-1]
        if not data:
            return
        now = self.now()
        start = max(now, self.tail_ms)
        dur = len(data) * 1000.0 / (tonecode.SAMPLE_RATE * 2)
        self.agent_seq += 1
        self.queue.append([start, bytes(data), now, self.agent_seq])
        self.tail_ms = start + dur

    def on_clear(self):
        now = self.now()
        kept, dropped_ms = [], 0.0
        for item in self.queue:
            if item[0] >= now:
                dropped_ms += len(item[1]) * 1000.0 / 32000.0
            else:
                kept.append(item)
        self.queue = kept
        self.tail_ms = max(now, max((q[0] + len(q[1]) * 1000.0 / 32000.0 for q in kept), default=now))
        self.tail_ms = min(self.tail_ms, now + FRAME_MS)
        self.log("agent_clear", dropped_ms=round(dropped_ms, 1))
        if self.barge and self.barge.get("agent_stop_ms") is None and self.agent_speaking:
            self.barge["agent_stop_ms"] = round(now, 1)
            self.barge["by"] = "clear"

    def play_due(self):
        """Move queued agent audio whose play time has come into the played timeline."""
        now = self.now()
        while self.queue and self.queue[0][0] <= now:
            start, pcm, recv, seq = self.queue.pop(0)
            voiced = tonecode.rms(pcm) > VOICED_RMS
            dur = len(pcm) * 1000.0 / 32000.0
            self.log("frame", dir="agent", seq=seq, play_ms=round(start, 1), recv_ms=round(recv, 1), ms=round(dur, 1), voiced=voiced)
            if self.s["keep_audio"]:
                self.agent_track.place(start, pcm)
            if voiced:
                if not self.agent_speaking:
                    self.agent_speaking = True
                    self.agent_utt += 1
                    self.utt_start = start
                    self.utt_pcm = bytearray()
                    self.utt_interrupted = False
                    self.log("agent_speech_start", utt=self.agent_utt)
                    if not self.agent_ever:
                        self.agent_ever = True
                        self.log("first_agent_audio", utt=self.agent_utt)
                    if self.caller_speaking:
                        self.log("overlap", by="agent", caller_turn=self.caller_turn, utt=self.agent_utt)
                    if self.pending_reply and not self.pending_reply.done():
                        # The agent kept talking before the caller answered: one longer agent turn.
                        self.pending_reply.cancel()
                        self.pending_reply = None
                        self.log("reply_withheld", reason="agent resumed speaking")
                self.last_voiced_end = start + dur
            if self.agent_speaking:
                self.utt_pcm += pcm

    def check_utterance_end(self):
        if not self.agent_speaking:
            return
        now = self.now()
        queued_voiced = any(tonecode.rms(q[1]) > VOICED_RMS for q in self.queue[:50])
        if queued_voiced or now - self.last_voiced_end < self.s["end_silence_ms"]:
            return
        self.agent_speaking = False
        utt = self.agent_utt
        self.log("agent_speech_end", utt=utt, start_ms=round(self.utt_start, 1), end_ms=round(self.last_voiced_end, 1))
        if self.barge and self.barge.get("agent_stop_ms") is None:
            self.barge["agent_stop_ms"] = round(self.last_voiced_end, 1)
            self.barge["by"] = "audio_ended"
        pcm = bytes(self.utt_pcm)
        interrupted = self.utt_interrupted
        self.floor = "caller"
        self.floor_since = now
        asyncio.get_running_loop().create_task(self.heard(utt, pcm, interrupted))

    async def heard(self, utt, pcm, interrupted):
        text, engine = await asyncio.get_running_loop().run_in_executor(None, self.engines.transcribe, pcm)
        self.log("agent_transcript", utt=utt, text=text, stt=engine, audio_ms=round(len(pcm) / 32.0, 1), interrupted=interrupted)
        content = text if text is not None else ""
        if interrupted:
            # What the caller heard before talking over the agent: recorded, not
            # answered. The caller already spoke (its interruption), so the floor
            # is the agent's again.
            self.floor = "agent"
            self.floor_since = self.now()
            with self.session.lock:
                self.transcript["events"].append({"seq": len(self.transcript["events"]) + 1, "role": "agent", "content": content,
                                                  "utt": utt, "stt": engine, "interrupted": True})
                self.session.persist()
            return
        if self.ended.is_set() or self.stop_after_speaking:
            return
        self.pending_reply = asyncio.get_running_loop().create_task(self.reply(utt, content, engine))

    async def reply(self, utt, content, engine):
        delay = self.s["reply_delay_ms"]
        if self.probe.get("id") == "silent" and self.caller_turn + 1 == self.probe["turn"]:
            delay = self.probe["silent_ms"]
            self.log("probe", id="silent", turn=self.caller_turn + 1, silent_ms=delay)
        await asyncio.sleep(delay / 1000.0)
        # An agent that answered while the caller was still talking is heard,
        # but the caller finishes its own line before it answers.
        while self.caller_speaking and not self.ended.is_set():
            await asyncio.sleep(FRAME_MS / 1000.0)
        if self.agent_speaking or self.ended.is_set():
            return
        # From here the caller is answering: the agent talking again no longer
        # withholds the answer (and cannot cancel the hangup after a last line).
        self.pending_reply = None
        self.answering = True
        try:
            text, is_error = await asyncio.get_running_loop().run_in_executor(None, self.respond_text, content, utt, engine)
            if is_error:
                self.log("simulator_error", text=text)
                return
            await self.say(text, "reply")
        finally:
            self.answering = False

    def respond_text(self, content, utt, engine):
        """The simulated customer's answer to what the agent said (the text simulator's turn)."""
        msg = content.strip() or "(inaudible)"
        reply, is_error = self.session.respond({"message": msg})
        with self.session.lock:
            # Tag the agent event with the utterance and the engine that heard it.
            for ev in reversed(self.transcript["events"]):
                if ev.get("role") == "agent" and "utt" not in ev:
                    ev["utt"], ev["stt"] = utt, engine
                    break
            self.session.persist()
        return reply, is_error

    # -- caller speech out --

    async def say(self, text, kind):
        """Speak one caller line at real-time pace; ends the call after a STOP line."""
        stop = STOP in text
        line = text.replace(STOP, "").strip() or "Okay."
        pcm, engine = await asyncio.get_running_loop().run_in_executor(None, self.engines.speak, line)
        applied = {}
        pid = self.probe.get("id")
        if pid == "noise":
            pcm = tonecode.add_noise(pcm, self.probe["snr_db"], self.probe["seed"] + self.caller_turn)
            applied = {"noise_snr_db": self.probe["snr_db"]}
        elif pid == "speed":
            pcm = tonecode.change_speed(pcm, self.probe["speed"])
            applied = {"speed": self.probe["speed"]}
        self.caller_turn += 1
        turn = self.caller_turn
        self.caller_speaking = True
        start = self.now()
        self.log("caller_speech_start", turn=turn, kind=kind, text=line, tts=engine, probe=applied or None,
                 audio_ms=round(len(pcm) / 32.0, 1), final=stop or kind == "closing")
        frames = tonecode.frames(pcm)
        t_start = time.monotonic()
        try:
            for i, f in enumerate(frames):
                target = t_start + i * FRAME_MS / 1000.0
                d = target - time.monotonic()
                if d > 0:
                    await asyncio.sleep(d)
                if self.ended.is_set():
                    break
                self.caller_seq += 1
                t = self.now()
                self.log("frame", dir="caller", seq=self.caller_seq, play_ms=round(t, 1), ms=FRAME_MS, voiced=tonecode.rms(f) > VOICED_RMS)
                if self.s["keep_audio"]:
                    self.caller_track.place(t, f)
                await self.conn.send_binary(f)
        except (wsproto.Closed, ConnectionError, OSError):
            self.finish("agent_disconnected")
        finally:
            self.caller_speaking = False
        end = self.now()
        self.log("caller_speech_end", turn=turn, kind=kind, start_ms=round(start, 1), end_ms=round(end, 1))
        if kind in ("reply", "opening", "reprompt"):
            self.floor = "agent"
            self.floor_since = end
        if stop:
            self.stop_after_speaking = True
            await asyncio.sleep(self.s["hangup_grace_ms"] / 1000.0)
            self.finish("caller_done" if self.transcript.get("ended_by") != "max_turns" else "max_turns")

    async def interrupt(self):
        line = self.probe["line"]
        self.barges += 1
        now = self.now()
        self.utt_interrupted = True
        self.barge = {"n": self.barges, "utt": self.agent_utt, "caller_start_ms": round(now, 1), "agent_stop_ms": None}
        self.log("barge_in_start", utt=self.agent_utt, line=line)
        with self.session.lock:
            self.transcript["events"].append({"seq": len(self.transcript["events"]) + 1, "role": "user", "content": line,
                                              "rule": "interrupt"})
            self.session.persist()
        await self.say(line, "interrupt")
        self.floor = "agent"
        self.floor_since = self.now()

    def finish(self, why):
        if self.ended.is_set():
            return
        self.ended_by = why
        self.ended.set()

    # -- loops --

    async def ticker(self):
        deadline = self.s["max_call_s"] * 1000.0
        while not self.ended.is_set():
            self.play_due()
            self.check_utterance_end()
            now = self.now()
            if self.barge and self.barge.get("agent_stop_ms") is None and not self.agent_speaking and self.last_voiced_end <= now:
                self.barge["agent_stop_ms"] = round(self.last_voiced_end, 1)
                self.barge["by"] = "audio_ended"
            if self.barge and self.barge.get("agent_stop_ms") is not None and "logged" not in self.barge:
                self.barge["logged"] = True
                stop = max(self.barge["agent_stop_ms"], self.barge["caller_start_ms"])
                self.log("barge_in", utt=self.barge["utt"], caller_start_ms=self.barge["caller_start_ms"],
                         agent_stop_ms=round(stop, 1), stopped_after_ms=round(stop - self.barge["caller_start_ms"], 1), by=self.barge.get("by"))
            if now >= deadline:
                self.log("limit", what="max_call_s", value=self.s["max_call_s"])
                self.finish("max_call")
                break
            # A started caller line counts as speaking before say() sets
            # caller_speaking: the text and the audio are made in an executor,
            # and a slow one (a loaded machine) must not let the next tick
            # start a second opening whose frames interleave with the first.
            starting = (self.speak_task is not None and not self.speak_task.done()) or self.answering
            busy = starting or self.caller_speaking or self.agent_speaking or (self.pending_reply and not self.pending_reply.done())
            if not busy and not self.stop_after_speaking:
                if self.caller_turn == 0 and not self.agent_ever and now >= self.s["first_wait_ms"]:
                    self.speak_task = asyncio.get_running_loop().create_task(self.opening())
                elif self.floor == "agent" and self.caller_turn > 0 and now - self.floor_since >= self.s["reprompt_ms"] and not self.queue:
                    if self.reprompts >= self.s["max_reprompts"]:
                        self.log("agent_silent", waited_ms=round(now - self.floor_since, 1))
                        self.speak_task = asyncio.get_running_loop().create_task(self.say("Okay, I'll call back later. Bye.", "closing"))
                        self.stop_after_speaking = True
                        asyncio.get_running_loop().call_later(4, self.finish, "agent_silent")
                    else:
                        self.reprompts += 1
                        self.floor_since = now
                        self.speak_task = asyncio.get_running_loop().create_task(self.say(REPROMPT, "reprompt"))
            pid = self.probe.get("id")
            if (pid == "interrupt" and self.agent_speaking and not self.caller_speaking and not starting and self.barges == 0
                    and self.agent_utt >= self.probe["turn"] and now - self.utt_start >= self.probe["after_ms"]):
                self.speak_task = asyncio.get_running_loop().create_task(self.interrupt())
            if self.agent_hung_up and not self.queue and not self.agent_speaking:
                self.finish("agent_hangup")
            await asyncio.sleep(FRAME_MS / 2000.0)

    async def opening(self):
        """The caller speaks first because the agent said nothing: the text simulator's opening."""
        if self.caller_turn > 0:
            return
        text, is_error = await asyncio.get_running_loop().run_in_executor(None, self.opening_text)
        if is_error:
            self.log("simulator_error", text=text)
            return
        await self.say(text, "opening")

    def opening_text(self):
        s, t = self.session, self.transcript
        with s.lock:
            try:
                reply, meta = s.user.reply("", t["events"])
            except Exception as e:  # noqa: BLE001 - the text simulator's own error class
                t["simulator_errors"] += 1
                s.persist()
                return str(e), True
            t["user_turns"] += 1
            if "model_served" in meta:
                t["sim_calls"].append(dict(meta, turn=t["user_turns"], ok=True))
            t["events"].append({"seq": len(t["events"]) + 1, "role": "user", "content": reply, "rule": meta.get("rule"),
                                "before_agent": True})
            if STOP in reply:
                t["ended_by"] = STOP
            s.persist()
        return reply, False

    async def reader(self):
        try:
            while not self.ended.is_set():
                kind, data = await self.conn.recv()
                if kind == "binary":
                    if len(data) > MAX_FRAME_BYTES:
                        self.log("protocol_error", error=f"binary message of {len(data)} bytes, over {MAX_FRAME_BYTES}")
                        data = data[:MAX_FRAME_BYTES]
                    self.on_audio(data)
                    continue
                try:
                    msg = json.loads(data)
                except ValueError:
                    self.log("protocol_error", error="text message is not JSON")
                    continue
                t = msg.get("type") if isinstance(msg, dict) else None
                if t == "clear":
                    self.on_clear()
                elif t == "hangup":
                    self.log("hangup", by="agent", reason=str(msg.get("reason") or "")[:200])
                    self.agent_hung_up = True
                elif t in ("transcript", "mark", "event"):
                    rec = {k: msg[k] for k in ("role", "text", "turn", "name", "final") if k in msg}
                    if isinstance(rec.get("text"), str):
                        rec["text"] = rec["text"][:2000]
                    self.log("agent_reported", kind=t, **rec)
                else:
                    self.log("protocol_error", error=f"unknown message type {t!r}")
        except wsproto.Closed as e:
            self.log("ws_closed", code=e.code, reason=e.reason)
            self.finish("agent_hangup" if self.agent_hung_up else "agent_disconnected")
        except (asyncio.IncompleteReadError, ConnectionError, OSError):
            self.log("ws_closed", code=1006, reason="connection lost")
            self.finish("agent_hangup" if self.agent_hung_up else "agent_disconnected")

    async def run(self):
        settings = {k: v for k, v in self.s.items() if k != "probe"}
        self.log("call_start", protocol=PROTOCOL, wall_ms=int(self.wall0 * 1000), task=self.cfg.get("id", ""),
                 mode=self.transcript.get("mode"), settings=settings, probe=self.probe if self.probe.get("id") else None,
                 stt_note=self.engines.whisper_error or None)
        await self.conn.send_text(json.dumps({"type": "start", "protocol": PROTOCOL, "call_id": self.cfg.get("id", "call"),
                                              "format": {"encoding": "pcm_s16le", "sample_rate": tonecode.SAMPLE_RATE,
                                                         "channels": 1, "frame_ms": FRAME_MS}}))
        reader = asyncio.get_running_loop().create_task(self.reader())
        ticker = asyncio.get_running_loop().create_task(self.ticker())
        await self.ended.wait()
        if self.ended_by not in ("agent_disconnected", "agent_hangup"):
            try:
                await self.conn.send_text(json.dumps({"type": "hangup", "reason": self.ended_by}))
            except (wsproto.Closed, ConnectionError, OSError):
                pass
            self.log("hangup", by="caller", reason=self.ended_by)
        for t in (ticker, self.speak_task, self.pending_reply):
            if t and not t.done():
                t.cancel()
        try:
            await asyncio.wait_for(self.conn.close(1000, "call over"), 2)
        except (asyncio.TimeoutError, OSError, ConnectionError):
            pass
        reader.cancel()
        self.close()

    def close(self):
        end = self.now()
        out = {"ended_by": self.ended_by, "duration_ms": round(end, 1), "caller_turns": self.caller_turn,
               "agent_utterances": self.agent_utt, "frames_dropped": self.timeline.dropped}
        if self.s["keep_audio"]:
            for name, track in (("caller.wav", self.caller_track), ("agent.wav", self.agent_track)):
                data = track.wav()
                with open(os.path.join(self.state_dir, name), "wb") as f:
                    f.write(data)
                out[name.replace(".wav", "_audio")] = {"file": name, "sha256": hashlib.sha256(data).hexdigest(),
                                                       "bytes": len(data), "truncated": track.truncated}
        else:
            for name, track in (("caller", self.caller_track), ("agent", self.agent_track)):
                out[name + "_audio"] = {"file": None, "sha256": hashlib.sha256(track.wav()).hexdigest(), "kept": False}
        self.log("call_end", **out)
        self.timeline.close()
        with self.session.lock:
            t = self.transcript
            if t.get("ended_by") is None:
                t["ended_by"] = self.ended_by
            t["voice"].update({"ended_by": self.ended_by, "duration_ms": out["duration_ms"]})
            self.session.persist()


class Service:
    def __init__(self, args, env):
        self.args = args
        self.env = env
        self.cfg = load_config(args.config)
        self.settings = env_settings(env)
        self.engines = Engines(self.settings)
        audio = self.cfg.get("audio") if isinstance(self.cfg.get("audio"), dict) else {}
        self.engines.audio_lines = {str(k).strip(): str(v) for k, v in audio.items()}
        self.simuser = load_simuser()
        self.call = None
        self.done = False
        # Every export exists from the start, so an agent that never calls is
        # graded on the untouched state (a fail), not on a missing export.
        self.session = self.simuser.Session(self.cfg, args.state, env)
        self.session.transcript["voice"] = {"protocol": PROTOCOL, "called": False}
        self.session.persist()
        with open(os.path.join(args.state, "timeline.jsonl"), "w", encoding="utf-8") as f:
            f.write(json.dumps({"ev": "no_call", "t_ms": 0, "note": "the agent has not called"}) + "\n")
        if self.settings["keep_audio"]:
            for name in ("caller.wav", "agent.wav"):
                with open(os.path.join(args.state, name), "wb") as f:
                    f.write(tonecode.wav_bytes(b""))

    async def handle(self, reader, writer):
        try:
            method, path, headers = await wsproto.read_http_head(reader)
        except (asyncio.IncompleteReadError, ValueError, asyncio.LimitOverrunError):
            writer.close()
            return
        route = path.split("?", 1)[0]
        if route == "/healthz":
            ok = self.engines.ready.is_set()
            body = json.dumps({"ok": ok, "server": "heimdall-voicecaller", "tts": self.settings["tts"], "stt": self.settings["stt"]}).encode()
            writer.write(b"HTTP/1.1 %d OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n" % (200 if ok else 503, len(body)) + body)
            await writer.drain()
            writer.close()
            return
        if route.startswith("/kit/") and route[5:] in KIT_FILES:
            # The protocol's client helpers, so a control or an agent needs nothing installed.
            with open(os.path.join(HERE, route[5:]), "rb") as f:
                body = f.read()
            writer.write(b"HTTP/1.1 200 OK\r\nContent-Type: text/x-python\r\nContent-Length: %d\r\nConnection: close\r\n\r\n" % len(body) + body)
            await writer.drain()
            writer.close()
            return
        if route != "/call":
            writer.write(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
            await writer.drain()
            writer.close()
            return
        if self.call is not None or self.done:
            code = b"409 Conflict" if not self.done else b"410 Gone"
            writer.write(b"HTTP/1.1 " + code + b"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
            await writer.drain()
            writer.close()
            return
        if not self.engines.ready.is_set():
            writer.write(b"HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
            await writer.drain()
            writer.close()
            return
        if not await wsproto.server_handshake(reader, writer, headers):
            writer.close()
            return
        conn = wsproto.AsyncConn(reader, writer)
        self.call = Call(conn, self.cfg, self.settings, self.engines, self.args.state, self.session)
        try:
            await self.call.run()
        finally:
            self.done = True

    def warm(self):
        """Load the engines, then synthesize the script's known lines so the caller answers without a TTS pause."""
        self.engines.load()
        script = self.cfg.get("script") if isinstance(self.cfg.get("script"), dict) else {}
        lines = [script.get("opening"), script.get("done"), script.get("default"), REPROMPT, self.settings["probe"].get("line")]
        lines += [r.get("say") for r in script.get("replies") or [] if isinstance(r, dict)]
        for line in lines:
            if isinstance(line, str) and line.strip():
                try:
                    self.engines.speak(line.replace(STOP, "").strip())
                except Exception as e:  # noqa: BLE001 - warming is best effort; say() reports a real failure
                    sys.stderr.write(f"voicecaller: warming {line[:40]!r}: {e}\n")

    async def main(self):
        threading.Thread(target=self.warm, daemon=True).start()
        srv = await asyncio.start_server(self.handle, "0.0.0.0", self.args.port)
        sys.stderr.write(f"voicecaller: {PROTOCOL} on :{self.args.port}/call (tts {self.settings['tts']}, stt {self.settings['stt']})\n")
        async with srv:
            await srv.serve_forever()


def load_config(path):
    with open(path, encoding="utf-8") as f:
        cfg = json.load(f)
    if not isinstance(cfg, dict):
        raise SystemExit(f"voicecaller: {path} is not a JSON object")
    return cfg


def cmd_ready(a):
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{a.port}/healthz", timeout=2) as r:
            return 0 if r.status == 200 else 1
    except OSError:
        return 1


def main(argv=None):
    p = argparse.ArgumentParser(prog="voicecaller")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--config", default="/sim/user.json")
    s.add_argument("--state", default="/state")
    s.add_argument("--port", type=int, default=int(os.environ.get("PORT", "8000")))
    r = sub.add_parser("ready")
    r.add_argument("--port", type=int, default=int(os.environ.get("PORT", "8000")))
    a = p.parse_args(argv)
    if a.cmd == "ready":
        return cmd_ready(a)
    os.makedirs(a.state, exist_ok=True)
    env = dict(os.environ)
    svc = Service(a, env)
    os.environ.pop("BENCH_SVC_GATEWAY_KEY", None)
    try:
        asyncio.run(svc.main())
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
