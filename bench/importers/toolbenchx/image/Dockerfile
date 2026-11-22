# ToolBench-X service and verifier image, written by `bifrost-bench import toolbench-x`.
# The same image runs every task's MCP server (tbxsvc.py serve, the task's
# upstream tool module is copied in at run time as /task/tools.py) and its
# verifier (tbxsvc.py verify). The agent never runs in it.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN pip install --no-cache-dir requests==2.32.3
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
COPY tbxsvc.py /opt/tbxsvc/tbxsvc.py
RUN useradd -m -u 1000 tbx \
 && mkdir -p /state /task /grader/out \
 && chown tbx:tbx /state /task /grader/out
USER tbx
WORKDIR /opt/tbxsvc
EXPOSE 8000
CMD ["python", "/opt/tbxsvc/tbxsvc.py", "serve"]
