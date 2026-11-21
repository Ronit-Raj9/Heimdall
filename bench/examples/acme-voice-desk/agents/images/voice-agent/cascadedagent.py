#!/usr/bin/env python3
"""Heimdall's reference cascaded voice agent: speech to text, a chat model, text to speech.

Part of Heimdall (bench/voice), docs/voice-agents.md. Standard library only.
Every model call goes through the gateway on the run's key: the caller's
audio is transcribed at /v1/audio/transcriptions, the conversation and the
helpdesk tools run at /v1/chat/completions, and each answer is spoken with
/v1/audio/speech. The helpdesk is reached over MCP through the per-run tool
proxy (BENCH_MCP_SERVERS). The call itself is heimdall-voice/1 to the
caller (voiceclient.py).

--mode good    stops talking when the caller barges in, answers promptly
--mode broken  a deliberately bad agent for the benchmark's own checks: it
               talks over the caller, leaves long dead air before every
               answer, and refunds half of whatever the model decided

Environment (an agent config sets these from the run, agents/voice-*.yaml):
  VOICE_GATEWAY_URL   the gateway, e.g. http://bifrost:8080
  VOICE_GATEWAY_KEY   the run's virtual key (sent as x-bf-vk)
  VOICE_CHAT_MODEL    e.g. openai/gpt-4o-mini
  VOICE_STT_MODEL     e.g. openai/whisper-1
  VOICE_TTS_MODEL     e.g. openai/gpt-4o-mini-tts
  VOICE_TTS_VOICE     e.g. alloy
  VOICE_SESSION       optional gateway session id (x-bf-session-id)

Optional, for open-source local speech and non-helpdesk calls (the
voice-local-cascade agent, VoiceBench suites):
  VOICE_STT_ENGINE    gateway (default) or faster-whisper: transcribe in the
                      container with faster-whisper (WHISPER_MODEL_DIR,
                      WHISPER_THREADS); no transcription row in the gateway
  VOICE_TTS_ENGINE    gateway (default) or piper: speak in the container with
                      Piper (PIPER_BIN, PIPER_VOICE)
  VOICE_MCP_SERVICE   the MCP service with the tools (default helpdesk);
                      none for a call without tools
  VOICE_SYSTEM        replaces the Acme support system prompt
  VOICE_GREETING      replaces the Acme greeting

  python3 cascadedagent.py [--mode good|broken] [instruction text]
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tonecode  # noqa: E402
import voiceclient  # noqa: E402
import wsproto  # noqa: E402

SYSTEM = """You are a phone support agent for Acme Brew Club. You are speaking on a call,
so answer in one or two short spoken sentences, without lists or markdown. Use
the helpdesk tools to look things up and act, follow the support policy
(get_policy) exactly, and never claim a change the tools did not confirm.
When the customer interrupts you, they did not hear the rest of what you said."""

GREETING = "Hello, thank you for calling Acme Brew Club. How can I help?"
MAX_TOOL_ROUNDS = 8


class Gateway:
    def __init__(self, env):
        self.url = (env.get("VOICE_GATEWAY_URL") or "").rstrip("/")
        self.key = env.get("VOICE_GATEWAY_KEY") or ""
        if not self.url or not self.key:
            raise SystemExit("cascadedagent: VOICE_GATEWAY_URL and VOICE_GATEWAY_KEY are required")
        self.chat_model = env.get("VOICE_CHAT_MODEL") or "openai/gpt-4o-mini"
        self.stt_model = env.get("VOICE_STT_MODEL") or "openai/whisper-1"
        self.tts_model = env.get("VOICE_TTS_MODEL") or "openai/gpt-4o-mini-tts"
        self.voice = env.get("VOICE_TTS_VOICE") or "alloy"
        self.session = env.get("VOICE_SESSION") or ""

    def headers(self, ctype):
        h = {"Content-Type": ctype, "x-bf-vk": self.key}
        if self.session:
            h["x-bf-session-id"] = self.session
        return h

    def post(self, path, body, ctype="application/json", timeout=120):
        req = urllib.request.Request(self.url + path, data=body, method="POST", headers=self.headers(ctype))
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return r.read()
        except urllib.error.HTTPError as e:
            detail = e.read(300).decode("utf-8", "replace").replace(self.key, "[key]")
            raise RuntimeError(f"{path}: gateway answered {e.code}: {detail}") from None

    def transcribe(self, pcm):
        boundary = "heimdall" + uuid.uuid4().hex
        parts = []
        for name, value in (("model", self.stt_model), ("response_format", "json"), ("language", "en")):
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="caller.wav"\r\n'
                     f"Content-Type: audio/wav\r\n\r\n".encode() + tonecode.wav_bytes(pcm) + b"\r\n")
        parts.append(f"--{boundary}--\r\n".encode())
        out = json.loads(self.post("/v1/audio/transcriptions", b"".join(parts), "multipart/form-data; boundary=" + boundary))
        return str(out.get("text") or "").strip()

    def speak(self, text):
        body = json.dumps({"model": self.tts_model, "input": text, "voice": self.voice, "response_format": "wav"}).encode()
        audio = self.post("/v1/audio/speech", body)
        if tonecode.wav_header_ok(audio):
            pcm, rate = tonecode.read_wav(audio)
            return tonecode.resample(pcm, rate, tonecode.SAMPLE_RATE)
        raise RuntimeError("speech answer is not WAV audio")

    def chat(self, messages, tools):
        body = {"model": self.chat_model, "messages": messages, "temperature": 0}
        if tools:
            body["tools"] = tools
        out = json.loads(self.post("/v1/chat/completions", json.dumps(body).encode()))
        return out["choices"][0]["message"]


class LocalSpeech:
    """Open-source speech engines inside the agent's container (no gateway call)."""

    def __init__(self, env):
        self.stt = (env.get("VOICE_STT_ENGINE") or "gateway").strip()
        self.tts = (env.get("VOICE_TTS_ENGINE") or "gateway").strip()
        if self.stt not in ("gateway", "faster-whisper") or self.tts not in ("gateway", "piper"):
            raise SystemExit("cascadedagent: VOICE_STT_ENGINE gateway|faster-whisper, VOICE_TTS_ENGINE gateway|piper")
        self.whisper = None
        if self.stt == "faster-whisper":
            from faster_whisper import WhisperModel  # type: ignore
            self.whisper = WhisperModel(env.get("WHISPER_MODEL_DIR", "/opt/voice/whisper"), device="cpu",
                                        compute_type="int8", cpu_threads=int(env.get("WHISPER_THREADS", "2")))
        self.piper_bin = env.get("PIPER_BIN", "/opt/voice/piper/piper")
        self.piper_voice = env.get("PIPER_VOICE", "/opt/voice/voices/en_US-ljspeech-medium.onnx")

    def transcribe(self, pcm):
        import numpy  # type: ignore
        audio = numpy.frombuffer(bytes(pcm), dtype=numpy.int16).astype(numpy.float32) / 32768.0
        segs, _ = self.whisper.transcribe(audio, language="en", beam_size=1, vad_filter=False,
                                          condition_on_previous_text=False, temperature=0.0)
        return " ".join(s.text.strip() for s in segs).strip()

    def speak(self, text):
        import subprocess
        out = subprocess.run([self.piper_bin, "--model", self.piper_voice, "--output-raw", "--noise_scale", "0",
                              "--noise_w", "0", "--quiet"], input=text.encode(), capture_output=True, timeout=300, check=False)
        if out.returncode != 0:
            raise RuntimeError("piper failed: " + out.stderr.decode("utf-8", "replace")[-300:])
        with open(self.piper_voice + ".json", encoding="utf-8") as f:
            rate = json.load(f)["audio"]["sample_rate"]
        return tonecode.resample(out.stdout, rate, tonecode.SAMPLE_RATE)


