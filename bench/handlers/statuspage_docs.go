package handlers

import "github.com/maximhq/bifrost/bench/statuspage"

// Lane Z14: the public status page route (served in local and hosted mode
// alike: a platform route in hosted mode).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: StatusPagePath, Tag: "status",
			Summary:  "Public status page: each platform component's health and the operator's incident notes; no session or token needed, checks cached 15 s",
			Response: statuspage.Page{}},
	)
}
