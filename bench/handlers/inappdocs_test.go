package handlers

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	benchdocs "github.com/maximhq/bifrost/bench/docs"
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

// TestInAppDocsListsWebGuides pins the website user guides (gap lane G16):
// each is embedded, listed with its heading as the title, and readable.
func TestInAppDocsListsWebGuides(t *testing.T) {
	want := map[string]string{
		"web-quickstart":   "Quickstart on the website",
		"web-agents":       "Agents on the website",
		"web-suites":       "Suites on the website",
		"web-experiments":  "Experiments on the website",
		"web-analysis":     "Analysis and governance on the website",
		"web-keys-secrets": "Secrets and keys on the website",
		"web-runners":      "Runners on the website",
		"web-integrations": "Integrations on the website",
		"web-sync":         "Sync with the CLI",
		"web-admin":        "Server admin on the website",
	}
	ctx := &fasthttp.RequestCtx{}
	listDocs(ctx, schemas.ModeLive)
	var out DocList
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range out.Docs {
		got[d.Name] = d.Title
	}
	for name, title := range want {
		if got[name] != title {
			t.Errorf("guide %s: title %q, want %q", name, got[name], title)
			continue
		}
		g := docCtx(name)
		getDoc(g, schemas.ModeLive)
		if g.Response.StatusCode() != 200 {
			t.Errorf("guide %s: status %d", name, g.Response.StatusCode())
			continue
		}
		var body DocBody
		if err := json.Unmarshal(g.Response.Body(), &body); err != nil || body.Markdown == "" {
			t.Errorf("guide %s: empty body (%v)", name, err)
		}
		if strings.ContainsAny(body.Markdown, "—–") {
			t.Errorf("guide %s: contains an em or en dash", name)
		}
	}
}

