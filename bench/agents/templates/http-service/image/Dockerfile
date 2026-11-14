# Agent image for __AGENT_ID__ (template "http-service"). It is built once per
# suite on that suite's base image: `bifrost-bench agent build
# agents/__AGENT_ID__.yaml -suite <suite>` passes --build-arg
# BASE_IMAGE=<the suite's base image> and tags the result __IMAGE_TAG__-<suite>.
#
# Install your server here instead of server.py, with its dependencies baked
# in: the container can reach nothing but the gateway, so nothing can be
# downloaded at start. The launcher (delivered by the agent YAML) needs
# python3 in the image.
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}
ARG BASE_IMAGE

USER root
# The launcher and server.py run on python3. Fail with a plain message
# instead of exit code 127 when the suite's base image has none (a node or
# golang toolchain).
RUN command -v python3 >/dev/null 2>&1 || { echo "this agent needs python3 and the suite base image ${BASE_IMAGE} has none: build it for a suite whose base image has python3, or install python3 in this Dockerfile" >&2; exit 1; }
COPY server.py /opt/agent/server.py
RUN python3 -m py_compile /opt/agent/server.py

# The bench runs every agent as uid 1000 with a read-only root filesystem;
# write only under /workspace, /tmp and $HOME.
USER 1000
WORKDIR /workspace
