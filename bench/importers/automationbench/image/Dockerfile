# AutomationBench service and verifier image, written by `bifrost-bench import automationbench`.
# The build fetches zapier/AutomationBench (MIT) at the pinned commit (network at
# build time only). The same image runs a task's MCP server (abench.py serve),
# its verifier (abench.py verify) and the task export (abench.py export). The
# agent never runs in it: it holds the upstream tasks and their assertions.
# libfaketime is preloaded so the task's run clock (FAKETIME, set by the bench
# from the task's clock: field) reaches upstream's datetime.now() calls; without
# FAKETIME it changes nothing.
FROM python:3.13-slim@sha256:e48f59cdba69a3cbd9ed2cf0f348d90d45a8d350d98134cf1070bc106ee9ed90
ARG AB_REPO=https://github.com/zapier/AutomationBench.git
ARG AB_REF=4a8e1061254004d9dac807054eed33fad7d1ff14
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates libfaketime \
 && ln -s "$(dpkg -L libfaketime | grep '/libfaketime.so.1$' | head -1)" /usr/local/lib/libfaketime.so.1 \
 && rm -rf /var/lib/apt/lists/*
RUN git init -q /opt/automationbench \
 && cd /opt/automationbench \
 && git remote add origin "$AB_REPO" \
 && git fetch -q --depth 1 origin "$AB_REF" \
 && git checkout -q FETCH_HEAD \
 && rm -rf .git visualizer tests \
 && pip install --no-cache-dir pydantic==2.12.5
COPY abench.py personas.py /opt/abench/
COPY stubs /opt/abench/stubs
ENV PYTHONPATH=/opt/automationbench:/opt/abench/stubs \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    LD_PRELOAD=/usr/local/lib/libfaketime.so.1 \
    FAKETIME_DONT_FAKE_MONOTONIC=1 \
    TZ=UTC
RUN useradd -m -u 1000 abench \
 && mkdir -p /state /task /grader/out \
 && chown abench:abench /state /task /grader/out \
 && python -c "import automationbench.tools.api, automationbench.rubric, automationbench.domains"
USER abench
WORKDIR /opt/abench
EXPOSE 8080
CMD ["python", "/opt/abench/abench.py", "serve"]
