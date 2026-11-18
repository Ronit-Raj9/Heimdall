# Image of the helpdesk MCP server and the user simulator (simuser.py, the
# simulated customer, with its versioned prompts). Python standard library
# only, so the build needs no network beyond the base image. Build it from the
# project root (`bifrost-bench suite admit` builds it when it is missing):
#
#   docker build -t heimdall-example/acme-helpdesk:1 benchmarks/acme-helpdesk/services/helpdesk
#
# simuser.py and prompts/ are written by Heimdall from bench/simuser; keep
# them in step with the bench (the planner pins the prompt's sha256 and the
# simulator refuses to start in llm mode when its prompt differs).
# The base is pinned by digest (tag kept for reading), as the suite base is.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN useradd -m -u 1000 svc \
 && mkdir -p /seed /state /sim \
 && chown svc:svc /state
COPY server.py /srv/server.py
COPY simuser.py /srv/simuser.py
COPY prompts/ /srv/prompts/
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PORT=8000
USER 1000
EXPOSE 8000
CMD ["python3", "-I", "/srv/server.py"]
