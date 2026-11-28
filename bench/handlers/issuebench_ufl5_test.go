package handlers

import (
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// The Past scans list of the Build tab (round 5, L5) reads GET
// /issuebench/runs as {"runs":[...]} with id, repo, status and created, and
// its Stop build button posts .../cancel with no body.
func TestIssueBenchPastScansFromTheWeb(t *testing.T) {
	h := &issueBenchHandler{mode: schemas.ModeLive, m: pipeline.NewManager(t.TempDir(), pipeline.Stages{Scanner: ibScan{}})}
	r := router.New()
	r.GET(issueBenchPrefix, h.list)
	r.POST(issueBenchPrefix, h.create)
	r.POST(issueBenchPrefix+"/{id}/cancel", h.cancel)
	code, s := ibDo(t, r, "POST", issueBenchPrefix, `{"repo":"a/b"}`)
	if code != 201 {
		t.Fatalf("scan: %d", code)
	}
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI(issueBenchPrefix)
	r.Handler(&ctx)
	var list struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal(ctx.Response.Body(), &list); err != nil || ctx.Response.StatusCode() != 200 || len(list.Runs) != 1 {
		t.Fatalf("list: %d %s %v", ctx.Response.StatusCode(), ctx.Response.Body(), err)
	}
	for _, k := range []string{"id", "repo", "status", "created"} {
		if _, ok := list.Runs[0][k]; !ok {
			t.Errorf("list row has no %q: %v", k, list.Runs[0])
		}
	}
	code, c := ibDo(t, r, "POST", issueBenchPrefix+"/"+s.ID+"/cancel", "")
	if code != 200 || c.ID != s.ID || c.Status != pipeline.RunScanned {
		t.Fatalf("cancel of a scanned run: %d %+v", code, c)
	}
	if code, _ := ibDo(t, r, "POST", issueBenchPrefix+"/nope/cancel", ""); code != 404 {
		t.Fatalf("cancel of an unknown run: %d", code)
	}
}
