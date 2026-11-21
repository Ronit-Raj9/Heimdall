package handlers_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func manifestRouter(t *testing.T, root string, st projectfs.Store) *router.Router {
	t.Helper()
	env := handlers.OrgEnv{Cfg: &lib.Config{Root: root}, Mode: schemas.ModeRecorded, Services: handlers.Services{Projects: st}}
	r := router.New()
	for _, a := range handlers.OrgAreas() {
		a(env, r)
	}
	return r
}

func getManifest(r *router.Router, query string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI(handlers.ProjectManifestPath + query)
	r.Handler(ctx)
	return ctx
}

func TestProjectManifest(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "agents", "sub"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "agents", ".heimdall"), 0o755))
	must(os.WriteFile(filepath.Join(root, "agents", "sub", "a.yaml"), []byte("abc"), 0o644))
	must(os.WriteFile(filepath.Join(root, "agents", ".heimdall", "v"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644))
	must(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "agents", "leak")))
	st, err := projectfs.Open(root)
	must(err)
	r := manifestRouter(t, root, st)

	ctx := getManifest(r, "")
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var m handlers.ProjectManifest
	must(json.Unmarshal(ctx.Response.Body(), &m))
	if len(m.Files) != 1 || m.Files[0].Path != "sub/a.yaml" || m.Files[0].Area != "agents" ||
		m.Files[0].SHA256 != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" || m.Files[0].Size != 3 {
		t.Fatalf("manifest = %+v", m.Files)
	}
	if c := getManifest(r, "?area=nope"); c.Response.StatusCode() != 400 {
		t.Errorf("bad area status %d", c.Response.StatusCode())
	}
	if c := getManifest(r, "?area=policies"); c.Response.StatusCode() != 200 {
		t.Errorf("empty area status %d", c.Response.StatusCode())
	}
	if c := getManifest(manifestRouter(t, root, nil), ""); c.Response.StatusCode() != 503 {
		t.Errorf("nil store status %d", c.Response.StatusCode())
	}
}
