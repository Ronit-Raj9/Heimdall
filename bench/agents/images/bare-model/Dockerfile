# Bare-model baseline for Bifrost Bench: the coding-core base plus one
# standard-library Python file that makes a single model call with the task
# text and no tools. Built on the suite's base image (build.base_arg in
# agents/bare-model.yaml), so it starts where any other agent starts.
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}

USER root
COPY agent.py /opt/bare-model/agent.py
RUN chmod 0755 /opt/bare-model/agent.py \
 && ln -sf /opt/bare-model/agent.py /usr/local/bin/bare-model \
 && python3 -m py_compile /opt/bare-model/agent.py

USER 1000
WORKDIR /workspace
