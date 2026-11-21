package handlers

import "github.com/valyala/fasthttp"

// Run debugging (lane X20b, rundebug.go).
func init() {
	p := APIPrefix + "/runs/{id}"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: p + "/calls", Tag: "runs", Summary: "The run's model calls in order, with replay labels, the first divergence and live breakpoints", Response: RunCalls{}},
		RouteDoc{Method: "GET", Path: p + "/calls/{request}", Tag: "runs", Summary: "One model call's raw request and response, secrets redacted, capped at 256 KiB each", Response: RunCallDetail{}},
		RouteDoc{Method: "POST", Path: p + "/replay", Tag: "runs", Summary: "Replay a sealed run's model calls from its recording at no model cost; the replay run is never counted",
			Auth: AuthWrite, Request: DebugRunRequest{}, OptionalBody: true, Status: fasthttp.StatusCreated, Response: DebugRunResponse{}},
		RouteDoc{Method: "POST", Path: p + "/fork", Tag: "runs", Summary: "Replay a sealed run up to call N, apply an edited response or an added message, then continue live; never counted",
			Auth: AuthWrite, Request: DebugRunRequest{}, Status: fasthttp.StatusCreated, Response: DebugRunResponse{}},
		RouteDoc{Method: "POST", Path: p + "/debug", Tag: "runs", Summary: "Rerun the run live with breakpoints on model calls; never counted",
			Auth: AuthWrite, Request: DebugRunRequest{}, Status: fasthttp.StatusCreated, Response: DebugRunResponse{}},
		RouteDoc{Method: "GET", Path: p + "/holds", Tag: "runs", Summary: "The model calls a breakpoint holds now and the time the run has been held", Response: HoldStatusDoc{}},
		RouteDoc{Method: "POST", Path: p + "/holds/release", Tag: "runs", Summary: "Continue, step, edit or abort one held model call",
			Auth: AuthWrite, Request: HoldReleaseDoc{}, NoContent: true},
	)
}
