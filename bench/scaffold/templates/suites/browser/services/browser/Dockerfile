# Image of the browser service: Microsoft's Playwright MCP server with a
# headless Chromium (about 1.5 GB), serving MCP over streamable HTTP. The
# agent drives the browser only through MCP tool calls (browser_navigate,
# browser_snapshot, browser_click, browser_type, ...), which reach this
# service through the per-run tool proxy and are recorded in tools.jsonl.
#
# The build adds nothing to the upstream image, so it runs no step that needs
# the network: docker only pulls the base. `bifrost-bench suite admit` builds
# it when it is missing; by hand, from the project root:
#
#   docker build -t <tag in the task's browser service image> benchmarks/<suite>/services/browser
#
# The base is pinned by digest (tag kept for reading). The entrypoint is the
# upstream one (tini, then `node /app/cli.js --headless --browser chromium
# --no-sandbox`); the task's `argv:` adds the server flags (port, host check,
# allowed origins). The Chromium sandbox is off because the service runs
# without the capabilities it needs; the container is the boundary: it sits on
# internal networks only, with no route out.
FROM mcr.microsoft.com/playwright/mcp:v0.0.83@sha256:690763cbb437ee7233f538a953bb5390bf03791eb5c965b471e59d17a6132fcb
EXPOSE 8931
