#!/usr/bin/env python3
"""Scripted OpenAI-compatible audio and chat endpoints for $0 voice runs.

Part of Heimdall (bench/voice). Standard library only. A gateway's openai
provider is pointed at this server (its base URL), so a voice agent can make
every call a cascaded agent makes, through the real gateway, and nothing is
spent:

  POST /v1/audio/speech          the input text as tone-coded audio (tonecode.py),
                                 wav or pcm at 24 kHz like the real API; with
                                 FAKE_TTS=piper and a Piper voice present, real
                                 synthesized speech instead
  POST /v1/audio/transcriptions  multipart upload; tone-coded audio decodes to its
                                 exact text, audio whose sha256 is in the known-audio
                                 table (FAKE_KNOWN_AUDIO) maps to its known text, and
                                 with FAKE_STT=whisper anything else is transcribed by
                                 the local Whisper model; otherwise the text is empty
  POST /v1/chat/completions      replies from a chat script (FAKE_CHAT_SCRIPT or
                                 POST /fake/script), in order: tool calls or text
  GET  /v1/realtime              WebSocket, a minimal OpenAI Realtime event subset
                                 (session.update, input_audio_buffer.append and
                                 commit, server VAD on tone code, response.create
                                 and cancel, output audio deltas, transcripts, done)
  GET  /v1/models                the model ids above
  GET  /fake/log                 every request this process answered (for tests)
  POST /fake/reset               clears the log and rewinds the chat script

Nothing here is a model. It answers what a test scripted; docs/voice-agents.md
says which results it can and cannot stand for.

  python3 fakeopenai.py serve [--port 9300] [--chat-script file.json]
"""

import argparse
import base64
import hashlib
import json
import os
import re
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tonecode  # noqa: E402
import wsproto  # noqa: E402

OPENAI_RATE = 24000
MAX_BODY = 32 << 20
MODELS = ["gpt-4o-mini-tts", "tts-1", "whisper-1", "gpt-4o-mini-transcribe", "gpt-4o-mini", "gpt-realtime", "gpt-realtime-mini"]


class State:
    def __init__(self, script=None, known=None):
        self.lock = threading.Lock()
        self.log = []
        self.script = script or {"replies": []}
        self.pos = 0
        self.known = known or {}
        self.whisper = None
        self.piper = None

    def record(self, entry):
        with self.lock:
            entry["seq"] = len(self.log) + 1
            entry["t"] = time.time()
            self.log.append(entry)

    def reply_for(self, msgs):
        """The scripted reply for one chat request.

        With conversation scripts ({"scripts": [{"match": text, "replies": [...]}]})
        the script is the first whose match text is in the conversation's
        first user message, and the reply is the one at the number of
        assistant messages after that user message: stateless, so parallel
        calls of different conversations never interleave. Without them,
        replies are given in request order.
        """
        scripts = self.script.get("scripts")
        if not scripts:
            return self.next_reply()
        # Asked to say it again (the caller talked over the answer): repeat the
        # last thing said, as a person would, without stepping the script.
        last_user = next((str(m.get("content") or "") for m in reversed(msgs) if m.get("role") == "user"), "")
        if re.search(r"\b(again|repeat)\b", last_user, re.I):
            said = next((m.get("content") for m in reversed(msgs) if m.get("role") == "assistant" and m.get("content")), None)
            if said:
                return {"content": said}, 0
        first_user, after = None, 0
        for m in msgs:
            if m.get("role") == "user" and first_user is None:
                first_user = str(m.get("content") or "")
            elif m.get("role") == "assistant" and first_user is not None:
                after += 1
        for sc in scripts:
            if first_user is not None and str(sc.get("match", "")).lower() in first_user.lower():
                replies = sc.get("replies") or []
                if after < len(replies):
                    return replies[after], after + 1
                return {"content": sc.get("default") or "Is there anything else I can help with?"}, after + 1
        return {"content": self.script.get("default") or "Sorry, could you tell me your order number?"}, 0

    def next_reply(self):
        with self.lock:
            replies = self.script.get("replies") or []
            if self.pos < len(replies):
                r = replies[self.pos]
                self.pos += 1
                return r, self.pos
            self.pos += 1
            return {"content": self.script.get("default") or "I am the scripted model."}, self.pos


def load_json(path, default):
    if not path:
        return default
    with open(path, encoding="utf-8") as f:
        return json.load(f)


# ---- multipart ----

