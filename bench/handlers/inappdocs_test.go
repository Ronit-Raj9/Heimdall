package handlers

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func docCtx(name string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue("name", name)
	return ctx
}

func TestInAppDocsList(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	listDocs(ctx, schemas.ModeLive)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d", ctx.Response.StatusCode())
	}
	var out DocList
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range out.Docs {
		if d.Name == "quickstart" {
			found = d.Title != "" && d.Title != "quickstart"
		}
	}
	if !found {
		t.Fatalf("quickstart guide with a heading title missing from %d docs", len(out.Docs))
	}
}

func TestInAppDocsGet(t *testing.T) {
	for _, name := range []string{"quickstart", "quickstart.md"} {
		ctx := docCtx(name)
		getDoc(ctx, schemas.ModeLive)
		if ctx.Response.StatusCode() != 200 {
			t.Fatalf("%s: status %d", name, ctx.Response.StatusCode())
		}
		var out DocBody
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil || out.Markdown == "" || out.Name != "quickstart" {
			t.Fatalf("%s: bad body %+v %v", name, out.Name, err)
		}
	}
}

func TestInAppDocsRejectsTraversal(t *testing.T) {
	cases := map[string]int{
		"../go.mod":     400,
		"..":            400,
		"a/b":           400,
		"%2e%2e":        400,
		"":              400,
		"embed.go":      404,
		"no-such-guide": 404,
	}
	for name, want := range cases {
		ctx := docCtx(name)
		getDoc(ctx, schemas.ModeLive)
		if got := ctx.Response.StatusCode(); got != want {
			t.Errorf("%q: status %d, want %d", name, got, want)
		}
	}
}
