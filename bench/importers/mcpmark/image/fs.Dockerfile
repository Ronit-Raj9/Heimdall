# MCPMark filesystem service, written by `bifrost-bench import mcpmark`.
# It runs the MCP server MCPMark uses for its filesystem tasks
# (@modelcontextprotocol/server-filesystem, at the version upstream pins) over
# the task's test environment, copied into /data with its file times kept.
# The server speaks only stdio, so `bifrost-bench toolproxy bridge` serves it
# as a streamable HTTP endpoint on :8080/mcp. Building needs the network once
# (npm install); the run never does.
ARG BENCH_IMAGE=bench/bifrost-bench:dev
FROM ${BENCH_IMAGE} AS bench

FROM node:22-slim
RUN npm install -g --no-audit --no-fund @modelcontextprotocol/server-filesystem@__FS_SERVER_VERSION__ \
 && npm cache clean --force
COPY --from=bench /usr/local/bin/bifrost-bench /usr/local/bin/bifrost-bench
COPY --chown=node:node state/ /data/
USER node
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/bifrost-bench", "toolproxy", "bridge", "-listen", ":8080", "-path", "/mcp", "--", "mcp-server-filesystem", "/data"]
