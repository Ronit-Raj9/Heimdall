package handlers

import (
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/valyala/fasthttp"
)

// Wave 4 (D5 WP6): resume and continue an improvement run, and studies.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/improvements/{id}/resume", Tag: "improvements",
			Summary: "Resume a planned, failed or interrupted improvement run; nothing already paid for runs again",
			Auth:    AuthWrite, Status: fasthttp.StatusAccepted, Response: improve.Summary{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/improvements/{id}/continue", Tag: "improvements",
			Summary: "Buy a second look for a finished run that kept a reserve: more tries, new held-out tasks, and a higher spending limit",
			Auth:    AuthWrite, Request: improve.ContinueRequest{}, Status: fasthttp.StatusAccepted, Response: improve.Summary{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/improvements/{id}/continue/estimate", Tag: "improvements",
			Summary: "What a second look would run and cost, and the gain it could confirm; nothing runs",
			Request: improve.ContinueRequest{}, Response: improve.ContinueOffer{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/improvement-studies", Tag: "improvements",
			Summary: "Start a study: the whole loop run 2 to 5 times from version 0, tested together on one held-out split",
			Auth:    AuthWrite, Request: improve.Spec{}, Status: fasthttp.StatusAccepted, Response: improve.StudyView{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/improvement-studies", Tag: "improvements",
			Summary: "Improvement studies with their status and pooled result", Response: improve.StudyList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/improvement-studies/{id}", Tag: "improvements",
			Summary: "One study: its loops, the pooled result on the held-out tasks and each loop's gain", Response: improve.StudyView{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/improvement-studies/{id}/stop", Tag: "improvements",
			Summary: "Stop a study and its running loop", Auth: AuthWrite, Response: improve.StudyView{}},
	)
}