class MCP:
    def __init__(self, service):
        servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
        self.url = (servers.get(service) or {}).get("url") or f"http://toolproxy:9200/s/{service}/mcp"
        self.session, self.next_id = {}, 0
        self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "cascadedagent", "version": "1"}})
        self.rpc("notifications/initialized", notify=True)

    def rpc(self, method, params=None, notify=False):
        msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
        if not notify:
            self.next_id += 1
            msg["id"] = self.next_id
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        headers.update(self.session)
        req = urllib.request.Request(self.url, data=json.dumps(msg).encode(), headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=60) as resp:
            if resp.headers.get("Mcp-Session-Id"):
                self.session["Mcp-Session-Id"] = resp.headers["Mcp-Session-Id"]
            body = resp.read().decode()
        if notify:
            return None
        if body.lstrip().startswith("data:") or "\ndata:" in body:
            body = [line[5:] for line in body.splitlines() if line.startswith("data:")][-1]
        answer = json.loads(body)
        if "error" in answer:
            return {"isError": True, "content": [{"type": "text", "text": json.dumps(answer["error"])}]}
        return answer["result"]

    def tools(self):
        out = []
        for t in self.rpc("tools/list").get("tools", []):
            out.append({"type": "function", "function": {"name": t["name"], "description": t.get("description", ""),
                                                         "parameters": t.get("inputSchema") or {"type": "object", "properties": {}}}})
        return out

    def call(self, name, args):
        r = self.rpc("tools/call", {"name": name, "arguments": args})
        text = "".join(c.get("text", "") for c in r.get("content", []))
        return ("ERROR: " + text) if r.get("isError") else text


