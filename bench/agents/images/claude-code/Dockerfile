# Claude Code agent image for Bifrost Bench.
# Built on the coding-core base so the agent sees the same Python, pytest and
# git the grader uses. Claude Code 2.x needs Node.js >= 22; Debian trixie ships
# 20, so Node comes from NodeSource. The CLI version is pinned exactly: a new
# Claude Code release is a new agent image tag, never a silent upgrade.
FROM bench/coding-core-base:1

ARG CLAUDE_CODE_VERSION=2.1.325
ARG NODE_MAJOR=22

USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates gnupg \
 && curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" -o /tmp/nodesource_setup.sh \
 && bash /tmp/nodesource_setup.sh \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -f /tmp/nodesource_setup.sh \
 && npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && npm cache clean --force \
 && apt-get purge -y --auto-remove curl gnupg \
 && rm -rf /var/lib/apt/lists/* \
 && claude --version

# The bench runs agents with a read-only root filesystem and a tmpfs home, so
# the config directory is recreated per run; it exists here for local use.
RUN mkdir -p /home/agent/.claude && chown -R agent:agent /home/agent/.claude

ENV CLAUDE_CONFIG_DIR=/home/agent/.claude \
    DISABLE_AUTOUPDATER=1 \
    DISABLE_TELEMETRY=1 \
    DISABLE_ERROR_REPORTING=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

USER agent
WORKDIR /workspace
