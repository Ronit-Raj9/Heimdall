# Image of the Acme Tea support desk MCP server (fictional). Python standard library only, so the
# build needs no network beyond the base image. Build it from the project
# root (`bifrost-bench suite admit` builds it when it is missing):
#
#   docker build -t heimdall-example/acme-support-desk:1 benchmarks/acme-support/services/desk
#
# The base is pinned by digest (tag kept for reading), as the suite base is.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN useradd -m -u 1000 svc \
 && mkdir -p /seed /state \
 && chown svc:svc /state
COPY server.py /srv/server.py
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PORT=8000
USER 1000
EXPOSE 8000
CMD ["python3", "-I", "/srv/server.py"]
