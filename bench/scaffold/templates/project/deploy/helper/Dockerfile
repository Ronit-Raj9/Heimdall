# Trusted helper image (images.helper in bench.yaml). The bench runs its
# workspace copy, commit and diff export steps in it, and the hidden_tests
# grader runs pytest in it, so it carries git, Python 3.12 and pytest. The
# control agents (agents/*.yaml) use it too. Build from the project root:
#
#   docker build -t bench/coding-core-base:1 deploy/helper
#
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
