# Image of the acme-voice-desk services: the helpdesk MCP server (server.py,
# copied from examples/acme-helpdesk), the text user simulator (simuser.py
# and its prompts, from bench/simuser) and the voice caller (voicecaller.py
# with tonecode.py, wsproto.py and voiceclient.py, from bench/voice). The
# scripted audio fake of the test stack (bench/voice/fakeopenai.py) runs from
# this image too, with bench/voice mounted. Build it from the project root
# (`bifrost-bench suite admit` builds it when it is missing):
#
#   docker build -t heimdall-example/acme-voice-desk:1 benchmarks/acme-voice-desk/services/helpdesk
#
# Local speech engines, downloaded at build time from their primary sources
# and checked against pinned sha256 sums (docs/voice-agents.md lists
# licences and sizes):
#   - Piper 2023.11.14-2 (MIT), github.com/rhasspy/piper release binary
#   - Piper voice en_US-ljspeech-medium (rhasspy/piper-voices v1.0.0, MIT;
#     trained on LJ Speech, public domain)
#   - faster-whisper 1.2.1 (MIT, PyPI) with CTranslate2 4.9.0 (MIT) and the
#     model Systran/faster-whisper-tiny.en (MIT) at revision 0d3d19a3
# The build needs the network; the running containers never get it.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_DISABLE_PIP_VERSION_CHECK=1
RUN pip install --no-cache-dir faster-whisper==1.2.1 ctranslate2==4.9.0 \
 && find / -xdev -name __pycache__ -type d -prune -exec rm -rf {} + 2>/dev/null; true
RUN python3 - <<'PY'
import hashlib, os, tarfile, urllib.request
files = [
    ("https://github.com/rhasspy/piper/releases/download/2023.11.14-2/piper_linux_x86_64.tar.gz",
     "/tmp/piper.tar.gz", "a50cb45f355b7af1f6d758c1b360717877ba0a398cc8cbe6d2a7a3a26e225992"),
    ("https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/en/en_US/ljspeech/medium/en_US-ljspeech-medium.onnx",
     "/opt/voice/voices/en_US-ljspeech-medium.onnx", "6f52a751e2349abe7a76735eb09dc1875298c77ea2342ffd2fef79ff81b87f22"),
    ("https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/en/en_US/ljspeech/medium/en_US-ljspeech-medium.onnx.json",
     "/opt/voice/voices/en_US-ljspeech-medium.onnx.json", None),
    ("https://huggingface.co/Systran/faster-whisper-tiny.en/resolve/0d3d19a32d3338f10357c0889762bd8d64bbdeba/model.bin",
     "/opt/voice/whisper/model.bin", "1a5afae06a4db91c975c9a9d78be5cc110ee4ea022ad57d55492e4550e936b2a"),
]
for name in ("config.json", "tokenizer.json", "vocabulary.txt"):
    files.append(("https://huggingface.co/Systran/faster-whisper-tiny.en/resolve/0d3d19a32d3338f10357c0889762bd8d64bbdeba/" + name,
                  "/opt/voice/whisper/" + name, None))
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
 && mkdir -p /seed /state /sim \
 && chown svc:svc /state
COPY server.py simuser.py voicecaller.py tonecode.py wsproto.py voiceclient.py /srv/
COPY prompts/ /srv/prompts/
ENV PORT=8000 PIPER_BIN=/opt/voice/piper/piper PIPER_VOICE=/opt/voice/voices/en_US-ljspeech-medium.onnx WHISPER_MODEL_DIR=/opt/voice/whisper
USER 1000
EXPOSE 8000
CMD ["python3", "-I", "/srv/server.py"]
