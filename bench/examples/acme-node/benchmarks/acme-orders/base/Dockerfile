# Base image for every acme-orders task, the grader and the agent images
# built on it (build.base_arg). Node.js 22 and git, nothing from npm: every
# task, test and the verifier run offline.
#
# Same conventions as the coding-core base: uid 1000 named agent with home
# /home/agent, /workspace and /grader/out owned by it, WORKDIR /workspace.
# The node image already uses uid 1000 for its "node" user, so that user is
# renamed rather than a second uid 1000 being added.
#
# The base is pinned by digest (tag kept for reading) to the image the
# example was verified with on 15 Nov 2026 (Node 22.23.5, Debian 12).
FROM node:22-slim@sha256:e9f565d32859f0cbfb52125eafe8be39390f24dac302dc3cdf566e8ba3ab21ee

RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN groupmod -n agent node \
 && usermod -l agent -d /home/agent -m node \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
# No update checks or telemetry from npm; the containers have no network.
ENV NO_UPDATE_NOTIFIER=1 \
    NPM_CONFIG_UPDATE_NOTIFIER=false \
    NPM_CONFIG_FUND=false \
    NPM_CONFIG_AUDIT=false
WORKDIR /workspace
USER agent
