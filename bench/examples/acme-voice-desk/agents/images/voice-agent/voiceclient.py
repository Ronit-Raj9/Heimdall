"""Agent-side helpers for the heimdall-voice/1 protocol, standard library only.

Part of Heimdall (bench/voice), docs/voice-protocol.md. A voice agent, a
reference agent or an admission control dials the caller with VoiceCall and
gets: the caller's speech one utterance at a time (energy based end of
speech, as a phone agent would detect it), sending audio at real-time pace
or in one burst, barge-in detection while it talks (it stops and sends
clear), agent-reported transcripts, and hangup. The caller serves this file
at http://<caller>:8000/kit/voiceclient.py (with tonecode.py and
wsproto.py), so nothing has to be installed in the agent's image:

    import urllib.request, sys, os, tempfile
    d = tempfile.mkdtemp()
    for f in ("voiceclient.py", "tonecode.py", "wsproto.py"):
        open(os.path.join(d, f), "wb").write(urllib.request.urlopen(base + "/kit/" + f).read())
    sys.path.insert(0, d)
    import voiceclient
"""

import json
import os
import threading
import time

import tonecode
import wsproto

DEFAULT_URL = "ws://customer:8000/call"
VOICED_RMS = 250.0


def caller_url():
    """BENCH_VOICE_URL, else the default caller address on the run's service network."""
    return os.environ.get("BENCH_VOICE_URL") or DEFAULT_URL


class Hangup(Exception):
    """The caller ended the call."""


class VoiceCall:
    def __init__(self, url=None, timeout=20):
        url = url or caller_url()
        deadline = time.monotonic() + timeout
        last = None
        while True:
            try:
                self.conn = wsproto.connect(url, timeout=5)
                break
            except (OSError, ConnectionError) as e:
                last = e
                if time.monotonic() > deadline:
                    raise ConnectionError(f"cannot reach the caller at {url}: {last}") from None
                time.sleep(0.25)
        self.lock = threading.Condition()
        self.frames = []           # (arrival monotonic seconds, pcm)
        self.ended = False
        self.end_reason = ""
        self.start = None
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()
        with self.lock:
            self.lock.wait_for(lambda: self.start is not None or self.ended, timeout=10)
        if self.start is None:
            raise ConnectionError("the caller sent no start message")

    def _read(self):
        try:
            while True:
                msg = self.conn.recv(timeout=None)
                if msg is None:
                    continue
                kind, data = msg
                with self.lock:
                    if kind == "binary":
                        self.frames.append((time.monotonic(), data))
                    else:
                        try:
                            ev = json.loads(data)
                        except ValueError:
                            ev = {}
                        if ev.get("type") == "start":
                            self.start = ev
                        elif ev.get("type") == "hangup":
                            self.ended = True
                            self.end_reason = ev.get("reason") or "hangup"
                    self.lock.notify_all()
        except (wsproto.Closed, OSError, ConnectionError):
            with self.lock:
                self.ended = True
                self.end_reason = self.end_reason or "closed"
                self.lock.notify_all()

    # -- listening --

    def drain(self):
        """Discard caller audio received so far."""
        with self.lock:
            self.frames.clear()

    def caller_speaking(self, window_ms=60):
        """True when the caller's most recent audio is speech (for barge-in)."""
        with self.lock:
            if not self.frames:
                return False
            t, f = self.frames[-1]
        return time.monotonic() - t < 0.2 and tonecode.rms(f) > VOICED_RMS

    def listen(self, end_silence_ms=700, max_wait_s=30.0, max_utterance_s=60.0):
        """The caller's next utterance as pcm, or None when the caller said nothing in max_wait_s.

        Raises Hangup when the call ended before an utterance completed.
        """
        start_wait = time.monotonic()
        speech = bytearray()
        speaking = False
        last_voiced = 0.0
        idx = 0
        while True:
            with self.lock:
                while idx >= len(self.frames) and not self.ended:
                    if not self.lock.wait(timeout=0.05):
                        break
                new = self.frames[idx:]
                idx = len(self.frames)
                ended = self.ended
            now = time.monotonic()
            for t, f in new:
                voiced = tonecode.rms(f) > VOICED_RMS
                if voiced:
                    speaking = True
                    last_voiced = t
                if speaking:
                    speech += f
            if speaking and now - last_voiced >= end_silence_ms / 1000.0:
                with self.lock:
                    del self.frames[:idx]
                return bytes(speech)
            if speaking and len(speech) > max_utterance_s * 32000:
                with self.lock:
                    del self.frames[:idx]
                return bytes(speech)
            if ended:
                with self.lock:
                    del self.frames[:idx]
                if speaking and speech:
                    return bytes(speech)
                raise Hangup(self.end_reason)
            if not speaking and now - start_wait > max_wait_s:
                return None

    # -- speaking --

    def send_audio(self, pcm, realtime=True, barge_in=False, barge_in_ms=200):
        """Send pcm in 20 ms frames; True when the caller barged in and the rest was dropped.

        realtime paces frames at their play rate; otherwise they go in one
        burst (the caller still plays them in real time). With barge_in the
        agent stops as soon as the caller has been speaking barge_in_ms, and
        sends clear so queued audio is dropped too.
        """
        frames = tonecode.frames(pcm)
        t0 = time.monotonic()
        voiced_since = None
        with self.lock:
            mark = len(self.frames)
        for i, f in enumerate(frames):
            if self.ended:
                raise Hangup(self.end_reason)
            if realtime:
                d = t0 + i * tonecode.FRAME_MS / 1000.0 - time.monotonic()
                if d > 0:
                    time.sleep(d)
            if barge_in and self._caller_voiced_since(mark):
                voiced_since = voiced_since or time.monotonic()
                if time.monotonic() - voiced_since >= barge_in_ms / 1000.0:
                    self.clear()
                    return True
            self.conn.send_binary(f)
        if barge_in and not realtime:
            # A burst is queued at the caller: keep watching until it has played.
            end = t0 + len(frames) * tonecode.FRAME_MS / 1000.0
            while time.monotonic() < end:
                if self._caller_voiced_since(mark):
                    voiced_since = voiced_since or time.monotonic()
                    if time.monotonic() - voiced_since >= barge_in_ms / 1000.0:
                        self.clear()
                        return True
                time.sleep(0.02)
        return False

    def _caller_voiced_since(self, mark):
        with self.lock:
            recent = self.frames[mark:][-3:]
        return any(tonecode.rms(f) > VOICED_RMS for _, f in recent)

    def say_tone(self, text, **kw):
        """Speak text as tone code (controls and $0 fakes only: a real caller cannot hear it)."""
        return self.send_audio(tonecode.encode(text, lead_ms=40, tail_ms=60), **kw)

    def clear(self):
        self.conn.send_text(json.dumps({"type": "clear"}))

    def report(self, role, text, turn=None):
        """An agent-reported transcript line (kept apart from the bench's own evidence)."""
        msg = {"type": "transcript", "role": role, "text": text}
        if turn is not None:
            msg["turn"] = turn
        self.conn.send_text(json.dumps(msg))

    def hangup(self, reason="done"):
        try:
            self.conn.send_text(json.dumps({"type": "hangup", "reason": reason}))
            time.sleep(0.2)
        finally:
            self.conn.close()

    def wait_end(self, timeout=30):
        with self.lock:
            self.lock.wait_for(lambda: self.ended, timeout=timeout)
        return self.ended
