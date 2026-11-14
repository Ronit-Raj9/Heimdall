# Toolchain image for this suite: the agent's workspace, the verifier and the
# control agents all run on it. The bench expects a user with uid 1000,
# /workspace and /grader/out owned by that user, and git. Install the tools
# your test.sh scripts need (pinned) here; the verifier has no network.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
WORKDIR /workspace
USER agent
