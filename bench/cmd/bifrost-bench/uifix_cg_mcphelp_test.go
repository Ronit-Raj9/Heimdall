package main

import (
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

// TestMCPBrowserGet: a browser gets the help page, an MCP client the JSON
// error; both 405 with Allow POST.
func TestMCPBrowserGet(t *testing.T) {
	m := &mcpHTTP{mode: "live"}
	for _, c := range []struct {
		accept string
		html   bool
	}{{"text/html,application/xhtml+xml", true}, {"application/json, text/event-stream", false}, {"", false}} {
		var ctx fasthttp.RequestCtx
		ctx.Request.Header.SetMethod("GET")
		ctx.Request.Header.Set("Accept", c.accept)
		m.notAllowed(&ctx)
		body := string(ctx.Response.Body())
		if ctx.Response.StatusCode() != 405 || string(ctx.Response.Header.Peek("Allow")) != "POST" || strings.Contains(body, "<h1>") != c.html {
			t.Fatalf("%q: %d %s", c.accept, ctx.Response.StatusCode(), body)
		}
	}
}
