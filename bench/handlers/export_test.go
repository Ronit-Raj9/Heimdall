package handlers

import (
	"context"
	"fmt"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type fakeExport struct {
	format string
	allow  bool
}

func (f *fakeExport) ExperimentExport(_ context.Context, expID, format string, allow bool) ([]byte, string, error) {
	f.format, f.allow = format, allow
	switch {
	case expID == "e_none":
		return nil, "", fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	case expID == "e_smoke" && !allow:
		return nil, "", fmt.Errorf("%w: synthetic results", ErrExportRefused)
	}
	return []byte("{}\n"), "application/x-ndjson", nil
}

func (f *fakeExport) SuiteHarbor(_ context.Context, suiteID string) ([]byte, error) {
	if suiteID == "nope" {
		return nil, fmt.Errorf("suite %s: %w", suiteID, schemas.ErrNotFound)
	}
	return []byte{0x1f, 0x8b}, nil
}

func serveExport(src ExportSource, path string) *fasthttp.Response {
	r := router.New()
	NewExportHandler(testMode, src).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("http://127.0.0.1" + path)
	ctx.Request.Header.SetMethod("GET")
	r.Handler(&ctx)
	var resp fasthttp.Response
	ctx.Response.CopyTo(&resp)
	return &resp
}

func TestExportRoutes(t *testing.T) {
	src := &fakeExport{}
	cases := []struct {
		path, disposition, ctype string
		status                   int
	}{
		{"/experiments/e_test/export?format=eee", `attachment; filename="e_test.eee.jsonl"`, "application/x-ndjson", 200},
		{"/experiments/e_test/export?format=jsonl", `attachment; filename="e_test.runs.jsonl"`, "application/x-ndjson", 200},
		{"/experiments/e_test/export?format=csv", `attachment; filename="e_test.runs.csv"`, "application/x-ndjson", 200},
		{"/experiments/e_test/export?format=xml", "", "", 400},
		{"/experiments/e_none/export?format=eee", "", "", 404},
		{"/experiments/e_smoke/export?format=eee", "", "", 409},
		{"/experiments/e_smoke/export?format=eee&allow_synthetic=true", `attachment; filename="e_smoke.eee.jsonl"`, "application/x-ndjson", 200},
		{"/suites/coding-core/export?format=harbor", `attachment; filename="coding-core.harbor.tar.gz"`, "application/gzip", 200},
		{"/suites/coding-core/export?format=zip", "", "", 400},
		{"/suites/nope/export?format=harbor", "", "", 404},
	}
	for _, c := range cases {
		resp := serveExport(src, APIPrefix+c.path)
		if resp.StatusCode() != c.status {
			t.Errorf("GET %s: status %d, want %d: %s", c.path, resp.StatusCode(), c.status, resp.Body())
			continue
		}
		if c.status == 200 {
			if got := string(resp.Header.Peek("Content-Disposition")); got != c.disposition {
				t.Errorf("GET %s: disposition %q", c.path, got)
			}
			if got := string(resp.Header.ContentType()); got != c.ctype {
				t.Errorf("GET %s: content type %q", c.path, got)
			}
		}
	}
	if resp := serveExport(nil, APIPrefix+"/experiments/e_test/export?format=eee"); resp.StatusCode() != 503 {
		t.Errorf("no source: status %d", resp.StatusCode())
	}
}
