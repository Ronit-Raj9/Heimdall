# Base image for every coding-core task, grader and agent image.
# Tasks may use only the Python standard library and pytest.
# The base is pinned by digest (tag kept for reading) to the image the bench
# was tested with.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir pytest==8.3.5
RUN useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
ENV PYTHONDONTWRITEBYTECODE=1
WORKDIR /workspace
USER agent

# Task workspace.
USER root
COPY repo/ /workspace/
RUN mkdir -p /workspace && cd /workspace && if command -v git >/dev/null 2>&1; then git -c safe.directory='*' init -q && git -c safe.directory='*' add -A && git -c safe.directory='*' -c user.name=task -c user.email=task@localhost commit -qm baseline >/dev/null; fi
RUN chown -R agent /workspace
USER agent
WORKDIR /workspace
