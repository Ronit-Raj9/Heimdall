# Agent image for __AGENT_ID__ (template "python"). It is built once per suite
# on that suite's base image, so the agent sees the same toolchain the grader
# uses: `bifrost-bench agent build agents/__AGENT_ID__.yaml -suite <suite>`
# passes --build-arg BASE_IMAGE=<the suite's base image> and tags the result
# __IMAGE_TAG__-<suite>. The default lets a plain `docker build` work too. The
# suite's base image needs python3.
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}
ARG BASE_IMAGE

USER root
# agent.py runs on python3. Fail with a plain message instead of exit code
# 127 when the suite's base image has none (a node or golang toolchain).
RUN command -v python3 >/dev/null 2>&1 || { echo "this agent needs python3 and the suite base image ${BASE_IMAGE} has none: build it for a suite whose base image has python3, or install python3 in this Dockerfile" >&2; exit 1; }
COPY agent.py /opt/bench-agent/agent.py
RUN chmod 0755 /opt/bench-agent/agent.py \
 && ln -sf /opt/bench-agent/agent.py /usr/local/bin/bench-agent \
 && python3 -m py_compile /opt/bench-agent/agent.py

# The bench runs every agent as uid 1000 with a read-only root filesystem;
# write only under /workspace, /tmp and $HOME.
USER 1000
WORKDIR /workspace
