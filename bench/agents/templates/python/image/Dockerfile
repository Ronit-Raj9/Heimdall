# Agent image for __AGENT_ID__ (template "python"). It is built once per suite
# on that suite's base image, so the agent sees the same toolchain the grader
# uses: `bifrost-bench agent build agents/__AGENT_ID__.yaml -suite <suite>`
# passes --build-arg BASE_IMAGE=<the suite's base image> and tags the result
# __IMAGE_TAG__-<suite>. The default lets a plain `docker build` work too. The
# suite's base image needs python3.
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}

USER root
COPY agent.py /opt/bench-agent/agent.py
RUN chmod 0755 /opt/bench-agent/agent.py \
 && ln -sf /opt/bench-agent/agent.py /usr/local/bin/bench-agent \
 && python3 -m py_compile /opt/bench-agent/agent.py

# The bench runs every agent as uid 1000 with a read-only root filesystem;
# write only under /workspace, /tmp and $HOME.
USER 1000
WORKDIR /workspace
