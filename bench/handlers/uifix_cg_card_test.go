package handlers

import (
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestEvaluationCard: the bundle holds exactly what the three routes
// answer, passes where on, is a 404 when the evaluation route refuses
// (another organization's evaluation), and drops a part that fails.
func TestEvaluationCard(t *testing.T) {
	r := router.New()
	own := func(ctx *fasthttp.RequestCtx) bool { return ctx.UserValue("id") == "ev_a" }
	r.GET(APIPrefix+"/evaluations/{id}", func(ctx *fasthttp.RequestCtx) {
		if !own(ctx) {
			ctx.SetStatusCode(404)
			return
		}
		ctx.SetBodyString(`{"id":"ev_a"}`)
	})
	var gotWhere string
	r.GET(APIPrefix+"/evaluations/{id}/report", func(ctx *fasthttp.RequestCtx) {
		gotWhere = string(ctx.QueryArgs().Peek("where"))
		ctx.SetBodyString(`{"score":0.5}`)
	})
	r.GET(APIPrefix+"/evaluations/{id}/failure-patterns", func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(500) })
	h := &evalCardHandler{mode: schemas.ModeLive, r: r}
	r.GET(APIPrefix+"/evaluations/{id}/card", h.get)

	code, body := abCall(r, "GET", APIPrefix+"/evaluations/ev_a/card?where=model:x", nil)
	var out EvaluationCard
	if code != 200 || json.Unmarshal(body, &out) != nil || string(out.Evaluation) != `{"id":"ev_a"}` || string(out.Report) != `{"score":0.5}` || out.FailurePatterns != nil || gotWhere != "model:x" {
		t.Fatalf("card: %d %s where=%q", code, body, gotWhere)
	}
	if code, _ := abCall(r, "GET", APIPrefix+"/evaluations/ev_b/card", nil); code != 404 {
		t.Fatalf("other org's evaluation = %d", code)
	}
}
