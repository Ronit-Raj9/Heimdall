# Gemini CLI agent image for Bifrost Bench.
# Built on the coding-core base so the agent sees the same Python, pytest and
# git the grader uses. Gemini CLI needs Node.js >= 20; Node 22 comes from
# NodeSource, as in the claude-code image. The CLI version is pinned exactly: a
# new Gemini CLI release is a new agent image tag, never a silent upgrade.
FROM bench/coding-core-base:1

ARG GEMINI_CLI_VERSION=0.68.0
ARG NODE_MAJOR=22

USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates gnupg \
 && curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" -o /tmp/nodesource_setup.sh \
 && bash /tmp/nodesource_setup.sh \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -f /tmp/nodesource_setup.sh \
 && npm install -g "@google/gemini-cli@${GEMINI_CLI_VERSION}" \
 && npm cache clean --force \
 && apt-get purge -y --auto-remove curl gnupg \
 && rm -rf /var/lib/apt/lists/* \
 && HOME=/tmp/gemini-build gemini --version \
 && rm -rf /tmp/gemini-build

# The bench runs agents with a read-only root filesystem and a tmpfs home, so
# ~/.gemini (settings.json, written by the agent config) is recreated per run;
# it exists here for local use.
RUN mkdir -p /home/agent/.gemini && chown -R agent:agent /home/agent/.gemini

USER agent
WORKDIR /workspace
