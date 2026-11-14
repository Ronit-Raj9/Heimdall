# Toolchain image for this suite: the agent's workspace, the hidden tests and
# the control agents all run on it. The bench expects a user with uid 1000,
# /workspace and /grader/out owned by that user, and git. The verifier has no
# network, so modules must be vendored in the task repo or added to this
# image's module cache. GOCACHE lives in /tmp because the verifier's root
# filesystem is read-only.
FROM golang:1.26-bookworm
COPY gojunit /opt/bench/gojunit
RUN cd /opt/bench/gojunit \
 && CGO_ENABLED=0 go build -trimpath -o /usr/local/bin/go-junit . \
 && rm -rf /root/.cache/go-build
RUN useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
ENV GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-buildvcs=false GOCACHE=/tmp/go-build GOMODCACHE=/home/agent/go/pkg/mod
WORKDIR /workspace
USER agent
