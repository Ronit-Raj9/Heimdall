package handlers

import (
	"github.com/maximhq/bifrost/bench/packs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Evaluations area (agent-first contract, section 3).
func init() {
	p := APIPrefix + "/evaluations"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: p, Tag: "evaluations",
			Summary:  "List evaluations, newest first, with live status from their experiments",
			Response: EvaluationList{}, Query: append(append([]QueryDoc{}, ListQueryDocs...),
				QueryDoc{Name: "status", Description: "Comma list of queued, running, done, failed, cancelled"},
				QueryDoc{Name: "fields", Description: "light: rows without the heavy parts (gate, stream, failover, skipped tasks, fault details, analyses)"})},
		RouteDoc{Method: "POST", Path: p, Tag: "evaluations",
			Summary: "Create an evaluation: one experiment per benchmark (pack resolved, incompatible benchmarks skipped), each launched like any experiment; mcp_route (direct, bifrost or bifrost_code_mode) picks how every arm reaches the tasks' MCP tool servers, and the report card shows it",
			Auth:    AuthWrite, Request: schemas.EvaluationSpec{}, Status: fasthttp.StatusAccepted, Response: schemas.Evaluation{}},
		RouteDoc{Method: "GET", Path: p + "/{id}", Tag: "evaluations",
			Summary:  "One evaluation with live status from its experiments; while it is queued, queue says how many of the organization's evaluations wait before it and whether its worker is online",
			Response: evaluationWithQueue{Evaluation: &schemas.Evaluation{}}},
		RouteDoc{Method: "POST", Path: p + "/{id}/cancel", Tag: "evaluations",
			Summary: "Cancel every experiment of the evaluation that still runs",
			Auth:    AuthWrite, Status: fasthttp.StatusAccepted, Response: schemas.Evaluation{}},
		RouteDoc{Method: "POST", Path: p + "/estimate", Tag: "evaluations",
			Summary: "Estimate runs, cost and minutes of an evaluation spec before launching it, with the budget that applies, the runs a smaller budget would leave unstarted and, on request, the Quick look, Standard and Thorough sizes; a first run is estimated from library runs of other agents; unknown prices stay unknown",
			Auth:    AuthWrite, Request: EvalEstimateRequest{}, Response: EvalEstimate{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluation-packs", Tag: "evaluations",
			Summary:  "Evaluation packs (coding, support, memory, ...) with the benchmarks of each present on this server and their ready task counts",
			Response: []packs.PackView{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/faults", Tag: "evaluations",
			Summary:  "Every fault an evaluation can ask for (bench/faults.Catalog), with its plain name, help and group",
			Response: FaultCatalog{}},
	)
}
