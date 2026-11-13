package server

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func TestUIHandler(t *testing.T) {
	tests := []struct {
		name         string
		dist         fstest.MapFS
		path         string
		status       int
		contains     string
		cacheControl string
	}{
		{"index", testUI(), "/", 200, "<title>bench</title>", "no-cache"},
		{"spa fallback", testUI(), "/runs/r_1", 200, "<title>bench</title>", "no-cache"},
		{"asset", testUI(), "/assets/app-1.js", 200, "console.log", "immutable"},
		{"missing asset", testUI(), "/assets/x.css", 404, "not found", ""},
		{"traversal stays inside the UI", testUI(), "/../../etc/passwd", 200, "<title>bench</title>", "no-cache"},
		{"api path", testUI(), "/api/other", 404, `"type":"not_found"`, ""},
		{"not built", fstest.MapFS{".gitkeep": {}}, "/", 404, "UI not built", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := router.New()
			NewUIHandler(schemas.ModeLive, tt.dist).RegisterRoutes(r)
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(tt.path)
			r.Handler(ctx)
			if ctx.Response.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", ctx.Response.StatusCode(), tt.status, ctx.Response.Body())
			}
			if !strings.Contains(string(ctx.Response.Body()), tt.contains) {
				t.Errorf("body %q lacks %q", ctx.Response.Body(), tt.contains)
			}
			if tt.cacheControl != "" && !strings.Contains(string(ctx.Response.Header.Peek("Cache-Control")), tt.cacheControl) {
				t.Errorf("cache-control = %q", ctx.Response.Header.Peek("Cache-Control"))
			}
		})
	}
}
