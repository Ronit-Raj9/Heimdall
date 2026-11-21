package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type catalogFakeJobs struct {
	JobsAPI
	created []jobs.NewJob
}

func (f *catalogFakeJobs) Create(_ context.Context, n jobs.NewJob) (*jobs.Job, error) {
	f.created = append(f.created, n)
	return &jobs.Job{ID: "job_1", OrgID: n.OrgID, Kind: n.Kind}, nil
}

func catalogWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBenchCatalogListAndAdd(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	catalogWrite(t, filepath.Join(root, "agents", "noop.yaml"), "id: noop\n")
	catalogWrite(t, filepath.Join(root, "benchmarks", "coding-core", "suite.yaml"), "id: coding-core\n")
	catalogWrite(t, filepath.Join(root, "examples", "acme-demo", "README.md"), "# Acme demo\n\nDemo.\n")
	catalogWrite(t, filepath.Join(root, "examples", "acme-demo", "benchmarks", "acme-demo", "suite.yaml"), "id: acme-demo\n")
	fj := &catalogFakeJobs{}
	h := &benchCatalogHandler{
		env:    OrgEnv{Cfg: &lib.Config{Root: project}, Mode: schemas.ModeLive, Services: Services{OrgID: "org_a", Jobs: fj}},
		source: func() benchcatalog.Source { return benchcatalog.Source{Root: root} },
	}

	ctx := &fasthttp.RequestCtx{}
	h.list(ctx)
	var list BenchCatalogList
	if err := json.Unmarshal(ctx.Response.Body(), &list); err != nil || !list.SourceRoot {
		t.Fatalf("list: %v %s", err, ctx.Response.Body())
	}
	ids := map[string]bool{}
	for _, e := range list.Entries {
		ids[e.ID] = true
	}
	if !ids["example-acme-demo"] || !ids["suite-coding-core"] {
		t.Fatalf("entries = %v", ids)
	}

	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "example-acme-demo")
	h.add(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("add example: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if _, err := os.Stat(filepath.Join(project, "benchmarks", "acme-demo", "suite.yaml")); err != nil {
		t.Fatal("suite not copied")
	}

	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "nope")
	h.add(ctx)
	if ctx.Response.StatusCode() != 404 {
		t.Fatalf("unknown id: %d", ctx.Response.StatusCode())
	}

	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "example-acme-demo")
	ctx.Request.SetBody([]byte(`{"bogus":1}`))
	h.add(ctx)
	if ctx.Response.StatusCode() != 400 {
		t.Fatalf("unknown field: %d", ctx.Response.StatusCode())
	}
	if len(fj.created) != 0 {
		t.Fatal("copy entries must not create jobs")
	}
}

func TestBenchCatalogImportNeedsFrom(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	catalogWrite(t, filepath.Join(root, "agents", "noop.yaml"), "id: noop\n")
	catalogWrite(t, filepath.Join(root, "benchmarks", "x", "suite.yaml"), "id: x\n")
	h := &benchCatalogHandler{
		env:    OrgEnv{Cfg: &lib.Config{Root: project}, Mode: schemas.ModeLive},
		source: func() benchcatalog.Source { return benchcatalog.Source{Root: root} },
	}
	// No importers are linked into the handlers test binary, so the recipe is unavailable.
	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "terminal-bench-2")
	h.add(ctx)
	if ctx.Response.StatusCode() != 409 || !strings.Contains(string(ctx.Response.Body()), "importer") {
		t.Fatalf("unavailable importer: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}
