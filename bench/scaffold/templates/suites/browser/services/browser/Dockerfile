# Image of the browser service: Microsoft's Playwright MCP server with a
# headless Chromium (about 1.5 GB), serving MCP over streamable HTTP. The
# agent drives the browser only through MCP tool calls (browser_navigate,
# browser_snapshot, browser_click, browser_type, ...), which reach this
# service through the per-run tool proxy and are recorded in tools.jsonl.
#
# The build only copies Heimdall's recording launcher (record.js,
# record.json) onto the upstream image, so it runs no step that needs the
# network: docker only pulls the base. `bifrost-bench suite admit` builds
# it when it is missing; by hand, from the project root:
#
#   docker build -t <tag in the task's browser service image> benchmarks/<suite>/services/browser
#
# The base is pinned by digest (tag kept for reading). The entrypoint is
# tini, then Heimdall's recording launcher (record.js), which starts the
# upstream server (`/app/cli.js --headless --browser chromium --no-sandbox`)
# with record.json: every browser context records a video (webm, 1280x720)
# and a HAR under /tmp/rec, and console messages go to
# /tmp/rec/console.jsonl. The task's `recordings:` block copies them into
# the run after the agent exits (they are never shown to the verifier), and
# its `live:` block lets the run page show the browser while the agent works
# (`node /opt/heimdall/record.js frame`). The task's `argv:` adds the server
# flags (port, host check, allowed origins). The Chromium sandbox is off
# because the service runs without the capabilities it needs; the container
# is the boundary: it sits on internal networks only, with no route out.
FROM mcr.microsoft.com/playwright/mcp:v0.0.83@sha256:690763cbb437ee7233f538a953bb5390bf03791eb5c965b471e59d17a6132fcb
EXPOSE 8931
COPY record.js record.json /opt/heimdall/
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "node", "/opt/heimdall/record.js", "--headless", "--browser", "chromium", "--no-sandbox", "--config", "/opt/heimdall/record.json"]
