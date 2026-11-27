package handlers

import (
	"encoding/json"
	"net/url"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Evaluation card bundle (fix wave M2-40, design in notes/G3.md): GET
// /evaluations/{id}/card answers in one call what the report card tab reads
// from three routes, each called in process with the caller's request (same
// organization, same role checks, same answers): the evaluation (GET
// /evaluations/{id}), its report card (GET /evaluations/{id}/report, the
// where filters passed on) and its failure patterns (GET
// /evaluations/{id}/failure-patterns). A part whose route does not answer
// 200 is left out; a missing evaluation is a 404.
type EvaluationCard struct {
	Evaluation      json.RawMessage `json:"evaluation"`
	Report          json.RawMessage `json:"report,omitempty"`
	FailurePatterns json.RawMessage `json:"failure_patterns,omitempty"`
}

type evalCardHandler struct {
	mode schemas.Mode
	r    *router.Router
}

func init() {
	RegisterOrgArea("uifix_cg_card", func(env OrgEnv, r *router.Router) {
		h := &evalCardHandler{mode: env.Mode, r: r}
		r.GET(APIPrefix+"/evaluations/{id}/card", env.R(h.get))
	})
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/card", Tag: "evaluations",
		Summary:  "The report card tab in one call: the evaluation, its report card (where filters as on /report) and its failure patterns, each as its own route answers it; a part that fails is left out",
		Query:    []QueryDoc{{Name: "where", Description: "Passed to GET /evaluations/{id}/report"}},
		Response: EvaluationCard{}})
}

func (h *evalCardHandler) get(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	base := APIPrefix + "/evaluations/" + url.PathEscape(id)
	var out EvaluationCard
	if !subGet(h.r, ctx, base, &out.Evaluation) {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no evaluation "+id)
		return
	}
	q := url.Values{}
	ctx.QueryArgs().VisitAll(func(k, v []byte) {
		if string(k) == "where" {
			q.Add("where", string(v))
		}
	})
	report := base + "/report"
	if len(q) > 0 {
		report += "?" + q.Encode()
	}
	if !subGet(h.r, ctx, report, &out.Report) {
		out.Report = nil
	}
	if !subGet(h.r, ctx, base+"/failure-patterns", &out.FailurePatterns) {
		out.FailurePatterns = nil
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
