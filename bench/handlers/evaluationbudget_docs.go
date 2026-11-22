package handlers

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Evaluation budget (evaluationbudget.go, G4-01).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/evaluations/{id}/raise-budget", Tag: "evaluations",
			Summary: "Raise the evaluation's budget and continue it: each benchmark gets an equal share (never below its spend), running ones take the new cap at their next run, and the ones the cap stopped continue from their finished runs",
			Auth:    AuthWrite, Request: RaiseBudgetRequest{}, Status: fasthttp.StatusAccepted, Response: schemas.Evaluation{}},
	)
}
