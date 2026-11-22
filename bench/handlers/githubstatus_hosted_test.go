package handlers

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// TestGitHubStatusHosted (fix wave 2, N67): GET /github/status says the
// server is hosted, so Compare two git refs never offers the project's own
// git repository (a hosted project has none). The GitHub routes are served
// only by the hosted platform router, so every answer carries it.
func TestGitHubStatusHosted(t *testing.T) {
	h := NewGitHubHandler(testMode, auth.NewGitHubRepoConnector(auth.Config{}), nil, "no key store in this test", nil)
	ctx := &fasthttp.RequestCtx{}
	tenancy.SetOrg(ctx, &tenancy.Org{ID: "org_1"})
	h.getStatus(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var st map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &st); err != nil {
		t.Fatal(err)
	}
	if st["hosted"] != true {
		t.Fatalf("hosted = %v, want true (body %s)", st["hosted"], ctx.Response.Body())
	}
}
