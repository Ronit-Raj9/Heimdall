# OpenCode agent image for Bifrost Bench.
# Built on the coding-core base so the agent sees the same Python, pytest and
# git the grader uses. The opencode-ai npm package ships a native binary per
# platform; Node 22 comes from NodeSource, as in the claude-code image. The CLI
# version is pinned exactly: a new OpenCode release is a new agent image tag,
# never a silent upgrade.
FROM bench/coding-core-base:1

ARG OPENCODE_VERSION=1.19.5
ARG NODE_MAJOR=22

USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates gnupg \
 && curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" -o /tmp/nodesource_setup.sh \
 && bash /tmp/nodesource_setup.sh \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -f /tmp/nodesource_setup.sh \
 && npm install -g "opencode-ai@${OPENCODE_VERSION}" \
 && npm cache clean --force \
 && apt-get purge -y --auto-remove curl gnupg \
 && rm -rf /var/lib/apt/lists/* \
 && HOME=/tmp/oc-build opencode --version \
 && rm -rf /tmp/oc-build

# The bench runs agents with a read-only root filesystem and a tmpfs home, so
# OpenCode's config, data and cache folders under the home are recreated per
# run; the config folder exists here for local use. The switches keep OpenCode
# off every network but the gateway: no self-update, no models.dev catalogue
# fetch, no default plugins (installed from npm at start), no LSP downloads.
RUN mkdir -p /home/agent/.config/opencode && chown -R agent:agent /home/agent/.config

ENV OPENCODE_DISABLE_AUTOUPDATE=1 \
    OPENCODE_DISABLE_MODELS_FETCH=1 \
    OPENCODE_DISABLE_DEFAULT_PLUGINS=1 \
    OPENCODE_DISABLE_LSP_DOWNLOAD=1

USER agent
WORKDIR /workspace
