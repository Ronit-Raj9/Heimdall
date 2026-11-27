package handlers

import (
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

// TestInboxUnauthorizedSaysInboxToken (round 2, lane RG): the inbox webhook
// names its secret the inbox token; "ingest token" belongs to trace ingest.
func TestInboxUnauthorizedSaysInboxToken(t *testing.T) {
	h := &InboxTokensHandler{mode: testMode}
	ctx := &fasthttp.RequestCtx{}
	h.unauthorized(ctx)
	body := string(ctx.Response.Body())
	if ctx.Response.StatusCode() != 401 || !strings.Contains(body, "inbox tokens") || strings.Contains(body, "ingest token") {
		t.Fatalf("%d %s", ctx.Response.StatusCode(), body)
	}
}
