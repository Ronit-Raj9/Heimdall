# Agent image for __AGENT_ID__ (template "command"). It is built once per suite
# on that suite's base image: `bifrost-bench agent build agents/__AGENT_ID__.yaml
# -suite <suite>` passes --build-arg BASE_IMAGE=<the suite's base image> and
# tags the result __IMAGE_TAG__-<suite>.
#
# Install your CLI here instead of example-cli: pin its version exactly, run
# it once (--version) so a broken install fails the build, and switch off
# auto-update, telemetry and plugin installs, because the container can reach
# nothing but the gateway.
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}
ARG BASE_IMAGE

USER root
# example-cli runs on python3: fail with a plain message instead of exit
# code 127 when the suite's base image has none (a node or golang
# toolchain). Drop this check with example-cli when your CLI needs no python3.
RUN command -v python3 >/dev/null 2>&1 || { echo "this agent needs python3 and the suite base image ${BASE_IMAGE} has none: build it for a suite whose base image has python3, or install python3 in this Dockerfile" >&2; exit 1; }
COPY example-cli.py /usr/local/bin/example-cli
RUN chmod 0755 /usr/local/bin/example-cli \
 && example-cli --help > /dev/null

# The bench runs every agent as uid 1000 with a read-only root filesystem;
# write only under /workspace, /tmp and $HOME.
USER 1000
WORKDIR /workspace
