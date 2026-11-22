package handlers_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/dist"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func publicSite(t *testing.T, c handlers.PublicSiteConfig) func(method, path string, hdr ...string) *fasthttp.Response {
	t.Helper()
	if c.Mode == "" {
		c.Mode = schemas.ModeLive
	}
	r := router.New()
	handlers.NewPublicSiteHandler(c).RegisterRoutes(r)
	h := r.Handler
	return func(method, path string, hdr ...string) *fasthttp.Response {
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod(method)
		ctx.Request.SetRequestURI(path)
		for i := 0; i+1 < len(hdr); i += 2 {
			ctx.Request.Header.Set(hdr[i], hdr[i+1])
		}
		h(ctx)
		out := &fasthttp.Response{}
		body := append([]byte(nil), ctx.Response.Body()...) // reads a body stream
		ctx.Response.CopyTo(out)
		out.SetBody(body)
		return out
	}
}

func pubDecode[T any](t *testing.T, r *fasthttp.Response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.Body(), &v); err != nil {
		t.Fatalf("decode %s: %v", r.Body(), err)
	}
	return v
}

func TestPublicSiteDocsLeaveInternalOut(t *testing.T) {
	get := publicSite(t, handlers.PublicSiteConfig{})
	r := get("GET", handlers.PublicPrefix+"/docs")
	if r.StatusCode() != 200 {
		t.Fatalf("docs: %d %s", r.StatusCode(), r.Body())
	}
	list := pubDecode[handlers.DocList](t, r)
	names := map[string]bool{}
	for _, d := range list.Docs {
		names[d.Name] = true
	}
	for _, want := range []string{"quickstart", "ci", "glossary"} {
		if !names[want] {
			t.Errorf("public docs lack %s", want)
		}
	}
	for _, internal := range []string{"DESIGN", "platform-design", "spike", "CLI_WEB_PARITY", "sources"} {
		if names[internal] {
			t.Errorf("public docs list internal %s", internal)
		}
		if r := get("GET", handlers.PublicPrefix+"/docs/"+internal); r.StatusCode() != 404 {
			t.Errorf("internal %s served: %d", internal, r.StatusCode())
		}
	}
	if r := get("GET", handlers.PublicPrefix+"/docs/quickstart"); r.StatusCode() != 200 || !strings.Contains(string(r.Body()), `"markdown"`) {
		t.Fatalf("quickstart: %d", r.StatusCode())
	}
	if r := get("GET", handlers.PublicPrefix+"/docs/..%2Fgo.mod"); r.StatusCode() == 200 {
		t.Fatal("path escape served")
	}
}

func TestPublicSitePricingDefaultsToBeta(t *testing.T) {
	get := publicSite(t, handlers.PublicSiteConfig{})
	p := pubDecode[handlers.PublicPricing](t, get("GET", handlers.PublicPrefix+"/pricing"))
	if !p.Beta || len(p.Plans) != 1 || p.Plans[0].Price != "Free" || !strings.Contains(p.Headline, "own model key") {
		t.Fatalf("beta pricing: %+v", p)
	}
	get = publicSite(t, handlers.PublicSiteConfig{Pricing: func() handlers.PublicPricing {
		return handlers.PublicPricing{Headline: "Plans", Plans: []handlers.PublicPlan{{ID: "team", Name: "Team"}}}
	}})
	p = pubDecode[handlers.PublicPricing](t, get("GET", handlers.PublicPrefix+"/pricing"))
	if p.Beta || len(p.Plans) != 1 || p.Plans[0].ID != "team" || p.FAQ == nil {
		t.Fatalf("plan source: %+v", p)
	}
}

func TestPublicSiteLibraryAndOverview(t *testing.T) {
	lib := handlers.PublicLibrary{Available: true, Totals: handlers.PublicLibraryTotals{Benchmarks: 2, Tasks: 90, Ready: 80, Packs: 1},
		Benchmarks: []handlers.PublicBenchmark{{ID: "hd-a", TaskCount: 50, ReadyCount: 45, Licenses: []string{"MIT"}, Packs: []string{"coding"}}}}
	get := publicSite(t, handlers.PublicSiteConfig{Version: "9.9.9", Library: func(context.Context) (handlers.PublicLibrary, error) { return lib, nil }})
	got := pubDecode[handlers.PublicLibrary](t, get("GET", handlers.PublicPrefix+"/benchmarks"))
	if !got.Available || got.Totals.Tasks != 90 || got.Benchmarks[0].Licenses[0] != "MIT" {
		t.Fatalf("library: %+v", got)
	}
	ov := pubDecode[handlers.PublicOverview](t, get("GET", handlers.PublicPrefix+"/overview"))
	if ov.Library.Ready != 80 || ov.Version != "9.9.9" || ov.Downloads {
		t.Fatalf("overview: %+v", ov)
	}
	// No library source: an empty, not missing, list.
	get = publicSite(t, handlers.PublicSiteConfig{})
	got = pubDecode[handlers.PublicLibrary](t, get("GET", handlers.PublicPrefix+"/benchmarks"))
	if got.Available || got.Benchmarks == nil {
		t.Fatalf("no library: %+v", got)
	}
}

