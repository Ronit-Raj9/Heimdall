package handlers

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Run control for evaluations (evaluationcontrol.go).
func init() {
	p := APIPrefix + "/evaluations/{id}"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: p + "/pause", Tag: "evaluations",
			Summary: "Pause every running experiment of the evaluation: in-flight runs finish, no new run starts until resume",
			Auth:    AuthWrite, Request: RunControlRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: schemas.Evaluation{}},
		RouteDoc{Method: "POST", Path: p + "/resume", Tag: "evaluations",
			Summary: "Resume every paused experiment of the evaluation",
			Auth:    AuthWrite, Request: RunControlRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: schemas.Evaluation{}},
		RouteDoc{Method: "POST", Path: p + "/rerun-failures", Tag: "evaluations",
			Summary: "Create an exploratory evaluation that reruns each finished benchmark's failed tasks; it never joins this evaluation's results or a leaderboard",
			Auth:    AuthWrite, Request: RerunFailuresRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: EvaluationRerunResult{}},
	)
}
