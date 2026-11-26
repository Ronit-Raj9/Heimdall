package handlers

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/valyala/fasthttp"
)

func w4b08GzipString(s string) string {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return buf.String()
}

func w4b08EncodedPost(ctx *fasthttp.RequestCtx, enc string) {
	ctx.Request.Header.Set("Content-Encoding", enc)
}

// TestOTLPIngestGzip: both ingest routes read a gzip export, refuse an
// unknown coding and stop a body that expands past the cap.
func TestOTLPIngestGzip(t *testing.T) {
	f := newTraceIngestFixture(t)
	tok := f.token(t, "org_a")
	post := func(path, bearer, enc, body string) *fasthttp.RequestCtx {
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.SetRequestURI(path)
		ctx.Request.Header.SetMethod("POST")
		ctx.Request.Header.SetHost("heimdall.example")
		ctx.Request.Header.SetContentType("application/json")
		if bearer != "" {
			ctx.Request.Header.Set("Authorization", "Bearer "+bearer)
		}
		w4b08EncodedPost(ctx, enc)
		ctx.Request.SetBodyString(body)
		return ctx
	}
	ctx := post(OTLPOrgIngestURL("org_a"), tok, "gzip", w4b08GzipString(traceExport))
	f.r.Handler(ctx)
	if ctx.Response.StatusCode() != 200 || !bytes.Contains(ctx.Response.Body(), []byte(`"accepted":4`)) {
		t.Fatalf("hosted gzip: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx = post(OTLPOrgIngestURL("org_a"), tok, "br", traceExport)
	f.r.Handler(ctx)
	if ctx.Response.StatusCode() != 415 {
		t.Fatalf("hosted br: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx = post(OTLPOrgIngestURL("org_a"), tok, "gzip", w4b08GzipString(string(bytes.Repeat([]byte(" "), OTLPMaxBytes+10))))
	f.r.Handler(ctx)
	if ctx.Response.StatusCode() != 413 {
		t.Fatalf("hosted bomb: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	r := connectTracesRouter(t, t.TempDir(), false)
	ctx = post(OTLPLocalIngestPath, "", "gzip", w4b08GzipString(traceExport))
	r.Handler(ctx)
	if ctx.Response.StatusCode() != 200 || !bytes.Contains(ctx.Response.Body(), []byte(`"accepted":4`)) {
		t.Fatalf("local gzip: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}
