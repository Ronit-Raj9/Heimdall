# tau2-bench service and verifier image, written by `bifrost-bench import tau2`.
# The build fetches the upstream repository at the pinned commit and installs
# its package (network at build time only). The same image runs the task's MCP
# server (tau2svc.py serve) and its verifier (tau2svc.py verify). The agent
# never runs in it: it holds the upstream task files.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
ARG TAU2_REPO=https://github.com/sierra-research/tau2-bench.git
ARG TAU2_REF=__TAU2_REF__
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN git init -q /opt/tau2-bench \
 && cd /opt/tau2-bench \
 && git remote add origin "$TAU2_REPO" \
 && git fetch -q --depth 1 origin "$TAU2_REF" \
 && git checkout -q FETCH_HEAD \
 && pip install --no-cache-dir /opt/tau2-bench rank-bm25 \
 && rm -rf .git data/voice data/tau2/results \
      web figs docs tests \
 && find data/tau2/domains -name 'tasks_voice.json' -delete \
 && find data/tau2/domains -name 'audio_difficulty.json' -delete
ENV TAU2_DATA_DIR=/opt/tau2-bench/data \
    LITELLM_LOCAL_MODEL_COST_MAP=True \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
COPY tau2svc.py /opt/tau2svc/tau2svc.py
RUN useradd -m -u 1000 tau2 \
 && mkdir -p /state /task /grader/out \
 && chown tau2:tau2 /state /task /grader/out \
 && python -c "import tau2.registry"
USER tau2
WORKDIR /opt/tau2svc
EXPOSE 8000
CMD ["python", "/opt/tau2svc/tau2svc.py", "serve"]
