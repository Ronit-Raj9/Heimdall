package main

import (
	"strings"

	"github.com/valyala/fasthttp"
)

// mcpBrowserGet reports a GET from a browser (fix wave X-35): it asks for
// HTML and not for an event stream, as an MCP client would.
func mcpBrowserGet(ctx *fasthttp.RequestCtx) bool {
	if !ctx.IsGet() {
		return false
	}
	accept := strings.ToLower(string(ctx.Request.Header.Peek("Accept")))
	return strings.Contains(accept, "text/html") && !strings.Contains(accept, "text/event-stream")
}

// mcpHelpPage is the short page a browser gets on GET /mcp, still with the
// 405 status and Allow header MCP clients rely on.
const mcpHelpHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Heimdall MCP server</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:3rem auto;padding:0 1rem;color:#1d1e22;background:#f9f9fb}@media (prefers-color-scheme:dark){body{color:#e8e8ec;background:#1d1e22}a{color:#8ab4ff}}</style>
</head><body>
<h1>Heimdall MCP server</h1>
<p>This address is for AI assistants and other MCP clients, not for a browser. Add it to your client as an MCP server, with an access token from Settings &gt; Access tokens.</p>
<p><a href="/docs?doc=mcp-server">How to connect a client</a></p>
</body></html>
`

func mcpHelpPage(ctx *fasthttp.RequestCtx) {
	ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
	ctx.SetContentType("text/html; charset=utf-8")
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBodyString(mcpHelpHTML)
}
