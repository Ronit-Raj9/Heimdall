package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/valyala/fasthttp"
)

func postImproveEstimate(t *testing.T, body string) *fasthttp.RequestCtx {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetBody([]byte(body))
	(&improveEstimateHandler{}).post(ctx)
	return ctx
}

const estimateSpecBody = `{"name": "est", "agent": "agents/a.yaml", "suite": "coding-core", "preset": "small", "editable": {"instructions": true}}`

func TestImproveEstimateRoute(t *testing.T) {
	old := ImproveEstimateInputs
	t.Cleanup(func() { ImproveEstimateInputs = old })

	ImproveEstimateInputs = nil
	ctx := postImproveEstimate(t, estimateSpecBody)
	if ctx.Response.StatusCode() != fasthttp.StatusServiceUnavailable || !strings.Contains(string(ctx.Response.Body()), "estimates are not available on this server") {
		t.Fatalf("nil hook: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	ctx = postImproveEstimate(t, `{"name": "est", "rounds": "many"}`)
	if ctx.Response.StatusCode() != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("bad spec: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	var got *improve.Spec
	ImproveEstimateInputs = func(c context.Context, spec *improve.Spec) (improve.EstimateInput, error) {
		got = spec
		if _, ok := ImproveEstimateEnv(c); !ok {
			t.Error("no org environment in the context")
		}
		return improve.EstimateInput{Pool: 8, Splits: improve.SplitCounts{Practice: 3, Gate: 2, HeldOut: 3}, PerRunUSD: 0.01}, nil
	}
	ctx = postImproveEstimate(t, estimateSpecBody)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("estimate: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var e improve.Estimate
	if err := json.Unmarshal(ctx.Response.Body(), &e); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Rounds != 3 || e.OK || len(e.Refusals) == 0 || e.Refusals[0].Code != "too_few_held_out" {
		t.Fatalf("spec %+v estimate %+v", got, e)
	}
}
