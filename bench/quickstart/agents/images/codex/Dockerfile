# Codex CLI agent image for Bifrost Bench.
# Built on the coding-core base so the agent sees the same Python, pytest and
# git the grader uses. The npm package needs Node.js; Debian trixie ships 20, so
# Node 22 comes from NodeSource, as in the claude-code image. The CLI version is
# pinned exactly: a new Codex release is a new agent image tag, never a silent
# upgrade.
FROM bench/coding-core-base:1

ARG CODEX_VERSION=0.169.0
ARG NODE_MAJOR=22

USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates gnupg \
 && curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" -o /tmp/nodesource_setup.sh \
 && bash /tmp/nodesource_setup.sh \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -f /tmp/nodesource_setup.sh \
 && npm install -g "@openai/codex@${CODEX_VERSION}" \
 && npm cache clean --force \
 && apt-get purge -y --auto-remove curl gnupg \
 && rm -rf /var/lib/apt/lists/* \
 && codex --version

# The bench runs agents with a read-only root filesystem and a tmpfs home, so
# CODEX_HOME is recreated per run (the adapter writes config.toml into it); it
# exists here for local use.
RUN mkdir -p /home/agent/.codex && chown -R agent:agent /home/agent/.codex

ENV CODEX_HOME=/home/agent/.codex

USER agent
WORKDIR /workspace
