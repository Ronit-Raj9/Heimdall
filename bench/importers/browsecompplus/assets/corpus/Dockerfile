# Image of the corpus search service of an imported BrowseComp-Plus suite
# (server.py beside this file). Python standard library only; the corpus
# itself is copied in at start from the suite's services/corpus/corpus.jsonl
# (the service's files: entry), so one image serves every imported suite.
# `bifrost-bench suite admit` builds it when it is missing. The base is
# pinned by digest, as the research track's websearch image is.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN useradd -m -u 1000 svc && mkdir -p /srv/corpus && chown 1000:1000 /srv/corpus
COPY server.py /srv/server.py
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PORT=8000
USER 1000
EXPOSE 8000
CMD ["python3", "-I", "/srv/server.py"]