// TestInAppDocsHideInternalNotes pins lane X14 (E11): internal design and
// planning notes are neither listed nor served in-app, and no listed guide
// mentions a judge login.
func TestInAppDocsHideInternalNotes(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	listDocs(ctx, schemas.ModeLive)
	var out DocList
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	for _, d := range out.Docs {
		if d.Name == "DESIGN" || d.Name == "spike" || d.Name == "CLI_WEB_PARITY" || strings.HasSuffix(d.Name, "-design") {
			t.Errorf("internal note %s is listed", d.Name)
		}
	}
	for _, name := range []string{"DESIGN", "spike", "CLI_WEB_PARITY", "platform-design", "adoption-design"} {
		g := docCtx(name)
		getDoc(g, schemas.ModeLive)
		if g.Response.StatusCode() != fasthttp.StatusNotFound {
			t.Errorf("internal note %s: status %d, want 404", name, g.Response.StatusCode())
		}
	}
	for _, name := range []string{"web-quickstart", "hosting", "runners"} {
		g := docCtx(name)
		getDoc(g, schemas.ModeLive)
		var body DocBody
		if err := json.Unmarshal(g.Response.Body(), &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		low := strings.ToLower(body.Markdown)
		if strings.Contains(low, "judge login") || strings.Contains(low, "judge account") {
			t.Errorf("guide %s mentions a judge login", name)
		}
	}
}

// TestExtraDocsServed covers G3: the guides link to the agent event schema
// and the acme-node example with paths the server does not serve, so both
// are served as in-app guides by name (in the app and on the public site),
// without being added to the guide list.
func TestExtraDocsServed(t *testing.T) {
	want := map[string][]string{
		"agent-event-schema": {"# Agent event schema", "```json", `"$schema"`},
		"example-acme-node":  {"# Acme example project", "acme-orders"},
	}
	public := NewPublicSiteHandler(PublicSiteConfig{Mode: schemas.ModeLive})
	for name, parts := range want {
		for _, serve := range []struct {
			where string
			fn    func(*fasthttp.RequestCtx)
		}{
			{"app", func(ctx *fasthttp.RequestCtx) { getDoc(ctx, schemas.ModeLive) }},
			{"public", public.doc},
		} {
			ctx := docCtx(name)
			serve.fn(ctx)
			if ctx.Response.StatusCode() != 200 {
				t.Fatalf("%s %s: status %d %s", serve.where, name, ctx.Response.StatusCode(), ctx.Response.Body())
			}
			var out DocBody
			if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil || out.Name != name || out.Title == name {
				t.Fatalf("%s %s: body %+v %v", serve.where, name, out.Name, err)
			}
			for _, p := range parts {
				if !strings.Contains(out.Markdown, p) {
					t.Errorf("%s %s: markdown lacks %q", serve.where, name, p)
				}
			}
		}
	}
	ctx := &fasthttp.RequestCtx{}
	listDocs(ctx, schemas.ModeLive)
	for name := range want {
		if strings.Contains(string(ctx.Response.Body()), `"`+name+`"`) {
			t.Errorf("guide list includes extra %s", name)
		}
	}
}

func docListCtx(q string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI("/api/bench/docs?q=" + url.QueryEscape(q))
	return ctx
}

func decodeDocList(t *testing.T, ctx *fasthttp.RequestCtx) DocList {
	t.Helper()
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var out DocList
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// N138: q searches guide bodies, not titles only, and each hit carries a snippet.
func TestDocsSearchMatchesBody(t *testing.T) {
	ctx := docListCtx("gVisor")
	listDocs(ctx, schemas.ModeLive)
	out := decodeDocList(t, ctx)
	var hosting *DocSummary
	for i := range out.Docs {
		if out.Docs[i].Name == "hosting" {
			hosting = &out.Docs[i]
		}
		if !strings.Contains(strings.ToLower(out.Docs[i].Title+" "+out.Docs[i].Name+" "+out.Docs[i].Snippet), "gvisor") {
			t.Errorf("%s matched q=gVisor without the word in its title, name or snippet", out.Docs[i].Name)
		}
	}
	if hosting == nil {
		t.Fatalf("q=gVisor did not find the hosting guide in %d hits", len(out.Docs))
	}
	if !strings.Contains(strings.ToLower(hosting.Snippet), "gvisor") || len([]rune(hosting.Snippet)) > 170 {
		t.Fatalf("hosting snippet %q", hosting.Snippet)
	}
	all := &fasthttp.RequestCtx{}
	listDocs(all, schemas.ModeLive)
	if n := len(decodeDocList(t, all).Docs); n <= len(out.Docs) {
		t.Fatalf("search returned %d of %d guides; want only matches", len(out.Docs), n)
	}
}

func TestDocsSearchBadQuery(t *testing.T) {
	for _, q := range []string{"g", " g ", strings.Repeat("x", 81)} {
		ctx := docListCtx(q)
		listDocs(ctx, schemas.ModeLive)
		if ctx.Response.StatusCode() != 400 || !strings.Contains(string(ctx.Response.Body()), "q must be 2 to 80 characters") {
			t.Errorf("q=%q: status %d body %s", q, ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
}

// The public list keeps leaving internal notes out when it searches.
func TestPublicDocsSearchSkipsInternal(t *testing.T) {
	h := NewPublicSiteHandler(PublicSiteConfig{Mode: schemas.ModeLive})
	ctx := docListCtx("design")
	h.docs(ctx)
	out := decodeDocList(t, ctx)
	if len(out.Docs) == 0 {
		t.Fatal("q=design found no public guide")
	}
	for _, d := range out.Docs {
		if _, ok := benchdocs.ReadPublic(d.Name); !ok {
			t.Errorf("public search listed internal guide %s", d.Name)
		}
	}
	bad := docListCtx("x")
	h.docs(bad)
	if bad.Response.StatusCode() != 400 {
		t.Fatalf("public q=x: status %d", bad.Response.StatusCode())
	}
}
