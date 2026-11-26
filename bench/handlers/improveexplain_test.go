package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/valyala/fasthttp"
)

// explainSource is an improvement source that explains version 2 only.
type explainSource struct{ ImproveSource }

func (explainSource) Explain(_ context.Context, id string, n int) (*improve.Explain, error) {
	if n != 2 {
		return nil, fmt.Errorf("version %d: %w", n, improve.ErrNotFound)
	}
	return &improve.Explain{Ref: "agents/a.yaml@v2", Round: 1, Files: []improve.ExplainFile{}, Gates: []improve.GateResult{}}, nil
}

// plainSource has no Explain method.
type plainSource struct{ ImproveSource }

func explainCall(t *testing.T, src ImproveSource, path string) (int, string) {
	t.Helper()
	old := OrgImproveSource
	t.Cleanup(func() { OrgImproveSource = old })
	OrgImproveSource = nil
	if src != nil {
		OrgImproveSource = func(OrgEnv) ImproveSource { return src }
	}
	r := router.New()
	registerImproveExplain(OrgEnv{}, r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI(path)
	r.Handler(ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body())
}

func TestImproveExplainRoute(t *testing.T) {
	code, body := explainCall(t, explainSource{}, APIPrefix+"/improvements/imp_x-abc123/versions/2/explain")
	if code != fasthttp.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	var x improve.Explain
	if err := json.Unmarshal([]byte(body), &x); err != nil || x.Ref != "agents/a.yaml@v2" {
		t.Fatalf("body %s: %v", body, err)
	}
	if code, body := explainCall(t, explainSource{}, APIPrefix+"/improvements/imp_x-abc123/versions/7/explain"); code != fasthttp.StatusNotFound {
		t.Fatalf("unknown version: %d %s", code, body)
	}
	if code, _ := explainCall(t, explainSource{}, APIPrefix+"/improvements/imp_x-abc123/versions/x/explain"); code != fasthttp.StatusBadRequest {
		t.Fatalf("bad version: %d", code)
	}
	if code, _ := explainCall(t, explainSource{}, APIPrefix+"/improvements/..%2Fetc/versions/2/explain"); code != fasthttp.StatusNotFound {
		t.Fatalf("bad id: %d", code)
	}
}

func TestImproveExplainNotImplemented(t *testing.T) {
	for name, src := range map[string]ImproveSource{"none": nil, "no explain": plainSource{}} {
		code, body := explainCall(t, src, APIPrefix+"/improvements/imp_x-abc123/versions/2/explain")
		if code != fasthttp.StatusNotImplemented || !strings.Contains(body, "This server cannot explain improvement versions.") {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
}
