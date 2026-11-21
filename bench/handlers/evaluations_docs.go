package handlers

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Evaluations area (agent-first contract, section 3).
func init() {
	p := APIPrefix + "/evaluations"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: p, Tag: "evaluations",
			Summary:  "List evaluations, newest first, with live status from their experiments",
			Response: EvaluationList{}},
		RouteDoc{Method: "POST", Path: p, Tag: "evaluations",
			Summary: "Create an evaluation: one experiment per benchmark (pack resolved, incompatible benchmarks skipped), each launched like any experiment",
			Auth:    AuthWrite, Request: schemas.EvaluationSpec{}, Status: fasthttp.StatusAccepted, Response: schemas.Evaluation{}},
		RouteDoc{Method: "GET", Path: p + "/{id}", Tag: "evaluations",
			Summary:  "One evaluation with live status from its experiments",
			Response: schemas.Evaluation{}},
		RouteDoc{Method: "POST", Path: p + "/{id}/cancel", Tag: "evaluations",
			Summary: "Cancel every experiment of the evaluation that still runs",
			Auth:    AuthWrite, Status: fasthttp.StatusAccepted, Response: schemas.Evaluation{}},
	)
}
