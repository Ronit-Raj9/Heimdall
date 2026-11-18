# The Acme Shop app: the agent's workspace (/app), also the image its
# verifier runs in. The two drivers are the only packages it adds; the build
# fetches them from PyPI (x-heimdall build_network in compose.yaml).
FROM python:3.12-slim
RUN pip install --no-cache-dir "psycopg[binary]==3.2.10" "redis==5.2.1"
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /app
COPY . /app
