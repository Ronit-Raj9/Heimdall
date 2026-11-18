# CrewAI coding agent (bench/examples/frameworks/crewai).
# Built on the suite's base image (build.base_arg in
# agents/fw-crewai.yaml). The framework lives in its own virtualenv,
# off the task's PATH. Versions are pinned in requirements.txt (compiled from
# requirements.in with `uv pip compile`).
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}

USER root
COPY requirements.txt /opt/fw-agent/requirements.txt
RUN python3 -m venv /opt/fw-agent/venv \
 && /opt/fw-agent/venv/bin/pip install --no-cache-dir --disable-pip-version-check -r /opt/fw-agent/requirements.txt
COPY agent.py /opt/fw-agent/agent.py
RUN printf '#!/bin/sh\nexec /opt/fw-agent/venv/bin/python /opt/fw-agent/agent.py "$@"\n' > /usr/local/bin/fw-agent \
 && chmod 0755 /usr/local/bin/fw-agent \
 && /opt/fw-agent/venv/bin/python -c "import crewai; from importlib.metadata import version; print('crewai', version('crewai'))"

# CrewAI's anonymous telemetry, trace upload and PyPI version check: off.
# Chroma (a CrewAI dependency, unused here) has its own telemetry: off too.
ENV CREWAI_DISABLE_TELEMETRY=true \
    CREWAI_DISABLE_TRACKING=true \
    CREWAI_TRACING_ENABLED=false \
    CREWAI_DISABLE_VERSION_CHECK=true \
    CREWAI_STORAGE_DIR=/tmp/crewai \
    ANONYMIZED_TELEMETRY=False \
    HF_HUB_OFFLINE=1

USER 1000
WORKDIR /workspace
