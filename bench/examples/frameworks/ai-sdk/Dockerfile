# Vercel AI SDK coding agent (bench/examples/frameworks/ai-sdk), Node.
# Built on the suite's base image (build.base_arg in agents/fw-ai-sdk.yaml),
# so the agent's commands see the grader's Python, pytest and git. Node and
# the agent's node_modules come from a pinned Node image; package-lock.json
# pins every package, installed with `npm ci`.
ARG BASE_IMAGE=bench/coding-core-base:1
ARG NODE_IMAGE=node:22.23.5-slim

FROM ${NODE_IMAGE} AS deps
WORKDIR /opt/fw-agent
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund

FROM ${BASE_IMAGE}
USER root
COPY --from=deps /usr/local/bin/node /usr/local/bin/node
COPY --from=deps /opt/fw-agent/node_modules /opt/fw-agent/node_modules
COPY package.json agent.mjs /opt/fw-agent/
RUN printf '#!/bin/sh\nexec /usr/local/bin/node /opt/fw-agent/agent.mjs "$@"\n' > /usr/local/bin/fw-agent \
 && chmod 0755 /usr/local/bin/fw-agent \
 && node --version \
 && cd /opt/fw-agent && node -e "import('ai').then(() => console.log('ai', require('./node_modules/ai/package.json').version, '@ai-sdk/openai', require('./node_modules/@ai-sdk/openai/package.json').version))"

# The AI SDK sends nothing by itself; no update check, no telemetry.
ENV NODE_ENV=production

USER 1000
WORKDIR /workspace
