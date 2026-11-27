package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type cgOrgKey struct{}

// TestHomeSummary: the summary carries what the routes it reads answer,
// for the caller's own organization (the caller's user values and headers
// reach every inner call), and leaves out a part whose route refuses.
func TestHomeSummary(t *testing.T) {
	r := router.New()
	seen := map[string]any{}
	stub := func(path, body string) {
		r.GET(path, func(ctx *fasthttp.RequestCtx) {
			seen[path] = ctx.UserValue(cgOrgKey{})
			if string(ctx.Request.Header.Peek("X-Heimdall-Org")) != "org_a" {
				ctx.SetStatusCode(403)
				return
			}
			ctx.SetStatusCode(200)
			ctx.SetBodyString(body)
		})
	}
	stub(APIPrefix+"/agents", `{"agents":[{"id":"a"}],"total":3}`)
	stub(APIPrefix+"/suites", `{"suites":[],"total":7}`)
	stub(APIPrefix+"/experiments", `{"experiments":[{"exp_id":"e_1"}],"total":2}`)
	r.GET(APIPrefix+"/evaluations", func(ctx *fasthttp.RequestCtx) {
		if string(ctx.QueryArgs().Peek("status")) == "queued,running" {
			ctx.SetBodyString(`{"evaluations":[],"total":1}`)
			return
		}
		ctx.SetBodyString(`{"evaluations":[{"id":"ev_1"}],"total":4}`)
	})
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	usage := &UsageHandler{mode: testMode, svc: Services{OrgID: "org_a", Index: cgScoreIndex{runs: map[string][]RunView{}}}, now: func() time.Time { return now }}
	r.GET(APIPrefix+"/usage", usage.get)
	h := &homeHandler{mode: schemas.ModeLive, r: r, now: func() time.Time { return now }}
	r.GET(HomeSummaryPath, h.get)

	call := func(uri, org string) (int, []byte) {
		var ctx fasthttp.RequestCtx
		ctx.Request.Header.SetMethod("GET")
		ctx.Request.SetRequestURI(uri)
		ctx.Request.Header.Set("X-Heimdall-Org", org)
		ctx.SetUserValue(cgOrgKey{}, org)
		r.Handler(&ctx)
		return ctx.Response.StatusCode(), append([]byte(nil), ctx.Response.Body()...)
	}
	code, body := call(HomeSummaryPath, "org_a")
	var out HomeSummary
	if code != 200 || json.Unmarshal(body, &out) != nil {
		t.Fatalf("summary: %d %s", code, body)
	}
	if out.Counts["agents"] != 3 || out.Counts["benchmarks"] != 7 || out.Counts["comparisons"] != 2 || out.Counts["evaluations"] != 4 || out.Running == nil || *out.Running != 1 {
		t.Fatalf("counts %s", body)
	}
	if seen[APIPrefix+"/agents"] != "org_a" {
		t.Fatalf("inner call lost the caller's values: %v", seen)
	}
	_, ub := call(APIPrefix+"/usage?from=2026-11-01&to=2026-11-27", "org_a")
	var direct UsageReport
	_ = json.Unmarshal(ub, &direct)
	if out.SpendMonthUSD == nil || *out.SpendMonthUSD != direct.TotalCostUSD || out.SpendMonthFrom != "2026-11-01" {
		t.Fatalf("spend %v vs usage %v", out.SpendMonthUSD, direct.TotalCostUSD)
	}
	// Another organization's caller gets nothing the routes refused it.
	code, body = call(HomeSummaryPath, "org_b")
	out = HomeSummary{}
	if code != 200 || json.Unmarshal(body, &out) != nil || out.Counts["agents"] != 0 || out.Counts["benchmarks"] != 0 || out.LatestComparisons != nil {
		t.Fatalf("org b: %s", body)
	}
}
