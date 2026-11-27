package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// fakeDatasetImporter counts the JSONL rows of From.
type fakeDatasetImporter struct{ from string }

func (f *fakeDatasetImporter) Info() importers.Info {
	return importers.Info{Name: "fakeds", Summary: "test importer", Source: "a jsonl"}
}

func (f *fakeDatasetImporter) Import(_ context.Context, o importers.Options) (*importers.Result, error) {
	f.from = o.From
	b, err := os.ReadFile(o.From)
	if err != nil {
		return nil, err
	}
	res := &importers.Result{Suite: o.SuiteID}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		res.Tasks = append(res.Tasks, line)
	}
	return res, nil
}

var fakeDS = &fakeDatasetImporter{}

func init() { importers.Register(fakeDS) }

func TestBenchCatalogUploadThenImport(t *testing.T) {
	if projectfs.MaxUploadFileBytes != lib.MaxUploadBytes {
		t.Fatalf("uploads per-file cap %d != upload body limit %d", projectfs.MaxUploadFileBytes, lib.MaxUploadBytes)
	}
	root, project := t.TempDir(), t.TempDir()
	catalogWrite(t, filepath.Join(root, "agents", "noop.yaml"), "id: noop\n")
	catalogWrite(t, filepath.Join(root, "benchmarks", "x", "suite.yaml"), "id: x\n")
	store, err := projectfs.Open(project)
	if err != nil {
		t.Fatal(err)
	}
	fj := &catalogFakeJobs{}
	h := &benchCatalogHandler{
		env:    OrgEnv{Cfg: &lib.Config{Root: project}, Mode: schemas.ModeLive, Services: Services{OrgID: "org_a", Jobs: fj, Projects: store}},
		source: func() benchcatalog.Source { return benchcatalog.Source{Root: root} },
	}

	// The card states the upload option.
	ctx := &fasthttp.RequestCtx{}
	h.list(ctx)
	var list BenchCatalogList
	_ = json.Unmarshal(ctx.Response.Body(), &list)
	var card benchcatalog.Entry
	for _, e := range list.Entries {
		if e.ID == "import-fakeds" {
			card = e.Entry
		}
	}
	if card.UploadExpects == "" || card.DatasetPath != "uploads/datasets/import-fakeds" || !card.Available {
		t.Fatalf("card = %+v", card)
	}

	// Upload a JSONL file as multipart.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "rows.jsonl")
	fw.Write([]byte("{\"id\":\"a\"}\n{\"id\":\"b\"}\n"))
	mw.Close()
	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "import-fakeds")
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.Header.SetContentType(mw.FormDataContentType())
	ctx.Request.SetBody(body.Bytes())
	h.dataset(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("upload: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var up BenchCatalogDataset
	_ = json.Unmarshal(ctx.Response.Body(), &up)
	if up.From != "uploads/datasets/import-fakeds/rows.jsonl" {
		t.Fatalf("from = %q", up.From)
	}

	// Import with that path: the job is created with it.
	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "import-fakeds")
	ctx.Request.SetBody([]byte(`{"from":"` + up.From + `","limit":2}`))
	h.add(ctx)
	if ctx.Response.StatusCode() != 202 || len(fj.created) != 1 {
		t.Fatalf("add: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	// Run the job's import step (hosted confinement) with the fake importer.
	var p benchcatalog.ImportParams
	_ = json.Unmarshal(fj.created[0].Params, &p)
	src := benchcatalog.Source{Root: root}
	imp, from, err := src.Normalize(project, &p, true)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	res, err := benchcatalog.Import(context.Background(), imp, project, "", from, p, nil)
	if err != nil || len(res.Tasks) != 2 || !strings.HasSuffix(fakeDS.from, "uploads/datasets/import-fakeds/rows.jsonl") {
		t.Fatalf("import: %+v %v from=%s", res, err, fakeDS.from)
	}

	// URL fetch: a private host is refused when the job is created.
	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "import-fakeds")
	ctx.Request.SetBody([]byte(`{"url":"https://169.254.169.254/latest"}`))
	h.add(ctx)
	if ctx.Response.StatusCode() != 202 {
		t.Fatalf("url add should create a job (the job kind validates): %d", ctx.Response.StatusCode())
	}
	var up2 benchcatalog.ImportParams
	_ = json.Unmarshal(fj.created[1].Params, &up2)
	if _, _, err := src.Normalize(project, &up2, true); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("private url accepted: %v", err)
	}

	// Empty body: next step named.
	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "import-fakeds")
	h.add(ctx)
	if ctx.Response.StatusCode() != 400 || !strings.Contains(string(ctx.Response.Body()), "upload") {
		t.Fatalf("no data: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}