def parse_multipart(body, ctype):
    m = re.search(r'boundary="?([^";]+)"?', ctype or "")
    if not m:
        raise ValueError("multipart body without a boundary")
    boundary = b"--" + m.group(1).encode()
    fields, files = {}, {}
    for part in body.split(boundary)[1:]:
        if part.startswith(b"--"):
            break
        part = part.lstrip(b"\r\n")
        if b"\r\n\r\n" not in part:
            continue
        head, data = part.split(b"\r\n\r\n", 1)
        if data.endswith(b"\r\n"):
            data = data[:-2]
        disp = re.search(rb'name="([^"]*)"', head)
        if not disp:
            continue
        name = disp.group(1).decode()
        fn = re.search(rb'filename="([^"]*)"', head)
        if fn:
            files[name] = (fn.group(1).decode("utf-8", "replace"), data)
        else:
            fields[name] = data.decode("utf-8", "replace")
    return fields, files


# ---- optional local models ----

def load_whisper(state):
    if state.whisper is None:
        try:
            from faster_whisper import WhisperModel  # type: ignore
        except ImportError:
            state.whisper = False
        else:
            path = os.environ.get("WHISPER_MODEL_DIR", "/opt/voice/whisper")
            state.whisper = WhisperModel(path, device="cpu", compute_type="int8", cpu_threads=2)
    return state.whisper or None


def whisper_text(state, pcm16k):
    model = load_whisper(state)
    if model is None:
        return None
    import array
    a = tonecode.to_samples(pcm16k)
    floats = [v / 32768.0 for v in a]
    try:
        import numpy  # type: ignore
        audio = numpy.asarray(floats, dtype=numpy.float32)
    except ImportError:
        return None
    segs, _ = model.transcribe(audio, language="en", beam_size=1, vad_filter=False, condition_on_previous_text=False)
    return " ".join(s.text.strip() for s in segs).strip()


def piper_speech(text):
    """Real speech from the Piper binary when present: (pcm 16 kHz, ok)."""
    import subprocess
    binary = os.environ.get("PIPER_BIN", "/opt/voice/piper/piper")
    voice = os.environ.get("PIPER_VOICE", "/opt/voice/voices/en_US-ljspeech-medium.onnx")
    if not (os.path.exists(binary) and os.path.exists(voice)):
        return None
    out = subprocess.run([binary, "--model", voice, "--output-raw", "--noise_scale", "0", "--noise_w", "0", "--quiet"],
                         input=text.encode(), capture_output=True, timeout=60, check=False)
    if out.returncode != 0:
        return None
    with open(voice + ".json", encoding="utf-8") as f:
        rate = json.load(f)["audio"]["sample_rate"]
    return tonecode.resample(out.stdout, rate, tonecode.SAMPLE_RATE)


# ---- HTTP ----

