# CrewAI coding agent (bench/examples/frameworks/crewai).
# Built on the suite's base image (build.base_arg in
# agents/fw-crewai.yaml). The framework lives in its own Python runtime,
# off the task's PATH. Versions are pinned in requirements.txt (compiled from
# requirements.in with `uv pip compile`).
ARG BASE_IMAGE=bench/coding-core-base:1

# The agent's runtime is built apart and copied in whole, so the agent never
# uses the task image's Python (a terminal task image may have none, or no
# venv, or a Python too old for the pins: PH1-1, PH4-1). The runtime is a
# relocatable CPython from python-build-standalone (fetched by uv, both
# pinned), which needs only glibc 2.17 or newer in the task image; an Alpine
# (musl) task image cannot run it.
FROM bench/coding-core-base:1 AS fwbuild
ARG UV_VERSION=0.9.5
ARG FW_PYTHON=3.12.11
USER root
RUN pip install --no-cache-dir --disable-pip-version-check "uv==${UV_VERSION}" \
 && UV_PYTHON_INSTALL_DIR=/opt/fw-agent/pythons uv python install --no-bin "${FW_PYTHON}" \
 && ln -s "$(dirname "$(dirname "$(UV_PYTHON_INSTALL_DIR=/opt/fw-agent/pythons uv python find --managed-python "${FW_PYTHON}")")")" /opt/fw-agent/python
COPY requirements.txt /opt/fw-agent/requirements.txt
RUN uv pip install --no-cache --python /opt/fw-agent/python/bin/python3 --break-system-packages -r /opt/fw-agent/requirements.txt
COPY agent.py /opt/fw-agent/agent.py
RUN printf '#!/bin/sh\nexec /opt/fw-agent/python/bin/python3 /opt/fw-agent/agent.py "$@"\n' > /opt/fw-agent/fw-agent \
 && chmod -R a+rX /opt/fw-agent && chmod 0755 /opt/fw-agent/fw-agent \
 && /opt/fw-agent/python/bin/python3 -c "import crewai; from importlib.metadata import version; print('crewai', version('crewai'))"

FROM ${BASE_IMAGE}
USER root
# --chown: the copied files belong to root whatever ids the build stage used.
COPY --from=fwbuild --chown=0:0 /opt/fw-agent /opt/fw-agent
COPY --from=fwbuild --chown=0:0 /opt/fw-agent/fw-agent /usr/local/bin/fw-agent

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
