# Workspace image of a ToolBench-X suite, written by `bifrost-bench import toolbench-x`.
# Agents and the script controls run in it. It holds no upstream data: the
# tools, their data and the expected answer live only in the service
# container, which the agent reaches through MCP.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
ENV PYTHONDONTWRITEBYTECODE=1
WORKDIR /workspace
USER agent
