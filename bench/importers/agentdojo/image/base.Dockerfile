# Workspace image of an AgentDojo suite, written by `bifrost-bench import
# agentdojo`. Agents and the script controls run in it. It holds no upstream
# code or data: the suite's environment, its tasks and their checks live only
# in the AgentDojo image, which the agent reaches through MCP and the
# verifier runs in after the agent is gone.
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