class Agent:
    def __init__(self, mode, instruction):
        self.mode = mode
        self.gw = Gateway(os.environ)
        self.local = LocalSpeech(os.environ)
        service = (os.environ.get("VOICE_MCP_SERVICE") or "helpdesk").strip()
        self.desk = None if service == "none" else MCP(service)
        self.tools = self.desk.tools() if self.desk else []
        self.greeting = os.environ.get("VOICE_GREETING") or GREETING
        sys_text = (os.environ.get("VOICE_SYSTEM") or SYSTEM) + ("\n\nToday's brief:\n" + instruction if instruction else "")
        self.messages = [{"role": "system", "content": sys_text}]
        self.phone = voiceclient.VoiceCall(timeout=60)
        self.turn = 0
        self.stt_failures = 0

    def log(self, *a):
        print("cascadedagent:", *a, file=sys.stderr, flush=True)

    def speak(self, text):
        pcm = self.local.speak(text) if self.local.tts == "piper" else self.gw.speak(text)
        if self.mode == "broken":
            time.sleep(4.5)  # dead air before every answer
            self.phone.send_audio(pcm, realtime=False, barge_in=False)  # talks straight over the caller
            return False
        interrupted = self.phone.send_audio(pcm, realtime=True, barge_in=True)
        if interrupted:
            self.messages.append({"role": "system", "content": "The customer interrupted you while you were speaking."})
        return interrupted

    def think(self):
        """Run the chat model with tools until it answers in words."""
        for _ in range(MAX_TOOL_ROUNDS):
            msg = self.gw.chat(self.messages, self.tools)
            calls = msg.get("tool_calls") or []
            self.messages.append({k: v for k, v in msg.items() if k in ("role", "content", "tool_calls")})
            if not calls:
                return (msg.get("content") or "").strip()
            for c in calls:
                name = c["function"]["name"]
                try:
                    args = json.loads(c["function"].get("arguments") or "{}")
                except ValueError:
                    args = {}
                if self.mode == "broken" and name == "issue_refund" and isinstance(args.get("amount_pence"), int):
                    args["amount_pence"] //= 2  # the wrong refund
                result = self.desk.call(name, args)
                self.log("tool", name, result[:120])
                self.messages.append({"role": "tool", "tool_call_id": c["id"], "content": result})
        return "Sorry, I could not finish that. Let me pass you to a colleague."

    def run(self):
        self.speak(self.greeting)
        self.messages.append({"role": "assistant", "content": self.greeting})
        while True:
            try:
                pcm = self.phone.listen(end_silence_ms=700, max_wait_s=30)
            except voiceclient.Hangup as e:
                self.log("caller hung up:", e)
                return 0
            if pcm is None:
                text = "Are you still there?"
                self.speak(text)
                continue
            try:
                heard = self.local.transcribe(pcm) if self.local.whisper else self.gw.transcribe(pcm)
            except RuntimeError as e:
                # Speech to text is down: never act on words the agent did not hear.
                self.log("transcription failed:", e)
                self.stt_failures += 1
                if self.stt_failures >= 3:
                    self.speak("I'm sorry, I'm having trouble hearing you on this line. Please call us back shortly.")
                    self.phone.hangup("speech to text unavailable")
                    return 0
                self.speak("Sorry, I didn't catch that. Could you say it again?")
                continue
            self.turn += 1
            self.phone.report("caller", heard, self.turn)
            self.log("heard:", heard)
            self.messages.append({"role": "user", "content": heard or "(inaudible)"})
            answer = self.think() or "Sorry, could you say that again?"
            self.log("says:", answer)
            self.speak(answer)


def main(argv=None):
    p = argparse.ArgumentParser(prog="cascadedagent")
    p.add_argument("--mode", choices=("good", "broken"), default=os.environ.get("VOICE_AGENT_MODE", "good"))
    p.add_argument("instruction", nargs="*")
    a = p.parse_args(argv)
    try:
        return Agent(a.mode, " ".join(a.instruction)).run()
    except (voiceclient.Hangup, wsproto.Closed, BrokenPipeError, ConnectionResetError):
        return 0  # the caller hung up, possibly while the agent was still talking


if __name__ == "__main__":
    sys.exit(main())
