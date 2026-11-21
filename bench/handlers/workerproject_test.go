package handlers

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/valyala/fasthttp"
)

func TestWorkerProject(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "benchmarks", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "benchmarks", "s", "suite.yaml"), []byte("id: s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &workerProjectHandler{mode: schemas.ModeLive, orgID: "org_a", root: root}
	call := func(p *auth.Principal, etag string) *fasthttp.RequestCtx {
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod("GET")
		ctx.Request.SetRequestURI(WorkerProjectPath)
		if etag != "" {
			ctx.Request.Header.Set("If-None-Match", etag)
		}
		if p != nil {
			auth.SetPrincipal(ctx, p)
		}
		h.get(ctx)
		return ctx
	}
	if c := call(&auth.Principal{Kind: auth.KindUser, UserID: "u", OrgID: "org_a", Role: auth.RoleOwner}, ""); c.Response.StatusCode() != 403 {
		t.Fatalf("user fetched the project: %d", c.Response.StatusCode())
	}
	if c := call(&auth.Principal{Kind: auth.KindRunner, OrgID: "org_b"}, ""); c.Response.StatusCode() != 403 {
		t.Fatalf("another org's runner fetched the project: %d", c.Response.StatusCode())
	}
	runner := &auth.Principal{Kind: auth.KindRunner, OrgID: "org_a"}
	c := call(runner, "")
	if c.Response.StatusCode() != 200 {
		t.Fatalf("runner: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	var body bytes.Buffer
	if err := c.Response.BodyWriteTo(&body); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := sharedpool.ExtractProject(&body, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "benchmarks", "s", "suite.yaml")); err != nil {
		t.Fatal("suite missing from the archive")
	}
	etag := string(c.Response.Header.Peek("ETag"))
	if c := call(runner, etag); etag == "" || c.Response.StatusCode() != 304 {
		t.Fatalf("etag %q: %d", etag, c.Response.StatusCode())
	}
}
