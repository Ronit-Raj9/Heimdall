# AgentDojo image for an imported Heimdall suite. It is three things at once:
# the MCP service each task starts beside the agent (server.py), the
# verifier that decides utility and attack success from the exported state
# (verify.py), and the catalog reader the importer runs (catalog.py).
#
# `bifrost-bench import agentdojo` writes this folder into the suite, with
# upstream/ holding the AgentDojo package copied from the local clone. The
# build installs that package and its dependencies from the package index;
# it is the only step that uses the network. Runs never do.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1
COPY upstream /tmp/agentdojo-src
RUN pip install /tmp/agentdojo-src && rm -rf /tmp/agentdojo-src
COPY heimdall /opt/heimdall
RUN chmod -R a+rX,go-w /opt/heimdall \
 && useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out /state \
 && chown agent:agent /workspace /grader/out /state
WORKDIR /workspace
USER agent
EXPOSE 8000
CMD ["python3", "-I", "/opt/heimdall/server.py"]
