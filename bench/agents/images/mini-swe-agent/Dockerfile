# mini-swe-agent image for Bifrost Bench: the gateway-level compatibility agent.
# By default built on the coding-core base so the agent sees the same Python,
# pytest and git the grader uses. On a benchmark whose tasks bring their own
# image (Terminal-Bench, Harbor suites) the evaluation rebuilds it FROM each
# task's image (build.base_arg BASE_IMAGE, handlers/evaltaskimage.go). The
# agent's own runtime is built apart and copied in whole, so it never uses the
# task image's Python (a task image may have none): a relocatable CPython from
# python-build-standalone (fetched by uv, both pinned), which needs glibc 2.17
# or newer in the task image. The version is pinned exactly.
ARG BASE_IMAGE=bench/coding-core-base:1

FROM bench/coding-core-base:1 AS mswebuild
ARG MINI_SWE_AGENT_VERSION=2.4.6
ARG UV_VERSION=0.9.5
ARG MSWEA_PYTHON=3.12.11
USER root
RUN pip install --no-cache-dir --disable-pip-version-check "uv==${UV_VERSION}" \
 && UV_PYTHON_INSTALL_DIR=/opt/mswea/pythons uv python install --no-bin "${MSWEA_PYTHON}" \
 && ln -s "$(dirname "$(dirname "$(UV_PYTHON_INSTALL_DIR=/opt/mswea/pythons uv python find --managed-python "${MSWEA_PYTHON}")")")" /opt/mswea/python \
 && uv pip install --no-cache --python /opt/mswea/python/bin/python3 --break-system-packages "mini-swe-agent==${MINI_SWE_AGENT_VERSION}" \
 && chmod -R a+rX /opt/mswea \
 && MSWEA_CONFIGURED=true MSWEA_SILENT_STARTUP=1 MSWEA_GLOBAL_CONFIG_DIR=/tmp/mswea-build /opt/mswea/python/bin/mini --help >/dev/null \
 && rm -rf /tmp/mswea-build

FROM ${BASE_IMAGE}
USER root
# --chown: the copied files belong to root whatever ids the build stage used.
COPY --from=mswebuild --chown=0:0 /opt/mswea /opt/mswea
COPY --from=mswebuild --chown=0:0 /opt/mswea/python/bin/mini /usr/local/bin/mini

# Skip the interactive first-run setup and keep litellm off the network for its
# price map; the bench prices every call itself from the gateway rows.
ENV MSWEA_CONFIGURED=true \
    MSWEA_SILENT_STARTUP=1 \
    LITELLM_LOCAL_MODEL_COST_MAP=True

# uid 1000 is the coding-core "agent" user; a task image may have no such name.
USER 1000
WORKDIR /workspace
