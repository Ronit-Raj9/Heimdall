# Toolchain image for this suite: the agent's workspace, the verifier and
# agents built per suite (build.base_arg) run on it. The bench expects a user
# with uid 1000, /workspace and /grader/out owned by that user, and git. The
# state verifier and the reference scripts need python3, which this image
# has. The base is pinned by digest (tag kept for reading).
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
