# Toolchain image for this suite: the agent's workspace, the hidden tests and
# the control agents all run on it. The bench expects a user with uid 1000,
# /workspace and /grader/out owned by that user, and git. Add the packages
# your tasks need (pinned) below the pytest line; the verifier has no network.
# The base is pinned by digest (tag kept for reading) to the image the bench
# was tested with.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir pytest==8.3.5
RUN useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
ENV PYTHONDONTWRITEBYTECODE=1
WORKDIR /workspace
USER agent
