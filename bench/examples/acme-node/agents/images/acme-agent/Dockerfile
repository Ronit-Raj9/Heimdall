# acme-agent image: the agent is one dependency-free Node.js file, laid on
# top of a suite's toolchain image. Heimdall builds it once per suite with
# --build-arg BASE_IMAGE=<that suite's base_image.tag> (build.base_arg in
# agents/acme-agent.yaml) and tags it "<build.tag>-<suite id>", so the agent
# sees exactly the node, npm and git the grader uses. The base must provide
# Node.js 22 or later, git and a uid 1000 user named agent.
ARG BASE_IMAGE=heimdall-example/acme-orders-base:1
FROM ${BASE_IMAGE}

USER root
COPY agent.mjs /opt/acme-agent/agent.mjs
RUN chmod 0755 /opt/acme-agent/agent.mjs \
 && ln -s /opt/acme-agent/agent.mjs /usr/local/bin/acme-agent \
 && node --check /opt/acme-agent/agent.mjs

USER agent
WORKDIR /workspace