func TestPublicSiteDownloads(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bifrost-bench-linux-amd64", "ELF binary")
	write("heimdall_bench-0.2.0-py3-none-any.whl", "PK wheel")
	write("bench-0123abcd.tar.gz", "source")
	if _, err := dist.WriteManifest(dir, "0123abcd", "0.1.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	write("secret.env", "not listed") // on disk, never listed: refused
	get := publicSite(t, handlers.PublicSiteConfig{DownloadsDir: dir, PublicURL: "https://heimdall.example.com/", MCP: true, GitLabTemplate: []byte("x:\n  HEIMDALL_URL: \"\"\n")})

	m := pubDecode[handlers.DownloadsManifest](t, get("GET", handlers.PublicPrefix+"/downloads"))
	if !m.Available || m.Commit != "0123abcd" || len(m.Files) != 3 || m.MCPURL != "https://heimdall.example.com/mcp" ||
		m.GitLabURL != "https://heimdall.example.com/downloads/heimdall.gitlab-ci.yml" {
		t.Fatalf("manifest: %+v", m)
	}
	r := get("GET", "/downloads/bifrost-bench-linux-amd64")
	if r.StatusCode() != 200 || string(r.Body()) != "ELF binary" || !strings.Contains(string(r.Header.Peek("Content-Disposition")), "attachment") {
		t.Fatalf("binary: %d %q", r.StatusCode(), r.Body())
	}
	etag := string(r.Header.Peek("ETag"))
	if r := get("GET", "/downloads/bifrost-bench-linux-amd64", "If-None-Match", etag); r.StatusCode() != 304 {
		t.Fatalf("conditional: %d", r.StatusCode())
	}
	sums := get("GET", "/downloads/SHA256SUMS")
	if sums.StatusCode() != 200 || !strings.Contains(string(sums.Body()), "  bifrost-bench-linux-amd64\n") {
		t.Fatalf("sums: %d %s", sums.StatusCode(), sums.Body())
	}
	// The worker install script checks each download against <file>.sha256.
	if r := get("GET", "/downloads/bifrost-bench-linux-amd64.sha256"); r.StatusCode() != 200 ||
		!strings.HasPrefix(string(r.Body()), strings.Trim(etag, `"`)+"  bifrost-bench-linux-amd64") {
		t.Fatalf("sha256 sidecar: %d %q", r.StatusCode(), r.Body())
	}
	if r := get("GET", "/downloads/secret.env.sha256"); r.StatusCode() != 404 {
		t.Fatalf("unlisted sidecar: %d", r.StatusCode())
	}
	if r := get("GET", "/downloads/bench-0123abcd.tar.gz"); r.StatusCode() != 200 || string(r.Header.Peek("Content-Type")) != "application/gzip" {
		t.Fatalf("source: %d %s", r.StatusCode(), r.Header.Peek("Content-Type"))
	}
	for _, bad := range []string{"secret.env", "..%2Fsecret.env", ".hidden", "missing-file"} {
		if r := get("GET", "/downloads/"+bad); r.StatusCode() != 404 {
			t.Errorf("%s: %d", bad, r.StatusCode())
		}
	}
	gl := get("GET", "/downloads/heimdall.gitlab-ci.yml")
	if gl.StatusCode() != 200 || !strings.Contains(string(gl.Body()), `HEIMDALL_URL: "https://heimdall.example.com"`) {
		t.Fatalf("gitlab template: %d %s", gl.StatusCode(), gl.Body())
	}

	// No downloads folder: an empty manifest and 404s.
	get = publicSite(t, handlers.PublicSiteConfig{})
	if m := pubDecode[handlers.DownloadsManifest](t, get("GET", handlers.PublicPrefix+"/downloads")); m.Available || m.Files == nil {
		t.Fatalf("empty manifest: %+v", m)
	}
	if r := get("GET", "/downloads/bifrost-bench-linux-amd64"); r.StatusCode() != 404 {
		t.Fatalf("no downloads: %d", r.StatusCode())
	}
}

func TestPublicSiteSecurityTxt(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	get := publicSite(t, handlers.PublicSiteConfig{PublicURL: "https://heimdall.example.com", Now: func() time.Time { return now }})
	r := get("GET", handlers.SecurityTxtPath)
	body := string(r.Body())
	for _, want := range []string{"Contact: https://heimdall.example.com/security\n", "Expires: 2027-05-22T00:00:00Z\n", "Canonical: https://heimdall.example.com/.well-known/security.txt\n", "Policy: https://heimdall.example.com/security\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("security.txt lacks %q:\n%s", want, body)
		}
	}
	get = publicSite(t, handlers.PublicSiteConfig{PublicURL: "https://h.example", SecurityContact: "mailto:security@h.example"})
	if !strings.Contains(string(get("GET", handlers.SecurityTxtPath).Body()), "Contact: mailto:security@h.example\n") {
		t.Error("configured contact not used")
	}
	if r := publicSite(t, handlers.PublicSiteConfig{})("GET", handlers.SecurityTxtPath); r.StatusCode() != 404 {
		t.Errorf("no contact: %d", r.StatusCode())
	}
}
