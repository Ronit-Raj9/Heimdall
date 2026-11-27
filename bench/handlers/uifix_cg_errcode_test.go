package handlers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/valyala/fasthttp"
)

// TestKeyRefusalCode: a model key refusal keeps its type, message and link
// and adds code no_model_key or budget_reached (fix wave M1-44).
func TestKeyRefusalCode(t *testing.T) {
	l := &fakeLauncher{}
	r, _ := quickRunServer(t, Services{OrgID: "org_a", Hosted: true, Launcher: l, Keys: keystore.NewMemory()})
	resp := postQuick(r, `{"model":{"provider":"openai","model":"gpt-x"},"start":false}`)
	body := string(resp.Response.Body())
	for _, want := range []string{`"type":"conflict"`, `"link":"/models"`, `"code":"no_model_key"`, "no working openai key"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	var ctx fasthttp.RequestCtx
	SendModelKeyRefusal(&ctx, testMode, fmt.Errorf("x: %w", ErrKeyOverBudget))
	if b := string(ctx.Response.Body()); !strings.Contains(b, `"code":"budget_reached"`) || !strings.Contains(b, `"type":"conflict"`) {
		t.Fatalf("over budget: %s", b)
	}
}
