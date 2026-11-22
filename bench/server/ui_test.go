package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

func TestUIHandler(t *testing.T) {
	tests := []struct {
		name         string
		dist         fstest.MapFS
		path         string
		status       int
		contains     string
		cacheControl string
	}{
		{"index", testUI(), "/", 200, "<title>bench</title>", "no-cache"},
		{"spa fallback", testUI(), "/runs/r_1", 200, "<title>bench</title>", "no-cache"},
		{"asset", testUI(), "/assets/app-1.js", 200, "console.log", "immutable"},
		{"missing asset", testUI(), "/assets/x.css", 404, "not found", ""},
		{"traversal stays inside the UI", testUI(), "/../../etc/passwd", 404, "<title>bench</title>", "no-cache"}, // "etc" is no UI route (N150)
		{"api path", testUI(), "/api/other", 404, `"type":"not_found"`, ""},
		{"not built", fstest.MapFS{".gitkeep": {}}, "/", 404, "UI not built", ""},
		{"fixture data never served", withFixture(testUI()), "/fixtures/experiments.list.json", 404, "not found", ""},
		{"fixture folder never served", withFixture(testUI()), "/fixtures", 404, "not found", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := router.New()
			NewUIHandler(schemas.ModeLive, tt.dist).RegisterRoutes(r)
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(tt.path)
			r.Handler(ctx)
			if ctx.Response.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", ctx.Response.StatusCode(), tt.status, ctx.Response.Body())
			}
			if !strings.Contains(string(ctx.Response.Body()), tt.contains) {
				t.Errorf("body %q lacks %q", ctx.Response.Body(), tt.contains)
			}
			if tt.cacheControl != "" && !strings.Contains(string(ctx.Response.Header.Peek("Cache-Control")), tt.cacheControl) {
				t.Errorf("cache-control = %q", ctx.Response.Header.Peek("Cache-Control"))
			}
		})
	}
}

// withFixture adds a fixture file, as a UI build that kept public/fixtures has.
func withFixture(m fstest.MapFS) fstest.MapFS {
	m["fixtures/experiments.list.json"] = &fstest.MapFile{Data: []byte(`{"experiments":[{"id":"e_synthetic-codewins"}]}`)}
	return m
}

// TestShareLinkWithDotServesApp checks that a client-side route whose last
// segment contains a dot (a share token such as org_a.shr_b) gets the app
// shell, while a missing asset file still answers 404.
func TestShareLinkWithDotServesApp(t *testing.T) {
	tests := []struct {
		path     string
		status   int
		contains string
	}{
		{"/s/org_a.shr_b", 200, "<title>bench</title>"},
		{"/s/org_x.shr_y/", 200, "<title>bench</title>"},
		{"/assets/missing.js", 404, "not found"},
		{"/assets/org_a.shr_b", 404, "not found"},
		{"/missing.css", 404, "not found"},
		{"/favicon.ico", 404, "not found"},
		{"/robots.txt", 404, "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			r := router.New()
			NewUIHandler(schemas.ModeLive, testUI()).RegisterRoutes(r)
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(tt.path)
			r.Handler(ctx)
			if ctx.Response.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", ctx.Response.StatusCode(), tt.status, ctx.Response.Body())
			}
			if !strings.Contains(string(ctx.Response.Body()), tt.contains) {
				t.Errorf("body %q lacks %q", ctx.Response.Body(), tt.contains)
			}
		})
	}
}

// TestUnknownRootIs404WithShell checks that a path whose first segment is no
// UI route answers 404 but still with the app shell, so the app shows its own
// "Page not found" (and its alias redirects), while known routes stay 200 (N150).
func TestUnknownRootIs404WithShell(t *testing.T) {
	tests := []struct {
		path   string
		status int
	}{
		{"/zz-no-such-page", 404},
		{"/zz-no-such-page/deeper", 404},
		{"/", 200},
		{"/runs/r_x", 200},
		{"/s/org_a.shr_b", 200},
		{"/agents/acme/setup", 200},
		{"/failure-tests", 200},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			r := router.New()
			NewUIHandler(schemas.ModeLive, testUI()).RegisterRoutes(r)
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(tt.path)
			r.Handler(ctx)
			if ctx.Response.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d", ctx.Response.StatusCode(), tt.status)
			}
			if !strings.Contains(string(ctx.Response.Body()), "<title>bench</title>") {
				t.Errorf("body %q is not the app shell", ctx.Response.Body())
			}
		})
	}
}

// TestSPARootsMatchUIRoutes keeps spaRoots equal to the route folders under
// ui/app: a folder ending in "_" is the same first segment as its base, the
// "index" folder is "/", and "__" files are not routes.
func TestSPARootsMatchUIRoutes(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "ui", "app"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"": true}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, "__") {
			continue
		}
		name = strings.TrimSuffix(name, "_")
		if name == "index" {
			name = ""
		}
		want[name] = true
	}
	for name := range want {
		if !spaRoots[name] {
			t.Errorf("ui/app has the route %q but spaRoots lacks it: add it to spaRoots in server/ui.go", name)
		}
	}
	for name := range spaRoots {
		if !want[name] {
			t.Errorf("spaRoots has %q but ui/app has no such route folder", name)
		}
	}
}

// TestHostedHeadDownloads checks that link checkers get 200 for HEAD on a
// download, an app page and the root, and 404 for an unknown page (N150).
func TestHostedHeadDownloads(t *testing.T) {
	cfg := testConfig()
	cfg.Auth = lib.AuthConfig{Mode: lib.AuthModeHosted, PublicURL: "https://heimdall.test"}
	reg := tenancy.NewRegistry(tenancy.Layout{Hosted: true, StorageRoot: t.TempDir()}, nil, tenancy.Options{})
	ui := testUI()
	ui["downloads/manifest.json"] = &fstest.MapFile{Data: []byte(`{"files":[]}`)}
	s, err := NewHosted(cfg, Hosted{Auth: func(h fasthttp.RequestHandler) fasthttp.RequestHandler { return h }, Directory: noOrgs{},
		Registry: reg, UI: ui})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path   string
		status int
	}{
		{"/downloads/manifest.json", 200},
		{"/", 200},
		{"/runs/r_x", 200},
		{"/zz-no-such-page", 404},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(tt.path)
			ctx.Request.Header.SetHost("heimdall.test")
			ctx.Request.Header.SetMethod(fasthttp.MethodHead)
			s.handler(ctx)
			if ctx.Response.StatusCode() != tt.status {
				t.Fatalf("HEAD %s = %d, want %d: %s", tt.path, ctx.Response.StatusCode(), tt.status, ctx.Response.Body())
			}
			if !ctx.Response.SkipBody {
				t.Error("HEAD answer would carry a body")
			}
		})
	}
}
