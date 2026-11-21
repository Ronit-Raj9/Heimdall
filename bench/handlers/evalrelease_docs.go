package handlers

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Releases from evaluations (lane X12).
func init() {
	p := APIPrefix + "/evaluations/{id}"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: p + "/release-preview", Tag: "releases",
			Summary:  "What a release of the evaluation would record: agent version, the live version it replaces, before and after numbers, the gate and what blocks it",
			Response: EvaluationReleasePreview{}},
		RouteDoc{Method: "POST", Path: p + "/release", Tag: "releases",
			Summary: "Release the evaluation's agent version: one release covering every finished benchmark; refused (409) on a block unless an override reason is given",
			Auth:    AuthWrite, Request: EvaluationReleaseRequest{}, Status: fasthttp.StatusCreated, Response: schemas.ReleaseRecord{}},
		RouteDoc{Method: "POST", Path: p + "/schedule", Tag: "evaluations",
			Summary: "Repeat the evaluation nightly or weekly: adds a schedule that starts a new evaluation of the same spec",
			Auth:    AuthWrite, Request: EvaluationScheduleRequest{}, Status: fasthttp.StatusCreated, Response: EvaluationScheduleResponse{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/live-version", Tag: "agents",
			Summary:  "The agent version the newest release put live (version 0: none released yet)",
			Response: LiveVersion{}},
	)
}
