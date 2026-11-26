package lib

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/plaintext"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestSendErrorCleansHostedMessages checks that a hosted server never sends a
// server path or an organization id in an error answer (wave 4, F1.3).
func TestSendErrorCleansHostedMessages(t *testing.T) {
	saved := plaintext.DefaultRoots
	t.Cleanup(func() { plaintext.DefaultRoots = saved })
	plaintext.DefaultRoots = func() []string { return []string{"/var/lib/heimdall/runs"} }
	msg := "stat /var/lib/heimdall/runs/projects/org_01m7yyh2wft8r2x5tjyxthksbj/x.yaml: no such file or directory"
	for name, send := range map[string]func(*fasthttp.RequestCtx){
		"SendError":     func(c *fasthttp.RequestCtx) { SendError(c, schemas.ModeLive, 400, ErrTypeBadRequest, msg) },
		"SendErrorLink": func(c *fasthttp.RequestCtx) { SendErrorLink(c, schemas.ModeLive, 400, ErrTypeBadRequest, msg, "/x") },
	} {
		var ctx fasthttp.RequestCtx
		send(&ctx)
		body := string(ctx.Response.Body())
		if strings.Contains(body, "org_") || strings.Contains(body, "/var/") {
			t.Errorf("%s body names the server: %s", name, body)
		}
		if !strings.Contains(body, "x.yaml does not exist") {
			t.Errorf("%s body lost the message: %s", name, body)
		}
	}
}
