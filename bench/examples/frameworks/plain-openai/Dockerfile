# No-framework (OpenAI Python SDK only) coding agent (bench/examples/frameworks/plain-openai).
# Built on the suite's base image (build.base_arg in
# agents/fw-plain-openai.yaml). The framework lives in its own virtualenv,
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
 && /opt/fw-agent/venv/bin/python -c "import openai; print('openai', openai.__version__)"

# The OpenAI SDK sends nothing but the requests the agent makes.

USER 1000
WORKDIR /workspace
