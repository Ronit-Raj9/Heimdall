package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type ibScan struct{}

func (ibScan) Scan(ctx context.Context, repo string, o pipeline.ScanOptions) ([]issuebench.Candidate, issuebench.Funnel, error) {
	return []issuebench.Candidate{{Repo: repo, Issue: 7, PR: 8}}, issuebench.Funnel{MergedPRs: 3, Linked: 1, Qualified: 1}, nil
}

func ibDo(t *testing.T, r *router.Router, method, path, body string) (int, pipeline.State) {
	t.Helper()
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	ctx.Request.SetBodyString(body)
	r.Handler(&ctx)
	var s pipeline.State
	_ = json.Unmarshal(ctx.Response.Body(), &s)
	return ctx.Response.StatusCode(), s
}

func TestIssueBenchRoutes(t *testing.T) {
	h := &issueBenchHandler{mode: schemas.ModeLive, m: pipeline.NewManager(t.TempDir(), pipeline.Stages{Scanner: ibScan{}})}
	r := router.New()
	r.POST(issueBenchPrefix, h.create)
	r.GET(issueBenchPrefix+"/{id}", h.get)
	r.POST(issueBenchPrefix+"/{id}/start", h.start)
	r.POST(issueBenchPrefix+"/{id}/issues/{issue}/review", h.review)
	r.POST(issueBenchPrefix+"/{id}/issues/{issue}/retry", h.retry)

	if code, _ := ibDo(t, r, "POST", issueBenchPrefix, `{"repo":"no slash"}`); code != 400 {
		t.Fatalf("bad repo: %d", code)
	}
	code, s := ibDo(t, r, "POST", issueBenchPrefix, `{"repo":"a/b"}`)
	if code != 201 || len(s.Items) != 1 || s.Funnel.MergedPRs != 3 {
		t.Fatalf("scan %d %+v", code, s)
	}
	if code, _ := ibDo(t, r, "POST", issueBenchPrefix+"/"+s.ID+"/start", `{"consent":false}`); code != 400 {
		t.Fatalf("start without consent: %d", code)
	}
	if code, _ := ibDo(t, r, "POST", issueBenchPrefix+"/"+s.ID+"/start", `{"consent":true}`); code != 202 {
		t.Fatalf("start: %d", code)
	}
	h.m.Wait()
	code, s = ibDo(t, r, "GET", issueBenchPrefix+"/"+s.ID+"?since=1", "")
	if code != 200 || s.Items[0].Status != pipeline.StatusFailed || len(s.Log) == 0 || s.Log[0].Seq <= 1 {
		t.Fatalf("get %d %+v", code, s)
	}
	if code, _ := ibDo(t, r, "POST", issueBenchPrefix+"/"+s.ID+"/issues/7/review", `{"decision":"accept"}`); code != 400 {
		t.Fatalf("accept failed: %d", code)
	}
	if code, _ := ibDo(t, r, "POST", issueBenchPrefix+"/"+s.ID+"/issues/x/retry", `{"fix":"retry"}`); code != 400 {
		t.Fatalf("bad issue: %d", code)
	}
	if code, _ := ibDo(t, r, "GET", issueBenchPrefix+"/ib-nope", ""); code != 404 {
		t.Fatalf("missing: %d", code)
	}
}
