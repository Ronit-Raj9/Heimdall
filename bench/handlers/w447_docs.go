package handlers

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/shadowreplay"
)

// Lane W4-47: shadow replay routes (replays_w447.go).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/replays/settings", Tag: "replays",
			Summary: "The organization's shadow replay settings: whether replay is allowed, and the caps per replay", Response: shadowreplay.Settings{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/replays/settings", Tag: "replays",
			Summary: "Allow or stop shadow replay for the organization and set its caps (admin)", Request: shadowreplay.Settings{}, Response: shadowreplay.Settings{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/replays", Tag: "replays",
			Summary: "The organization's replays, newest first, at most 50", Response: ReplayList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/replays/{id}", Tag: "replays",
			Summary: "One replay's report: each session's steps, recorded against new, and the counts per state", Response: shadowreplay.RunReport{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/replays/{id}/sessions/{recording}/to-inbox", Tag: "replays",
			Summary: "Send a replayed session to the failure inbox", Response: schemas.InboxIngestResult{}},
	)
}
