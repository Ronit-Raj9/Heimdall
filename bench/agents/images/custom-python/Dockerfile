# Custom agent template image for Bifrost Bench: the coding-core base plus one
# standard-library Python file. Copy this folder to package your own agent:
# install what it needs, put its entry point on PATH, keep the base image so
# the agent sees the same Python, pytest and git the grader uses.
FROM bench/coding-core-base:1

USER root
COPY agent.py /opt/bench-agent/agent.py
RUN chmod 0755 /opt/bench-agent/agent.py \
 && ln -s /opt/bench-agent/agent.py /usr/local/bin/bench-agent \
 && python3 -m py_compile /opt/bench-agent/agent.py

USER agent
WORKDIR /workspace
