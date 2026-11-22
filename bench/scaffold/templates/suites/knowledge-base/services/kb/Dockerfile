# Image of the knowledge-base search service. Python standard library only,
# so the build needs no network beyond the base image. The documents in docs/
# are baked into the image; `bifrost-bench suite admit` builds it when it is
# missing:
#
#   docker build -t <tag in the task's services: image> benchmarks/<suite>/services/kb
#
# The base is pinned by digest (tag kept for reading), as the suite base is.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN useradd -m -u 1000 svc \
 && mkdir -p /state \
 && chown svc:svc /state
COPY server.py /srv/server.py
COPY docs/ /srv/docs/
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PORT=8000 DOCS_DIR=/srv/docs
USER 1000
EXPOSE 8000
CMD ["python3", "-I", "/srv/server.py"]
