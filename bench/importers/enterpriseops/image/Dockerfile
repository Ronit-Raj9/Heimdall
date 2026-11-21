# EnterpriseOps-Gym domain service, written by `bifrost-bench import enterpriseops`.
# Upstream's MCP server image for one domain, unchanged, plus a small front
# (bench_front.py) that seeds the task's database at start, serves only /mcp
# on :8080 with the task's context headers and database id added, offers only
# the task's selected tools, and runs the task's SQL checks for the export.
ARG UPSTREAM
FROM ${UPSTREAM}
ARG UP_PORT=8005
ARG UP_APP=main:app
ENV BENCH_UP_PORT=${UP_PORT} BENCH_UP_APP=${UP_APP}
COPY bench_front.py /bench/bench_front.py
EXPOSE 8080
ENTRYPOINT []
CMD ["python", "/bench/bench_front.py"]
