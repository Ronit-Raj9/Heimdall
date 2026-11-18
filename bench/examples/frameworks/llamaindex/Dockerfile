# LlamaIndex coding agent (bench/examples/frameworks/llamaindex).
# Built on the suite's base image (build.base_arg in
# agents/fw-llamaindex.yaml). The framework lives in its own virtualenv,
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
 && /opt/fw-agent/venv/bin/python -c "import llama_index.core, llama_index.llms.openai_like; from importlib.metadata import version; print('llama-index-core', version('llama-index-core'))"

# LlamaIndex sends nothing but the agent's model calls; its tokenizer and
# NLTK data ship inside the package.

USER 1000
WORKDIR /workspace
