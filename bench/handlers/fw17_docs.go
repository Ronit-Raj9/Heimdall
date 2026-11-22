package handlers

import (
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/schedule"
)

// Fix wave lane F17: scheduled checks control (canarycontrol.go).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: APIPrefix + "/schedules/{name}/pause", Tag: "schedules", Summary: "Pause a schedule: it stays listed and Run now works, but the server no longer starts it at its times (a canary-<name> schedule pauses that drift monitor)", Response: schedule.Schedule{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: APIPrefix + "/schedules/{name}/resume", Tag: "schedules", Summary: "Resume a paused schedule", Response: schedule.Schedule{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: APIPrefix + "/canaries/{name}/pause", Tag: "canaries", Summary: "Pause a drift monitor's scheduled checks (Check now still works); answers its definition", Response: canary.Definition{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: APIPrefix + "/canaries/{name}/resume", Tag: "canaries", Summary: "Resume a drift monitor's scheduled checks; answers its definition", Response: canary.Definition{}},
		RouteDoc{Auth: AuthWrite, Method: "PUT", Path: APIPrefix + "/canaries/{name}", Tag: "canaries", Summary: "Edit a drift monitor: the create body, checked the same way; the name in the path wins and its baselines, checks and paused state are kept", Request: CanaryInitRequest{}, Response: canary.Definition{}},
	)
}
