#!/usr/bin/env python3
"""Bridge between the heimdall-voice/1 caller and an OpenAI Realtime event endpoint.

Part of Heimdall (bench/voice), docs/voice-protocol.md. Standard library only.
It is both the adapter for agents that speak the OpenAI Realtime event
protocol and the reference speech-to-speech agent: it dials the caller,
opens a Realtime WebSocket (by default the gateway's /v1/realtime, which
Bifrost v2.5.1 relays to the provider), and translates:

  caller audio frames (16 kHz)        -> input_audio_buffer.append (24 kHz, base64)
  response.output_audio.delta         -> audio frames to the caller (16 kHz)
  input_audio_buffer.speech_started   -> clear to the caller and response.cancel (barge-in)
  ...input_audio_transcription.completed -> an agent-reported transcript line
  response.function_call_arguments.done  -> the helpdesk tool over MCP, then
                                           conversation.item.create (function_call_output)
                                           and response.create
  caller hangup                        -> close both sides

A team whose own agent serves the Realtime event protocol points
VOICE_REALTIME_URL at it; nothing else changes.

Environment:
  VOICE_REALTIME_URL   ws:// URL of the Realtime endpoint; default
                       $VOICE_GATEWAY_URL/v1/realtime?model=$VOICE_REALTIME_MODEL
  VOICE_GATEWAY_URL, VOICE_GATEWAY_KEY (sent as x-bf-vk), VOICE_REALTIME_MODEL
  (e.g. openai/gpt-realtime), VOICE_SESSION

  python3 realtimebridge.py [instruction text]
"""

import base64
import json
import os
import sys
import threading
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tonecode  # noqa: E402
import voiceclient  # noqa: E402
import wsproto  # noqa: E402
from cascadedagent import MCP, SYSTEM  # noqa: E402

RT_RATE = 24000
APPEND_MS = 100
LINE_GAP_MS = 300  # caller silence after which the line is filled with silence


class Bridge:
    def __init__(self, instruction):
        env = os.environ
        base = (env.get("VOICE_GATEWAY_URL") or "").rstrip("/")
        model = env.get("VOICE_REALTIME_MODEL") or "openai/gpt-realtime"
        self.url = env.get("VOICE_REALTIME_URL") or (base.replace("http://", "ws://", 1) + "/v1/realtime?model=" + model)
        headers = {}
        if env.get("VOICE_GATEWAY_KEY"):
            headers["x-bf-vk"] = env["VOICE_GATEWAY_KEY"]
        if env.get("VOICE_SESSION"):
            headers["x-bf-session-id"] = env["VOICE_SESSION"]
        self.desk = MCP("helpdesk")
        self.tools = [{"type": "function", "name": t["function"]["name"], "description": t["function"]["description"],
                       "parameters": t["function"]["parameters"]} for t in self.desk.tools()]
        self.rt = wsproto.connect(self.url, headers=headers, timeout=20)
        self.phone = voiceclient.VoiceCall(timeout=60)
        self.instructions = SYSTEM + ("\n\nToday's brief:\n" + instruction if instruction else "")
        self.done = threading.Event()
        self.speaking = False
        self.turn = 0

    def log(self, *a):
        print("realtimebridge:", *a, file=sys.stderr, flush=True)

    def send(self, ev):
        self.rt.send_text(json.dumps(ev))

    def start(self):
        self.send({"type": "session.update", "session": {
            "type": "realtime", "instructions": self.instructions, "tools": self.tools, "tool_choice": "auto",
            "audio": {"input": {"format": {"type": "audio/pcm", "rate": RT_RATE}, "turn_detection": {"type": "server_vad"},
                                "transcription": {"model": "whisper-1"}},
                      "output": {"format": {"type": "audio/pcm", "rate": RT_RATE}}}}})
        # The agent speaks first: ask for a greeting.
        self.send({"type": "response.create", "response": {"instructions": "Greet the caller and ask how you can help."}})

    def pump_caller(self):
        """Caller audio to the Realtime input buffer, in 100 ms appends.

        A phone line carries audio all the time; the caller sends frames only
        while it speaks. Once the caller has sent nothing for 300 ms the gap
        is filled with silence, so the server's voice activity detection sees
        the end of each utterance; nothing is ever inserted inside one.
        """
        idx = 0
        buf = bytearray()
        last_audio = time.monotonic()
        while not self.done.is_set():
            with self.phone.lock:
                new = self.phone.frames[idx:]
                idx = len(self.phone.frames)
                ended = self.phone.ended
            now = time.monotonic()
            for _, f in new:
                buf += f
            if new:
                last_audio = now
            elif now - last_audio >= LINE_GAP_MS / 1000.0:
                gap_ms = int((now - last_audio) * 1000) // 20 * 20
                buf += bytes(gap_ms * 32)
                last_audio += gap_ms / 1000.0
            if len(buf) >= APPEND_MS * 32:
                pcm = tonecode.resample(bytes(buf), tonecode.SAMPLE_RATE, RT_RATE)
                buf.clear()
                try:
                    self.send({"type": "input_audio_buffer.append", "audio": base64.b64encode(pcm).decode()})
                except (OSError, wsproto.Closed):
                    self.done.set()
                    return
            if ended:
                self.done.set()
                return
            time.sleep(0.02)

    def handle(self, ev):
        t = ev.get("type", "")
        if t in ("response.output_audio.delta", "response.audio.delta"):
            pcm = tonecode.resample(base64.b64decode(ev.get("delta") or ""), RT_RATE, tonecode.SAMPLE_RATE)
            self.speaking = True
            self.phone.conn.send_binary(pcm)
        elif t == "input_audio_buffer.speech_started":
            if self.speaking:
                self.phone.clear()
                self.send({"type": "response.cancel"})
                self.speaking = False
        elif t == "conversation.item.input_audio_transcription.completed":
            self.turn += 1
            self.phone.report("caller", ev.get("transcript") or "", self.turn)
        elif t in ("response.output_audio_transcript.done", "response.audio_transcript.done"):
            self.log("said:", ev.get("transcript"))
        elif t == "response.function_call_arguments.done":
            try:
                args = json.loads(ev.get("arguments") or "{}")
            except ValueError:
                args = {}
            result = self.desk.call(ev.get("name") or "", args)
            self.log("tool", ev.get("name"), result[:120])
            self.send({"type": "conversation.item.create", "item": {"type": "function_call_output", "call_id": ev.get("call_id"), "output": result}})
            self.send({"type": "response.create"})
        elif t == "response.done":
            self.speaking = False
        elif t == "error":
            self.log("realtime error:", json.dumps(ev.get("error"))[:300])

    def run(self):
        self.start()
        threading.Thread(target=self.pump_caller, daemon=True).start()
        try:
            while not self.done.is_set():
                msg = self.rt.recv(timeout=0.5)
                if msg is None:
                    continue
                kind, data = msg
                if kind == "text":
                    try:
                        self.handle(json.loads(data))
                    except (OSError, wsproto.Closed):
                        break
        except wsproto.Closed as e:
            self.log("realtime closed:", e)
        finally:
            self.done.set()
            self.rt.close()
            if not self.phone.ended:
                self.phone.hangup("realtime session ended")
        return 0


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    return Bridge(" ".join(argv)).run()


if __name__ == "__main__":
    sys.exit(main())
