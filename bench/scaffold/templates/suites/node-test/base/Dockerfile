# Toolchain image for this suite: the agent's workspace, the hidden tests and
# the control agents all run on it. The bench expects a user with uid 1000,
# /workspace and /grader/out owned by that user, and git. The node image's
# own uid 1000 user is renamed to agent. Node 22 runs .ts tests by stripping
# types. The verifier has no network: tasks use no npm dependencies, or
# vendor them in the repo.
FROM node:22-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN groupmod -n agent node \
 && usermod -l agent -d /home/agent -m node \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
ENV NODE_ENV=test
WORKDIR /workspace
USER agent