def make_handler(state):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, fmt, *args):
            sys.stderr.write("fakeopenai: " + (fmt % args) + "\n")

        def send(self, code, body, ctype="application/json", extra=None):
            data = body if isinstance(body, bytes) else json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(data)))
            self.send_header("x-request-id", "req_fake_" + uuid.uuid4().hex[:16])
            for k, v in (extra or {}).items():
                self.send_header(k, v)
            self.end_headers()
            self.wfile.write(data)

        def error(self, code, msg, kind="invalid_request_error"):
            self.send(code, {"error": {"message": msg, "type": kind, "code": None}})

        def body(self):
            n = int(self.headers.get("Content-Length") or 0)
            if n > MAX_BODY:
                raise ValueError("body too large")
            return self.rfile.read(n) if n else b""

        def route(self):
            return self.path.split("?", 1)[0].rstrip("/")

        def do_GET(self):
            path = self.route()
            if path in ("/v1/realtime", "/realtime"):
                return self.realtime()
            if path in ("/v1/models", "/models"):
                return self.send(200, {"object": "list", "data": [{"id": m, "object": "model", "owned_by": "heimdall-fake"} for m in MODELS]})
            if path == "/fake/log":
                with state.lock:
                    return self.send(200, {"requests": list(state.log)})
            if path == "/healthz":
                return self.send(200, {"ok": True})
            return self.error(404, "no route " + path)

        def do_POST(self):
            path = self.route()
            try:
                raw = self.body()
            except ValueError as e:
                return self.error(413, str(e))
            if path == "/fake/reset":
                with state.lock:
                    state.log.clear()
                    state.pos = 0
                return self.send(200, {"ok": True})
            if path == "/fake/script":
                with state.lock:
                    state.script = json.loads(raw or b"{}")
                    state.pos = 0
                return self.send(200, {"ok": True})
            auth = self.headers.get("Authorization", "")
            if path in ("/v1/audio/speech", "/audio/speech"):
                return self.speech(raw, auth)
            if path in ("/v1/audio/transcriptions", "/audio/transcriptions"):
                return self.transcription(raw, auth)
            if path in ("/v1/chat/completions", "/chat/completions"):
                return self.chat(raw, auth)
            return self.error(404, "no route " + path)

        def speech(self, raw, auth):
            try:
                req = json.loads(raw)
            except ValueError:
                return self.error(400, "body is not JSON")
            text = str(req.get("input") or "")
            fmt = req.get("response_format") or "mp3"
            if not text:
                return self.error(400, "input is required")
            if fmt not in ("wav", "pcm"):
                return self.error(400, f"the fake speaks wav and pcm only, not {fmt}")
            kind = "tone_code"
            pcm = None
            if os.environ.get("FAKE_TTS") == "piper":
                pcm = piper_speech(text)
                if pcm is not None:
                    kind = "piper"
            if pcm is None:
                pcm = tonecode.encode(text, lead_ms=40, tail_ms=120)
            out = tonecode.resample(pcm, tonecode.SAMPLE_RATE, OPENAI_RATE)
            state.record({"route": "speech", "model": req.get("model"), "chars": len(text), "input": text,
                          "voice": req.get("voice"), "format": fmt, "audio": kind, "auth_present": bool(auth),
                          "seconds": round(len(pcm) / 32000.0, 3)})
            if fmt == "wav":
                return self.send(200, tonecode.wav_bytes(out, OPENAI_RATE), "audio/wav")
            return self.send(200, out, "audio/pcm")

        def transcription(self, raw, auth):
            try:
                fields, files = parse_multipart(raw, self.headers.get("Content-Type"))
            except ValueError as e:
                return self.error(400, str(e))
            if "file" not in files:
                return self.error(400, "file is required")
            name, data = files["file"]
            try:
                pcm = tonecode.to_16k(data)
            except Exception as e:  # noqa: BLE001 - a bad upload is the client's error
                return self.error(400, f"cannot read audio: {e}")
            seconds = round(len(pcm) / 32000.0, 3)
            sha = hashlib.sha256(pcm).hexdigest()
            text, how = tonecode.decode(pcm), "tone_code"
            if text is None and sha in state.known:
                text, how = state.known[sha], "known_audio"
            if text is None and os.environ.get("FAKE_STT") == "whisper":
                text = whisper_text(state, pcm)
                how = "whisper" if text is not None else "none"
            if text is None:
                text, how = "", "none"
            state.record({"route": "transcription", "model": fields.get("model"), "file": name, "seconds": seconds,
                          "sha256": sha, "decoded_by": how, "text": text, "auth_present": bool(auth)})
            fmt = fields.get("response_format") or "json"
            if fmt == "text":
                return self.send(200, text.encode(), "text/plain")
            body = {"text": text, "usage": {"type": "duration", "seconds": max(1, int(seconds + 0.999))}}
            if fmt == "verbose_json":
                body.update({"task": "transcribe", "language": "english", "duration": seconds, "segments": []})
            return self.send(200, body)

        def chat(self, raw, auth):
            try:
                req = json.loads(raw)
            except ValueError:
                return self.error(400, "body is not JSON")
            msgs = req.get("messages") or []
            reply, n = state.reply_for(msgs)
            prompt_chars = sum(len(json.dumps(m)) for m in msgs)
            msg = {"role": "assistant", "content": reply.get("content")}
            finish = "stop"
            if reply.get("tool_calls"):
                msg["content"] = None
                msg["tool_calls"] = [{"id": f"call_fake_{n}_{i}_{uuid.uuid4().hex[:8]}", "type": "function",
                                      "function": {"name": c["name"], "arguments": json.dumps(c.get("arguments") or {})}}
                                     for i, c in enumerate(reply["tool_calls"])]
                finish = "tool_calls"
            usage = {"prompt_tokens": max(1, prompt_chars // 4), "completion_tokens": max(1, len(json.dumps(msg)) // 4)}
            usage["total_tokens"] = usage["prompt_tokens"] + usage["completion_tokens"]
            state.record({"route": "chat", "model": req.get("model"), "reply": n, "tool_calls": [c["name"] for c in reply.get("tool_calls") or []],
                          "last_user": next((m.get("content") for m in reversed(msgs) if m.get("role") == "user"), None),
                          "auth_present": bool(auth)})
            out = {"id": f"chatcmpl-fake{n}-{uuid.uuid4().hex[:12]}", "object": "chat.completion", "created": int(time.time()),
                   "model": req.get("model") or "gpt-4o-mini",
                   "choices": [{"index": 0, "message": msg, "finish_reason": finish}], "usage": usage}
            if req.get("stream"):
                chunk = {"id": out["id"], "object": "chat.completion.chunk", "created": out["created"], "model": out["model"],
                         "choices": [{"index": 0, "delta": msg, "finish_reason": finish}]}
                last = dict(chunk, choices=[], usage=usage)
                data = ("data: " + json.dumps(chunk) + "\n\ndata: " + json.dumps(last) + "\n\ndata: [DONE]\n\n").encode()
                return self.send(200, data, "text/event-stream")
            return self.send(200, out)

        def realtime(self):
            headers = {k.lower(): v for k, v in self.headers.items()}
            conn = wsproto.server_accept(self.connection, headers)
            if conn is None:
                return
            self.close_connection = True
            state.record({"route": "realtime", "model": self.path.split("model=", 1)[-1] if "model=" in self.path else None,
                          "auth_present": bool(headers.get("authorization"))})
            RealtimeSession(state, conn).run()

    return Handler


class RealtimeSession:
    """A minimal OpenAI Realtime server: tone-code VAD, scripted replies as tone-coded audio."""

    def __init__(self, state, conn):
        self.state, self.conn = state, conn
        self.buf = bytearray()
        self.n = 0
        self.speaking = False
        self.silent_ms = 0
        self.cancel = threading.Event()
        self.responder = None
        self.history = []  # the session's first heard words and one entry per response

    def send(self, ev):
        self.n += 1
        ev.setdefault("event_id", f"event_fake_{self.n}")
        self.conn.send_text(json.dumps(ev))

    def run(self):
        sid = "sess_fake_" + uuid.uuid4().hex[:10]
        self.send({"type": "session.created", "session": {"id": sid, "object": "realtime.session", "model": "gpt-realtime",
                                                          "output_modalities": ["audio"], "audio": {"input": {"format": {"type": "audio/pcm", "rate": OPENAI_RATE}}}}})
        try:
            while True:
                msg = self.conn.recv(timeout=300)
                if msg is None:
                    break
                kind, data = msg
                if kind != "text":
                    continue
                try:
                    ev = json.loads(data)
                except ValueError:
                    self.send({"type": "error", "error": {"type": "invalid_request_error", "message": "event is not JSON"}})
                    continue
                self.handle(ev)
        except (wsproto.Closed, OSError):
            pass
        finally:
            self.cancel.set()

    def handle(self, ev):
        t = ev.get("type")
        if t == "session.update":
            self.send({"type": "session.updated", "session": ev.get("session") or {}})
        elif t == "input_audio_buffer.append":
            chunk = tonecode.resample(base64.b64decode(ev.get("audio") or ""), OPENAI_RATE, tonecode.SAMPLE_RATE)
            self.vad(chunk)
        elif t == "input_audio_buffer.commit":
            self.commit()
        elif t == "input_audio_buffer.clear":
            self.buf.clear()
            self.send({"type": "input_audio_buffer.cleared"})
        elif t == "response.create":
            self.respond("")
        elif t == "response.cancel":
            self.cancel.set()
        elif t == "conversation.item.create":
            self.send({"type": "conversation.item.created", "item": ev.get("item") or {}})
        else:
            self.send({"type": "error", "error": {"type": "invalid_request_error", "message": f"the fake does not handle {t}"}})

    def vad(self, chunk):
        self.buf += chunk
        loud = tonecode.rms(chunk) > 500
        if loud and not self.speaking:
            self.speaking = True
            self.silent_ms = 0
            self.cancel.set()
            self.send({"type": "input_audio_buffer.speech_started", "audio_start_ms": 0, "item_id": "item_in"})
        elif self.speaking:
            self.silent_ms = 0 if loud else self.silent_ms + len(chunk) * 1000 // 32000
            if self.silent_ms >= 500:
                self.speaking = False
                self.send({"type": "input_audio_buffer.speech_stopped", "audio_end_ms": 0, "item_id": "item_in"})
                self.commit()

    def commit(self):
        pcm, self.buf = bytes(self.buf), bytearray()
        text = tonecode.decode(pcm)
        if text is None and os.environ.get("FAKE_STT") == "whisper":
            text = whisper_text(self.state, pcm)  # real speech from the caller, as the transcription route does
        text = text or ""
        self.send({"type": "input_audio_buffer.committed", "item_id": "item_in"})
        self.send({"type": "conversation.item.input_audio_transcription.completed", "item_id": "item_in", "transcript": text})
        self.state.record({"route": "realtime.turn", "heard": text, "seconds": round(len(pcm) / 32000.0, 3)})
        self.respond(text)

    def respond(self, heard):
        # A conversation script is picked by the session's first heard words and
        # stepped once per response, like a chat conversation's assistant turns.
        if heard and not any(m["role"] == "user" for m in self.history):
            self.history.append({"role": "user", "content": heard})
        if self.state.script.get("scripts"):
            reply, n = self.state.reply_for(self.history)
            self.history.append({"role": "assistant", "content": ""})
        else:
            reply, n = self.state.next_reply()
        text = reply.get("content") or ""
        self.cancel = threading.Event()
        cancel = self.cancel
        rid = f"resp_fake_{n}_{uuid.uuid4().hex[:8]}"

        def run():
            self.send({"type": "response.created", "response": {"id": rid, "status": "in_progress"}})
            if reply.get("tool_calls"):
                for i, c in enumerate(reply["tool_calls"]):
                    cid = f"call_fake_{n}_{i}"
                    args = json.dumps(c.get("arguments") or {})
                    item = {"id": f"item_fc_{n}_{i}", "type": "function_call", "call_id": cid, "name": c["name"], "arguments": args}
                    self.send({"type": "response.output_item.done", "response_id": rid, "item": item})
                    self.send({"type": "response.function_call_arguments.done", "response_id": rid, "call_id": cid,
                               "name": c["name"], "arguments": args})
                self.send({"type": "response.done", "response": {"id": rid, "status": "completed",
                                                                 "usage": {"total_tokens": 12, "input_tokens": 10, "output_tokens": 2}}})
                self.state.record({"route": "realtime.response", "reply": n, "status": "tool_calls",
                                   "tool_calls": [c["name"] for c in reply["tool_calls"]]})
                return
            pcm =tonecode.resample(tonecode.encode(text, lead_ms=40, tail_ms=120), tonecode.SAMPLE_RATE, OPENAI_RATE)
            step = OPENAI_RATE * 2 // 10  # 100 ms of audio per delta
            status = "completed"
            for i in range(0, len(pcm), step):
                if cancel.is_set():
                    status = "cancelled"
                    break
                self.send({"type": "response.output_audio.delta", "response_id": rid, "item_id": "item_out",
                           "delta": base64.b64encode(pcm[i:i + step]).decode()})
                time.sleep(0.02)
            if status == "completed":
                self.send({"type": "response.output_audio.done", "response_id": rid, "item_id": "item_out"})
                self.send({"type": "response.output_audio_transcript.done", "response_id": rid, "item_id": "item_out", "transcript": text})
            usage = {"total_tokens": 10 + len(text) // 4, "input_tokens": 10, "output_tokens": len(text) // 4}
            self.send({"type": "response.done", "response": {"id": rid, "status": status, "usage": usage}})
            self.state.record({"route": "realtime.response", "reply": n, "status": status, "text": text})

        self.responder = threading.Thread(target=run, daemon=True)
        self.responder.start()


def main(argv=None):
    p = argparse.ArgumentParser(prog="fakeopenai")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("serve")
    s.add_argument("--port", type=int, default=int(os.environ.get("PORT", "9300")))
    s.add_argument("--host", default="0.0.0.0")
    s.add_argument("--chat-script", default=os.environ.get("FAKE_CHAT_SCRIPT", ""))
    s.add_argument("--known-audio", default=os.environ.get("FAKE_KNOWN_AUDIO", ""))
    a = p.parse_args(argv)
    state = State(load_json(a.chat_script, {"replies": []}), load_json(a.known_audio, {}))
    srv = ThreadingHTTPServer((a.host, a.port), make_handler(state))
    srv.daemon_threads = True
    sys.stderr.write(f"fakeopenai: on :{a.port}\n")
    srv.serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main())
