package telemetry

import (
	"strconv"

	"github.com/maximhq/bifrost/bench/selfmetrics"
	"github.com/valyala/fasthttp"
)

// Middleware gives every API request a server span, joined to the caller's
// trace when it sends a traceparent header (a worker on a lease). The span
// name is the method and the bounded area (selfmetrics.Area); the path
// goes in an attribute, never the query string or a body. With tracing
// off it is next itself.
//
// Handlers reach the span through the request: SpanFromContext(ctx) works
// on the *fasthttp.RequestCtx, whose Value reads user values.
func Middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		t := Default()
		if t.exp == nil {
			next(ctx)
			return
		}
		path := string(ctx.Path())
		if path == "/metrics" || path == "/healthz" || path == "/readyz" {
			next(ctx) // probes every few seconds: noise, not traces
			return
		}
		var opts []Option
		if sc, ok := ParseTraceparent(string(ctx.Request.Header.Peek("traceparent"))); ok {
			opts = append(opts, WithParent(sc))
		} else {
			opts = append(opts, WithParent(SpanContext{}))
		}
		method := string(ctx.Method())
		area := selfmetrics.Area(path)
		opts = append(opts, WithKind(KindServer), WithAttrs(
			String("http.request.method", method),
			String("url.path", path),
			String("heimdall.area", area),
		))
		s := t.StartSpan(nil, method+" "+area, opts...)
		ctx.SetUserValue(ctxKey{}, s)
		next(ctx)
		code := ctx.Response.StatusCode()
		s.SetAttrs(Int("http.response.status_code", code))
		if code >= 500 {
			s.SetError(strconv.Itoa(code))
		}
		s.End()
	}
}
