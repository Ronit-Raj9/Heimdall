# mini-swe-agent image for Bifrost Bench: the gateway-level compatibility agent.
# Built on the coding-core base so the agent sees the same Python, pytest and
# git the grader uses. The version is pinned exactly.
FROM bench/coding-core-base:1

ARG MINI_SWE_AGENT_VERSION=2.4.6

USER root
RUN pip install --no-cache-dir "mini-swe-agent==${MINI_SWE_AGENT_VERSION}" \
 && MSWEA_CONFIGURED=true MSWEA_SILENT_STARTUP=1 MSWEA_GLOBAL_CONFIG_DIR=/tmp/mswea-build mini --help >/dev/null \
 && rm -rf /tmp/mswea-build

# Skip the interactive first-run setup and keep litellm off the network for its
# price map; the bench prices every call itself from the gateway rows.
ENV MSWEA_CONFIGURED=true \
    MSWEA_SILENT_STARTUP=1 \
    LITELLM_LOCAL_MODEL_COST_MAP=True

USER agent
WORKDIR /workspace
