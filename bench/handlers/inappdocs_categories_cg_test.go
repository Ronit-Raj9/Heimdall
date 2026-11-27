package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestInAppDocsCategories: GET /docs gives every guide a category and the
// category index (fix wave M8-64); the public site's list stays without.
func TestInAppDocsCategories(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	listDocs(ctx, schemas.ModeLive)
	var out DocList
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Categories) == 0 || out.Categories[0].ID != "start" {
		t.Fatalf("categories %+v", out.Categories)
	}
	for _, d := range out.Docs {
		if d.Category == "" {
			t.Fatalf("%s has no category", d.Name)
		}
		if d.Name == "web-quickstart" && (d.Category != "start" || d.StartRank == nil || *d.StartRank != 0) {
			t.Fatalf("web-quickstart %+v", d)
		}
	}
	pub := &fasthttp.RequestCtx{}
	pub.Request.SetRequestURI(PublicPrefix + "/docs")
	NewPublicSiteHandler(PublicSiteConfig{Mode: schemas.ModeLive}).docs(pub)
	if body := string(pub.Response.Body()); strings.Contains(body, `"category"`) || strings.Contains(body, `"categories"`) {
		t.Fatalf("public docs list got categories: %.200s", body)
	}
}
