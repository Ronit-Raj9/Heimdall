package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// TestEstimateDeadlineAnswers503 checks an estimate that runs out of time
// answers 503 with Retry-After and a plain sentence, so the page tries again.
func TestEstimateDeadlineAnswers503(t *testing.T) {
	src := &fakeForecast{estimateErr: fmt.Errorf("load suite: %w", context.DeadlineExceeded)}
	r := router.New()
	NewForecastHandler(testMode, src, lib.RequireWriteToken(testMode, testToken)).
		RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("http://127.0.0.1" + APIPrefix + "/estimate")
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetBody([]byte("name: demo\nsuite: s@1\n"))
	ctx.Request.Header.SetContentType("application/yaml")
	ctx.Request.Header.Set(fasthttp.HeaderAuthorization, "Bearer "+testToken)
	r.Handler(&ctx)
	if got := ctx.Response.StatusCode(); got != fasthttp.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503; body %s", got, ctx.Response.Body())
	}
	if got := string(ctx.Response.Header.Peek("Retry-After")); got != "5" {
		t.Fatalf("Retry-After %q, want 5", got)
	}
	if body := string(ctx.Response.Body()); !strings.Contains(body, "Still reading this benchmark's tasks. Trying again in a few seconds.") {
		t.Fatalf("body %s", body)
	}
}
