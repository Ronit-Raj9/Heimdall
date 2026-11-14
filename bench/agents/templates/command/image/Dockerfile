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

USER root
COPY example-cli.py /usr/local/bin/example-cli
RUN chmod 0755 /usr/local/bin/example-cli \
 && example-cli --help > /dev/null

# The bench runs every agent as uid 1000 with a read-only root filesystem;
# write only under /workspace, /tmp and $HOME.
USER 1000
WORKDIR /workspace
