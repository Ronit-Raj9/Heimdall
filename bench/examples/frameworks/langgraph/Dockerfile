# LangGraph coding agent (bench/examples/frameworks/langgraph). Built on the
# suite's base image (build.base_arg in agents/fw-langgraph.yaml), so the
# agent's commands see the grader's Python, pytest and git. The framework
# lives in its own virtualenv, off the task's PATH. Versions are pinned in
# requirements.txt (compiled from requirements.in with `uv pip compile`).
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}

USER root
COPY requirements.txt /opt/fw-agent/requirements.txt
RUN python3 -m venv /opt/fw-agent/venv \
 && /opt/fw-agent/venv/bin/pip install --no-cache-dir --disable-pip-version-check -r /opt/fw-agent/requirements.txt
COPY agent.py /opt/fw-agent/agent.py
RUN printf '#!/bin/sh\nexec /opt/fw-agent/venv/bin/python /opt/fw-agent/agent.py "$@"\n' > /usr/local/bin/fw-agent \
 && chmod 0755 /usr/local/bin/fw-agent \
 && /opt/fw-agent/venv/bin/python -c "import langgraph, langchain_openai; from importlib.metadata import version; print('langgraph', version('langgraph'), 'langchain-openai', version('langchain-openai'))"

# LangSmith tracing is off unless switched on; say so explicitly.
ENV LANGSMITH_TRACING=false \
    LANGCHAIN_TRACING_V2=false

USER 1000
WORKDIR /workspace
