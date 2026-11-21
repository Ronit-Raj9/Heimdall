#!/bin/sh
# __KIND__ control of an imported VoiceBench item: answers the call
# (heimdall-voice/1, docs/voice-protocol.md), greets, listens to the recorded
# question and speaks __WHAT__ in tone code (the bench's deterministic test
# codec, which the caller reads exactly), then waits for the caller to hang up.
exec python3 -I - <<'PY'
import os, sys, tempfile, time, urllib.request

ANSWER = __ANSWER__
CALLER = os.environ.get("BENCH_VOICE_URL") or "ws://customer:8000/call"
BASE = "http://" + CALLER.split("://", 1)[1].split("/", 1)[0]
KIT = tempfile.mkdtemp()
for name in ("voiceclient.py", "tonecode.py", "wsproto.py"):
    for _ in range(120):
        try:
            with urllib.request.urlopen(BASE + "/kit/" + name, timeout=5) as r:
                data = r.read()
            break
        except OSError:
            time.sleep(0.5)
    else:
        sys.exit("cannot fetch the voice kit from " + BASE)
    with open(os.path.join(KIT, name), "wb") as f:
        f.write(data)
sys.path.insert(0, KIT)
import voiceclient  # noqa: E402

phone = voiceclient.VoiceCall(CALLER, timeout=60)
phone.say_tone("Hello, I am listening. What is your question?")
try:
    heard = phone.listen(end_silence_ms=1500, max_wait_s=90, max_utterance_s=180)
except voiceclient.Hangup:
    sys.exit(0)
print("question:", "nothing" if heard is None else f"{len(heard) // 32} ms of audio")
phone.say_tone(ANSWER)
if not phone.wait_end(30):
    phone.hangup("done")
PY
