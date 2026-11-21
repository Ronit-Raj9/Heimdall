package handlers

import "github.com/valyala/fasthttp"

// QuickRun area (web parity L28).
func init() {
	APIRoutes = append(APIRoutes, RouteDoc{
		Method: "POST", Path: APIPrefix + "/quickrun", Tag: "experiments",
		Summary: "Write experiments/quick-<ts>.yaml from a suite, agent and model, then plan, estimate, create and start it",
		Auth:    AuthWrite, Request: QuickRunRequest{}, Status: fasthttp.StatusCreated, Response: QuickRunResponse{},
	})
}
