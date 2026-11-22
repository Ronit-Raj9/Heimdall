# The voice caller with every persona voice (docs/voice-realtime.md): the
# caller (voicecaller.py with tonecode.py, wsproto.py, voiceclient.py and
# personas.json, from bench/voice), the text user simulator it loads
# (simuser.py and its prompts, from bench/simuser), Piper with the two
# openly licensed voices the persona library uses, faster-whisper
# tiny.en and the Silero voice activity model.
#
#   docker build -f deploy/voice/caller.Dockerfile -t heimdall/voice-caller:1 .   (from bench/)
#
# Local engines, downloaded at build time from their primary sources and
# checked against pinned sha256 sums:
#   - Piper 2023.11.14-2 (MIT), github.com/rhasspy/piper release binary
#   - en_US-ljspeech-medium (rhasspy/piper-voices v1.0.0; LJ Speech, public domain)
#   - en_GB-vctk-medium (rhasspy/piper-voices v1.0.0; VCTK, CC BY 4.0): 109
#     speakers whose accents VCTK's speaker-info.txt lists (personas.json
#     picks Indian, Scottish, Irish, Australian, South African, American
#     and Canadian speakers)
#   - faster-whisper 1.2.1 (MIT) with CTranslate2 4.9.0 and
#     Systran/faster-whisper-tiny.en (MIT) at revision 0d3d19a3
#   - Silero VAD v5.1.2 ONNX (MIT), snakers4/silero-vad, with onnxruntime 1.22.1
# The build needs the network; the running containers never get it.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_DISABLE_PIP_VERSION_CHECK=1
RUN pip install --no-cache-dir faster-whisper==1.2.1 ctranslate2==4.9.0 onnxruntime==1.22.1 \
 && find / -xdev -name __pycache__ -type d -prune -exec rm -rf {} + 2>/dev/null; true
RUN python3 - <<'PY'
import hashlib, os, tarfile, urllib.request
V = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/en/"
W = "https://huggingface.co/Systran/faster-whisper-tiny.en/resolve/0d3d19a32d3338f10357c0889762bd8d64bbdeba/"
files = [
    ("https://github.com/rhasspy/piper/releases/download/2023.11.14-2/piper_linux_x86_64.tar.gz",
     "/tmp/piper.tar.gz", "a50cb45f355b7af1f6d758c1b360717877ba0a398cc8cbe6d2a7a3a26e225992"),
    (V + "en_US/ljspeech/medium/en_US-ljspeech-medium.onnx", "/opt/voice/voices/en_US-ljspeech-medium.onnx",
     "6f52a751e2349abe7a76735eb09dc1875298c77ea2342ffd2fef79ff81b87f22"),
    (V + "en_US/ljspeech/medium/en_US-ljspeech-medium.onnx.json", "/opt/voice/voices/en_US-ljspeech-medium.onnx.json", None),
    (V + "en_GB/vctk/medium/en_GB-vctk-medium.onnx", "/opt/voice/voices/en_GB-vctk-medium.onnx",
     "4e9fc85ab9009385319fc6bae7f55577f8a2d7ee77fd9159a5500eb6531f41e6"),
    (V + "en_GB/vctk/medium/en_GB-vctk-medium.onnx.json", "/opt/voice/voices/en_GB-vctk-medium.onnx.json", None),
    (W + "model.bin", "/opt/voice/whisper/model.bin", "1a5afae06a4db91c975c9a9d78be5cc110ee4ea022ad57d55492e4550e936b2a"),
    ("https://raw.githubusercontent.com/snakers4/silero-vad/v5.1.2/src/silero_vad/data/silero_vad.onnx",
     "/opt/voice/silero_vad.onnx", "2623a2953f6ff3d2c1e61740c6cdb7168133479b267dfef114a4a3cc5bdd788f"),
]
for name in ("config.json", "tokenizer.json", "vocabulary.txt"):
    files.append((W + name, "/opt/voice/whisper/" + name, None))
for url, dest, want in files:
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    with urllib.request.urlopen(url, timeout=300) as r, open(dest, "wb") as f:
        h = hashlib.sha256()
        while True:
            b = r.read(1 << 20)
            if not b:
                break
            h.update(b)
            f.write(b)
    got = h.hexdigest()
    print(dest, got)
    if want and got != want:
        raise SystemExit(f"{url}: sha256 {got}, want {want}")
with tarfile.open("/tmp/piper.tar.gz") as t:
    t.extractall("/opt/voice", filter="data")
os.remove("/tmp/piper.tar.gz")
PY
RUN useradd -m -u 1000 svc \
 && mkdir -p /state /sim \
 && chown svc:svc /state
COPY simuser/simuser.py voice/voicecaller.py voice/tonecode.py voice/wsproto.py voice/voiceclient.py voice/personas.json /srv/
COPY simuser/prompts/ /srv/prompts/
ENV PORT=8000 PIPER_BIN=/opt/voice/piper/piper PIPER_VOICE=/opt/voice/voices/en_US-ljspeech-medium.onnx \
    PIPER_VOICES_DIR=/opt/voice/voices WHISPER_MODEL_DIR=/opt/voice/whisper SILERO_VAD_MODEL=/opt/voice/silero_vad.onnx
USER 1000
EXPOSE 8000
CMD ["python3", "-I", "/srv/voicecaller.py", "serve", "--config", "/sim/user.json", "--state", "/state"]
