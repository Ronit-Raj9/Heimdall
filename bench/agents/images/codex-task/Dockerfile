# Codex CLI on top of a task image (build.base_arg BASE_IMAGE), so the agent
# runs in the task environment and the snapshot verifier finds it.
# The codex binary is the static musl build from bench/agent-codex:1.
ARG BASE_IMAGE
FROM bench/agent-codex:1 AS codex
FROM ${BASE_IMAGE}
COPY --from=codex /usr/lib/node_modules/@openai/codex/node_modules/@openai/codex-linux-x64/vendor/x86_64-unknown-linux-musl/ /opt/codex/
RUN printf "#!/bin/sh\nPATH=/opt/codex/codex-path:\$PATH exec /opt/codex/bin/codex \"\$@\"\n" > /usr/local/bin/codex \
 && chmod 755 /usr/local/bin/codex && mkdir -p /home/agent/.codex && chmod -R 777 /home/agent && /usr/local/bin/codex --version
