package handlers

import (
	"encoding/json"
	"strings"
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
