# Codex CLI on a suite's toolchain image (build.base_arg in agents/codex.yaml).
# Same CLI and pin as the bench's own agents/images/codex/Dockerfile, which is
# built on the Python coding-core base; here the base is the suite's, so in
# acme-orders Codex sees the node, npm and git the grader uses. The base
# must provide Node.js 22 and npm (the acme-orders base does). The CLI
# version is pinned exactly: a new Codex release is a new image tag.
ARG BASE_IMAGE=heimdall-example/acme-orders-base:1
FROM ${BASE_IMAGE}

ARG CODEX_VERSION=0.169.0

USER root
RUN npm install -g "@openai/codex@${CODEX_VERSION}" \
 && npm cache clean --force \
 && codex --version

# The bench runs agents with a read-only root filesystem and a tmpfs home, so
# CODEX_HOME is recreated per run (the adapter writes config.toml into it); it
# exists here for local use.
RUN mkdir -p /home/agent/.codex && chown -R agent:agent /home/agent/.codex

ENV CODEX_HOME=/home/agent/.codex

USER agent
WORKDIR /workspace
