package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// publicPaths are the pages people reach without the sidebar: the public
// site, sign in, share links, invitations and the CLI device page.
var publicPaths = []string{
	"/", "/login", "/signup", "/docs", "/quickstart", "/reference", "/status",
	"/s/org_a.shr_b", "/invite/inv_abc123", "/device", "/device?code=ABCD-EFGH",
}

// navPaths reads the sidebar targets ("to:" entries) from the UI's navigation
// constants, so a new sidebar page is checked without editing this test.
func navPaths(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "ui", "lib", "constants", "navigation.ts"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`\bto: "(/[^"?]*)`).FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	if len(out) < 10 {
		t.Fatalf("found only %d sidebar paths in navigation.ts: %v", len(out), out)
	}
	return out
}

// aliasPaths reads the alias addresses (PATH_ALIASES keys) that the app's
// not-found page redirects.
func aliasPaths(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "ui", "lib", "routing", "pathAliases.ts"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "PATH_ALIASES")
	end := strings.Index(body[start:], "};")
	if start < 0 || end < 0 {
		t.Fatal("PATH_ALIASES not found in pathAliases.ts")
	}
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^\s*"(/[^"]*)":`).FindAllStringSubmatch(body[start:start+end], -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("no aliases parsed from pathAliases.ts")
	}
	return out
}

type pathWant struct {
	path   string
	status int
	shell  bool
}

func publicPathCases(t *testing.T) []pathWant {
	var cases []pathWant
	for _, p := range publicPaths {
		cases = append(cases, pathWant{p, 200, true})
	}
	for _, p := range navPaths(t) {
		cases = append(cases, pathWant{p, 200, true})
	}
	for _, p := range aliasPaths(t) {
		cases = append(cases, pathWant{p, 404, true})
	}
	cases = append(cases,
		pathWant{"/assets/app-1.js", 200, false},
		pathWant{"/zz-no-such-page", 404, true},
	)
	return cases
}

func checkPathAnswer(t *testing.T, ctx *fasthttp.RequestCtx, w pathWant) {
	t.Helper()
	if got := ctx.Response.StatusCode(); got != w.status {
		t.Fatalf("GET %s = %d, want %d: %.200s", w.path, got, w.status, ctx.Response.Body())
	}
	isShell := strings.Contains(string(ctx.Response.Body()), "<title>bench</title>")
	if isShell != w.shell {
		t.Errorf("GET %s: app shell = %v, want %v", w.path, isShell, w.shell)
	}
}

// TestPublicAndSidebarPathsAnswer200 checks, after the 404 rule for unknown
// first segments (N150), that every public page, every sidebar page and the
// static assets still answer 200, that each alias address (N129) answers 404
// with the app shell (the app then redirects it), and that no alias shadows a
// real route. Local mode, UI router alone.
func TestPublicAndSidebarPathsAnswer200(t *testing.T) {
	for _, a := range aliasPaths(t) {
		if spaRoots[strings.SplitN(strings.TrimPrefix(a, "/"), "/", 2)[0]] {
			t.Errorf("alias %s is a real UI route in spaRoots; the app would never redirect it", a)
		}
	}
	r := router.New()
	NewUIHandler(schemas.ModeLive, testUI()).RegisterRoutes(r)
	for _, w := range publicPathCases(t) {
		t.Run(w.path, func(t *testing.T) {
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(w.path)
			r.Handler(ctx)
			checkPathAnswer(t, ctx, w)
		})
	}
}

// TestHostedPublicAndSidebarPathsAnswer200 is the same check through the whole
// hosted server (dispatcher, platform routes, UI fallback), plus /healthz and
// a download.
func TestHostedPublicAndSidebarPathsAnswer200(t *testing.T) {
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
	cases := append(publicPathCases(t),
		pathWant{"/healthz", 200, false},
		pathWant{"/downloads/manifest.json", 200, false},
	)
	for _, w := range cases {
		t.Run(w.path, func(t *testing.T) {
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(w.path)
			ctx.Request.Header.SetHost("heimdall.test")
			s.handler(ctx)
			checkPathAnswer(t, ctx, w)
		})
	}
}
